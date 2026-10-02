// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// PXL types at run time: type expressions and the conversion of JSON
/// values into PXL values and back, following the literal forms of the
/// document model (docs/reference/document-model.md §3). Mirrors
/// `FromJSON` and `ToJSON` of `backend/internal/pxl`.
library;

import 'package:plux_flutter/src/pxl/decimal.dart';
import 'package:plux_flutter/src/pxl/tables.g.dart';
import 'package:plux_flutter/src/pxl/values.dart';

/// Kinds of type.
enum PxlKind {
  /// bool
  bool,

  /// int
  int,

  /// double
  double,

  /// string
  string,

  /// decimal
  decimal,

  /// money
  money,

  /// date
  date,

  /// dateTime
  dateTime,

  /// duration
  duration,

  /// color
  color,

  /// asset
  asset,

  /// route
  route,

  /// An enum type.
  enumeration,

  /// An object type.
  object,

  /// `list<T>`
  list,

  /// `map<string,T>`
  map,
}

/// An enum or object type declared by name.
final class NamedType {
  /// Creates an enum type.
  NamedType.enumeration(this.name, this.members) : fields = null;

  /// Creates an object type; fields are resolved later.
  NamedType.object(this.name) : members = const [], fields = {};

  /// The type name.
  final String name;

  /// The enum members.
  final List<String> members;

  /// The object fields, or null for an enum.
  final Map<String, PxlType>? fields;
}

/// A type.
final class PxlType {
  /// Creates a type.
  const PxlType(this.kind, {this.nullable = false, this.elem, this.named});

  /// The kind.
  final PxlKind kind;

  /// Whether null is a value of the type.
  final bool nullable;

  /// The element type of a list or map.
  final PxlType? elem;

  /// The enum or object type.
  final NamedType? named;

  static const _primitives = {
    'bool': PxlKind.bool,
    'int': PxlKind.int,
    'double': PxlKind.double,
    'string': PxlKind.string,
    'decimal': PxlKind.decimal,
    'money': PxlKind.money,
    'date': PxlKind.date,
    'dateTime': PxlKind.dateTime,
    'duration': PxlKind.duration,
    'color': PxlKind.color,
    'asset': PxlKind.asset,
    'route': PxlKind.route,
  };

  /// Parses a type expression, resolving names with [lookup]; throws
  /// [FormatException].
  static PxlType parse(String src, NamedType? Function(String name) lookup) {
    var pos = 0;
    bool eat(String lit) {
      if (!src.startsWith(lit, pos)) return false;
      pos += lit.length;
      return true;
    }

    PxlType read() {
      final m = RegExp('[A-Za-z][A-Za-z0-9]*').matchAsPrefix(src, pos);
      if (m == null) throw FormatException('type expected', src, pos);
      pos = m.end;
      final name = m[0]!;
      PxlType t;
      if (name == 'list' || name == 'map') {
        if (!eat('<') || (name == 'map' && !eat('string,'))) {
          throw FormatException('write list<T> or map<string,T>', src, pos);
        }
        final elem = read();
        if (!eat('>')) throw FormatException("missing '>'", src, pos);
        t = PxlType(name == 'list' ? PxlKind.list : PxlKind.map, elem: elem);
      } else if (_primitives[name] case final kind?) {
        t = PxlType(kind);
      } else {
        final n =
            lookup(name) ??
            (throw FormatException('unknown type "$name"', src, pos));
        t = PxlType(
          n.fields == null ? PxlKind.enumeration : PxlKind.object,
          named: n,
        );
      }
      return eat('?')
          ? PxlType(t.kind, nullable: true, elem: t.elem, named: t.named)
          : t;
    }

    final t = read();
    if (pos != src.length) throw FormatException('unexpected text', src, pos);
    return t;
  }
}

/// An environment's named types and roots, in the JSON form of the
/// conformance vectors: `{"types": {...}, "roots": {...}}`. The root `now`
/// of type dateTime is always present.
final class PxlEnv {
  /// Builds the environment; throws [FormatException].
  PxlEnv.fromJson(Map<String, Object?> spec) {
    _types['RoundingMode'] = NamedType.enumeration('RoundingMode', [
      for (final m in RoundingMode.values) m.name,
    ]);
    _types['DateStyle'] = NamedType.enumeration('DateStyle', const [
      'short',
      'medium',
      'long',
      'full',
    ]);
    final types = (spec['types'] as Map<String, Object?>?) ?? const {};
    for (final MapEntry(:key, :value) in types.entries) {
      final def = value! as Map<String, Object?>;
      final members = def['enum'] as List<Object?>?;
      _types[key] = members != null
          ? NamedType.enumeration(key, members.cast<String>())
          : NamedType.object(key);
    }
    for (final MapEntry(:key, :value) in types.entries) {
      final fields =
          (value! as Map<String, Object?>)['fields'] as Map<String, Object?>?;
      for (final f in fields?.entries ?? const <MapEntry<String, Object?>>[]) {
        _types[key]!.fields![f.key] = PxlType.parse(
          f.value! as String,
          (n) => _types[n],
        );
      }
    }
    for (final MapEntry(:key, :value)
        in (spec['roots']! as Map<String, Object?>).entries) {
      roots[key] = PxlType.parse(value! as String, (n) => _types[n]);
    }
    roots['now'] = const PxlType(PxlKind.dateTime);
  }

