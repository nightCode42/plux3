// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Resolves bundle values into PXL values (ADR-0009, ADR-0031): literals
/// as written, styles from the styles section, bindings by evaluating
/// their PXL program, design tokens and translations. Objects are resolved
/// field by field when a decoder reads them.
library;

import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/bundle/safe_read.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/pxl/decimal.dart';
import 'package:plux_flutter/src/pxl/program.dart';
import 'package:plux_flutter/src/pxl/values.dart';
import 'package:plux_flutter/src/pxl/vm.dart' as vm;
import 'package:plux_flutter/src/render/decoding.dart';
import 'package:plux_flutter/src/render/messages.dart';
import 'package:plux_flutter/src/render/sections.dart';
import 'package:plux_flutter/src/schema/registry.g.dart';

/// A binding that could not be evaluated; the prop takes its default and
/// the node reports it (PLX-4002).
final class BindingError implements Exception {
  /// Creates the error.
  const BindingError(this.message);

  /// What went wrong.
  final String message;

  @override
  String toString() => message;
}

/// Where a value's string indices point: a page or component section's
/// own table, or the bundle's strings section.
typedef StringTable = String Function(int index);

/// Resolves the values of one plugin's sections.
final class ValueResolver {
  /// Creates a resolver.
  ValueResolver({
    required this.plugin,
    required this.roots,
    required this.token,
    required this.translation,
    required this.limits,
  });

  /// The plugin's bundle.
  final BundleView plugin;

  /// The PXL roots in scope (`page`, `params`, `item`, …).
  final Map<String, Object?> Function() roots;

  /// A design token's value at a path, in PXL form, or null.
  final Object? Function(String path) token;

  /// A translation's pattern by key, or null.
  final String? Function(UuidKey key) translation;

  /// Limits of one evaluation.
  final vm.PxlLimits limits;

  /// Resolves [v], whose strings are in [strings]. The paths bindings read
  /// are added to [reads] (CMP-023). Throws [BindingError] when a binding
  /// fails and [PluxException] when the value is malformed.
  Object? resolve(fbs.Value? v, StringTable strings, [Set<String>? reads]) {
    if (v == null) return null;
    final kind = readEnum(() => v.kind);
    return switch (kind) {
      null => throw const PluxException(
        PluxErrorCode.bundleMalformed,
        'a value of a kind this runtime does not know',
      ),
      fbs.ValueKind.Null => null,
      fbs.ValueKind.Bool => v.i != 0,
      fbs.ValueKind.Int => v.i,
      fbs.ValueKind.Double => v.d,
      fbs.ValueKind.String || fbs.ValueKind.Route => strings(v.s),
      fbs.ValueKind.Decimal => _decimal(v),
      fbs.ValueKind.Money => Money(_decimal(v), strings(v.s)),
      fbs.ValueKind.Date => PxlDate(v.i),
      fbs.ValueKind.DateTime => PxlDateTime(v.i ~/ 1000, v.offset),
      fbs.ValueKind.Duration => PxlDuration(v.i ~/ 1000),
      fbs.ValueKind.Color => PxlColor(
        ((v.i & 0xffffff) << 8) | ((v.i >> 24) & 0xff),
      ),
      // Registry enums carry the permanent value ID; enums declared in
      // documents carry the member name as well.
      fbs.ValueKind.Enum => v.s != 0 ? strings(v.s) : v.i,
      fbs.ValueKind.Asset =>
        v.uuid == null ? null : uuidString(uuidOf(v.uuid!)),
      fbs.ValueKind.List => [
        for (final item in v.items ?? const <fbs.Value>[])
          resolve(item, strings, reads),
      ],
      fbs.ValueKind.Map => {
        for (final e in v.entries ?? const <fbs.Entry>[])
          strings(e.key): resolve(e.value, strings, reads),
      },
      fbs.ValueKind.Object => _Object(this, v, strings, reads),
      fbs.ValueKind.Style => _style(v.i, reads),
      fbs.ValueKind.Expr => evaluate(plugin.program(v.i), reads),
      fbs.ValueKind.Token => token(strings(v.s)),
      fbs.ValueKind.Translation => _translate(v, strings, reads),
    };
  }

