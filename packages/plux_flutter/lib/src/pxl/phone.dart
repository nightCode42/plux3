// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Phone-number validation by region (`pxl.phone.v1`): libphonenumber's
/// parse and isValidNumber on a stricter input syntax, as specified in
/// schema/pxl/phone.md and implemented identically in Go
/// (`backend/internal/pxl/phone`). The metadata's patterns run on the
/// `pxl.regex.v1` engine.
library;

import 'package:plux_flutter/src/pxl/phone.g.dart';
import 'package:plux_flutter/src/pxl/regex.dart';

const int _maxInput = 250;
const int _minNational = 2;
const int _maxNational = 17;
const int _maxCodeWidth = 3;

/// A metadata pattern, compiled on first use.
final class _Pattern {
  _Pattern(this.src);

  final String src;

  late final Regex? _re = () {
    try {
      return Regex.compile(src, RegexLimits.trusted);
    } on RegexError {
      return null; // every pattern compiles: the table tests prove it
    }
  }();

  bool full(String s) => _re?.fullMatch(s) ?? false;

  List<int>? prefix(String s) => _re?.prefix(s);
}

_Pattern? _pattern(String s) => s.isEmpty ? null : _Pattern(s);

final class _Desc {
  _Desc(this.lengths, this.pattern);

  final List<int> lengths;
  final _Pattern? pattern;

  bool matches(String n) =>
      (lengths.isEmpty || lengths.contains(n.length)) &&
      (pattern?.full(n) ?? false);
}

final class _Territory {
  _Territory({
    required this.id,
    required this.code,
    required this.main,
    required this.leading,
    required this.idd,
    required this.nationalPrefix,
    required this.transform,
    required this.general,
    required this.localOnly,
    required this.types,
  });

  final String id;
  final int code;
  final bool main;
  final _Pattern? leading, idd, nationalPrefix;
  final String transform;
  final _Desc general;
  final List<int> localOnly;
  final List<_Desc> types;

  /// getNumberTypeHelper(n) != UNKNOWN.
  bool valid(String n) => general.matches(n) && types.any((d) => d.matches(n));
}

final class _Tables {
  final Map<String, _Territory> byRegion = {};
  final Map<int, List<_Territory>> byCode = {};

  _Territory? region(String r) => r.length == 2 ? byRegion[_upper(r)] : null;
}

String _upper(String s) => String.fromCharCodes(
  s.codeUnits.map((c) => c >= 0x61 && c <= 0x7A ? c - 0x20 : c),
);

List<int> _lengths(String s) =>
    s.isEmpty ? const [] : [for (final p in s.split(',')) int.parse(p)];

final _Tables _tables = () {
  final t = _Tables();
  for (final line in phoneTable.trim().split('\n')) {
    final f = [for (final s in line.split(' ')) s == '~' ? '' : s];
    if (f.length < 10 || f.length.isOdd) continue;
    final x = _Territory(
      id: f[0],
      code: int.parse(f[1]),
      main: f[2] == '1',
      leading: _pattern(f[3]),
      idd: _pattern(f[4]),
      nationalPrefix: _pattern(f[5]),
      transform: f[6],
      general: _Desc(_lengths(f[8]), _pattern(f[7])),
      localOnly: _lengths(f[9]),
      types: [
        for (var i = 10; i + 1 < f.length; i += 2)
          _Desc(_lengths(f[i]), _pattern(f[i + 1])),
      ],
    );
    if (x.id != '001') t.byRegion[x.id] = x;
    final list = t.byCode.putIfAbsent(x.code, () => []);
    x.main ? list.insert(0, x) : list.add(x);
  }
  return t;
}();

/// Whether [region], two ASCII letters in either case, has metadata.
bool isPhoneRegion(String region) => _tables.region(region) != null;

/// Whether [number] is a valid phone number, read with [region] as the
/// default for numbers without a country calling code (schema/pxl/phone.md
/// §3). An unknown region validates only international numbers.
bool isValidPhone(String number, String region) {
  if (number.length > _maxInput && number.runes.length > _maxInput) {
    return false;
  }
  final norm = _normalize(number);
  if (norm == null) return false;
  final def = _tables.region(region);
  final ext = _extractCode(norm.$1, norm.$2, def);
  if (ext == null) return false;
  var (code, national, fromNumber) = ext;
  if (national.length < _minNational) return false;
  final meta = fromNumber ? _tables.byCode[code]!.first : def!;
  final p = _stripNationalPrefix(national, meta);
  final r = _testLength(p, meta);
  if (r == _Length.possible || r == _Length.tooLong) national = p;
  if (national.length < _minNational || national.length > _maxNational) {
    return false;
  }
  return _regionForNumber(code, national)?.valid(national) ?? false;
}

