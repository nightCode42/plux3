// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// PXL values (ADR-0009, docs/reference/pxl.md §4): null, bool, int,
/// double, String (also enums, assets and routes), [Decimal], [Money],
/// [PxlDate], [PxlDateTime], [PxlDuration], [PxlColor], `List<Object?>`
/// and `Map<String, Object?>` (also objects).
library;

import 'package:plux_flutter/src/pxl/decimal.dart';

/// An exact amount in an ISO 4217 currency.
final class Money {
  /// Creates a money value.
  const Money(this.amount, this.currency);

  /// The amount.
  final Decimal amount;

  /// The ISO 4217 code.
  final String currency;

  @override
  bool operator ==(Object other) =>
      other is Money && other.currency == currency && other.amount == amount;

  @override
  int get hashCode => Object.hash(currency, amount);

  @override
  String toString() => '$amount $currency';
}

/// A proleptic Gregorian date, as days since 1970-01-01.
final class PxlDate implements Comparable<PxlDate> {
  /// Creates a date from its day number.
  const PxlDate(this.days);

  /// Days since 1970-01-01.
  final int days;

  @override
  int compareTo(PxlDate other) => days.compareTo(other.days);

  @override
  bool operator ==(Object other) => other is PxlDate && other.days == days;

  @override
  int get hashCode => days.hashCode;

  @override
  String toString() {
    final (y, m, d) = civilFromDays(days);
    return '${_pad(y, 4)}-${_pad(m, 2)}-${_pad(d, 2)}';
  }
}

/// An instant with the UTC offset it was written in; equal and ordered by
/// instant.
final class PxlDateTime implements Comparable<PxlDateTime> {
  /// Creates a dateTime.
  const PxlDateTime(this.millis, this.offset);

  /// Milliseconds since 1970-01-01T00:00Z.
  final int millis;

  /// The UTC offset in minutes.
  final int offset;

  /// The local day number and millisecond of the day.
  (int, int) get local {
    final l = millis + offset * msPerMinute;
    final days = floorDiv(l, msPerDay);
    return (days, l - days * msPerDay);
  }

  @override
  int compareTo(PxlDateTime other) => millis.compareTo(other.millis);

  @override
  bool operator ==(Object other) =>
      other is PxlDateTime && other.millis == millis;

  @override
  int get hashCode => millis.hashCode;

  @override
  String toString() {
    final (days, ms) = local;
    final b = StringBuffer(PxlDate(days).toString())
      ..write(
        'T${_pad(ms ~/ msPerHour, 2)}:${_pad(ms % msPerHour ~/ msPerMinute, 2)}:${_pad(ms % msPerMinute ~/ 1000, 2)}',
      );
    if (ms % 1000 != 0) b.write('.${_pad(ms % 1000, 3)}');
    if (offset == 0) {
      b.write('Z');
    } else {
      final o = offset.abs();
      b.write(
        '${offset < 0 ? '-' : '+'}${_pad(o ~/ 60, 2)}:${_pad(o % 60, 2)}',
      );
    }
    return b.toString();
  }
}

/// A length of time in milliseconds.
final class PxlDuration implements Comparable<PxlDuration> {
  /// Creates a duration.
  const PxlDuration(this.millis);

  /// The length in milliseconds.
  final int millis;

  @override
  int compareTo(PxlDuration other) => millis.compareTo(other.millis);

  @override
  bool operator ==(Object other) =>
      other is PxlDuration && other.millis == millis;

  @override
  int get hashCode => millis.hashCode;

  @override
  String toString() {
    if (millis == 0) return 'PT0S';
    final b = StringBuffer(millis < 0 ? '-P' : 'P');
    var ms = BigInt.from(millis).abs();
    final day = BigInt.from(msPerDay);
    if (ms >= day) b.write('${ms ~/ day}D');
    ms = ms % day;
    if (ms == BigInt.zero) return b.toString();
    var rest = ms.toInt();
    b.write('T');
    if (rest >= msPerHour) b.write('${rest ~/ msPerHour}H');
    rest %= msPerHour;
    if (rest >= msPerMinute) b.write('${rest ~/ msPerMinute}M');
    rest %= msPerMinute;
    if (rest > 0) {
      b.write(rest ~/ 1000);
      if (rest % 1000 != 0) {
        b.write('.${_pad(rest % 1000, 3)}'.replaceFirst(RegExp(r'0+$'), ''));
      }
      b.write('S');
    }
    return b.toString();
  }
}