  Object? _style(int id, Set<String>? reads) {
    final s = plugin.style(id);
    final v = s.value;
    // A value type's object is keyed by permanent field IDs, which its
    // style's type names (BND-017).
    if (v != null && readEnum(() => v.kind) == fbs.ValueKind.Object) {
      return _Object(this, v, plugin.string, reads, names: _fieldNames[s.type]);
    }
    return resolve(v, plugin.string, reads);
  }

  /// The field names of each registry value type, by permanent field ID.
  static final Map<int, Map<int, String>> _fieldNames = {
    for (final t in valueTypeDescriptors)
      t.id: {for (final f in t.fields.entries) f.value: f.key},
  };

  /// Evaluates [program] against the roots; adds its read set to [reads].
  Object? evaluate(Program program, [Set<String>? reads]) {
    reads?.addAll(program.reads);
    final inputs = {
      ...roots(),
      'now': PxlDateTime(
        DateTime.now().millisecondsSinceEpoch,
        DateTime.now().timeZoneOffset.inMinutes,
      ),
    };
    return switch (vm.evaluate(program, inputs, limits)) {
      vm.PxlValue(:final value) => value,
      vm.PxlFailure(:final error) => throw BindingError(error.toString()),
    };
  }

  String _translate(fbs.Value v, StringTable strings, Set<String>? reads) {
    final key = v.uuid;
    if (key == null) return '';
    final pattern = translation(uuidOf(key));
    if (pattern == null) {
      throw BindingError('translation ${uuidString(uuidOf(key))} is missing');
    }
    final args = <String, Object?>{
      for (final e in v.entries ?? const <fbs.Entry>[])
        strings(e.key): toPxl(resolve(e.value, strings, reads)),
    };
    return formatMessage(pattern, args);
  }

  static Decimal _decimal(fbs.Value v) {
    final bytes = v.unscaled ?? const <int>[];
    var n = BigInt.zero;
    for (final b in bytes) {
      n = (n << 8) | BigInt.from(b);
    }
    if (bytes.isNotEmpty && bytes.first & 0x80 != 0) {
      n -= BigInt.one << (8 * bytes.length);
    }
    return Decimal(n, v.scale);
  }
}

/// Converts a resolved value for PXL: objects become maps keyed by name.
Object? toPxl(Object? v) => switch (v) {
  PluxObject() => {for (final e in v.toMap().entries) e.key: toPxl(e.value)},
  List<Object?>() => [for (final x in v) toPxl(x)],
  Map<String, Object?>() => {for (final e in v.entries) e.key: toPxl(e.value)},
  _ => v,
};

/// A UUID in its canonical text form.
String uuidString(UuidKey u) {
  String hex(int v) =>
      ((v >> 32) & 0xffffffff).toRadixString(16).padLeft(8, '0') +
      (v & 0xffffffff).toRadixString(16).padLeft(8, '0');
  final s = hex(u.$1) + hex(u.$2);
  return '${s.substring(0, 8)}-${s.substring(8, 12)}-${s.substring(12, 16)}-'
      '${s.substring(16, 20)}-${s.substring(20)}';
}

/// An object literal whose fields are resolved when read: a declared
/// type's, keyed by string index, or a registry value type's, keyed by
/// permanent field ID and named by [names].
final class _Object implements PluxObject {
  _Object(this._r, this._v, this._strings, this._reads, {this.names});

  final ValueResolver _r;
  final fbs.Value _v;
  final StringTable _strings;
  final Set<String>? _reads;

  /// A value type's field names by permanent field ID; null for a declared
  /// type.
  final Map<int, String>? names;

  @override
  Object? field(int id) {
    for (final e in _v.entries ?? const <fbs.Entry>[]) {
      if (e.key == id) return _r.resolve(e.value, _strings, _reads);
    }
    return null;
  }

  @override
  Map<String, Object?> toMap() => {
    for (final e in _v.entries ?? const <fbs.Entry>[])
      (names?[e.key] ?? _strings(e.key)): _r.resolve(e.value, _strings, _reads),
  };
}
