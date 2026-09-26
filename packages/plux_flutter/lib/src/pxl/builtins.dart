// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The operators and the core standard library of PXL (PXL-006), identical
/// to the Go implementations in `backend/internal/pxl` (arith.go,
/// builtins.go, builtins2.go, value.go).
library;

import 'package:plux_flutter/src/pxl/decimal.dart';
import 'package:plux_flutter/src/pxl/tables.g.dart';
import 'package:plux_flutter/src/pxl/values.dart';
import 'package:plux_flutter/src/pxl/vm.dart';

/// One overload's implementation; arguments have the declared types.
typedef Builtin = Object? Function(Vm vm, List<Object?> args);

const int _minInt = -0x8000000000000000;

PxlError _err(PxlErrorKind kind, String message) => PxlError(kind, message);

/// The implementations indexed by overload ID - 1; null for overloads of
/// groups this runtime does not evaluate (ADR-0009) and for those the
/// checker expands inline.
final List<Builtin?> builtins = () {
  final impl = <String, Builtin>{
    ..._stringFns(),
    ..._numberFns(),
    ..._decimalFns(),
    ..._moneyFns(),
    ..._dateFns(),
    ..._collectionFns(),
    ..._logicFns(),
  };
  return [
    for (final def in overloads) impl['${def.name}(${def.params.join(',')})'],
  ];
}();

// ---------------------------------------------------------------------------
// Values

/// The cost weight of a value: code points of a string, entries of a
/// collection, zero otherwise.
int sizeOf(Object? v) => switch (v) {
  final String s => s.runes.length,
  final List<Object?> l => l.length,
  final Map<String, Object?> m => m.length,
  _ => 0,
};

/// Whether run-time arguments fit a signature, so malformed bytecode cannot
/// reach an implementation.
bool argsMatch(OverloadDef def, List<Object?> args) {
  if (args.length != def.params.length && !(def.variadic && args.isNotEmpty)) {
    return false;
  }
  for (var i = 0; i < args.length; i++) {
    if (!_matches(
      def.params[i < def.params.length ? i : def.params.length - 1],
      args[i],
    )) {
      return false;
    }
  }
  return true;
}

bool _matches(String param, Object? v) {
  final nullable = param.endsWith('?');
  final p = nullable ? param.substring(0, param.length - 1) : param;
  final variable = p == 'T';
  if (v == null) return nullable || variable;
  if (variable) return true;
  if (p.startsWith('list<')) return v is List<Object?>;
  if (p.startsWith('map<')) return v is Map<String, Object?>;
  return switch (p) {
    'int' => v is int,
    'double' => v is double,
    'bool' => v is bool,
    'decimal' => v is Decimal,
    'money' => v is Money,
    'date' => v is PxlDate,
    'dateTime' => v is PxlDateTime,
    'duration' => v is PxlDuration,
    'color' => v is PxlColor,
    _ => v is String, // string, enum and the named enums
  };
}

/// Deep equality of two values of one type; [visits] counts the nested
/// values compared, for the operation budget.
bool deepEqual(Object? a, Object? b, List<int> visits) {
  switch (a) {
    case final List<Object?> x:
      if (b is! List<Object?> || x.length != b.length) return false;
      for (var i = 0; i < x.length; i++) {
        visits[0]++;
        if (!deepEqual(x[i], b[i], visits)) return false;
      }
      return true;
    case final Map<String, Object?> x:
      if (b is! Map<String, Object?> || x.length != b.length) return false;
      for (final k in sortedKeys(x)) {
        visits[0]++;
        if (!b.containsKey(k) || !deepEqual(x[k], b[k], visits)) return false;
      }
      return true;
    default:
      return a == b; // operands share a type (the checker guarantees it)
  }
}

/// Orders two values of an ordered type; money values must share a
/// currency.
int compareValues(int kind, Object? a, Object? b) {
  switch (kind) {
    case CmpKind.ofInt:
      return (a! as int).compareTo(b! as int);
    case CmpKind.ofDouble:
      final (x, y) = (a! as double, b! as double);
      return x < y ? -1 : (x > y ? 1 : 0); // -0 equals 0, as in Go
    case CmpKind.ofString:
      return compareCodePoints(a! as String, b! as String);
    case CmpKind.ofDate:
      return (a! as PxlDate).compareTo(b! as PxlDate);
    case CmpKind.ofDuration:
      return (a! as PxlDuration).compareTo(b! as PxlDuration);
    case CmpKind.ofDecimal:
      return (a! as Decimal).compareTo(b! as Decimal);
    case CmpKind.ofDateTime:
      return (a! as PxlDateTime).compareTo(b! as PxlDateTime);
    case CmpKind.ofMoney:
      final (x, y) = (a! as Money, b! as Money);
      if (x.currency != y.currency) {
        throw _err(
          PxlErrorKind.currencyMismatch,
          '${x.currency} and ${y.currency}',
        );
      }
      return x.amount.compareTo(y.amount);
    default:
      throw _err(PxlErrorKind.invalidProgram, 'unknown comparison kind $kind');
  }
}