/// A color as red, green, blue and alpha bytes, most significant first.
final class PxlColor {
  /// Creates a color from its 32-bit RGBA value.
  const PxlColor(this.rgba);

  /// The RGBA value.
  final int rgba;

  @override
  bool operator ==(Object other) => other is PxlColor && other.rgba == rgba;

  @override
  int get hashCode => rgba.hashCode;

  @override
  String toString() =>
      '#${rgba.toRadixString(16).toUpperCase().padLeft(8, '0')}';
}

/// Milliseconds per unit.
const int msPerMinute = 60000, msPerHour = 3600000, msPerDay = 86400000;

/// Day numbers of 0001-01-01 and 9999-12-31, the range of dates.
final int minDate = daysFromCivil(1, 1, 1),
    maxDate = daysFromCivil(9999, 12, 31);

String _pad(int n, int width) => n.toString().padLeft(width, '0');

/// Division rounding toward negative infinity.
int floorDiv(int a, int b) {
  final q = a ~/ b;
  return (a % b != 0 && (a < 0) != (b < 0)) ? q - 1 : q;
}

/// Days since 1970-01-01 of a Gregorian date (H. Hinnant's algorithm).
int daysFromCivil(int y, int m, int d) {
  final yy = m <= 2 ? y - 1 : y;
  final era = floorDiv(yy, 400);
  final yoe = yy - era * 400;
  final mp = (m + 9) % 12;
  final doy = (153 * mp + 2) ~/ 5 + d - 1;
  final doe = yoe * 365 + yoe ~/ 4 - yoe ~/ 100 + doy;
  return era * 146097 + doe - 719468;
}

/// The inverse of [daysFromCivil].
(int, int, int) civilFromDays(int z) {
  final zz = z + 719468;
  final era = floorDiv(zz, 146097);
  final doe = zz - era * 146097;
  final yoe = (doe - doe ~/ 1460 + doe ~/ 36524 - doe ~/ 146096) ~/ 365;
  final doy = doe - (365 * yoe + yoe ~/ 4 - yoe ~/ 100);
  final mp = (5 * doy + 2) ~/ 153;
  final d = doy - (153 * mp + 2) ~/ 5 + 1;
  final m = mp < 10 ? mp + 3 : mp - 9;
  return (yoe + era * 400 + (m <= 2 ? 1 : 0), m, d);
}

/// The length of a month.
int daysInMonth(int y, int m) => switch (m) {
  2 => y % 4 == 0 && (y % 100 != 0 || y % 400 == 0) ? 29 : 28,
  4 || 6 || 9 || 11 => 30,
  _ => 31,
};

/// A valid date in years 1–9999, or null.
PxlDate? makeDate(int y, int m, int d) =>
    y < 1 || y > 9999 || m < 1 || m > 12 || d < 1 || d > daysInMonth(y, m)
    ? null
    : PxlDate(daysFromCivil(y, m, d));

/// Whether a day number lies in years 1–9999.
bool validDate(int days) => days >= minDate && days <= maxDate;

/// A dateTime from a local day, time and offset, or null outside years
/// 1–9999.
PxlDateTime? makeDateTime(int days, int msOfDay, int offset) => validDate(days)
    ? PxlDateTime(days * msPerDay + msOfDay - offset * msPerMinute, offset)
    : null;

int? _digits(String s) => RegExp(r'^[0-9]+$').hasMatch(s) ? int.parse(s) : null;

/// Parses YYYY-MM-DD.
PxlDate? parseDate(String s) {
  if (s.length != 10 || s[4] != '-' || s[7] != '-') return null;
  final (y, m, d) = (
    _digits(s.substring(0, 4)),
    _digits(s.substring(5, 7)),
    _digits(s.substring(8, 10)),
  );
  return y == null || m == null || d == null ? null : makeDate(y, m, d);
}

