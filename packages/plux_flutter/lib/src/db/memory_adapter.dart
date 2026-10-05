// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// An adapter that keeps everything in memory: the reference every other
/// adapter's behaviour is checked against (the shared conformance suite),
/// and the database of tests and previews. Nothing survives the process.
library;

import 'dart:async';
import 'dart:convert';

import 'package:plux_flutter/src/db/adapter.dart';
import 'package:plux_flutter/src/db/query.dart';
import 'package:plux_flutter/src/db/schema.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';

/// A [PluxDatabaseAdapter] in memory.
final class MemoryDatabaseAdapter implements PluxDatabaseAdapter {
  /// Creates an empty database. With [encrypted] it claims encryption at
  /// rest, to test the profiles that require it.
  MemoryDatabaseAdapter({this.encrypted = false});

  @override
  final bool encrypted;

  @override
  bool get supportsCollections => true;

  final Map<String, _Collection> _collections = {};
  final Map<String, Map<String, Object?>> _kv = {};
  final List<_Watch> _watches = [];
  Future<void> _tail = Future.value();
  bool _open = false;

  @override
  Future<void> open({required bool requireEncryption}) async {
    if (requireEncryption && !encrypted) {
      throw const PluxException(
        PluxErrorCode.dbEncryptionRequired,
        'the in-memory database is not encrypted',
      );
    }
    _open = true;
  }

  @override
  Future<void> close() async {
    _open = false;
    for (final w in _watches.toList()) {
      unawaited(w.controller.close());
    }
    _watches.clear();
  }

  /// Runs [fn] after every earlier operation, one at a time.
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

  Future<R> _single<R>(R Function(_Ops ops) f) => _serial(() {
    _ensureOpen();
    final touched = <String>{};
    final r = f(_Ops(this, touched));
    _publish(touched);
    return r;
  });

  @override
  Future<List<DbMigrationOutcome>> migrate(List<DbCollectionSchema> schemas) =>
      _serial(() {
        _ensureOpen();
        final out = <DbMigrationOutcome>[];
        final touched = <String>{};
        for (final s in schemas) {
          out.add(_migrateOne(s, touched));
        }
        _publish(touched);
        return out;
      });

  DbMigrationOutcome _migrateOne(
    DbCollectionSchema target,
    Set<String> touched,
  ) {
    final existing = _collections[target.name];
    if (existing == null) {
      _collections[target.name] = _Collection(target);
      touched.add(target.name);
      return DbMigrationOutcome(
        collection: target.name,
        version: target.version,
      );
    }
    final stored = existing.schema;
    if (stored.version == target.version && stored.sameStorage(target)) {
      return DbMigrationOutcome(
        collection: target.name,
        version: stored.version,
      );
    }
    try {
      if (stored.version >= target.version) {
        throw PluxException(
          PluxErrorCode.dbMigrationFailed,
          'collection ${target.key} is stored at version ${stored.version}, '
          'not below ${target.version}',
        );
      }
      final plan = DbMigrator.plan(stored, target);
      final rows = <String, Map<String, Object?>>{};
      for (final r in existing.rows.values) {
        final migrated = plan.row(r);
        rows[target.keyOf(migrated)] = target.checked(migrated);
      }
      _collections[target.name] = _Collection(target)..rows.addAll(rows);
      touched.add(target.name);
      return DbMigrationOutcome(
        collection: target.name,
        version: target.version,
      );
    } on Object catch (e) {
      return DbMigrationOutcome(
        collection: target.name,
        version: stored.version,
        error: e is PluxException
            ? e
            : PluxException(
                PluxErrorCode.dbMigrationFailed,
                'migration failed',
              ),
      );
    }
  }

  @override
  Future<void> dropCollections(List<String> names) => _serial(() {
    _ensureOpen();
    final touched = <String>{};
    for (final n in names) {
      if (_collections.remove(n) != null) touched.add(n);
    }
    _publish(touched);
  });

  @override
  Future<R> transaction<R>(Future<R> Function(DbOperations tx) body) =>
      _serial(() async {
        _ensureOpen();
        final snapshot = _snapshot();
        final touched = <String>{};
        try {
          final r = await body(_Ops(this, touched));
          _publish(touched);
          return r;
        } on Object {
          _restore(snapshot);
          rethrow;
        }
      });

  ({Map<String, _Collection> collections, Map<String, Map<String, Object?>> kv})
  _snapshot() => (
    collections: {for (final e in _collections.entries) e.key: e.value.copy()},
    kv: {for (final e in _kv.entries) e.key: Map<String, Object?>.of(e.value)},
  );

