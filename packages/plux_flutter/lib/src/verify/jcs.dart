// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// RFC 8785 (JSON Canonicalization Scheme) for the manifests the runtime
/// verifies (ADR-0029): a manifest's signed bytes must be exactly the
/// canonical form of themselves, so that what is verified is what is read.
/// Checked against the vectors in `schema/testdata/jcs` that the Go
/// implementation passes.
library;

import 'dart:convert';

/// Returns the canonical JSON of [value] — `null`, `bool`, `num`,
/// `String`, `List` and `Map<String, Object?>` — as RFC 8785 defines it.
/// Throws [ArgumentError] for a value JSON cannot hold, such as NaN.
String canonicalJson(Object? value) {
  final out = StringBuffer();
  _write(out, value);
  return out.toString();
}

/// Whether [bytes] are UTF-8 JSON already in RFC 8785 canonical form. A
/// duplicate key, whitespace, a non-canonical number or escape, or a key
/// out of order makes the answer false.
bool isCanonicalJson(List<int> bytes) {
  final String text;
  final Object? value;
  try {
    text = utf8.decode(bytes);
    value = jsonDecode(text);
  } on FormatException {
    return false;
  }
  try {
    return canonicalJson(value) == text;
  } on ArgumentError {
    return false;
  }
}

void _write(StringBuffer out, Object? v) {
  switch (v) {
    case null:
      out.write('null');
    case bool():
      out.write(v ? 'true' : 'false');
    case num():
      out.write(canonicalNumber(v.toDouble()));
    case String():
      _string(out, v);
    case List<Object?>():
      out.write('[');
      for (var i = 0; i < v.length; i++) {
        if (i > 0) out.write(',');
        _write(out, v[i]);
      }
      out.write(']');
    case Map<String, Object?>():
      // Keys sort by UTF-16 code units, which String.compareTo compares.
      final keys = v.keys.toList()..sort();
      out.write('{');
      for (var i = 0; i < keys.length; i++) {
        if (i > 0) out.write(',');
        _string(out, keys[i]);
        out.write(':');
        _write(out, v[keys[i]]);
      }
      out.write('}');
    default:
      throw ArgumentError.value(v, 'value', 'not a JSON value');
  }
}

void _string(StringBuffer out, String s) {
  out.write('"');
  for (final c in s.codeUnits) {
    switch (c) {
      case 0x22:
        out.write(r'\"');
      case 0x5C:
        out.write(r'\\');
      case 0x08:
        out.write(r'\b');
      case 0x09:
        out.write(r'\t');
      case 0x0A:
        out.write(r'\n');
      case 0x0C:
        out.write(r'\f');
      case 0x0D:
        out.write(r'\r');
      default:
        if (c < 0x20) {
          out.write(r'\u');
          out.write(c.toRadixString(16).padLeft(4, '0'));
        } else {
          out.writeCharCode(c);
        }
    }
  }
  out.write('"');
}

/// Formats [d] as ECMAScript's `Number.prototype.toString` does, which is
/// the number form of RFC 8785 (§3.2.2.3).
String canonicalNumber(double d) {
  if (d.isNaN || d.isInfinite) {
    throw ArgumentError.value(d, 'number', 'not a JSON number');
  }
  if (d == 0) return '0';
  if (d < 0) return '-${canonicalNumber(-d)}';
  // The shortest digits that round-trip, and their decimal exponent.
  final exp = d.toStringAsExponential();
  final e = exp.indexOf('e');
  final digits = exp.substring(0, e).replaceAll('.', '');
  final k = digits.length, n = int.parse(exp.substring(e + 1)) + 1;
  if (k <= n && n <= 21) return digits + '0' * (n - k);
  if (0 < n && n <= 21) {
    return '${digits.substring(0, n)}.${digits.substring(n)}';
  }
  if (-6 < n && n <= 0) return '0.${'0' * -n}$digits';
  final mantissa = k == 1 ? digits : '${digits[0]}.${digits.substring(1)}';
  final x = n - 1;
  return '${mantissa}e${x < 0 ? '-' : '+'}${x.abs()}';
}
