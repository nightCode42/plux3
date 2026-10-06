// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The data isolate (L-6, ADR-0048): network I/O, JSON decoding, cache
/// file I/O and cache encryption happen here, never on the UI isolate.
/// The UI isolate builds and checks each request — domains, auth, size —
/// and sends it; it reads the decoded JSON back.
library;

import 'dart:async';
import 'dart:io' show HttpClient;
import 'dart:isolate';
import 'dart:typed_data';

import 'package:http/http.dart' as http;
import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/data/cache.dart';
import 'package:plux_flutter/src/data/failure.dart';
import 'package:plux_flutter/src/data/stream_transport.dart';
import 'package:plux_flutter/src/data/transfer_transport.dart';
import 'package:plux_flutter/src/data/transport.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/store/kv_store.dart';

/// The key that encrypts cached responses of sources marked `encrypted`
/// (DAT-010): 32 bytes, kept by the platform's secure storage (D6). The
/// runtime's key provider supplies it; the data layer never stores it.
abstract interface class DataCacheKeyProvider {
  /// The key; fails when none can be had.
  Future<Uint8List> cacheKey();
}

/// The cache key of this installation, kept in the platform's secure
/// storage like the secure state store's key (D6): made on first use, then
/// read back. A storage failure fails the key with `PLX-5109`, so
/// encrypted sources are not cached.
final class SecretCacheKeys implements DataCacheKeyProvider {
  /// Creates the provider of key [name] in [secrets].
  const SecretCacheKeys(this.secrets, this.name);

  /// Where the key is kept.
  final SecretStore secrets;

  /// The key's name.
  final String name;

  @override
  Future<Uint8List> cacheKey() async {
    try {
      return (await installationKey(secrets, name, create: true))!;
    } on StoreException catch (e) {
      throw DataFailure(
        ActionErrorKind.custom,
        PluxErrorCode.dataCacheUnavailable,
        'the cache key: ${e.message}',
      );
    }
  }
}

sealed class _Command {
  const _Command(this.reply);
  final SendPort reply;
}

final class _Send extends _Command {
  const _Send(super.reply, this.request);
  final DataRequest request;
}

final class _Store extends _Command {
  const _Store(
    super.reply,
    this.secure,
    this.op, [
    this.key,
    this.json,
    this.at,
  ]);
  final bool secure;
  final String op; // read, write, remove, clear, entries
  final String? key;
  final Object? json;
  final int? at;
}

final class _OpenStream extends _Command {
  const _OpenStream(super.reply, this.request);
  final StreamRequest request;
}

final class _StartTransfer extends _Command {
  const _StartTransfer(super.reply, this.request);
  final TransferRequest request;
}

final class _SetKey extends _Command {
  const _SetKey(super.reply, this.key);
  final Uint8List key;
}

final class _Close extends _Command {
  const _Close(super.reply);
}

/// The UI isolate's handle on the data isolate.
final class DataWorker
    implements DataTransport, StreamTransport, TransferTransport {
  DataWorker._(this._isolate, this._commands, this._keys);

  /// Starts the data isolate with the client [httpClient] creates (a
  /// top-level or static function) and its cache under [cacheDirectory].
  /// WebSockets connect with the `dart:io` client [webSocketClient] creates
  /// (also top-level or static), or `dart:io`'s default when null.
  static Future<DataWorker> start({
    required http.Client Function() httpClient,
    required String cacheDirectory,
    HttpClient Function()? webSocketClient,
    DataCacheKeyProvider? keys,
  }) async {
    final ready = ReceivePort();
    final isolate = await Isolate.spawn(_main, (
      httpClient,
      webSocketClient,
      cacheDirectory,
      ready.sendPort,
    ), debugName: 'plux-data');
    final commands = await ready.first as SendPort;
    ready.close();
    return DataWorker._(isolate, commands, keys);
  }

  final Isolate _isolate;
  final SendPort _commands;
  final DataCacheKeyProvider? _keys;
  Future<void>? _keySent;

  Future<Object?> _call(_Command Function(SendPort reply) command) async {
    final port = ReceivePort();
    _commands.send(command(port.sendPort));
    final (ok, value) = await port.first as (bool, Object?);
    port.close();
    if (!ok) throw value! as DataFailure;
    return value;
  }

  @override
  Future<DataResponse> send(DataRequest request) async =>
      await _call((r) => _Send(r, request)) as DataResponse;

  /// The cache store of plain or encrypted entries.
  CacheStore store({required bool secure}) => _WorkerStore(this, secure);

  @override
  Future<StreamConnection> open(StreamRequest request) async {
    final events = ReceivePort();
    _commands.send(_OpenStream(events.sendPort, request));
    final opened = Completer<SendPort>();
    final frames = StreamController<StreamFrame>();
    events.listen((Object? m) {
      final (tag, value) = m! as (String, Object?);
      switch (tag) {
        case 'open':
          opened.complete(value! as SendPort);
        case 'frame':
          frames.add(value! as StreamFrame);
        case 'error':
          if (opened.isCompleted) {
            frames.addError(value!);
          } else {
            opened.completeError(value!);
            events.close();
          }
        case 'done':
          unawaited(frames.close());
          events.close();
      }
    });
    final control = await opened.future;
    return _ProxyConnection(frames, control, events);
  }

  @override
  TransferJob begin(TransferRequest request) {
    final events = ReceivePort();
    _commands.send(_StartTransfer(events.sendPort, request));
    final progress = StreamController<TransferProgress>();
    final result = Completer<TransferResult>();
    SendPort? control;
    var cancelled = false;
    events.listen((Object? m) {
      final (tag, value) = m! as (String, Object?);
      switch (tag) {
        case 'ready':
          control = value! as SendPort;
          if (cancelled) control!.send('cancel');
        case 'progress':
          progress.add(value! as TransferProgress);
        case 'result':
          result.complete(value! as TransferResult);
          unawaited(progress.close());
          events.close();
        case 'error':
          result.completeError(value!);
          unawaited(progress.close());
          events.close();
      }
    });
    unawaited(result.future.then((_) {}, onError: (Object _) {}));
    return TransferJob(progress.stream, result.future, () {
      cancelled = true;
      control?.send('cancel');
    });
  }

  Future<void> _ensureKey() => _keySent ??= () async {
    final keys = _keys;
    if (keys == null) {
      throw const DataFailure(
        ActionErrorKind.custom,
        PluxErrorCode.dataCacheUnavailable,
        'no key provider: encrypted sources are not cached',
      );
    }
    final key = await keys.cacheKey();
    await _call((r) => _SetKey(r, key));
  }();

  @override
  Future<void> close() async {
    await _call(_Close.new);
    _isolate.kill();
  }
}