  void _restore(
    ({
      Map<String, _Collection> collections,
      Map<String, Map<String, Object?>> kv,
    })
    s,
  ) {
    _collections
      ..clear()
      ..addAll(s.collections);
    _kv
      ..clear()
      ..addAll(s.kv);
  }

  @override
  Stream<List<Map<String, Object?>>> watch(String collection, DbQuery query) {
    late final _Watch w;
    final c = StreamController<List<Map<String, Object?>>>(
      onListen: () => unawaited(_arm(w)),
      onCancel: () {
        _watches.remove(w);
        return w.controller.close();
      },
    );
    w = _Watch(collection, query, c);
    return c.stream;
  }

  Future<void> _arm(_Watch w) async {
    try {
      await _serial(() {
        _ensureOpen();
        final c = _need(w.collection);
        w.query.check(c.schema);
        _watches.add(w);
        w.emit(c);
      });
    } on Object catch (e, s) {
      if (!w.controller.isClosed) w.controller.addError(e, s);
    }
  }

  void _publish(Set<String> touched) {
    for (final w in _watches.toList()) {
      if (touched.contains(w.collection)) {
        final c = _collections[w.collection];
        if (c != null) w.emit(c);
      }
    }
  }

  _Collection _need(String name) =>
      _collections[name] ??
      (throw PluxException(
        PluxErrorCode.dbCollectionUnknown,
        'no collection is stored under that name',
      ));

  @override
  Future<void> wipe({String? namespace}) => _serial(() {
    final touched = <String>{};
    if (namespace == null) {
      touched.addAll(_collections.keys);
      _collections.clear();
      _kv.clear();
    } else {
      for (final c in _collections.values) {
        if (c.schema.namespace == namespace && c.rows.isNotEmpty) {
          c.rows.clear();
          touched.add(c.schema.name);
        }
      }
      _kv.remove(namespace);
    }
    _open = namespace != null && _open;
    _publish(touched);
  });

  @override
  Future<Map<String, Object?>?> get(String collection, String key) =>
      _single((o) => o.getSync(collection, key));

  @override
  Future<void> insert(String collection, Map<String, Object?> record) =>
      _single((o) => o.insertSync(collection, record));

  @override
  Future<void> update(
    String collection,
    String key,
    Map<String, Object?> patch,
  ) => _single((o) => o.updateSync(collection, key, patch));

  @override
  Future<void> upsert(String collection, Map<String, Object?> record) =>
      _single((o) => o.upsertSync(collection, record));

  @override
  Future<bool> delete(String collection, String key) =>
      _single((o) => o.deleteSync(collection, key));

  @override
  Future<List<Map<String, Object?>>> query(String collection, DbQuery query) =>
      _single((o) => o.querySync(collection, query));

  @override
  Future<int> count(String collection, [DbFilter? filter]) =>
      _single((o) => o.countSync(collection, filter));

  @override
  Future<Object?> kvGet(String namespace, String key) =>
      _single((o) => o.kvGetSync(namespace, key));

  @override
  Future<void> kvSet(String namespace, String key, Object value) =>
      _single((o) => o.kvSetSync(namespace, key, value));

  @override
  Future<bool> kvRemove(String namespace, String key) =>
      _single((o) => o.kvRemoveSync(namespace, key));

  @override
  Future<Map<String, Object?>> kvAll(String namespace) =>
      _single((o) => o.kvAllSync(namespace));
}

final class _Collection {
  _Collection(this.schema);

  final DbCollectionSchema schema;
  final Map<String, Map<String, Object?>> rows = {};

  _Collection copy() => _Collection(schema)
    ..rows.addAll({
      for (final e in rows.entries) e.key: Map<String, Object?>.of(e.value),
    });
}

final class _Watch {
  _Watch(this.collection, this.query, this.controller);

  final String collection;
  final DbQuery query;
  final StreamController<List<Map<String, Object?>>> controller;
  String? _last;

  void emit(_Collection c) {
    final rows = [
      for (final r in query.run(c.schema, c.rows.values))
        Map<String, Object?>.of(r),
    ];
    final fingerprint = jsonEncode(rows);
    if (fingerprint == _last || controller.isClosed) return;
    _last = fingerprint;
    controller.add(rows);
  }
}

