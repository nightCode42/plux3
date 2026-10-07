// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// What a stream is on the wire (DAT-012, ADR-0048): one connection, a
/// WebSocket on `dart:io` or a server-sent-events response streamed over
/// the runtime's HTTP client, delivering decoded frames until it ends.
/// Reconnection, backoff and resubscription are the [StreamSession]'s
/// business, so they run the same against any transport. In apps the
/// transport runs on the data isolate (L-6): the UI isolate receives only
/// decoded frames.
library;

import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:http/http.dart' as http;
import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/data/failure.dart';
import 'package:plux_flutter/src/data/sse.dart';
import 'package:plux_flutter/src/data/transport.dart' show describeUrl;
import 'package:plux_flutter/src/errors/plux_exception.dart';

/// The kinds of stream connection.
enum StreamKind {
  /// A WebSocket.
  webSocket,

  /// Server-sent events.
  sse,
}

/// One connection to open, built and checked on the UI isolate.
final class StreamRequest {
  /// Creates a request.
  const StreamRequest({
    required this.kind,
    required this.url,
    this.headers = const {},
    this.protocols = const [],
    this.lastEventId,
    required this.maxMessageBytes,
    required this.connectTimeout,
  });

  /// The kind.
  final StreamKind kind;

  /// The URL, `ws`/`wss` for a WebSocket, `http`/`https` for SSE; its
  /// domain was checked before (DAT-030).
  final Uri url;

  /// The headers, including the token of an authenticated stream.
  final Map<String, String> headers;

  /// The WebSocket sub-protocols offered.
  final List<String> protocols;

  /// The last event ID an SSE stream saw, sent as `Last-Event-ID`.
  final String? lastEventId;

  /// The largest message accepted (`data.streamMessageSize`).
  final int maxMessageBytes;

  /// How long connecting may take (`data.requestTimeout`).
  final Duration connectTimeout;
}

/// The kinds of frame.
enum StreamFrameKind {
  /// A message.
  message,

  /// The server set the reconnection delay (SSE `retry`).
  retry,

  /// The peer closed the connection; the stream ends after it.
  close,
}

/// One thing a connection delivers.
final class StreamFrame {
  /// A message: [data] is the decoded JSON, or the text when it is not
  /// JSON; [event] and [id] are SSE's.
  const StreamFrame.message(this.data, {this.event = 'message', this.id})
    : kind = StreamFrameKind.message,
      retryMs = null,
      closeCode = null;

  /// A reconnection delay.
  const StreamFrame.retry(int this.retryMs)
    : kind = StreamFrameKind.retry,
      data = null,
      event = '',
      id = null,
      closeCode = null;

  /// The peer closed the connection with [closeCode], when it gave one.
  const StreamFrame.close(this.closeCode)
    : kind = StreamFrameKind.close,
      data = null,
      event = '',
      id = null,
      retryMs = null;

  /// The kind.
  final StreamFrameKind kind;

  /// The decoded message.
  final Object? data;

  /// An SSE message's event type.
  final String event;

  /// An SSE message's last event ID.
  final String? id;

  /// A reconnection delay in milliseconds.
  final int? retryMs;

  /// A WebSocket's close code.
  final int? closeCode;
}

/// An open connection.
abstract interface class StreamConnection {
  /// The frames; an error is a [DataFailure], after which the stream
  /// ends. A peer closing ends it with a close frame.
  Stream<StreamFrame> get frames;

  /// Sends [json] (a WebSocket only).
  void send(Object? json);

  /// Closes the connection.
  Future<void> close();
}

/// Opens stream connections.
abstract interface class StreamTransport {
  /// Opens a connection for [request]; fails with a [DataFailure] when it
  /// cannot be made.
  Future<StreamConnection> open(StreamRequest request);
}

/// Opens a WebSocket; `WebSocket.connect` in the runtime.
typedef WebSocketConnector = Future<WebSocket> Function(
  String url, {
  Iterable<String>? protocols,
  Map<String, dynamic>? headers,
});

/// Streams over `dart:io`'s WebSocket and the HTTP client's responses.
final class SocketStreamTransport implements StreamTransport {
  /// Creates the transport; [client] reads SSE responses. WebSockets
  /// connect with the `dart:io` client [webSocketClient] creates, or
  /// `dart:io`'s default when null; a [connector] replaces the connection
  /// altogether.
  SocketStreamTransport(
    this.client, {
    WebSocketConnector? connector,
    HttpClient Function()? webSocketClient,
  }) : _connect =
           connector ??
           ((url, {protocols, headers}) => WebSocket.connect(
             url,
             protocols: protocols,
             headers: headers,
             customClient: webSocketClient?.call(),
           ));

  /// The HTTP client SSE responses are read with.
  final http.Client client;

  final WebSocketConnector _connect;

  @override
  Future<StreamConnection> open(StreamRequest r) => switch (r.kind) {
    StreamKind.webSocket => _openWebSocket(r),
    StreamKind.sse => _openSse(r),
  };

  Future<StreamConnection> _openWebSocket(StreamRequest r) async {
    try {
      // The connection owns the socket and closes it.
      // ignore: close_sinks
      final ws = await _connect(
        r.url.toString(),
        protocols: r.protocols.isEmpty ? null : r.protocols,
        headers: r.headers,
      ).timeout(r.connectTimeout);
      return _WebSocketConnection(ws, r.maxMessageBytes, r.url);
    } on TimeoutException {
      throw _network(r.url, 'connecting took longer than the timeout');
    } on WebSocketException catch (e) {
      throw _network(r.url, e.message);
    } on SocketException catch (e) {
      throw _network(r.url, e.osError?.message ?? 'connection failed');
    } on HandshakeException {
      throw _network(r.url, 'the TLS handshake failed');
    } on HttpException catch (e) {
      throw _network(r.url, e.message);
    }
  }

