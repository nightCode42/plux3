// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// One subscription's life over a [StreamTransport] (DAT-012): connect,
/// say what is subscribed (a WebSocket's first message, or the
/// `graphql-transport-ws` handshake and `subscribe`), deliver messages,
/// and when the connection ends for a reason that may pass — the network,
/// a server error, a throttle — connect again after an exponentially
/// growing, jittered wait and subscribe again. Anything else — a refused
/// connection, an oversized message, a GraphQL error, the server
/// completing the subscription — ends it. Time and randomness are
/// injected, so tests run it without waiting.
library;

import 'dart:async';
import 'dart:math';

import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/data/failure.dart';
import 'package:plux_flutter/src/data/stream_transport.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';

/// The sub-protocol of GraphQL subscriptions over a WebSocket.
const String graphqlWsProtocol = 'graphql-transport-ws';

/// What a session is doing.
enum StreamStatus {
  /// Connecting for the first time.
  connecting,

  /// Connected and subscribed.
  open,

  /// Waiting to connect again after a connection ended.
  reconnecting,

  /// Ended; it will not connect again.
  closed,
}

/// How a session talks over its connection.
enum StreamWire {
  /// Messages are delivered as they come; a WebSocket may send one
  /// message after connecting.
  plain,

  /// `graphql-transport-ws`: `connection_init`, `subscribe`, `next`.
  graphql,
}

/// Builds the request of a (re)connection, with a current token.
typedef StreamOpener = Future<StreamRequest> Function(String? lastEventId);

/// Starts a timer; [Timer.new] in the runtime.
typedef StreamScheduler = Timer Function(Duration after, void Function() run);

/// Whether a failure of connecting passes with time: the network, a
/// timeout, a server error, throttling.
bool streamRetryable(DataFailure f) =>
    f.code == PluxErrorCode.dataNetworkFailed ||
    f.code == PluxErrorCode.dataRequestTimeout ||
    (f.code == PluxErrorCode.dataHttpError &&
        (f.status >= 500 || f.status == 408 || f.status == 429));

/// The wait before reconnecting after [attempt] consecutive failures
/// (from 0): [base] doubling up to [max], then jittered into its upper
/// half by [random] so that clients disconnected together do not return
/// together.
Duration backoffDelay(int attempt, Duration base, Duration max, Random random) {
  var ms = base.inMilliseconds;
  for (var i = 0; i < attempt && ms < max.inMilliseconds; i++) {
    ms *= 2;
  }
  ms = min(ms, max.inMilliseconds);
  return Duration(
    milliseconds: (ms / 2 + random.nextDouble() * ms / 2).round(),
  );
}

/// One subscription.
final class StreamSession {
  /// Creates the session; [start] connects.
  StreamSession({
    required this.transport,
    required this.open,
    required this.wire,
    this.subscribe,
    required this.minBackoff,
    required this.maxBackoff,
    required this.onMessage,
    required this.onStatus,
    required this.onEnd,
    this.onProblem,
    this.refreshAuth,
    Random? random,
    StreamScheduler? scheduler,
    this.networkAvailable,
  }) : _random = random ?? Random(),
       _schedule = scheduler ?? Timer.new;

  /// Opens connections.
  final StreamTransport transport;

  /// Builds each connection's request.
  final StreamOpener open;

  /// The protocol.
  final StreamWire wire;

  /// What is subscribed: a plain WebSocket's message sent after
  /// connecting (null sends none), or the GraphQL `subscribe` payload
  /// (`query`, `variables`).
  final Object? subscribe;

  /// The first wait before reconnecting (`data.streamBackoffMin`).
  final Duration minBackoff;

  /// The longest wait (`data.streamBackoffMax`).
  final Duration maxBackoff;

  /// Receives each message, decoded.
  final void Function(Object? message) onMessage;

  /// Receives each change of [status].
  final void Function(StreamStatus status) onStatus;

  /// Receives the end of the session: null when the server completed it,
  /// else why it cannot go on.
  final void Function(DataFailure? failure) onEnd;

  /// Receives a failure that does not end the session, such as a GraphQL
  /// execution error in one message.
  final void Function(DataFailure failure)? onProblem;

  /// Refreshes the user's token after a connection was refused with 401;
  /// whether it did. Null for unauthenticated streams.
  final Future<bool> Function()? refreshAuth;

  /// Whether the host says the network is available; while it says not,
  /// the session waits to reconnect. Null means always.
  final bool Function()? networkAvailable;

  final Random _random;
  final StreamScheduler _schedule;

  StreamStatus _status = StreamStatus.connecting;
  StreamConnection? _conn;
  // Cancelled in [_release], which every way out of a connection calls.
  // ignore: cancel_subscriptions
  StreamSubscription<StreamFrame>? _sub;
  Timer? _timer;
  bool _stopped = false, _acked = false;
  bool _heard = false;
  int _attempt = 0;
  int? _serverRetryMs;
  int? _closeCode;
  String? _lastEventId;

  final Completer<void> _ready = Completer();

  /// Completes when the first attempt to connect has an outcome: the
  /// session is open, or waits to connect again after a failure that
  /// passes; fails with the [DataFailure] when it cannot go on at all,
  /// such as a blocked domain.
  Future<void> get ready => _ready.future;

  /// What the session is doing.
  StreamStatus get status => _status;

  /// Connects.
  void start() {
    _ready.future.ignore();
    unawaited(_connect());
  }

  /// Ends the session and closes its connection.
  Future<void> stop() async {
    if (_stopped) return;
    _stopped = true;
    _timer?.cancel();
    _timer = null;
    await _release();
    _set(StreamStatus.closed);
  }