final class _ProxyConnection implements StreamConnection {
  _ProxyConnection(this._frames, this._control, this._events);

  final StreamController<StreamFrame> _frames;
  final SendPort _control;
  final ReceivePort _events;

  @override
  Stream<StreamFrame> get frames => _frames.stream;

  @override
  void send(Object? json) => _control.send(['send', json]);

  @override
  Future<void> close() async {
    _control.send('close');
    _events.close();
    if (!_frames.isClosed) await _frames.close();
  }
}

final class _WorkerStore implements CacheStore {
  _WorkerStore(this.worker, this.secure);

  final DataWorker worker;
  final bool secure;

  Future<Object?> _op(String op, [String? key, Object? json, int? at]) async {
    // Clearing removes the files and needs no key, so a logout or a wipe
    // never makes one.
    if (secure && op != 'clear') {
      try {
        await worker._ensureKey();
      } on Object {
        worker._keySent = null; // try again next time
        rethrow;
      }
    }
    return worker._call((r) => _Store(r, secure, op, key, json, at));
  }

  @override
  Future<CachedEntry?> read(String key) async =>
      await _op('read', key) as CachedEntry?;

  @override
  Future<int> write(String key, Object? json, int storedAtMs) async =>
      await _op('write', key, json, storedAtMs) as int;

  @override
  Future<void> remove(String key) => _op('remove', key);

  @override
  Future<void> clear() => _op('clear');

  @override
  Future<List<CacheIndexEntry>> entries() async =>
      (await _op('entries') as List<Object?>).cast<CacheIndexEntry>();
}

Future<void> _main(
  (http.Client Function(), HttpClient Function()?, String, SendPort) args,
) async {
  final (client, socketClient, dir, ready) = args;
  final http = client();
  final transport = ClientTransport(http);
  final streams = SocketStreamTransport(http, webSocketClient: socketClient);
  final transfers = HttpTransferTransport(http);
  final plain = FileCacheStore('$dir/plain');
  FileCacheStore? secure;
  final port = ReceivePort();
  ready.send(port.sendPort);
  await for (final c in port.cast<_Command>()) {
    if (c is _OpenStream) {
      unawaited(_serveStream(c, streams));
      continue;
    }
    if (c is _StartTransfer) {
      _serveTransfer(c, transfers);
      continue;
    }
    Future<Object?> run() async => switch (c) {
      _Send(:final request) => transport.send(request),
      _OpenStream() || _StartTransfer() => null,
      _SetKey(:final key) => secure = FileCacheStore('$dir/secure', key: key),
      _Close() => transport.close(),
      final _Store s => _store(
        s.secure
            ? secure ?? (s.op == 'clear' ? FileCacheStore('$dir/secure') : null)
            : plain,
        s.op,
        s.key,
        s.json,
        s.at,
      ),
    };
    unawaited(
      run().then(
        (v) => c.reply.send((true, v is FileCacheStore ? null : v)),
        onError: (Object e) => c.reply.send((false, _sendable(e))),
      ),
    );
    if (c is _Close) {
      port.close();
    }
  }
}