/// Parses RFC 3339 with an offset; fraction digits beyond milliseconds are
/// truncated.
PxlDateTime? parseDateTime(String s) {
  final match = RegExp(
    r'^([0-9]{4}-[0-9]{2}-[0-9]{2})[Tt]([0-9]{2}):([0-9]{2}):([0-9]{2})(?:\.([0-9]{1,9}))?([Zz]|[+-][0-9]{2}:[0-9]{2})$',
  ).firstMatch(s);
  if (match == null) return null;
  final date = parseDate(match[1]!);
  final (h, mi, se) = (
    int.parse(match[2]!),
    int.parse(match[3]!),
    int.parse(match[4]!),
  );
  if (date == null || h > 23 || mi > 59 || se > 59) return null;
  final ms = match[5] == null ? 0 : int.parse('${match[5]}00'.substring(0, 3));
  var offset = 0;
  final o = match[6]!;
  if (o != 'Z' && o != 'z') {
    final (oh, om) = (
      int.parse(o.substring(1, 3)),
      int.parse(o.substring(4, 6)),
    );
    if (oh > 23 || om > 59) return null;
    offset = (oh * 60 + om) * (o[0] == '-' ? -1 : 1);
  }
  return makeDateTime(
    date.days,
    h * msPerHour + mi * msPerMinute + se * 1000 + ms,
    offset,
  );
}

/// Parses [-]P[nD][T[nH][nM][n[.f{1,3}]S]] with at least one component.
PxlDuration? parseDuration(String s) {
  final match = RegExp(
    r'^(-)?P(?:([0-9]+)D)?(?:T(?:([0-9]+)H)?(?:([0-9]+)M)?(?:([0-9]+)(?:\.([0-9]{1,3}))?S)?)?$',
  ).firstMatch(s);
  if (match == null ||
      s.endsWith('T') ||
      [2, 3, 4, 5].every((i) => match[i] == null)) {
    return null;
  }
  var total = BigInt.zero;
  for (final (i, unit) in [
    (2, msPerDay),
    (3, msPerHour),
    (4, msPerMinute),
    (5, 1000),
  ]) {
    if (match[i] != null) total += BigInt.parse(match[i]!) * BigInt.from(unit);
  }
  if (match[6] != null) total += BigInt.parse('${match[6]}00'.substring(0, 3));
  if (!total.isValidInt || total > BigInt.from(0x7FFFFFFFFFFFFFFF)) return null;
  return PxlDuration(match[1] == null ? total.toInt() : -total.toInt());
}

/// Parses #RRGGBB or #RRGGBBAA.
PxlColor? parseColor(String s) {
  if (!RegExp(r'^#([0-9A-Fa-f]{6}|[0-9A-Fa-f]{8})$').hasMatch(s)) return null;
  final hex = s.length == 7 ? '${s.substring(1)}FF' : s.substring(1);
  return PxlColor(int.parse(hex, radix: 16));
}

/// The ECMAScript `Number.prototype.toString` text of a finite double.
String es6(double f) {
  if (f == 0) return '0';
  final s = f
      .toString(); // Dart prints shortest round-trip digits like ECMAScript…
  return s.endsWith('.0')
      ? s.substring(0, s.length - 2)
      : s; // …but keeps ".0" on integers
}

/// Compares strings by code point, unlike [String.compareTo], which
/// compares UTF-16 code units.
int compareCodePoints(String a, String b) {
  final x = a.runes.iterator, y = b.runes.iterator;
  while (true) {
    final (hasX, hasY) = (x.moveNext(), y.moveNext());
    if (!hasX || !hasY) return hasX ? 1 : (hasY ? -1 : 0);
    if (x.current != y.current) return x.current < y.current ? -1 : 1;
  }
}

/// The keys of a map in code-point order.
List<String> sortedKeys(Map<String, Object?> m) =>
    m.keys.toList()..sort(compareCodePoints);
