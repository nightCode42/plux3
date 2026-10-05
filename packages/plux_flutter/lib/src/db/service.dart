// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The runtime's local database (DB-001 to DB-009): the layer between the
/// action handlers and a [PluxDatabaseAdapter]. It resolves a plugin's
/// collections — its own private ones and the app's shared ones, never
/// another plugin's (DB-004) — names them for the adapter, converts PXL
/// values to the JSON the adapter stores, enforces the limits (LIM-004),
/// runs the migrations of the active release before the first use of a
/// namespace (DB-005) and tracks watched queries so that only the rows
/// that changed are new objects (DB-006).
library;

import 'dart:async';
import 'dart:convert';

import 'package:plux_flutter/src/data/failure.dart';
import 'package:plux_flutter/src/data/mapping.dart' show mapJson;
import 'package:plux_flutter/src/db/adapter.dart';
import 'package:plux_flutter/src/db/query.dart';
import 'package:plux_flutter/src/db/schema.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/pxl/types.dart';
import 'package:plux_flutter/src/schema/limits.g.dart';

/// What the active release declares for the database: the collections of
/// each bundle, its limits and security profile.
abstract interface class DbDeclarations {
  /// The release sequence; declarations of another release are another
  /// object.
  int get sequence;

  /// Whether the security profile requires an encrypted database
  /// (`strict`, `maximum`; DB-002).
  bool get requireEncryption;

  /// The limits of the app bundle (LIM-004), by key.
  Map<String, int> get limits;

  /// The collections plugin [plugin] ("" for the app) declares itself,
  /// named for the adapter.
  List<DbCollectionSchema> collectionsOf(String plugin);

  /// The physical names of the collections the bundle of [plugin] drops.
  List<String> droppedOf(String plugin);

  /// The named types visible to [plugin], for converting values.
  NamedType? Function(String name) typesOf(String plugin);
}

/// The namespace of a plugin's collections and key-value entries: `app`
/// for the app, `plugin.<key>` for a plugin.
String dbNamespace(String plugin) => plugin.isEmpty ? 'app' : 'plugin.$plugin';

/// The rows of a watched query after a change (DB-006).
final class DbRows {
  /// Creates a result.
  const DbRows(this.rows, this.changed, this.removed);

  /// The rows in query order, as PXL values. A row that did not change
  /// since the previous result is the same object.
  final List<Object?> rows;

  /// The keys of the rows that are new or changed.
  final Set<String> changed;

  /// The keys of the rows that left the result.
  final Set<String> removed;
}

/// The runtime's local database.
final class PluxDatabase {
  /// Creates the database over [adapter]; [declarations] gives the active
  /// release's, or null before one is active; [report] hears of problems
  /// that do not fail the operation that found them, such as a migration
  /// that failed.
  PluxDatabase({
    required this.adapter,
    required DbDeclarations? Function() declarations,
    required void Function(PluxException error) report,
  }) : _declarations = declarations, // ignore: prefer_initializing_formals
       _report = report; // ignore: prefer_initializing_formals

  /// The adapter.
  final PluxDatabaseAdapter adapter;

  final DbDeclarations? Function() _declarations;
  final void Function(PluxException error) _report;

  DbDeclarations? _prepared;
  final Map<String, Future<_Namespace>> _ready = {};
  bool? _openFor;
  Future<void>? _opening;

  Future<DbDeclarations> _current() async {
    final d = _declarations();
    if (d == null) {
      throw const PluxException(
        PluxErrorCode.dbUnavailable,
        'no release is active, so no collection is declared',
      );
    }
    if (!identical(d, _prepared)) {
      _prepared = d;
      _ready.clear();
    }
    return d;
  }

  Future<void> _open(bool requireEncryption) async {
    if (_openFor != null && (_openFor! || !requireEncryption)) return;
    try {
      await (_opening ??= adapter.open(requireEncryption: requireEncryption));
      _openFor = requireEncryption;
    } finally {
      _opening = null;
    }
  }

  Future<_Namespace> _namespace(DbDeclarations d, String plugin) async {
    var f = _ready[plugin];
    if (f == null) {
      f = _prepare(d, plugin);
      _ready[plugin] = f;
    }
    try {
      return await f;
    } on Object {
      // A failed preparation is tried again by the next operation.
      _ready.removeWhere((k, v) => k == plugin && identical(v, f));
      rethrow;
    }
  }

