// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The core's built-in adapter (DB-001, DB-009, ADR-0049): the plugins'
/// key-value stores on a [PluxKeyValueStore], the encrypted file of the
/// state engine's family. It stores no collections — those need
/// `plux_db_drift` or a host adapter (PLX-5201) — and is what runs when
/// the host supplies no adapter, so `kvGet`, `kvSet` and `kvRemove` work
/// in every app without declaring a collection.
library;

import 'dart:async';
import 'dart:convert';

import 'package:plux_flutter/src/db/adapter.dart';
import 'package:plux_flutter/src/db/query.dart';
import 'package:plux_flutter/src/db/schema.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/store/kv_store.dart';

/// A [PluxDatabaseAdapter] for key-value entries only.
final class BuiltInDatabaseAdapter implements PluxDatabaseAdapter {
  /// Creates the adapter over [store], which holds every namespace's
  /// entries as one JSON object. [encrypted] says whether [store] encrypts
  /// its file, as an `EncryptedFileStore` does; a store that does not is
  /// refused under the profiles that require encryption. [report] hears of
  /// a store that was discarded as corrupt.
  BuiltInDatabaseAdapter({
    required PluxKeyValueStore store,
    required this.encrypted,
    void Function(PluxException error)? report,
  }) : _store = store, // ignore: prefer_initializing_formals
       _report = report; // ignore: prefer_initializing_formals

  final PluxKeyValueStore _store;
  final void Function(PluxException error)? _report;

  @override
  final bool encrypted;

  @override
  bool get supportsCollections => false;

  Map<String, Map<String, Object?>> _data = {};
  bool _open = false;
  Future<void> _tail = Future.value();

  @override
  Future<void> open({required bool requireEncryption}) async {
    if (requireEncryption && !encrypted) {
      throw const PluxException(
        PluxErrorCode.dbEncryptionRequired,
        'the key-value store is not encrypted',
      );
    }
    if (_open) return;
    await _serial(() async {
      try {
        final raw = await _store.load();
        _data = {
          for (final e in raw.entries)
            if (e.value is Map<String, Object?>)
              e.key: Map<String, Object?>.of(e.value! as Map<String, Object?>),
        };
      } on StoreException catch (e) {
        _data = {};
        final error = PluxException(
          PluxErrorCode.dbStoreFailed,
          'the key-value store was discarded: ${e.message}',
        );
        if (e.failure != StoreFailure.corrupt) throw error;
        _report?.call(error);
      }
      _open = true;
    });
  }

  @override
  Future<void> close() async {
    await _serial(() => _open = false);
  }

  Future<R> _serial<R>(FutureOr<R> Function() fn) {
    final done = Completer<R>();
    final previous = _tail;
    _tail = done.future.then((_) {}, onError: (Object _) {});
    unawaited(
      previous.then((_) async {
        try {
          done.complete(await fn());
        } on Object catch (e, s) {
          done.completeError(e, s);
        }
      }),
    );
    return done.future;
  }

  void _ensureOpen() {
    if (!_open) {
      throw const PluxException(
        PluxErrorCode.dbStoreFailed,
        'the database is not open',
      );
    }
  }

  Never _noCollections() => throw const PluxException(
    PluxErrorCode.dbUnavailable,
    'the built-in store keeps no collections; add plux_db_drift or set '
    'PluxConfig.databaseAdapter',
  );

  Future<void> _save() async {
    try {
      await _store.save(_data);
    } on StoreException catch (e) {
      throw PluxException(
        e.failure == StoreFailure.tooLarge
            ? PluxErrorCode.dbLimitExceeded
            : PluxErrorCode.dbStoreFailed,
        'the key-value store could not be saved: ${e.message}',
      );
    }
  }

  Map<String, Map<String, Object?>> _copy() => {
    for (final e in _data.entries) e.key: Map<String, Object?>.of(e.value),
  };

  @override
  Future<Object?> kvGet(String namespace, String key) => _serial(() {
    _ensureOpen();
    final v = _data[namespace]?[key];
    return v == null ? null : jsonDecode(jsonEncode(v));
  });

  @override
  Future<void> kvSet(String namespace, String key, Object value) =>
      _serial(() async {
        _ensureOpen();
        await _write((d) => _set(d, namespace, key, value));
      });

  @override
  Future<bool> kvRemove(String namespace, String key) => _serial(() async {
    _ensureOpen();
    var removed = false;
    await _write((d) => removed = d[namespace]?.remove(key) != null);
    return removed;
  });

  @override
  Future<Map<String, Object?>> kvAll(String namespace) => _serial(() {
    _ensureOpen();
    return {
      for (final e in (_data[namespace] ?? const <String, Object?>{}).entries)
        e.key: jsonDecode(jsonEncode(e.value)),
    };
  });

  /// Applies [change] and saves; a failure restores the entries.
  Future<void> _write(
    void Function(Map<String, Map<String, Object?>>) change,
  ) async {
    final before = _copy();
    try {
      change(_data);
      await _save();
    } on Object {
      _data = before;
      rethrow;
    }
  }

