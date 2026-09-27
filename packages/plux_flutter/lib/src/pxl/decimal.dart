// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Exact decimal numbers of PXL (PXL-005, ADR-0009), identical to the Go
/// package `backend/internal/pxl/decimal`.
library;

/// How rounding and division discard digits. The names are PXL's.
enum RoundingMode {
  /// To the nearest neighbour, ties to the even one; PXL's default.
  halfEven,

  /// To the nearest neighbour, ties away from zero.
  halfUp,

  /// Toward zero.
  down,

  /// Away from zero.
  up,

  /// Toward positive infinity.
  ceiling,

  /// Toward negative infinity.
  floor;

  /// The mode with the given PXL name, or null.
  static RoundingMode? byName(String name) {
    for (final m in values) {
      if (m.name == name) return m;
    }
    return null;
  }
}

/// An exact decimal: [unscaled] × 10^-[scale]. Values are immutable; they
/// keep their scale (12.50 prints as "12.50") and compare numerically.
final class Decimal implements Comparable<Decimal> {
  /// Creates unscaled × 10^-scale; scale must not be negative.
  const Decimal(this.unscaled, this.scale)
    : assert(scale >= 0, 'negative scale');

  /// An integer with scale 0.
  Decimal.fromInt(int v) : this(BigInt.from(v), 0);

  /// The unscaled integer.
  final BigInt unscaled;

  /// The number of fraction digits.
  final int scale;

  static final _plain = RegExp(r'^-?[0-9]+(\.[0-9]+)?$');

  /// Parses `-?[0-9]+(\.[0-9]+)?`, or returns null.
  static Decimal? tryParse(String s) {
    if (!_plain.hasMatch(s)) return null;
    final point = s.indexOf('.');
    final digits = point < 0
        ? s
        : s.substring(0, point) + s.substring(point + 1);
    return Decimal(BigInt.parse(digits), point < 0 ? 0 : s.length - point - 1);
  }

  /// The shortest decimal that reads back as [f], in plain notation, or
  /// null for NaN and infinities.
  static Decimal? fromDouble(double f) {
    if (f.isNaN || f.isInfinite) return null;
    return tryParse(plainShortest(f));
  }

  /// Whether the value is zero.
  bool get isZero => unscaled == BigInt.zero;

  /// -1, 0 or 1.
  int get sign => unscaled.sign;

  /// The number of digits in plain notation, without sign and point.
  int get digits {
    final precision = unscaled.abs().toString().length;
    return precision > scale + 1 ? precision : scale + 1;
  }

  @override
  String toString() {
    var digits = unscaled.abs().toString();
    if (scale > 0) {
      if (digits.length < scale + 1) digits = digits.padLeft(scale + 1, '0');
      final cut = digits.length - scale;
      digits = '${digits.substring(0, cut)}.${digits.substring(cut)}';
    }
    return unscaled.isNegative ? '-$digits' : digits;
  }

  static BigInt _pow10(int n) => BigInt.from(10).pow(n);

  static (BigInt, BigInt, int) _align(Decimal a, Decimal b) {
    final s = a.scale > b.scale ? a.scale : b.scale;
    return (
      a.unscaled * _pow10(s - a.scale),
      b.unscaled * _pow10(s - b.scale),
      s,
    );
  }

  /// The exact sum, at the larger scale.
  Decimal operator +(Decimal o) {
    final (a, b, s) = _align(this, o);
    return Decimal(a + b, s);
  }

  /// The exact difference, at the larger scale.
  Decimal operator -(Decimal o) {
    final (a, b, s) = _align(this, o);
    return Decimal(a - b, s);
  }

  /// The exact product; the scales add.
  Decimal operator *(Decimal o) =>
      Decimal(unscaled * o.unscaled, scale + o.scale);

  /// The negation.
  Decimal operator -() => Decimal(-unscaled, scale);