  Future<StreamConnection> _openSse(StreamRequest r) async {
    final req = http.Request('GET', r.url)
      ..headers.addAll({
        'accept': 'text/event-stream',
        'cache-control': 'no-cache',
        if (r.lastEventId != null) 'last-event-id': r.lastEventId!,
        ...r.headers,
      });
    final http.StreamedResponse res;
    try {
      res = await client.send(req).timeout(r.connectTimeout);
    } on TimeoutException {
      throw _network(r.url, 'connecting took longer than the timeout');
    } on SocketException catch (e) {
      throw _network(r.url, e.osError?.message ?? 'connection failed');
    } on HandshakeException {
      throw _network(r.url, 'the TLS handshake failed');
    } on http.ClientException catch (e) {
      throw _network(r.url, e.message);
    }
    final status = res.statusCode;
    if (status != 200) {
      await res.stream.drain<void>().catchError((_) {});
      throw _refused(r.url, status);
    }
    final type = res.headers['content-type'] ?? '';
    if (!type.toLowerCase().startsWith('text/event-stream')) {
      await res.stream.drain<void>().catchError((_) {});
      throw DataFailure(
        ActionErrorKind.http,
        PluxErrorCode.dataStreamFailed,
        '${describeUrl(r.url)} did not answer with an event stream',
        status: status,
      );
    }
    return _SseConnection(res, r.maxMessageBytes, r.lastEventId, r.url);
  }
}

DataFailure _network(Uri url, String why) => DataFailure(
  ActionErrorKind.network,
  PluxErrorCode.dataNetworkFailed,
  'the stream at ${describeUrl(url)} failed: $why',
);

/// A refusal: a client error ends the stream for good, a server error,
/// a timeout or throttling is retried (DAT-012).
DataFailure _refused(Uri url, int status) {
  final retry = status >= 500 || status == 408 || status == 429;
  return DataFailure(
    ActionErrorKind.http,
    retry ? PluxErrorCode.dataHttpError : PluxErrorCode.dataStreamFailed,
    'the stream at ${describeUrl(url)} was answered with $status',
    status: status,
  );
}

DataFailure _tooLarge(Uri url, int max) => DataFailure(
  ActionErrorKind.validation,
  PluxErrorCode.dataStreamMessageTooLarge,
  'a message of the stream at ${describeUrl(url)} exceeds '
  'data.streamMessageSize = $max bytes',
);

Object? _decode(String text) {
  try {
    return jsonDecode(text);
  } on FormatException {
    return text;
  }
}

final class _WebSocketConnection implements StreamConnection {
  _WebSocketConnection(this._ws, int max, Uri url) {
    _sub = _ws.listen(
      (data) {
        if (data is! String) return;
        if (data.length > max || utf8.encode(data).length > max) {
          _controller.addError(_tooLarge(url, max));
          unawaited(close());
          return;
        }
        _controller.add(StreamFrame.message(_decode(data)));
      },
      onError: (Object e) {
        _controller.addError(_network(url, 'the connection broke'));
        unawaited(_controller.close());
      },
      onDone: () {
        if (_controller.isClosed) return;
        _controller.add(StreamFrame.close(_ws.closeCode));
        unawaited(_controller.close());
      },
    );
  }

  final WebSocket _ws;
  late final StreamSubscription<dynamic> _sub;
  // Closed by [close], or when the peer ends the connection.
  // ignore: close_sinks
  final StreamController<StreamFrame> _controller = StreamController();

  @override
  Stream<StreamFrame> get frames => _controller.stream;

  @override
  void send(Object? json) => _ws.add(jsonEncode(json));

  @override
  Future<void> close() async {
    await _sub.cancel();
    try {
      await _ws.close(WebSocketStatus.normalClosure);
    } on Object {
      // Already closed or broken: nothing is left to close.
    }
    if (!_controller.isClosed) await _controller.close();
  }
}

final class _SseConnection implements StreamConnection {
  _SseConnection(
    http.StreamedResponse res,
    int max,
    String? lastEventId,
    Uri url,
  ) : _parser = SseParser(maxDataBytes: max)..lastEventId = lastEventId {
    var retry = _parser.retryMs;
    _sub = utf8.decoder
        .bind(res.stream)
        .listen(
          (text) {
            try {
              for (final e in _parser.add(text)) {
                _controller.add(
                  StreamFrame.message(
                    _decode(e.data),
                    event: e.event,
                    id: e.id,
                  ),
                );
              }
            } on SseTooLarge {
              _controller.addError(_tooLarge(url, max));
              unawaited(close());
              return;
            }
            if (_parser.retryMs != retry && _parser.retryMs != null) {
              retry = _parser.retryMs;
              _controller.add(StreamFrame.retry(retry!));
            }
          },
          onError: (Object e) {
            _controller.addError(_network(url, 'the connection broke'));
            unawaited(_controller.close());
          },
          onDone: () {
            if (_controller.isClosed) return;
            _controller.add(const StreamFrame.close(null));
            unawaited(_controller.close());
          },
        );
  }

  final SseParser _parser;
  late final StreamSubscription<String> _sub;
  // Closed by [close], or when the peer ends the connection.
  // ignore: close_sinks
  final StreamController<StreamFrame> _controller = StreamController();

  @override
  Stream<StreamFrame> get frames => _controller.stream;

  @override
  void send(Object? json) =>
      throw UnsupportedError('a server-sent-events stream receives only');

  @override
  Future<void> close() async {
    await _sub.cancel();
    if (!_controller.isClosed) await _controller.close();
  }
}