// ---------------------------------------------------------------------------
// Operators

/// Negation and widening.
Object? unaryOp(int op, Object? v) {
  switch (op) {
    case Op.intToDouble:
      return (v! as int).toDouble();
    case Op.intToDec:
      return Decimal.fromInt(v! as int);
    case Op.negInt:
      final i = v! as int;
      if (i == _minInt) throw _err(PxlErrorKind.overflow, '-($i)');
      return -i;
    case Op.negDouble:
      return -(v! as double);
    case Op.negDec:
      return -(v! as Decimal);
    case Op.negMoney:
      final m = v! as Money;
      return Money(-m.amount, m.currency);
    default: // Op.negDur
      final d = (v! as PxlDuration).millis;
      if (d == _minInt) throw _err(PxlErrorKind.overflow, 'negated duration');
      return PxlDuration(-d);
  }
}

/// The typed binary operators.
Object? binaryOp(int op, Object? a, Object? b) {
  switch (op) {
    case Op.addInt || Op.subInt || Op.mulInt || Op.divInt || Op.modInt:
      return _intOp(op, a! as int, b! as int);
    case Op.addDouble ||
        Op.subDouble ||
        Op.mulDouble ||
        Op.divDouble ||
        Op.modDouble:
      final (x, y) = (a! as double, b! as double);
      return _finite(switch (op) {
        Op.addDouble => x + y,
        Op.subDouble => x - y,
        Op.mulDouble => x * y,
        Op.divDouble => x / y,
        _ => x.remainder(y), // truncated, like Go's math.Mod
      });
    case Op.addDec:
      return (a! as Decimal) + (b! as Decimal);
    case Op.subDec:
      return (a! as Decimal) - (b! as Decimal);
    case Op.mulDec:
      return (a! as Decimal) * (b! as Decimal);
    case Op.mulMoneyDec:
      final m = a! as Money;
      return Money(m.amount * (b! as Decimal), m.currency);
    case Op.mulDecMoney:
      final m = b! as Money;
      return Money((a! as Decimal) * m.amount, m.currency);
    case Op.addMoney || Op.subMoney:
      final (x, y) = (a! as Money, b! as Money);
      if (x.currency != y.currency) {
        throw _err(
          PxlErrorKind.currencyMismatch,
          '${x.currency} and ${y.currency}',
        );
      }
      return Money(
        op == Op.addMoney ? x.amount + y.amount : x.amount - y.amount,
        x.currency,
      );
    case Op.concatString:
      return (a! as String) + (b! as String);
    case Op.concatList:
      return [...a! as List<Object?>, ...b! as List<Object?>];
    default:
      return _timeOp(op, a, b);
  }
}

int _intOp(int op, int x, int y) {
  switch (op) {
    case Op.addInt:
      final r = x + y;
      if ((r > x) != (y > 0)) throw _err(PxlErrorKind.overflow, '$x + $y');
      return r;
    case Op.subInt:
      final r = x - y;
      if ((r < x) != (y > 0)) throw _err(PxlErrorKind.overflow, '$x - $y');
      return r;
    case Op.mulInt:
      if (x == 0 || y == 0) return 0;
      if ((x == -1 && y == _minInt) || (y == -1 && x == _minInt)) {
        throw _err(PxlErrorKind.overflow, '$x * $y');
      }
      final r = x * y;
      if (r ~/ y != x) throw _err(PxlErrorKind.overflow, '$x * $y');
      return r;
    default:
      if (y == 0) throw _err(PxlErrorKind.divisionByZero, '$x by zero');
      if (op == Op.modInt) return y == -1 ? 0 : x.remainder(y);
      if (x == _minInt && y == -1) throw _err(PxlErrorKind.overflow, '$x / -1');
      return x ~/ y;
  }
}

double _finite(double f) {
  if (f.isNaN || f.isInfinite) {
    throw _err(PxlErrorKind.nonFinite, 'the result is not a finite number');
  }
  return f;
}