  /// The absolute value.
  Decimal abs() => Decimal(unscaled.abs(), scale);

  @override
  int compareTo(Decimal o) {
    final (a, b, _) = _align(this, o);
    return a.compareTo(b);
  }

  @override
  bool operator ==(Object other) => other is Decimal && compareTo(other) == 0;

  @override
  int get hashCode => reduced().toString().hashCode;

  /// This value at [target] scale: exact when widening, rounded with
  /// [mode] when narrowing.
  Decimal round(int target, RoundingMode mode) {
    if (target >= scale) {
      return Decimal(unscaled * _pow10(target - scale), target);
    }
    return Decimal(_divRound(unscaled, _pow10(scale - target), mode), target);
  }

  /// This ÷ [o] rounded with [mode] to [target] scale, or null when [o] is
  /// zero.
  Decimal? div(Decimal o, int target, RoundingMode mode) {
    if (o.isZero) return null;
    var num = unscaled, den = o.unscaled;
    final k = target + o.scale - scale;
    if (k >= 0) {
      num *= _pow10(k);
    } else {
      den *= _pow10(-k);
    }
    return Decimal(_divRound(num, den, mode), target);
  }

  static BigInt _divRound(BigInt num, BigInt den, RoundingMode mode) {
    final q = num ~/ den; // truncates toward zero
    final r = num.remainder(den);
    if (r == BigInt.zero) return q;
    final sign = num.sign * den.sign;
    final twice = r.abs() * BigInt.two;
    final cmp = twice.compareTo(den.abs());
    final away = switch (mode) {
      RoundingMode.down => false,
      RoundingMode.up => true,
      RoundingMode.ceiling => sign > 0,
      RoundingMode.floor => sign < 0,
      RoundingMode.halfUp => cmp >= 0,
      RoundingMode.halfEven => cmp > 0 || (cmp == 0 && q.isOdd),
    };
    return away ? q + BigInt.from(sign) : q;
  }

  /// The nearest double.
  double toDouble() => double.parse(toString());

  /// The value truncated toward zero, or null outside 64 bits.
  int? toInt() {
    final t = round(0, RoundingMode.down).unscaled;
    return t.isValidInt ? t.toInt() : null;
  }

  /// This value without trailing fraction zeros.
  Decimal reduced() {
    var u = unscaled, s = scale;
    final ten = BigInt.from(10);
    while (s > 0 && u.remainder(ten) == BigInt.zero) {
      u = u ~/ ten;
      s--;
    }
    return Decimal(
      u == BigInt.zero ? BigInt.zero : u,
      u == BigInt.zero ? 0 : s,
    );
  }
}

/// The shortest round-trip digits of a finite double in plain notation,
/// like Go's `strconv.FormatFloat(f, 'f', -1, 64)`.
String plainShortest(double f) {
  var s = f.toString(); // shortest round-trip digits, possibly with an exponent
  final negative = s.startsWith('-');
  if (negative) s = s.substring(1);
  var exp = 0;
  final e = s.indexOf('e');
  if (e >= 0) {
    exp = int.parse(s.substring(e + 1));
    s = s.substring(0, e);
  }
  final point = s.indexOf('.');
  var digits = point < 0 ? s : s.substring(0, point) + s.substring(point + 1);
  var intLen = (point < 0 ? s.length : point) + exp;
  final trimmed = digits.replaceFirst(RegExp(r'^0+'), '');
  intLen -= digits.length - trimmed.length;
  digits = trimmed.replaceFirst(RegExp(r'0+$'), '');
  String out;
  if (digits.isEmpty) {
    out = '0';
  } else if (intLen <= 0) {
    out = '0.${'0' * -intLen}$digits';
  } else if (intLen >= digits.length) {
    out = digits + '0' * (intLen - digits.length);
  } else {
    out = '${digits.substring(0, intLen)}.${digits.substring(intLen)}';
  }
  return negative && out != '0' ? '-$out' : out;
}