  Future<_Namespace> _prepare(DbDeclarations d, String plugin) async {
    await _open(d.requireEncryption);
    final schemas = d.collectionsOf(plugin);
    final dropped = d.droppedOf(plugin);
    final ns = _Namespace({for (final s in schemas) s.key: s});
    if ((schemas.isNotEmpty || dropped.isNotEmpty) &&
        !adapter.supportsCollections) {
      throw const PluxException(
        PluxErrorCode.dbUnavailable,
        'the app declares collections but its database adapter stores none; '
        'add plux_db_drift or set PluxConfig.databaseAdapter',
      );
    }
    if (dropped.isNotEmpty) await adapter.dropCollections(dropped);
    for (final o in await adapter.migrate(schemas)) {
      if (o.ok) continue;
      final e = o.error is PluxException
          ? o.error! as PluxException
          : const PluxException(
              PluxErrorCode.dbMigrationFailed,
              'a collection could not be migrated',
            );
      ns.failed[o.collection] = e;
      _report(e);
    }
    return ns;
  }

  /// The collection [collection] as plugin [plugin] sees it: its own, or
  /// the app's. Never another plugin's (DB-004).
  Future<_Resolved> _resolve(String plugin, String collection) async {
    final d = await _current();
    final own = await _namespace(d, plugin);
    var ns = own;
    var schema = own.byKey[collection];
    if (schema == null && plugin.isNotEmpty) {
      ns = await _namespace(d, '');
      schema = ns.byKey[collection];
    }
    if (schema == null) {
      throw PluxException(
        PluxErrorCode.dbCollectionUnknown,
        'no collection "$collection" is declared for this plugin or the app',
      );
    }
    final failed = ns.failed[schema.name];
    if (failed != null) {
      throw PluxException(
        PluxErrorCode.dbMigrationFailed,
        'collection "$collection" stays at its previous version because its '
        'migration failed: ${failed.message}',
      );
    }
    return _Resolved(schema, d, d.typesOf(ns == own ? plugin : ''), plugin);
  }

  int _limit(DbDeclarations d, PluxLimit l) =>
      d.limits[l.key] ?? l.defaultValue;

  // ── Records ─────────────────────────────────────────────────────────────

  /// Adds [record], a PXL value, to [collection] and returns its key.
  Future<String> insert(
    String plugin,
    String collection,
    Map<String, Object?> record,
  ) async {
    final r = await _resolve(plugin, collection);
    final row = r.toStored(record);
    r.checkSize(row);
    final key = r.schema.keyOf(row);
    await adapter.transaction((tx) async {
      await _checkCount(tx, r, adding: true);
      await tx.insert(r.schema.name, row);
    });
    return key;
  }

  /// Replaces the fields in [patch] of the record with [key].
  Future<void> update(
    String plugin,
    String collection,
    String key,
    Map<String, Object?> patch,
  ) async {
    final r = await _resolve(plugin, collection);
    final stored = r.toStored(patch, partial: true);
    await adapter.transaction((tx) async {
      final old = await tx.get(r.schema.name, key);
      if (old == null) {
        throw const PluxException(
          PluxErrorCode.dbRecordNotFound,
          'no record has that key',
        );
      }
      r.checkSize({...old, ...stored});
      await tx.update(r.schema.name, key, stored);
    });
  }

  /// Adds [record] or replaces the record with [key].
  Future<void> upsert(
    String plugin,
    String collection,
    String key,
    Map<String, Object?> record,
  ) async {
    final r = await _resolve(plugin, collection);
    final withKey = r.withKey(record, key);
    final row = r.toStored(withKey);
    if (r.schema.keyOf(row) != key) {
      throw const PluxException(
        PluxErrorCode.dbRecordInvalid,
        'the record has another key than the one given',
      );
    }
    r.checkSize(row);
    await adapter.transaction((tx) async {
      await _checkCount(
        tx,
        r,
        adding: await tx.get(r.schema.name, key) == null,
      );
      await tx.upsert(r.schema.name, row);
    });
  }

  /// Removes the record with [key]; true if there was one.
  Future<bool> delete(String plugin, String collection, String key) async {
    final r = await _resolve(plugin, collection);
    return adapter.delete(r.schema.name, key);
  }

  Future<void> _checkCount(
    DbOperations tx,
    _Resolved r, {
    required bool adding,
  }) async {
    if (!adding) return;
    final max = _limit(r.declarations, PluxLimit.dbCollectionRecords);
    if (await tx.count(r.schema.name) >= max) {
      throw PluxException(
        PluxErrorCode.dbLimitExceeded,
        'collection ${r.schema.key} holds db.collectionRecords = $max records',
        details: {'limit': PluxLimit.dbCollectionRecords.key},
      );
    }
  }

