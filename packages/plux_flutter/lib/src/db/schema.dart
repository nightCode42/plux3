// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// What the database layer knows of a collection (DB-004, DB-005): its
/// typed fields, key and indexes as the bundle declares them, the plans
/// that take older versions to it, and the rules every adapter shares for
/// checking a record and naming its key. Adapters store JSON records; the
/// conversion to and from PXL values belongs to the action handlers.
library;

import 'dart:convert';

import 'package:plux_flutter/src/errors/plux_exception.dart';

/// How an adapter stores a field: the scalar kinds it can sort and
/// compare, and `json` for everything else (lists, maps, objects, money).
enum DbFieldKind {
  /// Text; also dates, colors, assets, routes, decimals and enum members.
  string,

  /// A 64-bit integer; also durations in milliseconds.
  integer,

  /// A floating-point number.
  real,

  /// A boolean.
  boolean,

  /// Any other JSON value; compared for equality only.
  json,
}

/// A typed field of a collection.
final class DbField {
  /// Creates a field of the type expression [type].
  DbField(this.name, this.type)
    : nullable = type.endsWith('?'),
      kind = _kindOf(type);

  /// The field's name.
  final String name;

  /// The PXL type expression, such as `string`, `int?` or `list<string>`.
  final String type;

  /// Whether null is a value of the field.
  final bool nullable;

  /// How adapters store the field.
  final DbFieldKind kind;

  static DbFieldKind _kindOf(String type) {
    final t = type.endsWith('?') ? type.substring(0, type.length - 1) : type;
    return switch (t) {
      'string' ||
      'date' ||
      'dateTime' ||
      'color' ||
      'asset' ||
      'route' ||
      'decimal' => DbFieldKind.string,
      'int' || 'duration' => DbFieldKind.integer,
      'double' => DbFieldKind.real,
      'bool' => DbFieldKind.boolean,
      _ => DbFieldKind.json,
    };
  }

  @override
  bool operator ==(Object other) =>
      other is DbField && other.name == name && other.type == type;

  @override
  int get hashCode => Object.hash(name, type);

  @override
  String toString() => '$name: $type';
}

/// The plan that takes a collection from version [from] to the next
/// (DB-005).
final class DbMigrationPlan {
  /// Creates a plan.
  const DbMigrationPlan({
    required this.from,
    this.rename = const {},
    this.drop = const [],
    this.reset = const [],
  });

  /// The version the plan starts from.
  final int from;

  /// Fields renamed, as new name to old name; their values are kept.
  final Map<String, String> rename;

  /// Fields of the older version that are removed.
  final List<String> drop;

  /// Fields whose values start again from the type's empty value.
  final List<String> reset;
}

/// A collection as a bundle declares it.
final class DbCollectionSchema {
  /// Creates a schema; [name] is the physical name the adapter stores the
  /// collection under (`<namespace>/<collection id>`).
  DbCollectionSchema({
    required this.name,
    required this.id,
    required this.key,
    required this.version,
    required this.fields,
    required this.primaryKey,
    this.indexes = const [],
    this.migrations = const [],
  }) : _byName = {for (final f in fields) f.name: f};

  /// Rebuilds a schema from [DbCollectionSchema.toJson].
  factory DbCollectionSchema.fromJson(Map<String, Object?> json) =>
      DbCollectionSchema(
        name: json['name']! as String,
        id: json['id']! as String,
        key: json['key']! as String,
        version: json['version']! as int,
        fields: [
          for (final f
              in (json['fields']! as List<Object?>).cast<List<Object?>>())
            DbField(f[0]! as String, f[1]! as String),
        ],
        primaryKey: (json['primaryKey']! as List<Object?>).cast<String>(),
        indexes: [
          for (final i in (json['indexes']! as List<Object?>))
            (i! as List<Object?>).cast<String>(),
        ],
      );

  /// The physical name: `<namespace>/<collection id>`.
  final String name;

  /// The collection's UUID.
  final String id;

  /// The collection's key in documents.
  final String key;

  /// The schema version.
  final int version;

  /// The fields, in declaration order.
  final List<DbField> fields;

  /// The names of the primary key fields.
  final List<String> primaryKey;

  /// Indexes, each a list of field names.
  final List<List<String>> indexes;

  /// The plans from older versions.
  final List<DbMigrationPlan> migrations;

  final Map<String, DbField> _byName;

  /// The field [name], or null.
  DbField? field(String name) => _byName[name];

  /// The namespace part of [name]: `app`, or `plugin.<key>`.
  String get namespace => name.substring(0, name.indexOf('/'));

  /// The schema as JSON, for the adapter to remember what it stored.
  Map<String, Object?> toJson() => {
    'name': name,
    'id': id,
    'key': key,
    'version': version,
    'fields': [
      for (final f in fields) [f.name, f.type],
    ],
    'primaryKey': primaryKey,
    'indexes': indexes,
  };

  /// Whether [other] stores the same fields, key and indexes.
  bool sameStorage(DbCollectionSchema other) =>
      _sameList(fields, other.fields) &&
      _sameList(primaryKey, other.primaryKey) &&
      indexes.length == other.indexes.length &&
      [
        for (var i = 0; i < indexes.length; i++)
          _sameList(indexes[i], other.indexes[i]),
      ].every((b) => b);

  /// The key of [record] in canonical text: the key field's value, or for
  /// a composite key a JSON list of its fields' values.
  String keyOf(Map<String, Object?> record) {
    if (primaryKey.length == 1) return '${record[primaryKey.single]}';
    return jsonEncode([for (final k in primaryKey) record[k]]);
  }

