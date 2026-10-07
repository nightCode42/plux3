// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The Drift adapter (DB-002, ADR-0049): collections in SQLite, one table
/// per collection with a typed column per field, SQLCipher or
/// SQLite3MultipleCiphers encryption under a key made for this
/// installation and kept in the platform's secure storage, and all SQL run
/// on a background isolate (DB-007). Drift supplies the isolate and the
/// transactions; the schema, queries and migrations are the adapter's own,
/// so no code generation is involved.
library;

import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:isolate';

import 'package:drift/drift.dart';
import 'package:drift/native.dart';
import 'package:plux_flutter/plux_flutter.dart';
import 'package:sqlite3/sqlite3.dart' as sqlite;

/// Opens the database executor. [keyPragma] is the statement that sets the
/// encryption key, or null for an unencrypted database; the executor runs
/// it before anything else on its connection.
typedef DriftOpener = QueryExecutor Function(String? keyPragma);

/// The encryption setup of a connection, run on the database isolate: a
/// top-level function's closure, so that it captures nothing but the key.
sqlite.Database Function(sqlite.Database) _setup(String? keyHex) {
  return (db) {
    if (keyHex != null) {
      // SQLite3MultipleCiphers speaks SQLCipher's format when asked;
      // SQLCipher itself has no `cipher` pragma and needs no asking.
      final multiple =
          db.select('pragma cipher_version').isEmpty &&
          db.select('pragma cipher').isNotEmpty;
      if (multiple) db.execute("pragma cipher = 'sqlcipher'");
      db.execute('''pragma key = "x'$keyHex'"''');
    }
    db
      ..execute('pragma journal_mode = wal')
      ..execute('pragma synchronous = normal')
      ..execute('pragma secure_delete = on');
    return db;
  };
}

/// Whether the linked SQLite encrypts, probed on a throwaway isolate.
bool _probeCipherSync() {
  final db = sqlite.sqlite3.openInMemory();
  try {
    return db.select('pragma cipher_version').isNotEmpty ||
        db.select('pragma cipher').isNotEmpty;
  } finally {
    db.close();
  }
}

Future<bool> _probeCipher() => Isolate.run(_probeCipherSync);

String _hex(Uint8List bytes) =>
    bytes.map((b) => b.toRadixString(16).padLeft(2, '0')).join();

QueryExecutor _fileOpener(File file, String? keyPragma) {
  final hex = keyPragma == null
      ? null
      : RegExp(r"x'([0-9a-f]+)'").firstMatch(keyPragma)!.group(1);
  return NativeDatabase.createInBackground(
    file,
    enableMigrations: false,
    setup: (db) => _setup(hex)(db),
  );
}

/// A database with no tables of its own: Drift's executor, isolate and
/// transactions, with the adapter's SQL on top.
final class _Db extends GeneratedDatabase {
  _Db(super.executor);

  @override
  Iterable<TableInfo<Table, Object?>> get allTables => const [];

  @override
  int get schemaVersion => 1;
}

/// A collection's table and what the database remembers of its schema.
final class _Stored {
  _Stored(this.id, this.schema);

  final int id;
  final DbCollectionSchema schema;

  String get table => 'c$id';
}

final RegExp _identifier = RegExp(r'^[A-Za-z][A-Za-z0-9_]*$');

/// A [PluxDatabaseAdapter] over Drift and SQLite with SQLCipher.
///
/// The host app selects the encrypting SQLite build in its `pubspec.yaml`:
///
/// ```yaml
/// hooks:
///   user_defines:
///     sqlite3:
///       source: sqlcipher # or sqlite3mc
/// ```
///
/// With a plain SQLite the adapter still works, unencrypted, until the
/// app's security profile is `strict` or `maximum`: then [open] refuses
/// with `PLX-5202` and creates neither the database nor its key.
final class PluxDriftAdapter implements PluxDatabaseAdapter {
  /// Creates the adapter for the database file [path]; the platform's
  /// storage directory, under `plux-db/`, when null. [secrets] keeps the
  /// database key under the name [keyName] (the platform's secure storage
  /// when null). [onProblem] hears of a database that could not be
  /// decrypted and was discarded.
  PluxDriftAdapter({
    this.path,
    SecretStore? secrets,
    this.keyName = 'db-drift',
    void Function(PluxException problem)? onProblem,
  }) : _secrets = secrets ?? const PlatformSecretStore(),
       _onProblem = onProblem, // ignore: prefer_initializing_formals
       _opener = null,
       _probe = _probeCipher,
       _deleteStorage = null;