  /// The records of [collection] that match, as PXL values. [where], when
  /// given, is evaluated on each record after the adapter has sorted
  /// them; it makes sorting and paging happen here, over at most
  /// `db.queryRows` records beyond [offset].
  Future<List<Object?>> query(
    String plugin,
    String collection, {
    bool Function(Object? record)? where,
    DbFilter? filter,
    String? orderBy,
    bool descending = false,
    int limit = 100,
    int offset = 0,
  }) async {
    final r = await _resolve(plugin, collection);
    final cap = _limit(r.declarations, PluxLimit.dbQueryRows);
    if (limit < 0 || offset < 0) {
      throw const PluxException(
        PluxErrorCode.dbQueryInvalid,
        'the limit and the offset are not negative',
      );
    }
    final take = limit < cap ? limit : cap;
    final sort = [if (orderBy != null) DbSort(orderBy, descending: descending)];
    final q = DbQuery(
      filter: filter,
      sort: sort,
      limit: where == null ? take : null,
      offset: where == null ? offset : 0,
    );
    q.check(r.schema);
    final rows = await adapter.query(r.schema.name, q);
    final out = <Object?>[];
    var skipped = 0;
    for (final row in rows) {
      final value = r.fromStored(row);
      if (where != null) {
        if (!_matches(where, value)) continue;
        if (skipped < offset) {
          skipped++;
          continue;
        }
        if (out.length >= take) break;
      }
      out.add(value);
    }
    return out;
  }

  bool _matches(bool Function(Object?) where, Object? record) {
    try {
      return where(record);
    } on PluxException {
      rethrow;
    } on Object {
      throw const PluxException(
        PluxErrorCode.dbQueryInvalid,
        'the query condition could not be evaluated',
      );
    }
  }

  /// A watched query (DB-006): the rows of [collection] matching
  /// [filter], now and after every change, as [DbRows] whose unchanged
  /// rows are the objects of the previous result. The adapter does the
  /// work off the UI isolate (DB-007). Closing the subscription stops it.
  Stream<DbRows> watch(
    String plugin,
    String collection, {
    DbFilter? filter,
    String? orderBy,
    bool descending = false,
    int? limit,
    int offset = 0,
  }) async* {
    final r = await _resolve(plugin, collection);
    final cap = _limit(r.declarations, PluxLimit.dbQueryRows);
    final q = DbQuery(
      filter: filter,
      sort: [if (orderBy != null) DbSort(orderBy, descending: descending)],
      limit: limit == null || limit > cap ? cap : limit,
      offset: offset,
    );
    q.check(r.schema);
    final tracker = _RowTracker(r);
    await for (final rows in adapter.watch(r.schema.name, q)) {
      yield tracker.next(rows);
    }
  }

  // ── Key-value store ─────────────────────────────────────────────────────

  Future<(DbDeclarations?, String)> _kv(String plugin) async {
    final d = _declarations();
    await _open(d?.requireEncryption ?? false);
    return (d, dbNamespace(plugin));
  }

  /// The value of [key] in plugin [plugin]'s key-value store, or null
  /// (DB-009).
  Future<Object?> kvGet(String plugin, String key) async {
    final (_, ns) = await _kv(plugin);
    return adapter.kvGet(ns, key);
  }

  /// Stores [value] under [key]; the first write fixes the key's type.
  Future<void> kvSet(String plugin, String key, Object? value) async {
    if (value == null) {
      throw const PluxException(
        PluxErrorCode.dbRecordInvalid,
        'a key-value entry cannot be null; remove the key instead',
      );
    }
    final json = toJson(value);
    final (d, ns) = await _kv(plugin);
    final max =
        d?.limits[PluxLimit.dbKvBytes.key] ?? PluxLimit.dbKvBytes.defaultValue;
    await adapter.transaction((tx) async {
      final all = await tx.kvAll(ns);
      all[key] = json;
      if (utf8.encode(jsonEncode(all)).length > max) {
        throw PluxException(
          PluxErrorCode.dbLimitExceeded,
          'the key-value store of a plugin holds db.kvBytes = $max bytes',
          details: {'limit': PluxLimit.dbKvBytes.key},
        );
      }
      await tx.kvSet(ns, key, json!);
    });
  }

  /// Removes [key]; true if it existed.
  Future<bool> kvRemove(String plugin, String key) async {
    final (_, ns) = await _kv(plugin);
    return adapter.kvRemove(ns, key);
  }

  // ── Wiping ──────────────────────────────────────────────────────────────