  /// Connects now if the session is waiting to: the host says the
  /// network is back.
  void nudge() {
    if (_stopped || _status != StreamStatus.reconnecting) return;
    _timer?.cancel();
    _timer = null;
    _attempt = 0;
    unawaited(_connect());
  }

  void _set(StreamStatus s) {
    if (_status == s) return;
    _status = s;
    if (s != StreamStatus.connecting && !_ready.isCompleted) _ready.complete();
    onStatus(s);
  }

  Future<void> _release() async {
    final sub = _sub;
    final conn = _conn;
    _sub = null;
    _conn = null;
    await sub?.cancel();
    await conn?.close();
  }

  Future<void> _connect() async {
    if (_stopped) return;
    if (networkAvailable?.call() == false) {
      _set(StreamStatus.reconnecting);
      return;
    }
    _acked = false;
    _heard = false;
    _closeCode = null;
    final StreamConnection? made = await _make();
    if (made == null) return;
    final conn = made;
    if (_stopped) {
      await conn.close();
      return;
    }
    _conn = conn;
    _sub = conn.frames.listen(
      _onFrame,
      onError: (Object e) {
        if (_sub != null) _ended(e is DataFailure ? e : _broken());
      },
      onDone: () {
        if (_sub != null) _ended(null);
      },
    );
    _greet(conn);
  }

  /// Opens the connection; a 401 refreshes the token once and tries
  /// again. Null after the failure was handled.
  Future<StreamConnection?> _make() async {
    var refreshed = false;
    while (true) {
      try {
        return await transport.open(await open(_lastEventId));
      } on DataFailure catch (f) {
        final refresh = refreshAuth;
        if (f.status == 401 && !refreshed && refresh != null && !_stopped) {
          refreshed = true;
          if (await refresh()) continue;
        }
        _ended(f);
        return null;
      }
    }
  }

  DataFailure _broken() => const DataFailure(
    ActionErrorKind.network,
    PluxErrorCode.dataNetworkFailed,
    'the stream broke',
  );

  /// Says what is subscribed, once the connection is open.
  void _greet(StreamConnection conn) {
    if (wire == StreamWire.graphql) {
      conn.send(<String, Object?>{
        'type': 'connection_init',
        'payload': <String, Object?>{},
      });
      return;
    }
    if (subscribe != null) conn.send(subscribe);
    _set(StreamStatus.open);
  }

  void _onFrame(StreamFrame f) {
    switch (f.kind) {
      case StreamFrameKind.retry:
        _serverRetryMs = f.retryMs;
      case StreamFrameKind.close:
        _closeCode = f.closeCode;
      case StreamFrameKind.message:
        if (f.id != null) _lastEventId = f.id;
        if (wire == StreamWire.graphql) {
          _graphql(f.data);
        } else {
          _deliver(f.data);
        }
    }
  }

  void _deliver(Object? message) {
    if (!_heard) {
      _heard = true;
      _attempt = 0;
    }
    onMessage(message);
  }

  void _graphql(Object? m) {
    if (m is! Map<String, Object?>) return;
    switch (m['type']) {
      case 'connection_ack':
        _acked = true;
        _attempt = 0;
        _conn?.send(<String, Object?>{
          'id': '1',
          'type': 'subscribe',
          'payload': subscribe,
        });
        _set(StreamStatus.open);
      case 'ping':
        _conn?.send(<String, Object?>{'type': 'pong'});
      case 'next':
        if (_acked) _next(m['payload']);
      case 'error':
        final n = m['payload'] is List ? (m['payload']! as List).length : 1;
        _finish(
          DataFailure(
            ActionErrorKind.http,
            PluxErrorCode.dataGraphqlError,
            'the GraphQL subscription failed with $n error(s)',
          ),
        );
      case 'complete':
        _finish(null);
    }
  }

  void _next(Object? payload) {
    if (payload is! Map<String, Object?>) return;
    final data = payload['data'];
    final errors = payload['errors'];
    if (data == null && errors is List && errors.isNotEmpty) {
      // The messages may echo user data: only their count is reported.
      onProblem?.call(
        DataFailure(
          ActionErrorKind.http,
          PluxErrorCode.dataGraphqlError,
          'a GraphQL subscription message had ${errors.length} error(s)',
        ),
      );
      return;
    }
    _deliver(data);
  }

  /// Ends the session for good, closing the connection.
  void _finish(DataFailure? failure) {
    if (_stopped) return;
    _stopped = true;
    _timer?.cancel();
    unawaited(_release());
    if (failure != null && !_ready.isCompleted) _ready.completeError(failure);
    _set(StreamStatus.closed);
    onEnd(failure);
  }

  void _ended(DataFailure? failure) {
    if (_stopped) return;
    unawaited(_release());
    var f = failure;
    if (f == null && _closeCode != null && _fatalClose(_closeCode!)) {
      f = DataFailure(
        ActionErrorKind.http,
        PluxErrorCode.dataStreamFailed,
        'the server closed the stream with code $_closeCode',
      );
    }
    if (f != null && !streamRetryable(f)) {
      _finish(f);
      return;
    }
    _set(StreamStatus.reconnecting);
    final base = _serverRetryMs == null
        ? minBackoff
        : Duration(milliseconds: _serverRetryMs!);
    final wait = backoffDelay(_attempt, base, maxBackoff, _random);
    _attempt++;
    _timer = _schedule(wait, () {
      _timer = null;
      unawaited(_connect());
    });
  }

  /// A GraphQL server closes with 44xx to refuse for good (bad request,
  /// unauthorised, forbidden, a second `connection_init`), except 4408
  /// (timeout) and 4429 (too many requests), which pass.
  bool _fatalClose(int code) =>
      wire == StreamWire.graphql &&
      code >= 4400 &&
      code < 4500 &&
      code != 4408 &&
      code != 4429;
}