  /// Creates an adapter over an executor [opener] makes, for databases
  /// kept elsewhere than in a file, and for tests. [probeCipher] says
  /// whether the SQLite behind [opener] encrypts; [deleteStorage] removes
  /// what [opener] stores.
  PluxDriftAdapter.custom({
    required DriftOpener opener,
    required Future<bool> Function() probeCipher,
    required SecretStore secrets,
    Future<void> Function()? deleteStorage,
    this.keyName = 'db-drift',
    void Function(PluxException problem)? onProblem,
  }) : path = null,
       // ignore: prefer_initializing_formals
       _secrets = secrets,
       // ignore: prefer_initializing_formals
       _onProblem = onProblem,
       // ignore: prefer_initializing_formals
       _opener = opener,
       _probe = probeCipher,
       // ignore: prefer_initializing_formals
       _deleteStorage = deleteStorage;

  /// The database file, or null for the platform's default.
  final String? path;

  /// The name of the database key in secure storage.
  final String keyName;

  final SecretStore _secrets;
  final void Function(PluxException problem)? _onProblem;
  final DriftOpener? _opener;
  final Future<bool> Function() _probe;
  final Future<void> Function()? _deleteStorage;

  _Db? _db;
  bool _encrypted = false;
  final Map<String, _Stored> _stored = {};
  final List<_Watch> _watches = [];
  // Null while nothing ran: a future, even a completed one, delivers its
  // result in a microtask of the zone that created it, so a first future made
  // in a widget test's fake-async zone would stall a later caller's chain.
  Future<void>? _tail;

  @override
  bool get supportsCollections => true;

  @override
  bool get encrypted => _encrypted;

  // ── Lifecycle ───────────────────────────────────────────────────────────