  /// Checks [record] against the fields and returns it normalized (whole
  /// numbers of `double` fields become doubles). With [partial] fields may
  /// be missing, as in a patch. Throws `PLX-5203`, naming the field, never
  /// its value.
  Map<String, Object?> checked(
    Map<String, Object?> record, {
    bool partial = false,
  }) {
    final out = <String, Object?>{};
    for (final name in record.keys) {
      if (!_byName.containsKey(name)) {
        throw _invalid('field "$name" is not a field of $key');
      }
    }
    for (final f in fields) {
      if (!record.containsKey(f.name)) {
        if (partial) continue;
        if (!f.nullable) throw _invalid('field "${f.name}" of $key is missing');
        out[f.name] = null;
        continue;
      }
      out[f.name] = _checkValue(f, record[f.name]);
    }
    if (partial) {
      for (final k in primaryKey) {
        if (out.containsKey(k)) {
          // The key of a record is its identity; a patch cannot move it.
          throw _invalid('the key field "$k" of $key cannot be patched');
        }
      }
    }
    return out;
  }

  Object? _checkValue(DbField f, Object? v) {
    if (v == null) {
      if (f.nullable) return null;
      throw _invalid('field "${f.name}" of $key is null, expected ${f.type}');
    }
    final ok = switch (f.kind) {
      DbFieldKind.string => v is String,
      DbFieldKind.integer => v is int,
      DbFieldKind.real => v is num,
      DbFieldKind.boolean => v is bool,
      DbFieldKind.json => _isJson(v),
    };
    if (!ok) throw _invalid('field "${f.name}" of $key is not a ${f.type}');
    return f.kind == DbFieldKind.real ? (v as num).toDouble() : v;
  }

  PluxException _invalid(String message) =>
      PluxException(PluxErrorCode.dbRecordInvalid, message);

  static bool _isJson(Object? v) => switch (v) {
    null || bool() || num() || String() => true,
    final List<Object?> l => l.every(_isJson),
    final Map<String, Object?> m => m.values.every(_isJson),
    _ => false,
  };

  static bool _sameList<T>(List<T> a, List<T> b) {
    if (a.length != b.length) return false;
    for (var i = 0; i < a.length; i++) {
      if (a[i] != b[i]) return false;
    }
    return true;
  }

  /// The empty value of a reset field: 0, 0.0, "", false, [] or {}, or null
  /// where the field is nullable.
  static Object? emptyValue(DbField f) {
    if (f.nullable) return null;
    return switch (f.type) {
      'int' => 0,
      'double' => 0.0,
      'bool' => false,
      'string' => '',
      final String t when t.startsWith('list<') => <Object?>[],
      final String t when t.startsWith('map<') => <String, Object?>{},
      _ => null,
    };
  }
}

/// Takes rows stored under [stored] to [target] by the plans of [target]
/// from [stored]'s version on (DB-005), exactly as the compiler simulated
/// them at publish: renames, drops and resets in order of version, then
/// fields the plans do not explain are an error. Pure, so every adapter
/// migrates the same way.
final class DbMigrator {
  /// Plans the migration of [stored] to [target]; throws `PLX-5200` when
  /// the plans do not explain the difference.
  factory DbMigrator.plan(
    DbCollectionSchema stored,
    DbCollectionSchema target,
  ) {
    final plans = [
      for (final p in target.migrations)
        if (p.from >= stored.version && p.from < target.version) p,
    ]..sort((a, b) => a.from.compareTo(b.from));
    final types = {for (final f in stored.fields) f.name: f};
    final renames = <MapEntry<String, String>>[];
    final dropped = <String>{};
    final reset = <String>{};
    for (final p in plans) {
      for (final e in p.rename.entries) {
        final f = types.remove(e.value);
        if (f != null) {
          types[e.key] = DbField(e.key, f.type);
          renames.add(MapEntry(e.key, e.value));
        }
      }
      for (final d in p.drop) {
        if (types.remove(d) != null) dropped.add(d);
      }
      reset.addAll(p.reset);
    }
    for (final name in types.keys) {
      if (target.field(name) == null) {
        throw PluxException(
          PluxErrorCode.dbMigrationFailed,
          'collection ${target.key}: field "$name" is gone and no migration '
          'from version ${stored.version} drops or renames it',
        );
      }
    }
    for (final f in target.fields) {
      final old = types[f.name];
      if (reset.contains(f.name)) continue;
      if (old == null) {
        if (!f.nullable) {
          throw PluxException(
            PluxErrorCode.dbMigrationFailed,
            'collection ${target.key}: field "${f.name}" is new and not '
            'nullable, and no migration resets it',
          );
        }
      } else if (old.type != f.type && '${old.type}?' != f.type) {
        throw PluxException(
          PluxErrorCode.dbMigrationFailed,
          'collection ${target.key}: field "${f.name}" changes from '
          '${old.type} to ${f.type}, and no migration resets it',
        );
      }
    }
    return DbMigrator._(target, renames, dropped, reset);
  }

  DbMigrator._(this.target, this._renames, this._dropped, this._reset);

  /// The schema rows migrate to.
  final DbCollectionSchema target;

  final List<MapEntry<String, String>> _renames;
  final Set<String> _dropped;
  final Set<String> _reset;

  /// Whether the migration deletes values.
  bool get destructive => _dropped.isNotEmpty || _reset.isNotEmpty;

  /// Converts one stored row to the target schema.
  Map<String, Object?> row(Map<String, Object?> old) {
    final cur = Map<String, Object?>.of(old);
    for (final r in _renames) {
      if (cur.containsKey(r.value)) cur[r.key] = cur.remove(r.value);
    }
    return {
      for (final f in target.fields)
        f.name: _reset.contains(f.name)
            ? DbCollectionSchema.emptyValue(f)
            : cur[f.name],
    };
  }
}