  final _types = <String, NamedType>{};

  /// The roots and their types.
  final roots = <String, PxlType>{};
}

/// Converts a decoded JSON value into a PXL value of type [t]; throws
/// [FormatException].
Object? fromJson(PxlType t, Object? v) {
  if (v == null) {
    if (t.nullable) return null;
    throw FormatException('null is not a ${t.kind.name}');
  }
  final Object? out = switch (t.kind) {
    PxlKind.bool => v is bool ? v : null,
    PxlKind.int => v is int ? v : null,
    PxlKind.double => v is num ? v.toDouble() : null,
    PxlKind.list =>
      v is List<Object?> ? [for (final e in v) fromJson(t.elem!, e)] : null,
    PxlKind.map ||
    PxlKind.object => v is Map<String, Object?> ? _mapFromJson(t, v) : null,
    PxlKind.money => _moneyFromJson(v),
    PxlKind.duration => v is int && v >= 0 ? PxlDuration(v) : null,
    _ => v is String ? _scalarFromString(t, v) : null,
  };
  return out ?? (throw FormatException('$v is not a ${t.kind.name}'));
}

/// Converts text, such as a deep link's or a redirect's parameter or a
/// user-context attribute, into a PXL value of type [t] (ADR-0040): the
/// literal forms of the scalars and declared enums; throws
/// [FormatException] for text that is not one, or for a type text cannot
/// carry.
Object? fromText(PxlType t, String text) {
  final Object? out = switch (t.kind) {
    PxlKind.bool => switch (text) {
      'true' => true,
      'false' => false,
      _ => null,
    },
    PxlKind.int => int.tryParse(text),
    PxlKind.double => double.tryParse(text),
    PxlKind.string ||
    PxlKind.enumeration ||
    PxlKind.decimal ||
    PxlKind.date ||
    PxlKind.dateTime => _scalarFromString(t, text),
    _ => throw FormatException('a ${t.kind.name} cannot be written as text'),
  };
  return out ?? (throw FormatException('"$text" is not a ${t.kind.name}'));
}

Map<String, Object?> _mapFromJson(PxlType t, Map<String, Object?> v) {
  final fields = t.named?.fields;
  final out = <String, Object?>{};
  for (final k in sortedKeys(v)) {
    final et = t.kind == PxlKind.object
        ? (fields![k] ??
              (throw FormatException('${t.named!.name} has no field "$k"')))
        : t.elem!;
    out[k] = fromJson(et, v[k]);
  }
  if (fields != null) {
    for (final MapEntry(:key, :value) in fields.entries) {
      if (out.containsKey(key)) continue;
      if (!value.nullable) {
        throw FormatException('${t.named!.name} requires field "$key"');
      }
      out[key] = null;
    }
  }
  return out;
}

Money? _moneyFromJson(Object v) {
  if (v is! Map<String, Object?> || v.length != 2) return null;
  final (amount, code) = (v['amount'], v['currency']);
  if (amount is! String || code is! String || !minorUnits.containsKey(code)) {
    return null;
  }
  final d = Decimal.tryParse(amount);
  return d == null ? null : Money(d, code);
}

Object? _scalarFromString(PxlType t, String s) => switch (t.kind) {
  PxlKind.string || PxlKind.asset || PxlKind.route => s,
  PxlKind.enumeration => t.named!.members.contains(s) ? s : null,
  PxlKind.decimal => Decimal.tryParse(s),
  PxlKind.date => parseDate(s),
  PxlKind.dateTime => parseDateTime(s),
  PxlKind.color => parseColor(s),
  _ => null,
};

/// Converts a PXL value into its JSON form, the inverse of [fromJson]: ints
/// and durations as ints, doubles as doubles, decimals and the date types as
/// strings, money as `{"amount", "currency"}`.
Object? toJson(Object? v) => switch (v) {
  null || bool() || int() || double() || String() => v,
  final Money m => {'amount': m.amount.toString(), 'currency': m.currency},
  final PxlDuration d => d.millis,
  final List<Object?> l => [for (final e in l) toJson(e)],
  final Map<String, Object?> m => {
    for (final e in m.entries) e.key: toJson(e.value),
  },
  _ => v.toString(), // decimal, date, dateTime, color
};