  static void _set(
    Map<String, Map<String, Object?>> data,
    String namespace,
    String key,
    Object value,
  ) {
    final type = kvTypeOf(value);
    if (type == null) {
      throw const PluxException(
        PluxErrorCode.dbRecordInvalid,
        'a key-value entry holds a JSON value',
      );
    }
    final ns = data.putIfAbsent(namespace, () => {});
    final old = ns[key];
    if (old != null && kvTypeOf(old) != type) {
      throw PluxException(
        PluxErrorCode.dbValueTypeMismatch,
        'key "$key" holds a ${kvTypeOf(old)}, not a $type',
      );
    }
    ns[key] = jsonDecode(jsonEncode(value));
  }

  @override
  Future<R> transaction<R>(Future<R> Function(DbOperations tx) body) =>
      _serial(() async {
        _ensureOpen();
        final before = _copy();
        try {
          final r = await body(_KvTransaction(this));
          await _save();
          return r;
        } on Object {
          _data = before;
          rethrow;
        }
      });

  @override
  Future<void> wipe({String? namespace}) => _serial(() async {
    if (namespace == null) {
      _data = {};
      _open = false;
      try {
        await _store.wipe();
      } on StoreException catch (e) {
        throw PluxException(
          PluxErrorCode.dbStoreFailed,
          'the key-value store could not be removed: ${e.message}',
        );
      }
      return;
    }
    if (!_open) await _loadForWipe();
    await _write((d) => d.remove(namespace));
  });

  Future<void> _loadForWipe() async {
    try {
      final raw = await _store.load();
      _data = {
        for (final e in raw.entries)
          if (e.value is Map<String, Object?>)
            e.key: Map<String, Object?>.of(e.value! as Map<String, Object?>),
      };
    } on StoreException {
      _data = {};
    }
  }

  @override
  Future<void> dropCollections(List<String> names) =>
      names.isEmpty ? Future.value() : Future.sync(_noCollections);

  @override
  Stream<List<Map<String, Object?>>> watch(String collection, DbQuery query) =>
      Stream.error(
        const PluxException(
          PluxErrorCode.dbUnavailable,
          'the built-in store keeps no collections',
        ),
      );

  @override
  Future<List<DbMigrationOutcome>> migrate(List<DbCollectionSchema> schemas) =>
      schemas.isEmpty ? Future.value(const []) : Future.sync(_noCollections);

  @override
  Future<Map<String, Object?>?> get(String collection, String key) =>
      Future.sync(_noCollections);

  @override
  Future<void> insert(String collection, Map<String, Object?> record) =>
      Future.sync(_noCollections);

  @override
  Future<void> update(
    String collection,
    String key,
    Map<String, Object?> patch,
  ) => Future.sync(_noCollections);

  @override
  Future<void> upsert(String collection, Map<String, Object?> record) =>
      Future.sync(_noCollections);

  @override
  Future<bool> delete(String collection, String key) =>
      Future.sync(_noCollections);

  @override
  Future<List<Map<String, Object?>>> query(String collection, DbQuery query) =>
      Future.sync(_noCollections);

  @override
  Future<int> count(String collection, [DbFilter? filter]) =>
      Future.sync(_noCollections);
}

/// The operations of a transaction of the built-in adapter: entries only;
/// the adapter saves once, when the body ends.
final class _KvTransaction implements DbOperations {
  _KvTransaction(this.db);

  final BuiltInDatabaseAdapter db;

  @override
  Future<Object?> kvGet(String namespace, String key) async {
    final v = db._data[namespace]?[key];
    return v == null ? null : jsonDecode(jsonEncode(v));
  }

  @override
  Future<void> kvSet(String namespace, String key, Object value) async =>
      BuiltInDatabaseAdapter._set(db._data, namespace, key, value);

  @override
  Future<bool> kvRemove(String namespace, String key) async =>
      db._data[namespace]?.remove(key) != null;

  @override
  Future<Map<String, Object?>> kvAll(String namespace) async => {
    for (final e in (db._data[namespace] ?? const <String, Object?>{}).entries)
      e.key: jsonDecode(jsonEncode(e.value)),
  };

  @override
  Future<Map<String, Object?>?> get(String collection, String key) =>
      Future.sync(db._noCollections);

  @override
  Future<void> insert(String collection, Map<String, Object?> record) =>
      Future.sync(db._noCollections);

  @override
  Future<void> update(
    String collection,
    String key,
    Map<String, Object?> patch,
  ) => Future.sync(db._noCollections);

  @override
  Future<void> upsert(String collection, Map<String, Object?> record) =>
      Future.sync(db._noCollections);

  @override
  Future<bool> delete(String collection, String key) =>
      Future.sync(db._noCollections);

  @override
  Future<List<Map<String, Object?>>> query(String collection, DbQuery query) =>
      Future.sync(db._noCollections);

  @override
  Future<int> count(String collection, [DbFilter? filter]) =>
      Future.sync(db._noCollections);
}