Object? _timeOp(int op, Object? a, Object? b) {
  switch (op) {
    case Op.addDur:
      return PxlDuration(
        _intOp(
          Op.addInt,
          (a! as PxlDuration).millis,
          (b! as PxlDuration).millis,
        ),
      );
    case Op.subDur:
      return PxlDuration(
        _intOp(
          Op.subInt,
          (a! as PxlDuration).millis,
          (b! as PxlDuration).millis,
        ),
      );
    case Op.mulDurInt:
      return PxlDuration(
        _intOp(Op.mulInt, (a! as PxlDuration).millis, b! as int),
      );
    case Op.mulIntDur:
      return PxlDuration(
        _intOp(Op.mulInt, (b! as PxlDuration).millis, a! as int),
      );
    case Op.addDtDur:
      return _shift(a! as PxlDateTime, (b! as PxlDuration).millis);
    case Op.addDurDt:
      return _shift(b! as PxlDateTime, (a! as PxlDuration).millis);
    case Op.subDtDur:
      return _shift(a! as PxlDateTime, -(b! as PxlDuration).millis);
    case Op.subDtDt:
      return PxlDuration(
        (a! as PxlDateTime).millis - (b! as PxlDateTime).millis,
      );
    default:
      throw _err(PxlErrorKind.invalidProgram, 'unknown opcode $op');
  }
}

PxlDateTime _shift(PxlDateTime t, int ms) {
  final r = PxlDateTime(t.millis + ms, t.offset);
  if ((ms > 0 && r.millis < t.millis) ||
      (ms < 0 && r.millis > t.millis) ||
      !validDate(r.local.$1)) {
    throw _err(
      PxlErrorKind.invalidArgument,
      'the dateTime leaves years 1–9999',
    );
  }
  return r;
}

// ---------------------------------------------------------------------------
// Strings

int _mapRune(List<int> pairs, int r) {
  var lo = 0, hi = pairs.length ~/ 2;
  while (lo < hi) {
    final mid = (lo + hi) >> 1;
    final k = pairs[2 * mid];
    if (k == r) return pairs[2 * mid + 1];
    if (k < r) {
      lo = mid + 1;
    } else {
      hi = mid;
    }
  }
  return r;
}

String _upper(String s) =>
    String.fromCharCodes(s.runes.map((r) => _mapRune(upperCase, r)));

String _lower(String s) =>
    String.fromCharCodes(s.runes.map((r) => _mapRune(lowerCase, r)));

bool _isSpace(int r) {
  for (var i = 0; i < whiteSpace.length; i += 2) {
    if (r >= whiteSpace[i] && r <= whiteSpace[i + 1]) return true;
  }
  return false;
}

String _trim(String s) {
  final r = s.runes.toList();
  var start = 0, end = r.length;
  while (start < end && _isSpace(r[start])) {
    start++;
  }
  while (end > start && _isSpace(r[end - 1])) {
    end--;
  }
  return String.fromCharCodes(r, start, end);
}

PxlError _stringLimit(Vm vm) => _err(
  PxlErrorKind.sizeLimit,
  'a string longer than ${vm.limits.stringLength} code points',
);

Map<String, Builtin> _stringFns() => {
  'len(string)': (_, a) => sizeOf(a[0]),
  'upper(string)': (_, a) => _upper(a[0]! as String),
  'lower(string)': (_, a) => _lower(a[0]! as String),
  'trim(string)': (_, a) => _trim(a[0]! as String),
  'contains(string,string)': (_, a) =>
      (a[0]! as String).contains(a[1]! as String),
  'startsWith(string,string)': (_, a) =>
      (a[0]! as String).startsWith(a[1]! as String),
  'endsWith(string,string)': (_, a) =>
      (a[0]! as String).endsWith(a[1]! as String),
  'replace(string,string,string)': _replace,
  'split(string,string)': _split,
  'join(list<string>,string)': _join,
  'substring(string,int)': (_, a) =>
      _substring(a[0]! as String, a[1]! as int, sizeOf(a[0])),
  'substring(string,int,int)': (_, a) =>
      _substring(a[0]! as String, a[1]! as int, a[2]! as int),
  'padLeft(string,int,string)': (vm, a) => _pad(vm, a, left: true),
  'padRight(string,int,string)': (vm, a) => _pad(vm, a, left: false),
  'format.iban(string)': (_, a) {
    final r = _upper((a[0]! as String).replaceAll(' ', '')).runes.toList();
    final b = StringBuffer();
    for (var i = 0; i < r.length; i++) {
      if (i > 0 && i % 4 == 0) b.write(' ');
      b.writeCharCode(r[i]);
    }
    return b.toString();
  },
};