/// The digits of [s] and whether a "+" preceded them; null when [s] has
/// anything but digits, one leading "+" and separators.
(String, bool)? _normalize(String s) {
  final b = StringBuffer();
  var plus = false;
  for (final c in s.codeUnits) {
    if (c >= 0x30 && c <= 0x39) {
      b.writeCharCode(c);
    } else if (c == 0x2B && !plus && b.isEmpty) {
      plus = true;
    } else if (!(c == 0x20 ||
        c == 0x2D ||
        c == 0x2E ||
        c == 0x28 ||
        c == 0x29 ||
        c == 0x2F ||
        c == 0xA0)) {
      return null;
    }
  }
  return b.isEmpty ? null : (b.toString(), plus);
}

/// libphonenumber's maybeExtractCountryCode: the calling code, the
/// national number and whether the code came from the number.
(int, String, bool)? _extractCode(String num, bool plus, _Territory? def) {
  if (plus) return _withCode(num);
  if (def == null) return null;
  final m = def.idd?.prefix(num);
  if (m != null && (m[1] == num.length || num.codeUnitAt(m[1]) != 0x30)) {
    return _withCode(num.substring(m[1]));
  }
  final cc = '${def.code}';
  if (num.startsWith(cc)) {
    final p = _stripNationalPrefix(num.substring(cc.length), def);
    final g = def.general.pattern;
    if ((!(g?.full(num) ?? false) && (g?.full(p) ?? false)) ||
        _testLength(num, def) == _Length.tooLong) {
      return (def.code, p, true);
    }
  }
  return (def.code, num, false);
}

(int, String, bool)? _withCode(String s) {
  if (s.length <= _minNational || s.codeUnitAt(0) == 0x30) return null;
  for (var i = 1; i <= _maxCodeWidth && i <= s.length; i++) {
    final code = int.parse(s.substring(0, i));
    if (_tables.byCode.containsKey(code)) return (code, s.substring(i), true);
  }
  return null;
}

/// libphonenumber's maybeStripNationalPrefixAndCarrierCode.
String _stripNationalPrefix(String p, _Territory m) {
  final np = m.nationalPrefix;
  if (p.isEmpty || np == null) return p;
  final g = np.prefix(p);
  if (g == null) return p;
  final general = m.general.pattern;
  final viable = general?.full(p) ?? false;
  final last = g.length ~/ 2 - 1;
  var candidate = p.substring(g[1]);
  if (m.transform.isNotEmpty && g[2 * last] >= 0) {
    candidate = _expand(m.transform, g, p) + candidate;
  }
  if (viable && !(general?.full(candidate) ?? false)) return p;
  return candidate;
}

String _expand(String rule, List<int> g, String s) {
  final b = StringBuffer();
  for (var i = 0; i < rule.length; i++) {
    if (rule.codeUnitAt(i) == 0x24 && i + 1 < rule.length) {
      final n = rule.codeUnitAt(i + 1) - 0x30;
      if (2 * n + 1 < g.length && g[2 * n] >= 0) {
        b.write(s.substring(g[2 * n], g[2 * n + 1]));
      }
      i++;
      continue;
    }
    b.writeCharCode(rule.codeUnitAt(i));
  }
  return b.toString();
}

enum _Length { possible, localOnly, tooShort, tooLong, invalid }

/// libphonenumber's testNumberLength for an unknown type.
_Length _testLength(String n, _Territory m) {
  final ls = m.general.lengths;
  final l = n.length;
  if (ls.isEmpty) return _Length.invalid;
  if (m.localOnly.contains(l)) return _Length.localOnly;
  if (l == ls.first) return _Length.possible;
  if (l < ls.first) return _Length.tooShort;
  if (l > ls.last) return _Length.tooLong;
  return ls.contains(l) ? _Length.possible : _Length.invalid;
}

/// libphonenumber's getRegionCodeForNumber.
_Territory? _regionForNumber(int code, String n) {
  final list = _tables.byCode[code]!;
  if (list.length == 1) return list.first;
  for (final r in list) {
    final leading = r.leading;
    if (leading != null) {
      if (leading.prefix(n) != null) return r;
    } else if (r.valid(n)) {
      return r;
    }
  }
  return null;
}
