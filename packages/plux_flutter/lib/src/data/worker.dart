// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The data isolate (L-6, ADR-0048): network I/O, JSON decoding, cache
/// file I/O and cache encryption happen here, never on the UI isolate.
/// The UI isolate builds and checks each request — domains, auth, size —
/// and sends it; it reads the decoded JSON back.
library;

import 'dart:async';
import 'dart:isolate';
import 'dart:typed_data';

import 'package:http/http.dart' as http;
import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/data/cache.dart';
import 'package:plux_flutter/src/data/failure.dart';
import 'package:plux_flutter/src/data/transport.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';

/// The key that encrypts cached responses of sources marked `encrypted`
/// (DAT-010): 32 bytes, kept by the platform's secure storage (D6). The
/// runtime's key provider supplies it; the data layer never stores it.
abstract interface class DataCacheKeyProvider {
  /// The key; fails when none can be had.
  Future<Uint8List> cacheKey();
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

final class _SetKey extends _Command {
  const _SetKey(super.reply, this.key);
  final Uint8List key;
}

final class _Close extends _Command {
  const _Close(super.reply);
}

/// The UI isolate's handle on the data isolate.
final class DataWorker implements DataTransport {
  DataWorker._(this._isolate, this._commands, this._keys);

  /// Starts the data isolate with the client [httpClient] creates (a
  /// top-level or static function) and its cache under [cacheDirectory].
  static Future<DataWorker> start({
    required http.Client Function() httpClient,
    required String cacheDirectory,
    DataCacheKeyProvider? keys,
  }) async {
    final ready = ReceivePort();
    final isolate = await Isolate.spawn(_main, (
      httpClient,
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

final class _WorkerStore implements CacheStore {
  _WorkerStore(this.worker, this.secure);

  final DataWorker worker;
  final bool secure;

  Future<Object?> _op(String op, [String? key, Object? json, int? at]) async {
    if (secure) {
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

Future<void> _main((http.Client Function(), String, SendPort) args) async {
  final (client, dir, ready) = args;
  final transport = ClientTransport(client());
  final plain = FileCacheStore('$dir/plain');
  FileCacheStore? secure;
  final port = ReceivePort();
  ready.send(port.sendPort);
  await for (final c in port.cast<_Command>()) {
    Future<Object?> run() async => switch (c) {
      _Send(:final request) => transport.send(request),
      _SetKey(:final key) => secure = FileCacheStore('$dir/secure', key: key),
      _Close() => transport.close(),
      final _Store s => _store(
        s.secure ? secure : plain,
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
final class LazyDataWorker implements DataTransport {
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
