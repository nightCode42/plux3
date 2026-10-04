// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Response mapping (DAT-004): a dot-path selector into the decoded JSON,
/// then conversion to the declared type. Unlike route parameters, a
/// response may carry fields its type does not declare: APIs grow, so
/// those are ignored. Every failure is a [DataFailure] naming the path
/// and the expected type, never the value.
library;

import 'package:plux_flutter/src/data/failure.dart';
import 'package:plux_flutter/src/pxl/decimal.dart';
import 'package:plux_flutter/src/pxl/tables.g.dart' show minorUnits;
import 'package:plux_flutter/src/pxl/types.dart';
import 'package:plux_flutter/src/pxl/values.dart';

/// The value at [selector] in [json]: names select object fields, numbers
/// list items. A null or empty selector selects [json] itself.
Object? select(Object? json, String? selector) {
  if (selector == null || selector.isEmpty) return json;
  var cur = json;
  for (final part in selector.split('.')) {
    final index = int.tryParse(part);
    cur = switch (cur) {
      final Map<String, Object?> m when index == null && m.containsKey(part) =>
        m[part],
      final List<Object?> l when index != null && index < l.length => l[index],
      _ => throw DataFailure.mapping('the response has nothing at $selector'),
    };
  }
  return cur;
}

/// Converts decoded JSON [v] to a PXL value of type [t]; [at] names the
/// position for errors.
Object? mapJson(PxlType t, Object? v, [String at = 'response']) {
  if (v == null) {
    if (t.nullable) return null;
    throw DataFailure.mapping('$at is null, expected ${_name(t)}');
  }
  final Object? out = switch (t.kind) {
    PxlKind.bool => v is bool ? v : null,
    PxlKind.int =>
      v is int ? v : (v is double && v == v.truncate() ? v.toInt() : null),
    PxlKind.double => v is num ? v.toDouble() : null,
    PxlKind.list =>
      v is List<Object?>
          ? [
              for (var i = 0; i < v.length; i++)
                mapJson(t.elem!, v[i], '$at.$i'),
            ]
          : null,
    PxlKind.map =>
      v is Map<String, Object?>
          ? {
              for (final e in v.entries)
                e.key: mapJson(t.elem!, e.value, '$at.${e.key}'),
            }
          : null,
    PxlKind.object => v is Map<String, Object?> ? _object(t, v, at) : null,
    PxlKind.money => _money(v),
    PxlKind.duration => v is int && v >= 0 ? PxlDuration(v) : null,
    PxlKind.decimal =>
      v is String
          ? Decimal.tryParse(v)
          : (v is int ? Decimal.tryParse('$v') : null),
    PxlKind.string || PxlKind.asset || PxlKind.route => v is String ? v : null,
    PxlKind.enumeration =>
      v is String && t.named!.members.contains(v) ? v : null,
    PxlKind.date => v is String ? parseDate(v) : null,
    PxlKind.dateTime => v is String ? parseDateTime(v) : null,
    PxlKind.color => v is String ? parseColor(v) : null,
  };
  return out ?? (throw DataFailure.mapping('$at is not a ${_name(t)}'));
}

Map<String, Object?> _object(PxlType t, Map<String, Object?> v, String at) => {
  for (final MapEntry(:key, :value) in t.named!.fields!.entries)
    key: mapJson(value, v[key], '$at.$key'),
};

Money? _money(Object v) {
  if (v is! Map<String, Object?>) return null;
  final (amount, code) = (v['amount'], v['currency']);
  if (amount is! String || code is! String || !minorUnits.containsKey(code)) {
    return null;
  }
  final d = Decimal.tryParse(amount);
  return d == null ? null : Money(d, code);
}

String _name(PxlType t) => t.named?.name ?? t.kind.name;