Object? _replace(Vm vm, List<Object?> a) {
  final (s, old, repl) = (a[0]! as String, a[1]! as String, a[2]! as String);
  if (old.isEmpty) return s;
  final n = old.allMatches(s).length;
  if (sizeOf(s) + n * (sizeOf(repl) - sizeOf(old)) > vm.limits.stringLength) {
    throw _stringLimit(vm);
  }
  return s.replaceAll(old, repl);
}

Object? _split(Vm vm, List<Object?> a) {
  final (s, sep) = (a[0]! as String, a[1]! as String);
  final n = sep.isEmpty ? sizeOf(s) : sep.allMatches(s).length + 1;
  if (n > vm.limits.collectionSize) {
    throw _err(
      PxlErrorKind.sizeLimit,
      'a list of more than ${vm.limits.collectionSize} items',
    );
  }
  if (sep.isEmpty) {
    return <Object?>[for (final r in s.runes) String.fromCharCode(r)];
  }
  return <Object?>[...s.split(sep)];
}

Object? _join(Vm vm, List<Object?> a) {
  final items = (a[0]! as List<Object?>).cast<String>();
  final sep = a[1]! as String;
  final total =
      items.fold(0, (n, s) => n + sizeOf(s)) +
      (items.isEmpty ? 0 : items.length - 1) * sizeOf(sep);
  if (total > vm.limits.stringLength) throw _stringLimit(vm);
  return items.join(sep);
}

Object? _substring(String s, int start, int end) {
  final r = s.runes.toList();
  if (start < 0 || start > end || end > r.length) {
    throw _err(
      PxlErrorKind.indexOutOfRange,
      'substring($start, $end) of ${r.length} code points',
    );
  }
  return String.fromCharCodes(r, start, end);
}

Object? _pad(Vm vm, List<Object?> a, {required bool left}) {
  final (s, width, p) = (a[0]! as String, a[1]! as int, a[2]! as String);
  if (p.runes.length != 1) {
    throw _err(
      PxlErrorKind.invalidArgument,
      'the padding must be one code point, not "$p"',
    );
  }
  if (width > vm.limits.stringLength) throw _stringLimit(vm);
  final missing = width - sizeOf(s);
  if (missing <= 0) return s;
  final fill = p * missing;
  return left ? fill + s : s + fill;
}

// ---------------------------------------------------------------------------
// Numbers

int _roundDouble(double f, RoundingMode mode) {
  final d = Decimal.fromDouble(f) ?? (throw _err(PxlErrorKind.nonFinite, '$f'));
  return _decToInt(d.round(0, mode));
}

int _decToInt(Decimal d) =>
    d.toInt() ??
    (throw _err(PxlErrorKind.overflow, '$d is outside the int range'));

final _intText = RegExp(r'^-?[0-9]+$');

Map<String, Builtin> _numberFns() {
  final f = <String, Builtin>{
    'abs(int)': (_, a) {
      final x = a[0]! as int;
      if (x == _minInt) throw _err(PxlErrorKind.overflow, 'abs($x)');
      return x.abs();
    },
    'abs(double)': (_, a) => (a[0]! as double).abs(),
    'abs(decimal)': (_, a) => (a[0]! as Decimal).abs(),
    'abs(money)': (_, a) {
      final m = a[0]! as Money;
      return Money(m.amount.abs(), m.currency);
    },
    'round(double)': (_, a) =>
        _roundDouble(a[0]! as double, RoundingMode.halfEven),
    'floor(double)': (_, a) =>
        _roundDouble(a[0]! as double, RoundingMode.floor),
    'ceil(double)': (_, a) =>
        _roundDouble(a[0]! as double, RoundingMode.ceiling),
    'int(double)': (_, a) => _roundDouble(a[0]! as double, RoundingMode.down),
    'int(decimal)': (_, a) =>
        _decToInt((a[0]! as Decimal).round(0, RoundingMode.down)),
    'int(string)': (_, a) {
      final s = a[0]! as String;
      final n = _intText.hasMatch(s) ? BigInt.parse(s) : null;
      if (n == null || !n.isValidInt) {
        throw _err(PxlErrorKind.invalidArgument, '"$s" is not an int');
      }
      return n.toInt();
    },
    'double(int)': (_, a) => (a[0]! as int).toDouble(),
    'double(decimal)': (_, a) => _finite((a[0]! as Decimal).toDouble()),
    'double(string)': (_, a) {
      final s = a[0]! as String;
      if (!_jsonNumber.hasMatch(s)) {
        throw _err(PxlErrorKind.invalidArgument, '"$s" is not a number');
      }
      return _finite(double.parse(s));
    },
  };
  for (final t in ['int', 'double', 'decimal', 'money', 'duration']) {
    f['min($t,$t)'] = _extremum(t, -1);
    f['max($t,$t)'] = _extremum(t, 1);
  }
  for (final t in ['int', 'double', 'decimal']) {
    f['clamp($t,$t,$t)'] = _clamp(t);
  }
  return f;
}