/// Runs one stream connection on the data isolate: the frames go to the
/// UI isolate as they come, and its `send` and `close` come back.
Future<void> _serveStream(_OpenStream c, StreamTransport transport) async {
  final control = ReceivePort();
  final StreamConnection conn;
  try {
    conn = await transport.open(c.request);
  } on Object catch (e) {
    control.close();
    c.reply.send(('error', _sendable(e)));
    return;
  }
  c.reply.send(('open', control.sendPort));
  final sub = conn.frames.listen(
    (f) => c.reply.send(('frame', f)),
    onError: (Object e) => c.reply.send(('error', _sendable(e))),
    onDone: () {
      c.reply.send(('done', null));
      control.close();
    },
  );
  control.listen((Object? m) {
    if (m is List<Object?> && m.first == 'send') {
      try {
        conn.send(m.last);
      } on Object {
        // The connection ended: its frames say so.
      }
    } else if (m == 'close') {
      unawaited(sub.cancel());
      unawaited(conn.close());
      control.close();
    }
  });
}

/// Runs one transfer on the data isolate.
void _serveTransfer(_StartTransfer c, TransferTransport transport) {
  final control = ReceivePort();
  final TransferJob job;
  try {
    job = transport.begin(c.request);
  } on Object catch (e) {
    control.close();
    c.reply.send(('error', _sendable(e)));
    return;
  }
  c.reply.send(('ready', control.sendPort));
  control.listen((Object? m) {
    if (m == 'cancel') job.cancel();
  });
  job.progress.listen((p) => c.reply.send(('progress', p)));
  unawaited(
    job.result.then(
      (r) {
        c.reply.send(('result', r));
        control.close();
      },
      onError: (Object e) {
        c.reply.send(('error', _sendable(e)));
        control.close();
      },
    ),
  );
}

Future<Object?> _store(
  FileCacheStore? s,
  String op,
  String? key,
  Object? json,
  int? at,
) async {
  if (s == null) {
    throw const DataFailure(
      ActionErrorKind.custom,
      PluxErrorCode.dataCacheUnavailable,
      'the encrypted cache has no key',
    );
  }
  switch (op) {
    case 'read':
      return s.read(key!);
    case 'write':
      return s.write(key!, json, at!);
    case 'remove':
      await s.remove(key!);
    case 'clear':
      await s.clear();
    default:
      return s.entries();
  }
  return null;
}

/// What crosses back to the UI isolate: a [DataFailure] as it is, anything
/// else as a cache failure naming only its type.
Object _sendable(Object e) => e is DataFailure
    ? e
    : DataFailure(
        ActionErrorKind.custom,
        PluxErrorCode.dataCacheUnavailable,
        'the data isolate failed: ${e.runtimeType}',
      );

/// A [DataWorker] started on first use, so a runtime whose pages load no
/// data never spawns the data isolate.
final class LazyDataWorker
    implements DataTransport, StreamTransport, TransferTransport {
  /// Creates the handle; [start] starts the worker.
  LazyDataWorker(this._start);

  final Future<DataWorker> Function() _start;
  Future<DataWorker>? _worker;

  Future<DataWorker> get _started => _worker ??= _start();

  @override
  Future<DataResponse> send(DataRequest request) async =>
      (await _started).send(request);

  /// The cache store of plain or encrypted entries.
  CacheStore store({required bool secure}) => _LazyStore(this, secure);

  @override
  Future<StreamConnection> open(StreamRequest request) async =>
      (await _started).open(request);

  @override
  TransferJob begin(TransferRequest request) {
    final progress = StreamController<TransferProgress>();
    TransferJob? job;
    var cancelled = false;
    final result = _started
        .then((w) {
          job = w.begin(request);
          if (cancelled) job!.cancel();
          job!.progress.listen(progress.add);
          return job!.result;
        })
        .whenComplete(progress.close);
    unawaited(result.then((_) {}, onError: (Object _) {}));
    return TransferJob(progress.stream, result, () {
      cancelled = true;
      job?.cancel();
    });
  }

  @override
  Future<void> close() async {
    final w = _worker;
    if (w != null) await (await w).close();
  }
}

final class _LazyStore implements CacheStore {
  _LazyStore(this.lazy, this.secure);

  final LazyDataWorker lazy;
  final bool secure;

  Future<CacheStore> get _s async =>
      (await lazy._started).store(secure: secure);

  @override
  Future<CachedEntry?> read(String key) async => (await _s).read(key);

  @override
  Future<int> write(String key, Object? json, int storedAtMs) async =>
      (await _s).write(key, json, storedAtMs);

  @override
  Future<void> remove(String key) async => (await _s).remove(key);

  @override
  Future<void> clear() async => (await _s).clear();

  @override
  Future<List<CacheIndexEntry>> entries() async => (await _s).entries();
}
