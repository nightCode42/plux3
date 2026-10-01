// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// What the generated and hand-written decoders read values with (ADR-0031).
///
/// A value reaches a decoder in PXL form (ADR-0009): `bool`, `int`,
/// `double`, `String`, [PxlColor], [PxlDuration], lists, and objects —
/// either a [PluxObject] from a bundle literal, whose fields are keyed by
/// permanent field ID, or a `Map<String, Object?>` produced by a PXL
/// expression, keyed by field name. Enums arrive as their permanent value
/// ID (literals) or member name (PXL). A decoder returns null for a value
/// of the wrong shape; the node reports it and uses the prop's default.
library;

import 'package:flutter/widgets.dart';
import 'package:plux_flutter/src/assets/icon_fonts.dart';
import 'package:plux_flutter/src/assets/image_providers.dart';
import 'package:plux_flutter/src/pxl/decimal.dart';
import 'package:plux_flutter/src/pxl/values.dart';

/// The environment a decoder runs in.
abstract interface class Decoding {
  /// The reading direction, for directional insets, radii and alignments.
  TextDirection get textDirection;

  /// Resolves an icon of the release's icon fonts (THM-005); null, and
  /// reported, when the release has no font for [set].
  PluxIconSource? icon(String name, String set);

  /// Resolves an image: an asset of the release by ID, or a URL.
  ImageProvider<Object>? image({String? asset, String? url});

  /// Resolves the asset [asset] when it is an SVG (CMP-031); null for any
  /// other asset.
  PluxVectorSource? vector(String asset);
}

/// A [Decoding] with no runtime behind it: left-to-right, and no icons or
/// images. Used for descriptor defaults, which never need them.
final class PlainDecoding implements Decoding {
  /// Creates the decoding.
  const PlainDecoding();

  @override
  TextDirection get textDirection => TextDirection.ltr;

  @override
  PluxIconSource? icon(String name, String set) => null;

  @override
  ImageProvider<Object>? image({String? asset, String? url}) => null;

  @override
  PluxVectorSource? vector(String asset) => null;
}

/// The [PlainDecoding].
const Decoding plainDecoding = PlainDecoding();

/// A value that cannot be decoded; the node reports it (PLX-4002).
final class DecodeError implements Exception {
  /// Creates the error.
  const DecodeError(this.message);

  /// What is wrong.
  final String message;

  @override
  String toString() => message;
}

/// An object literal of a bundle, whose fields are resolved when read.
abstract interface class PluxObject {
  /// The field with permanent ID [id], resolved; null when absent.
  Object? field(int id);

  /// The object keyed by string-table entries, for declared types.
  Map<String, Object?> toMap();
}

/// The fields of an object value, by permanent ID or by name.
final class Fields {
  const Fields._(this._object, this._map);

  /// The fields of [v], or null when it is not an object.
  static Fields? of(Decoding d, Object? v) => switch (v) {
    PluxObject() => Fields._(v, null),
    Map<String, Object?>() => Fields._(null, v),
    _ => null,
  };

  final PluxObject? _object;
  final Map<String, Object?>? _map;

  /// The field with permanent ID [id] and name [name].
  Object? get(int id, String name) =>
      _object != null ? _object.field(id) : _map![name];

  /// Whether the field is present.
  bool has(int id, String name) => get(id, name) != null;

  /// Fails decoding: a required field is absent.
  Never missing(String name) => throw DecodeError('field $name is missing');
}

/// A bool.
bool? asBool(Decoding d, Object? v) => v is bool ? v : null;

/// An int.
int? asInt(Decoding d, Object? v) => v is int ? v : null;

/// A double; ints and decimals widen.
double? asDouble(Decoding d, Object? v) => switch (v) {
  double() => v,
  int() => v.toDouble(),
  Decimal() => v.toDouble(),
  _ => null,
};

/// A string.
String? asString(Decoding d, Object? v) => v is String ? v : null;

/// A color.
Color? asColor(Decoding d, Object? v) => v is PxlColor
    ? Color(((v.rgba & 0xff) << 24) | ((v.rgba >> 8) & 0xffffff))
    : null;

/// A duration.
Duration? asDuration(Decoding d, Object? v) =>
    v is PxlDuration ? Duration(milliseconds: v.millis) : null;

/// A list of decoded items; null when [v] is not a list or an item cannot
/// be decoded.
List<T>? asList<T>(
  Decoding d,
  Object? v,
  T? Function(Decoding d, Object? v) item,
) {
  if (v is! List<Object?>) return null;
  final out = <T>[];
  for (final x in v) {
    final decoded = item(d, x);
    if (decoded == null) return null;
    out.add(decoded);
  }
  return out;
}

/// A list of strings.
List<String>? asStrings(Decoding d, Object? v) => asList(d, v, asString);

/// A list of colors.
List<Color>? asColors(Decoding d, Object? v) => asList(d, v, asColor);

/// A list of doubles.
List<double>? asDoubles(Decoding d, Object? v) => asList(d, v, asDouble);

/// A locale from its BCP 47 tag.
Locale? asLocale(Decoding d, Object? v) {
  if (v is! String || v.isEmpty) return null;
  final parts = v.split(RegExp('[-_]'));
  return switch (parts.length) {
    1 => Locale(parts[0]),
    2 when parts[1].length == 4 => Locale.fromSubtags(
      languageCode: parts[0],
      scriptCode: parts[1],
    ),
    2 => Locale(parts[0], parts[1]),
    _ => Locale.fromSubtags(
      languageCode: parts[0],
      scriptCode: parts[1].length == 4 ? parts[1] : null,
      countryCode: parts.last,
    ),
  };
}

/// A list of locales.
List<Locale>? asLocales(Decoding d, Object? v) => asList(d, v, asLocale);

/// A set of strings.
Set<String>? asStringSet(Decoding d, Object? v) => asStrings(d, v)?.toSet();