final _jsonNumber = RegExp(r'^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$');

const _cmpKinds = {
  'int': CmpKind.ofInt,
  'double': CmpKind.ofDouble,
  'decimal': CmpKind.ofDecimal,
  'money': CmpKind.ofMoney,
  'string': CmpKind.ofString,
  'date': CmpKind.ofDate,
  'dateTime': CmpKind.ofDateTime,
  'duration': CmpKind.ofDuration,
};

Builtin _extremum(String t, int sign) =>
    (_, a) => compareValues(_cmpKinds[t]!, a[0], a[1]) * sign < 0 ? a[1] : a[0];

Builtin _clamp(String t) => (_, a) {
  final kind = _cmpKinds[t]!;
  if (compareValues(kind, a[1], a[2]) > 0) {
    throw _err(PxlErrorKind.invalidArgument, 'clamp bounds ${a[1]} > ${a[2]}');
  }
  if (compareValues(kind, a[0], a[1]) < 0) return a[1];
  if (compareValues(kind, a[0], a[2]) > 0) return a[2];
  return a[0];
};

// ---------------------------------------------------------------------------
// Decimal and money

int _scaleArg(Vm vm, Object? v) {
  final s = v! as int;
  if (s < 0) throw _err(PxlErrorKind.invalidArgument, 'negative scale $s');
  if (s > vm.limits.decimalDigits || s > 0xFFFF) {
    throw _err(
      PxlErrorKind.sizeLimit,
      'scale $s exceeds ${vm.limits.decimalDigits} digits',
    );
  }
  return s;
}

RoundingMode _modeArg(Object? v) =>
    RoundingMode.byName(v! as String) ??
    (throw _err(PxlErrorKind.invalidArgument, 'unknown rounding mode $v'));

int _minorScale(Money m) =>
    minorUnits[m.currency] ??
    (throw _err(PxlErrorKind.unknownCurrency, '"${m.currency}"'));

Decimal _parseDecimal(Object? v) =>
    Decimal.tryParse(v! as String) ??
    (throw _err(PxlErrorKind.invalidArgument, '"$v" is not a decimal'));

Object? _divDecimal(Vm vm, List<Object?> a, RoundingMode mode) {
  final s = _scaleArg(vm, a[2]);
  return (a[0]! as Decimal).div(a[1]! as Decimal, s, mode) ??
      (throw _err(PxlErrorKind.divisionByZero, 'decimal division by zero'));
}

Map<String, Builtin> _decimalFns() {
  final f = <String, Builtin>{
    'decimal(int)': (_, a) => Decimal.fromInt(a[0]! as int),
    'decimal(double)': (_, a) =>
        Decimal.fromDouble(a[0]! as double) ??
        (throw _err(PxlErrorKind.nonFinite, '${a[0]}')),
    'decimal(string)': (_, a) => _parseDecimal(a[0]),
    'add(decimal,decimal)': (_, a) => binaryOp(Op.addDec, a[0], a[1]),
    'sub(decimal,decimal)': (_, a) => binaryOp(Op.subDec, a[0], a[1]),
    'mul(decimal,decimal)': (_, a) => binaryOp(Op.mulDec, a[0], a[1]),
    'div(decimal,decimal,int)': (vm, a) =>
        _divDecimal(vm, a, RoundingMode.halfEven),
    'div(decimal,decimal,int,RoundingMode)': (vm, a) {
      final mode = _modeArg(a[3]);
      return _divDecimal(vm, a, mode);
    },
    'round(decimal)': (_, a) =>
        (a[0]! as Decimal).round(0, RoundingMode.halfEven),
    'round(decimal,int)': (vm, a) =>
        (a[0]! as Decimal).round(_scaleArg(vm, a[1]), RoundingMode.halfEven),
    'round(decimal,int,RoundingMode)': (vm, a) {
      final s = _scaleArg(vm, a[1]);
      return (a[0]! as Decimal).round(s, _modeArg(a[2]));
    },
    'floor(decimal)': (_, a) => (a[0]! as Decimal).round(0, RoundingMode.floor),
    'ceil(decimal)': (_, a) =>
        (a[0]! as Decimal).round(0, RoundingMode.ceiling),
    'isZero(int)': (_, a) => a[0] == 0,
    'isZero(double)': (_, a) => a[0]! as double == 0,
    'isZero(decimal)': (_, a) => (a[0]! as Decimal).isZero,
    'isZero(money)': (_, a) => (a[0]! as Money).amount.isZero,
  };
  for (final MapEntry(key: t, value: kind) in _cmpKinds.entries) {
    f['compare($t,$t)'] = (_, a) => compareValues(kind, a[0], a[1]);
  }
  return f;
}