  Future<R> _serial<R>(FutureOr<R> Function() fn) {
    final done = Completer<R>();
    final previous = _tail ?? Future<void>.value();
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

  PluxException _failed(String message) =>
      PluxException(PluxErrorCode.dbStoreFailed, message);

  PluxException _encryptionRequired(String why) =>
      PluxException(PluxErrorCode.dbEncryptionRequired, why);

  @override
  Future<void> open({required bool requireEncryption}) => _serial(() async {
    if (_db != null) {
      if (requireEncryption && !_encrypted) {
        throw _encryptionRequired(
          'the open database is not encrypted; use an encrypting SQLite',
        );
      }
      return;
    }
    final cipher = await _probe();
    if (!cipher && requireEncryption) {
      throw _encryptionRequired(
        'this SQLite cannot encrypt: select sqlcipher or sqlite3mc in the '
        'app\'s hooks.user_defines.sqlite3.source',
      );
    }
    Uint8List? key;
    if (cipher) {
      try {
        key = await installationKey(_secrets, keyName, create: true);
      } on StoreException catch (e) {
        throw _failed('the database key is unavailable: ${e.message}');
      }
    }
    await _openWith(key, requireEncryption: requireEncryption, retry: true);
  });

  Future<File> _file() async {
    final p = path;
    if (p != null) return File(p);
    final dir = await platformStorageDirectory();
    return File('$dir/plux-db/plux.db');
  }

  Future<void> _openWith(
    Uint8List? key, {
    required bool requireEncryption,
    required bool retry,
  }) async {
    final keyPragma = key == null ? null : '''pragma key = "x'${_hex(key)}'"''';
    final QueryExecutor executor;
    final custom = _opener;
    if (custom != null) {
      executor = custom(keyPragma);
    } else {
      final file = await _file();
      await file.parent.create(recursive: true);
      executor = _fileOpener(file, keyPragma);
    }
    final db = _Db(executor);
    try {
      final cipherRows = await db.customSelect('pragma cipher_version').get();
      final multiple = cipherRows.isEmpty
          ? await db.customSelect('pragma cipher').get()
          : cipherRows;
      final encrypting = key != null && multiple.isNotEmpty;
      if (requireEncryption && !encrypting) {
        await db.close();
        throw _encryptionRequired('the database could not be encrypted');
      }
      await db.customSelect('select count(*) from sqlite_master').get();
      _encrypted = encrypting;
      _db = db;
      await _createMeta();
      await _loadSchemas();
    } on PluxException {
      _db = null;
      rethrow;
    } on Object catch (e) {
      _db = null;
      try {
        await db.close();
      } on Object {
        // Already closed or never opened.
      }
      if (key != null && retry) {
        // The file does not decrypt under this installation's key: it is
        // another installation's, or the key was lost with a restore. It
        // is unreadable either way; start again, like the state stores do.
        await _removeStorage();
        _onProblem?.call(
          _failed('the database did not decrypt and was discarded'),
        );
        await _openWith(
          key,
          requireEncryption: requireEncryption,
          retry: false,
        );
        return;
      }
      throw _failed('the database could not be opened: ${e.runtimeType}');
    }
  }

  Future<void> _removeStorage() async {
    final custom = _deleteStorage;
    if (_opener != null) {
      await custom?.call();
      return;
    }
    final file = await _file();
    for (final suffix in const ['', '-wal', '-shm', '-journal']) {
      final f = File('${file.path}$suffix');
      if (f.existsSync()) await f.delete();
    }
  }

  Future<void> _createMeta() async {
    final db = _db!;
    await db.customStatement(
      'create table if not exists plux_meta ('
      'id integer primary key autoincrement, '
      'name text not null unique, '
      'schema text not null)',
    );
    await db.customStatement(
      'create table if not exists plux_kv ('
      'ns text not null, k text not null, v text not null, t text not null, '
      'primary key (ns, k)) without rowid',
    );
  }

  Future<void> _loadSchemas() async {
    _stored.clear();
    final rows = await _db!
        .customSelect('select id, name, schema from plux_meta')
        .get();
    for (final r in rows) {
      final schema = DbCollectionSchema.fromJson(
        (jsonDecode(r.read<String>('schema')) as Map).cast<String, Object?>(),
      );
      _stored[schema.name] = _Stored(r.read<int>('id'), schema);
    }
  }

  _Db get _open {
    final db = _db;
    if (db == null) throw _failed('the database is not open');
    return db;
  }

  @override
  Future<void> close() => _serial(() async {
    final db = _db;
    _db = null;
    for (final w in _watches.toList()) {
      w.stop();
    }
    _watches.clear();
    _stored.clear();
    if (db != null) await db.close();
  });

  // ── Migrations ──────────────────────────────────────────────────────────

  @override
  Future<List<DbMigrationOutcome>> migrate(List<DbCollectionSchema> schemas) =>
      _serial(() async {
        final out = <DbMigrationOutcome>[];
        final touched = <String>{};
        for (final s in schemas) {
          out.add(await _migrateOne(s, touched));
        }
        _publish(touched);
        return out;
      });

  Future<DbMigrationOutcome> _migrateOne(
    DbCollectionSchema target,
    Set<String> touched,
  ) async {
    final db = _open;
    final existing = _stored[target.name];
    try {
      _checkIdentifiers(target);
      if (existing == null) {
        final created = await db.transaction(() => _create(target));
        _stored[target.name] = created;
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
      if (stored.version >= target.version) {
        throw PluxException(
          PluxErrorCode.dbMigrationFailed,
          'collection ${target.key} is stored at version ${stored.version}, '
          'not below ${target.version}',
        );
      }
      final plan = DbMigrator.plan(stored, target);
      await db.transaction(() => _rebuild(existing, target, plan));
      _stored[target.name] = _Stored(existing.id, target);
      touched.add(target.name);
      return DbMigrationOutcome(
        collection: target.name,
        version: target.version,
      );
    } on Object catch (e) {
      // The transaction rolled back: the table and its schema are as they
      // were.
      return DbMigrationOutcome(
        collection: target.name,
        version: existing?.schema.version ?? 0,
        error: e is PluxException
            ? e
            : PluxException(
                PluxErrorCode.dbMigrationFailed,
                'the migration of ${target.key} failed in the database',
              ),
      );
    }
  }

  void _checkIdentifiers(DbCollectionSchema s) {
    for (final f in s.fields) {
      if (!_identifier.hasMatch(f.name)) {
        throw PluxException(
          PluxErrorCode.dbMigrationFailed,
          'collection ${s.key} has a field name the database cannot store',
        );
      }
    }
  }

  static String _column(DbField f) => switch (f.kind) {
    DbFieldKind.string => 'text',
    DbFieldKind.integer => 'integer',
    DbFieldKind.real => 'real',
    DbFieldKind.boolean => 'integer',
    DbFieldKind.json => 'text',
  };

  String _ddl(String table, DbCollectionSchema s) {
    final cols = [
      '"_key" text not null primary key',
      for (final f in s.fields)
        '"f_${f.name}" ${_column(f)}${f.nullable ? '' : ' not null'}',
    ];
    return 'create table "$table" (${cols.join(', ')})';
  }

  Future<void> _indexes(String table, DbCollectionSchema s) async {
    var n = 0;
    for (final index in s.indexes) {
      final cols = [for (final f in index) '"f_$f"'].join(', ');
      await _db!.customStatement(
        'create index "${table}_i${n++}" on "$table" ($cols)',
      );
    }
  }

  Future<_Stored> _create(DbCollectionSchema s) async {
    final db = _db!;
    await db.customUpdate(
      'insert into plux_meta (name, schema) values (?, ?)',
      variables: [
        Variable.withString(s.name),
        Variable.withString(jsonEncode(s.toJson())),
      ],
    );
    final id =
        (await db
                .customSelect(
                  'select id from plux_meta where name = ?',
                  variables: [Variable.withString(s.name)],
                )
                .getSingle())
            .read<int>('id');
    final stored = _Stored(id, s);
    await db.customStatement(_ddl(stored.table, s));
    await _indexes(stored.table, s);
    return stored;
  }

  /// Rebuilds a collection's table for [target]: rows are read in key
  /// order, converted by [plan] and written to a new table, which replaces
  /// the old one. Runs in one transaction: a failure leaves the old table.
  Future<void> _rebuild(
    _Stored old,
    DbCollectionSchema target,
    DbMigrator plan,
  ) async {
    final db = _db!;
    final fresh = '${old.table}_new';
    await db.customStatement('drop table if exists "$fresh"');
    await db.customStatement(_ddl(fresh, target));
    String? after;
    for (;;) {
      final rows = await db
          .customSelect(
            'select * from "${old.table}" '
            '${after == null ? '' : 'where "_key" > ? '}'
            'order by "_key" limit 500',
            variables: [if (after != null) Variable.withString(after)],
          )
          .get();
      if (rows.isEmpty) break;
      for (final r in rows) {
        final migrated = target.checked(plan.row(_readRow(old.schema, r.data)));
        await _insertRow(fresh, target, migrated);
        after = r.read<String>('_key');
      }
    }
    await db.customStatement('drop table "${old.table}"');
    await db.customStatement('alter table "$fresh" rename to "${old.table}"');
    await _indexes(old.table, target);
    await db.customUpdate(
      'update plux_meta set schema = ? where id = ?',
      variables: [
        Variable.withString(jsonEncode(target.toJson())),
        Variable.withInt(old.id),
      ],
    );
  }

  @override
  Future<void> dropCollections(List<String> names) => _serial(() async {
    final db = _open;
    final touched = <String>{};
    for (final n in names) {
      final s = _stored[n];
      if (s == null) continue;
      await db.transaction(() async {
        await db.customStatement('drop table "${s.table}"');
        await db.customUpdate(
          'delete from plux_meta where id = ?',
          variables: [Variable.withInt(s.id)],
        );
      });
      _stored.remove(n);
      touched.add(n);
    }
    _publish(touched);
  });

  // ── Rows ────────────────────────────────────────────────────────────────

  static Object? _toSql(DbField f, Object? v) {
    if (v == null) return null;
    return switch (f.kind) {
      DbFieldKind.boolean => (v as bool) ? 1 : 0,
      DbFieldKind.json => jsonEncode(v),
      _ => v,
    };
  }

  static Object? _fromSql(DbField f, Object? v) {
    if (v == null) return null;
    return switch (f.kind) {
      DbFieldKind.boolean => v == 1,
      DbFieldKind.json => jsonDecode(v as String),
      DbFieldKind.real => (v as num).toDouble(),
      _ => v,
    };
  }

  static Variable<Object> _variable(Object? v) => switch (v) {
    null => const Variable<String>(null) as Variable<Object>,
    final String s => Variable.withString(s),
    final int i => Variable.withInt(i),
    final double d => Variable.withReal(d),
    final bool b => Variable.withInt(b ? 1 : 0),
    _ => Variable.withString(jsonEncode(v)),
  };

  Map<String, Object?> _readRow(
    DbCollectionSchema s,
    Map<String, Object?> data,
  ) => {for (final f in s.fields) f.name: _fromSql(f, data['f_${f.name}'])};

  Future<void> _insertRow(
    String table,
    DbCollectionSchema s,
    Map<String, Object?> row,
  ) async {
    final names = ['"_key"', for (final f in s.fields) '"f_${f.name}"'];
    await _db!.customUpdate(
      'insert into "$table" (${names.join(', ')}) '
      'values (${List.filled(names.length, '?').join(', ')})',
      variables: [
        Variable.withString(s.keyOf(row)),
        for (final f in s.fields) _variable(_toSql(f, row[f.name])),
      ],
    );
  }

  _Stored _need(String collection) =>
      _stored[collection] ??
      (throw const PluxException(
        PluxErrorCode.dbCollectionUnknown,
        'no collection is stored under that name',
      ));

  // ── Queries ─────────────────────────────────────────────────────────────

  /// The SQL condition of [filter] over [s], binding into [vars].
  String _where(DbCollectionSchema s, DbFilter filter, List<Variable> vars) {
    switch (filter) {
      case DbAnd(:final filters):
        if (filters.isEmpty) return '1';
        return '(${filters.map((f) => _where(s, f, vars)).join(' and ')})';
      case DbOr(:final filters):
        if (filters.isEmpty) return '0';
        return '(${filters.map((f) => _where(s, f, vars)).join(' or ')})';
      case DbNot(:final filter):
        return 'not (${_where(s, filter, vars)})';
      case DbCompare(:final field, :final op, :final value):
        final f = s.field(field)!;
        final col = '"f_$field"';
        Variable bind(Object? v) => _variable(_toSql(f, v));
        switch (op) {
          case DbOp.isNull:
            return '$col is null';
          case DbOp.notNull:
            return '$col is not null';
          case DbOp.eq:
            vars.add(bind(value));
            return '$col is ?';
          case DbOp.ne:
            vars.add(bind(value));
            return '$col is not ?';
          case DbOp.lt || DbOp.le || DbOp.gt || DbOp.ge:
            vars.add(bind(value));
            final sym = switch (op) {
              DbOp.lt => '<',
              DbOp.le => '<=',
              DbOp.gt => '>',
              _ => '>=',
            };
            return '$col $sym ?';
          case DbOp.isIn:
            final items = value! as List<Object?>;
            final nonNull = [for (final v in items) ?v];
            final parts = <String>[
              if (nonNull.isNotEmpty)
                '$col in (${List.filled(nonNull.length, '?').join(', ')})',
              if (nonNull.length != items.length) '$col is null',
            ];
            for (final v in nonNull) {
              vars.add(bind(v));
            }
            return parts.isEmpty ? '0' : '(${parts.join(' or ')})';
          case DbOp.contains:
            vars.add(Variable.withString(value! as String));
            return 'instr($col, ?) > 0';
          case DbOp.startsWith:
            final prefix = value! as String;
            vars
              ..add(Variable.withString(prefix))
              ..add(Variable.withString(prefix));
            return 'substr($col, 1, length(?)) = ?';
        }
    }
  }

  String _order(DbCollectionSchema s, DbQuery q) => [
    for (final k in q.sort) '"f_${k.field}" ${k.descending ? 'desc' : 'asc'}',
    '"_key" asc',
  ].join(', ');

  Future<List<Map<String, Object?>>> _query(
    String collection,
    DbQuery query,
  ) async {
    final st = _need(collection);
    query.check(st.schema);
    final vars = <Variable>[];
    final where = query.filter == null
        ? ''
        : ' where ${_where(st.schema, query.filter!, vars)}';
    final limit = query.limit ?? -1;
    final rows = await _open
        .customSelect(
          'select * from "${st.table}"$where order by ${_order(st.schema, query)} '
          'limit ? offset ?',
          variables: [
            ...vars,
            Variable.withInt(limit),
            Variable.withInt(query.offset),
          ],
        )
        .get();
    return [for (final r in rows) _readRow(st.schema, r.data)];
  }

  @override
  Future<List<Map<String, Object?>>> query(String collection, DbQuery query) =>
      _query(collection, query);

  @override
  Future<int> count(String collection, [DbFilter? filter]) async {
    final st = _need(collection);
    filter?.check(st.schema);
    final vars = <Variable>[];
    final where = filter == null
        ? ''
        : ' where ${_where(st.schema, filter, vars)}';
    final r = await _open
        .customSelect(
          'select count(*) as n from "${st.table}"$where',
          variables: vars,
        )
        .getSingle();
    return r.read<int>('n');
  }

  @override
  Future<Map<String, Object?>?> get(String collection, String key) async {
    final st = _need(collection);
    final rows = await _open
        .customSelect(
          'select * from "${st.table}" where "_key" = ?',
          variables: [Variable.withString(key)],
        )
        .get();
    return rows.isEmpty ? null : _readRow(st.schema, rows.single.data);
  }

  // ── Writes ──────────────────────────────────────────────────────────────

  /// Runs [body] in a transaction and tells the watches of the collections
  /// it touched once it committed.
  Future<R> _write<R>(Future<R> Function(_Ops ops) body) async {
    final touched = <String>{};
    final r = await _open.transaction(() => body(_Ops(this, touched)));
    _publish(touched);
    return r;
  }

  @override
  Future<void> insert(String collection, Map<String, Object?> record) =>
      _write((o) => o.insert(collection, record));

  @override
  Future<void> update(
    String collection,
    String key,
    Map<String, Object?> patch,
  ) => _write((o) => o.update(collection, key, patch));

  @override
  Future<void> upsert(String collection, Map<String, Object?> record) =>
      _write((o) => o.upsert(collection, record));

  @override
  Future<bool> delete(String collection, String key) =>
      _write((o) => o.delete(collection, key));

  @override
  Future<Object?> kvGet(String namespace, String key) async {
    final rows = await _open
        .customSelect(
          'select v from plux_kv where ns = ? and k = ?',
          variables: [Variable.withString(namespace), Variable.withString(key)],
        )
        .get();
    return rows.isEmpty ? null : jsonDecode(rows.single.read<String>('v'));
  }

  @override
  Future<void> kvSet(String namespace, String key, Object value) =>
      _write((o) => o.kvSet(namespace, key, value));

  @override
  Future<bool> kvRemove(String namespace, String key) =>
      _write((o) => o.kvRemove(namespace, key));

  @override
  Future<Map<String, Object?>> kvAll(String namespace) async {
    final rows = await _open
        .customSelect(
          'select k, v from plux_kv where ns = ?',
          variables: [Variable.withString(namespace)],
        )
        .get();
    return {
      for (final r in rows)
        r.read<String>('k'): jsonDecode(r.read<String>('v')),
    };
  }

  @override
  Future<R> transaction<R>(Future<R> Function(DbOperations tx) body) =>
      _write(body);

  // ── Watches ─────────────────────────────────────────────────────────────

  @override
  Stream<List<Map<String, Object?>>> watch(String collection, DbQuery query) {
    late final _Watch w;
    // ignore: close_sinks
    final c = StreamController<List<Map<String, Object?>>>(
      onListen: () {
        _watches.add(w);
        w.refresh();
      },
      onCancel: () {
        w.stop();
        _watches.remove(w);
        return null;
      },
    );
    w = _Watch(this, collection, query, c);
    return c.stream;
  }

  void _publish(Set<String> touched) {
    for (final w in _watches.toList()) {
      if (touched.contains(w.collection)) w.refresh();
    }
  }

  // ── Wiping ──────────────────────────────────────────────────────────────

  @override
  Future<void> wipe({String? namespace}) => _serial(() async {
    if (namespace == null) {
      final db = _db;
      _db = null;
      _stored.clear();
      for (final w in _watches.toList()) {
        w.stop();
      }
      _watches.clear();
      if (db != null) await db.close();
      await _removeStorage();
      try {
        await _secrets.delete(keyName);
      } on Object {
        throw _failed('the database key could not be removed');
      }
      return;
    }
    if (_db == null) {
      // Nothing is open: if a database exists, open it to clear it.
      final exists = _opener != null || (await _file()).existsSync();
      if (!exists) return;
      final cipher = await _probe();
      Uint8List? key;
      if (cipher) {
        try {
          key = await installationKey(_secrets, keyName, create: false);
        } on StoreException {
          return;
        }
        if (key == null) return;
      }
      await _openWith(key, requireEncryption: false, retry: false);
    }
    final db = _open;
    final touched = <String>{};
    await db.transaction(() async {
      for (final st in _stored.values) {
        if (st.schema.namespace != namespace) continue;
        await db.customUpdate('delete from "${st.table}"');
        touched.add(st.schema.name);
      }
      await db.customUpdate(
        'delete from plux_kv where ns = ?',
        variables: [Variable.withString(namespace)],
      );
    });
    _publish(touched);
  });
}

/// The record operations of one transaction.
final class _Ops implements DbOperations {
  _Ops(this.a, this.touched);

  final PluxDriftAdapter a;
  final Set<String> touched;

  _Db get _db => a._open;

  @override
  Future<Map<String, Object?>?> get(String collection, String key) =>
      a.get(collection, key);

  @override
  Future<List<Map<String, Object?>>> query(String collection, DbQuery query) =>
      a._query(collection, query);

  @override
  Future<int> count(String collection, [DbFilter? filter]) =>
      a.count(collection, filter);

  @override
  Future<void> insert(String collection, Map<String, Object?> record) async {
    final st = a._need(collection);
    final row = st.schema.checked(record);
    final key = st.schema.keyOf(row);
    if (await a.get(collection, key) != null) {
      throw const PluxException(
        PluxErrorCode.dbKeyConflict,
        'a record with that key exists',
      );
    }
    await a._insertRow(st.table, st.schema, row);
    touched.add(collection);
  }

  @override
  Future<void> update(
    String collection,
    String key,
    Map<String, Object?> patch,
  ) async {
    final st = a._need(collection);
    final p = st.schema.checked(patch, partial: true);
    const missing = PluxException(
      PluxErrorCode.dbRecordNotFound,
      'no record has that key',
    );
    if (p.isEmpty) {
      if (await a.get(collection, key) == null) throw missing;
      return;
    }
    final fields = [for (final name in p.keys) st.schema.field(name)!];
    final n = await _db.customUpdate(
      'update "${st.table}" set '
      '${fields.map((f) => '"f_${f.name}" = ?').join(', ')} '
      'where "_key" = ?',
      variables: [
        for (final f in fields)
          PluxDriftAdapter._variable(PluxDriftAdapter._toSql(f, p[f.name])),
        Variable.withString(key),
      ],
    );
    if (n == 0) throw missing;
    touched.add(collection);
  }

  @override
  Future<void> upsert(String collection, Map<String, Object?> record) async {
    final st = a._need(collection);
    final row = st.schema.checked(record);
    final key = st.schema.keyOf(row);
    await _db.customUpdate(
      'delete from "${st.table}" where "_key" = ?',
      variables: [Variable.withString(key)],
    );
    await a._insertRow(st.table, st.schema, row);
    touched.add(collection);
  }

  @override
  Future<bool> delete(String collection, String key) async {
    final st = a._need(collection);
    final n = await _db.customUpdate(
      'delete from "${st.table}" where "_key" = ?',
      variables: [Variable.withString(key)],
    );
    if (n > 0) touched.add(collection);
    return n > 0;
  }

  @override
  Future<Object?> kvGet(String namespace, String key) =>
      a.kvGet(namespace, key);

  @override
  Future<Map<String, Object?>> kvAll(String namespace) => a.kvAll(namespace);

  @override
  Future<void> kvSet(String namespace, String key, Object value) async {
    final type = kvTypeOf(value);
    if (type == null) {
      throw const PluxException(
        PluxErrorCode.dbRecordInvalid,
        'a key-value entry holds a JSON value',
      );
    }
    final old = await _db
        .customSelect(
          'select t from plux_kv where ns = ? and k = ?',
          variables: [Variable.withString(namespace), Variable.withString(key)],
        )
        .get();
    if (old.isNotEmpty && old.single.read<String>('t') != type) {
      throw PluxException(
        PluxErrorCode.dbValueTypeMismatch,
        'key "$key" holds a ${old.single.read<String>('t')}, not a $type',
      );
    }
    await _db.customUpdate(
      'insert or replace into plux_kv (ns, k, v, t) values (?, ?, ?, ?)',
      variables: [
        Variable.withString(namespace),
        Variable.withString(key),
        Variable.withString(jsonEncode(value)),
        Variable.withString(type),
      ],
    );
  }

  @override
  Future<bool> kvRemove(String namespace, String key) async =>
      await _db.customUpdate(
        'delete from plux_kv where ns = ? and k = ?',
        variables: [Variable.withString(namespace), Variable.withString(key)],
      ) >
      0;
}

/// A watched query: runs again after a write to its collection, one run at
/// a time, and delivers a result only when it changed.
final class _Watch {
  _Watch(this.a, this.collection, this.query, this.controller);

  final PluxDriftAdapter a;
  final String collection;
  final DbQuery query;
  final StreamController<List<Map<String, Object?>>> controller;

  bool _running = false;
  bool _dirty = false;
  bool _stopped = false;
  String? _last;

  void stop() => _stopped = true;

  void refresh() {
    if (_stopped) return;
    if (_running) {
      _dirty = true;
      return;
    }
    _running = true;
    unawaited(_run());
  }

  Future<void> _run() async {
    try {
      do {
        _dirty = false;
        final rows = await a._query(collection, query);
        if (_stopped) return;
        final fingerprint = jsonEncode(rows);
        if (fingerprint != _last) {
          _last = fingerprint;
          controller.add(rows);
        }
      } while (_dirty && !_stopped);
    } on Object catch (e, s) {
      if (!_stopped && !controller.isClosed) controller.addError(e, s);
    } finally {
      _running = false;
    }
  }
}