  /// Removes the data Plux keeps in the database (DB-008): that of
  /// [plugin] — its collections' records and key-value entries — or, with
  /// no plugin, everything including the app's shared collections and the
  /// database's key. Collections are created again, empty, on next use.
  Future<void> wipe({String? plugin}) async {
    if (plugin == null) {
      await adapter.wipe();
      _ready.clear();
      _openFor = null;
      _opening = null;
      return;
    }
    await adapter.wipe(namespace: dbNamespace(plugin));
  }
}

final class _Namespace {
  _Namespace(this.byKey);

  final Map<String, DbCollectionSchema> byKey;
  final Map<String, PluxException> failed = {};
}

/// A collection resolved for one plugin, with the conversions between PXL
/// values and stored JSON.
final class _Resolved {
  _Resolved(this.schema, this.declarations, this._lookup, this.plugin);

  final DbCollectionSchema schema;
  final DbDeclarations declarations;
  final NamedType? Function(String) _lookup;
  final String plugin;

  final Map<String, PxlType> _types = {};

  PxlType _type(DbField f) => _types[f.name] ??= PxlType.parse(f.type, _lookup);

  /// Converts a PXL record to the JSON the adapter stores, checking each
  /// value against its field's type.
  Map<String, Object?> toStored(
    Map<String, Object?> record, {
    bool partial = false,
  }) {
    final out = <String, Object?>{};
    for (final e in record.entries) {
      final f = schema.field(e.key);
      if (f == null) {
        throw PluxException(
          PluxErrorCode.dbRecordInvalid,
          'field "${e.key}" is not a field of ${schema.key}',
        );
      }
      final json = toJson(e.value);
      try {
        mapJson(_type(f), json, f.name);
      } on DataFailure catch (x) {
        throw PluxException(PluxErrorCode.dbRecordInvalid, x.message);
      } on FormatException {
        throw PluxException(
          PluxErrorCode.dbRecordInvalid,
          'field "${f.name}" has a type this runtime cannot read',
        );
      }
      out[e.key] = json;
    }
    return schema.checked(out, partial: partial);
  }

  /// Converts a stored row to a PXL record.
  Map<String, Object?> fromStored(Map<String, Object?> row) {
    try {
      return {
        for (final f in schema.fields)
          f.name: mapJson(_type(f), row[f.name], f.name),
      };
    } on DataFailure catch (x) {
      throw PluxException(
        PluxErrorCode.dbRecordInvalid,
        'a stored record: ${x.message}',
      );
    }
  }

  /// [record] with the key field filled from [key] when it lacks it and
  /// the key is a single field.
  Map<String, Object?> withKey(Map<String, Object?> record, String key) {
    if (schema.primaryKey.length != 1) return record;
    final name = schema.primaryKey.single;
    if (record.containsKey(name)) return record;
    final f = schema.field(name)!;
    final value = f.kind == DbFieldKind.integer ? int.tryParse(key) : key;
    return {...record, name: value};
  }

  /// Fails with `PLX-5207` when [row] is larger than `db.recordBytes`.
  void checkSize(Map<String, Object?> row) {
    final max =
        declarations.limits[PluxLimit.dbRecordBytes.key] ??
        PluxLimit.dbRecordBytes.defaultValue;
    if (utf8.encode(jsonEncode(row)).length > max) {
      throw PluxException(
        PluxErrorCode.dbLimitExceeded,
        'a record of ${schema.key} exceeds db.recordBytes = $max bytes',
        details: {'limit': PluxLimit.dbRecordBytes.key},
      );
    }
  }
}

/// Keeps the previous result of a watched query so that a row that did
/// not change is not converted again and stays the same object, which is
/// what lets a list rebuild only the items that changed (DB-006).
final class _RowTracker {
  _RowTracker(this.r);

  final _Resolved r;
  Map<String, (String, Object?)> _previous = {};

  DbRows next(List<Map<String, Object?>> rows) {
    final current = <String, (String, Object?)>{};
    final changed = <String>{};
    final out = <Object?>[];
    for (final row in rows) {
      final key = r.schema.keyOf(row);
      final fingerprint = jsonEncode(row);
      final old = _previous[key];
      final entry = old != null && old.$1 == fingerprint
          ? old
          : (fingerprint, r.fromStored(row));
      if (!identical(entry, old)) changed.add(key);
      current[key] = entry;
      out.add(entry.$2);
    }
    final removed = _previous.keys
        .where((k) => !current.containsKey(k))
        .toSet();
    _previous = current;
    return DbRows(out, changed, removed);
  }
}