Money _newMoney(Decimal d, String code) {
  if (!minorUnits.containsKey(code)) {
    throw _err(
      PxlErrorKind.unknownCurrency,
      '"$code" is not an ISO 4217 currency',
    );
  }
  return Money(d, code);
}

Object? _divMoney(List<Object?> a, RoundingMode mode) {
  final m = a[0]! as Money;
  final q = m.amount.div(a[1]! as Decimal, _minorScale(m), mode);
  if (q == null) {
    throw _err(PxlErrorKind.divisionByZero, 'money division by zero');
  }
  return Money(q, m.currency);
}

Object? _roundMoney(Object? v, RoundingMode mode) {
  final m = v! as Money;
  return Money(m.amount.round(_minorScale(m), mode), m.currency);
}

Map<String, Builtin> _moneyFns() => {
  'money(decimal,string)': (_, a) =>
      _newMoney(a[0]! as Decimal, a[1]! as String),
  'money(string,string)': (_, a) =>
      _newMoney(_parseDecimal(a[0]), a[1]! as String),
  'add(money,money)': (_, a) => binaryOp(Op.addMoney, a[0], a[1]),
  'sub(money,money)': (_, a) => binaryOp(Op.subMoney, a[0], a[1]),
  'mul(money,decimal)': (_, a) => binaryOp(Op.mulMoneyDec, a[0], a[1]),
  'div(money,decimal)': (_, a) => _divMoney(a, RoundingMode.halfEven),
  'div(money,decimal,RoundingMode)': (_, a) => _divMoney(a, _modeArg(a[2])),
  'round(money)': (_, a) => _roundMoney(a[0], RoundingMode.halfEven),
  'round(money,RoundingMode)': (_, a) => _roundMoney(a[0], _modeArg(a[1])),
  'currency(money)': (_, a) => (a[0]! as Money).currency,
  'amount(money)': (_, a) => (a[0]! as Money).amount,
};

// ---------------------------------------------------------------------------
// Dates

T _checked<T>(T? v, String message) =>
    v ?? (throw _err(PxlErrorKind.invalidArgument, message));

PxlError _outOfYears() =>
    _err(PxlErrorKind.invalidArgument, 'the date leaves years 1–9999');

int _addDays(int days, int n) {
  final r = days + n;
  if (n > maxDate - minDate || n < minDate - maxDate || !validDate(r)) {
    throw _outOfYears();
  }
  return r;
}

int _addMonths(int days, int n) {
  if (n > 12 * 9999 || n < -12 * 9999) throw _outOfYears();
  final (y, m, d) = civilFromDays(days);
  final total = y * 12 + (m - 1) + n;
  final ny = floorDiv(total, 12), nm = total - ny * 12 + 1;
  if (ny < 1 || ny > 9999) throw _outOfYears();
  final dim = daysInMonth(ny, nm);
  return daysFromCivil(ny, nm, d < dim ? d : dim);
}

PxlDateTime _atDay(PxlDateTime t, int days, int ms) =>
    makeDateTime(days, ms, t.offset)!;

int _weekday(int days) => days + 3 - floorDiv(days + 3, 7) * 7 + 1;

Builtin _order(int kind, int sign) =>
    (_, a) => compareValues(kind, a[0], a[1]) == sign;