/// The operations over the adapter's state: one set serves a single
/// operation, another a whole transaction.
final class _Ops implements DbOperations {
  _Ops(this.db, this.touched);

  final MemoryDatabaseAdapter db;
  final Set<String> touched;

  _Collection _c(String name) => db._need(name);

  Map<String, Object?>? getSync(String collection, String key) {
    final r = _c(collection).rows[key];
    return r == null ? null : Map<String, Object?>.of(r);
  }

  void insertSync(String collection, Map<String, Object?> record) {
    final c = _c(collection);
    final row = c.schema.checked(record);
    final key = c.schema.keyOf(row);
    if (c.rows.containsKey(key)) {
      throw const PluxException(
        PluxErrorCode.dbKeyConflict,
        'a record with that key exists',
      );
    }
    c.rows[key] = row;
    touched.add(collection);
  }

  void updateSync(String collection, String key, Map<String, Object?> patch) {
    final c = _c(collection);
    final old = c.rows[key];
    if (old == null) {
      throw const PluxException(
        PluxErrorCode.dbRecordNotFound,
        'no record has that key',
      );
    }
    final p = c.schema.checked(patch, partial: true);
    c.rows[key] = {...old, ...p};
    touched.add(collection);
  }

  void upsertSync(String collection, Map<String, Object?> record) {
    final c = _c(collection);
    final row = c.schema.checked(record);
    c.rows[c.schema.keyOf(row)] = row;
    touched.add(collection);
  }

  bool deleteSync(String collection, String key) {
    final removed = _c(collection).rows.remove(key) != null;
    if (removed) touched.add(collection);
    return removed;
  }

  List<Map<String, Object?>> querySync(String collection, DbQuery query) {
    final c = _c(collection);
    query.check(c.schema);
    return [
      for (final r in query.run(c.schema, c.rows.values))
        Map<String, Object?>.of(r),
    ];
  }

  int countSync(String collection, DbFilter? filter) {
    final c = _c(collection);
    filter?.check(c.schema);
    return c.rows.values.where((r) => filter?.matches(r) ?? true).length;
  }

  Object? kvGetSync(String namespace, String key) {
    final v = db._kv[namespace]?[key];
    return v == null ? null : jsonDecode(jsonEncode(v));
  }

  void kvSetSync(String namespace, String key, Object value) {
    final type = kvTypeOf(value);
    if (type == null) {
      throw const PluxException(
        PluxErrorCode.dbRecordInvalid,
        'a key-value entry holds a JSON value',
      );
    }
    final ns = db._kv.putIfAbsent(namespace, () => {});
    final old = ns[key];
    if (old != null && kvTypeOf(old) != type) {
      throw PluxException(
        PluxErrorCode.dbValueTypeMismatch,
        'key "$key" holds a ${kvTypeOf(old)}, not a $type',
      );
    }
    ns[key] = jsonDecode(jsonEncode(value));
  }

  bool kvRemoveSync(String namespace, String key) =>
      db._kv[namespace]?.remove(key) != null;

  Map<String, Object?> kvAllSync(String namespace) => {
    for (final e in (db._kv[namespace] ?? const <String, Object?>{}).entries)
      e.key: jsonDecode(jsonEncode(e.value)),
  };

  @override
  Future<Map<String, Object?>?> get(String collection, String key) async =>
      getSync(collection, key);

  @override
  Future<void> insert(String collection, Map<String, Object?> record) async =>
      insertSync(collection, record);

  @override
  Future<void> update(
    String collection,
    String key,
    Map<String, Object?> patch,
  ) async => updateSync(collection, key, patch);

  @override
  Future<void> upsert(String collection, Map<String, Object?> record) async =>
      upsertSync(collection, record);

  @override
  Future<bool> delete(String collection, String key) async =>
      deleteSync(collection, key);

  @override
  Future<List<Map<String, Object?>>> query(
    String collection,
    DbQuery query,
  ) async => querySync(collection, query);

  @override
  Future<int> count(String collection, [DbFilter? filter]) async =>
      countSync(collection, filter);

  @override
  Future<Object?> kvGet(String namespace, String key) async =>
      kvGetSync(namespace, key);

  @override
  Future<void> kvSet(String namespace, String key, Object value) async =>
      kvSetSync(namespace, key, value);

  @override
  Future<bool> kvRemove(String namespace, String key) async =>
      kvRemoveSync(namespace, key);

  @override
  Future<Map<String, Object?>> kvAll(String namespace) async =>
      kvAllSync(namespace);
}