Map<String, Builtin> _dateFns() => {
  'date(string)': (_, a) =>
      _checked(parseDate(a[0]! as String), '"${a[0]}" is not a date'),
  'date(int,int,int)': (_, a) => _checked(
    makeDate(a[0]! as int, a[1]! as int, a[2]! as int),
    '${a[0]}-${a[1]}-${a[2]} is not a date in years 1–9999',
  ),
  'date(dateTime)': (_, a) => PxlDate((a[0]! as PxlDateTime).local.$1),
  'dateTime(string)': (_, a) => _checked(
    parseDateTime(a[0]! as String),
    '"${a[0]}" is not an RFC 3339 dateTime with an offset',
  ),
  'duration(int)': (_, a) => PxlDuration(a[0]! as int),
  'duration(string)': (_, a) => _checked(
    parseDuration(a[0]! as String),
    '"${a[0]}" is not an ISO 8601 duration',
  ),
  'addDays(date,int)': (_, a) =>
      PxlDate(_addDays((a[0]! as PxlDate).days, a[1]! as int)),
  'addDays(dateTime,int)': (_, a) {
    final t = a[0]! as PxlDateTime;
    final (days, ms) = t.local;
    return _atDay(t, _addDays(days, a[1]! as int), ms);
  },
  'addMonths(date,int)': (_, a) =>
      PxlDate(_addMonths((a[0]! as PxlDate).days, a[1]! as int)),
  'addMonths(dateTime,int)': (_, a) {
    final t = a[0]! as PxlDateTime;
    final (days, ms) = t.local;
    return _atDay(t, _addMonths(days, a[1]! as int), ms);
  },
  'diffDays(date,date)': (_, a) =>
      (a[1]! as PxlDate).days - (a[0]! as PxlDate).days,
  'startOfDay(dateTime)': (_, a) {
    final t = a[0]! as PxlDateTime;
    return _atDay(t, t.local.$1, 0);
  },
  'weekday(date)': (_, a) => _weekday((a[0]! as PxlDate).days),
  'weekday(dateTime)': (_, a) => _weekday((a[0]! as PxlDateTime).local.$1),
  'isBefore(date,date)': _order(CmpKind.ofDate, -1),
  'isBefore(dateTime,dateTime)': _order(CmpKind.ofDateTime, -1),
  'isAfter(date,date)': _order(CmpKind.ofDate, 1),
  'isAfter(dateTime,dateTime)': _order(CmpKind.ofDateTime, 1),
};

// ---------------------------------------------------------------------------
// Collections

Object? _at(List<Object?> l, int i) => i < 0 || i >= l.length ? null : l[i];

Object? _sum(
  List<Object?> l,
  Object? zero,
  bool Function(Object? x) isItem,
  Object? Function(Object? x, Object? y) add,
) {
  var acc = zero;
  for (final x in l) {
    if (!isItem(x)) {
      throw _err(PxlErrorKind.invalidProgram, 'sum over ${x.runtimeType}');
    }
    acc = add(acc, x);
  }
  return acc;
}

String _scalarKey(Object? v) => switch (v) {
  final Decimal d => 'd${d.reduced()}',
  final Money m => 'm${m.currency}${m.amount.reduced()}',
  final PxlDateTime t => 't${t.millis}',
  final double f => 'f${es6(f)}',
  _ => '${v.runtimeType}:$v',
};

Map<String, Builtin> _collectionFns() => {
  'size(list<T>)': (_, a) => sizeOf(a[0]),
  'size(map<string,T>)': (_, a) => sizeOf(a[0]),
  'isEmpty(list<T>)': (_, a) => sizeOf(a[0]) == 0,
  'isEmpty(map<string,T>)': (_, a) => sizeOf(a[0]) == 0,
  'isEmpty(string)': (_, a) => (a[0]! as String).isEmpty,
  'first(list<T>)': (_, a) => _at(a[0]! as List<Object?>, 0),
  'last(list<T>)': (_, a) {
    final l = a[0]! as List<Object?>;
    return _at(l, l.length - 1);
  },
  'at(list<T>,int)': (_, a) => _at(a[0]! as List<Object?>, a[1]! as int),
  'slice(list<T>,int,int)': (_, a) {
    final (l, start, end) = (
      a[0]! as List<Object?>,
      a[1]! as int,
      a[2]! as int,
    );
    if (start < 0 || start > end || end > l.length) {
      throw _err(
        PxlErrorKind.indexOutOfRange,
        'slice($start, $end) of ${l.length} items',
      );
    }
    return l.sublist(start, end);
  },
  'distinct(list<T>)': (_, a) {
    final seen = <String>{};
    return <Object?>[
      for (final x in a[0]! as List<Object?>)
        if (seen.add(_scalarKey(x))) x,
    ];
  },
  'keys(map<string,T>)': (_, a) => <Object?>[
    ...sortedKeys(a[0]! as Map<String, Object?>),
  ],
  'values(map<string,T>)': (_, a) {
    final m = a[0]! as Map<String, Object?>;
    return <Object?>[for (final k in sortedKeys(m)) m[k]];
  },
  'has(map<string,T>,string)': (_, a) =>
      (a[0]! as Map<String, Object?>).containsKey(a[1]),
  'sum(list<int>)': (_, a) => _sum(
    a[0]! as List<Object?>,
    0,
    (x) => x is int,
    (x, y) => _intOp(Op.addInt, x! as int, y! as int),
  ),
  'sum(list<double>)': (_, a) => _sum(
    a[0]! as List<Object?>,
    0.0,
    (x) => x is double,
    (x, y) => _finite((x! as double) + (y! as double)),
  ),
  'sum(list<decimal>)': (vm, a) => _sum(
    a[0]! as List<Object?>,
    Decimal.fromInt(0),
    (x) => x is Decimal,
    (x, y) {
      final r = (x! as Decimal) + (y! as Decimal);
      vm.checkSize(r);
      return r;
    },
  ),
  'sum(list<duration>)': (_, a) => _sum(
    a[0]! as List<Object?>,
    const PxlDuration(0),
    (x) => x is PxlDuration,
    (x, y) => _timeOp(Op.addDur, x, y),
  ),
};

// ---------------------------------------------------------------------------
// Conversion and validation

Map<String, Builtin> _logicFns() {
  final f = <String, Builtin>{
    'string(int)': (_, a) => '${a[0]}',
    'string(double)': (_, a) => es6(a[0]! as double),
    'string(bool)': (_, a) => '${a[0]}',
    'string(enum)': (_, a) => a[0],
    'isEmail(string)': (_, a) => _isEmail(a[0]! as String),
    'isIban(string)': (_, a) => _isIban(a[0]! as String),
    'isNumeric(string)': (_, a) => Decimal.tryParse(a[0]! as String) != null,
    'luhn(string)': (_, a) => _luhn(a[0]! as String),
  };
  for (final t in [
    'decimal',
    'money',
    'date',
    'dateTime',
    'duration',
    'color',
  ]) {
    f['string($t)'] = (_, a) => '${a[0]}';
  }
  return f;
}

bool _isAlpha(int c) => (c >= 0x61 && c <= 0x7A) || (c >= 0x41 && c <= 0x5A);

bool _isDigit(int c) => c >= 0x30 && c <= 0x39;

bool _isAlnum(int c) => _isAlpha(c) || _isDigit(c);

const _localSpecials = "!#\$%&'*+/=?^`{|}~.-";

bool _isEmail(String s) {
  final at = s.indexOf('@');
  if (at < 0 || s.runes.any((r) => r >= 0x80) || s.length > 254) return false;
  final local = s.substring(0, at), domain = s.substring(at + 1);
  if (local.isEmpty ||
      local.length > 64 ||
      local.startsWith('.') ||
      local.endsWith('.') ||
      local.contains('..')) {
    return false;
  }
  if (!local.codeUnits.every(
    (c) => _isAlnum(c) || c == 0x5F || _localSpecials.codeUnits.contains(c),
  )) {
    return false;
  }
  final labels = domain.split('.');
  if (labels.length < 2) return false;
  for (final l in labels) {
    if (l.isEmpty || l.length > 63 || l.startsWith('-') || l.endsWith('-')) {
      return false;
    }
    if (!l.codeUnits.every((c) => _isAlnum(c) || c == 0x2D)) return false;
  }
  final tld = labels.last;
  return tld.length >= 2 && tld.codeUnits.every(_isAlpha);
}

bool _isIban(String input) {
  final s = _upper(input.replaceAll(' ', ''));
  if (s.runes.any((r) => r >= 0x80)) return false;
  final c = s.codeUnits;
  bool isUpper(int x) => x >= 0x41 && x <= 0x5A;
  if (c.length < 15 ||
      c.length > 34 ||
      !isUpper(c[0]) ||
      !isUpper(c[1]) ||
      !_isDigit(c[2]) ||
      !_isDigit(c[3])) {
    return false;
  }
  final digits = StringBuffer();
  for (final x in [...c.sublist(4), ...c.sublist(0, 4)]) {
    if (_isDigit(x)) {
      digits.writeCharCode(x);
    } else if (isUpper(x)) {
      digits.write(x - 0x41 + 10);
    } else {
      return false;
    }
  }
  return BigInt.parse(digits.toString()) % BigInt.from(97) == BigInt.one;
}

bool _luhn(String s) {
  if (s.length < 2 || !s.codeUnits.every(_isDigit)) return false;
  var total = 0;
  for (var i = 0; i < s.length; i++) {
    var d = s.codeUnitAt(s.length - 1 - i) - 0x30;
    if (i.isOdd) {
      d *= 2;
      if (d > 9) d -= 9;
    }
    total += d;
  }
  return total % 10 == 0;
}
