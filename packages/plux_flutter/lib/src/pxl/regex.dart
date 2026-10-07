// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// PXL's regular-expression engine (`pxl.regex.v1`): a bounded RE2 subset
/// matched by a Pike VM in O(n·m) time, with no backtracking. It implements
/// schema/pxl/regex.md exactly as the Go engine (`backend/internal/pxl/regex`)
/// does; the vectors in schema/testdata/regex prove they agree. Dart's own
/// `RegExp` backtracks and differs from RE2, so it is never used for PXL.
library;

const int _maxRune = 0x10FFFF;

/// The deepest nesting of groups a pattern may have.
const int regexMaxNesting = 100;

/// Limits of a pattern, from the limits registry (LIM-001).
final class RegexLimits {
  /// Creates limits.
  const RegexLimits({
    required this.patternLength,
    required this.programSize,
    required this.repeat,
  });

  /// Limits for patterns that come with the runtime, such as the phone
  /// metadata, rather than from a document.
  static const RegexLimits trusted = RegexLimits(
    patternLength: 0x7FFFFFFF,
    programSize: 0x7FFFFFFF,
    repeat: 1000,
  );

  /// The longest pattern in code points.
  final int patternLength;

  /// The most instructions a compiled pattern may have.
  final int programSize;

  /// The largest count of a `{n,m}` repetition.
  final int repeat;
}

/// Why a pattern is rejected; the names are those of the shared vectors.
enum RegexErrorKind {
  /// The pattern is not in the grammar.
  syntax,

  /// The pattern is longer than [RegexLimits.patternLength].
  patternLength,

  /// The program would exceed [RegexLimits.programSize].
  programSize,

  /// A repeat count exceeds [RegexLimits.repeat].
  repeat,
}

/// A rejected pattern, with the code-point offset of the problem.
final class RegexError implements Exception {
  /// Creates an error.
  const RegexError(this.kind, this.offset, this.message);

  /// The kind.
  final RegexErrorKind kind;

  /// The code-point offset in the pattern.
  final int offset;

  /// A description for developers.
  final String message;

  @override
  String toString() => 'regex: ${kind.name} at $offset: $message';
}

enum _Kind { empty, rune, klass, begin, end, capture, concat, alt, repeat }

final class _Node {
  _Node(
    this.kind, {
    this.r = 0,
    this.ranges = const [],
    this.index = 0,
    this.subs = const [],
    this.min = 0,
    this.max = 0,
  });

  final _Kind kind;
  final int r;
  final List<int> ranges;
  final int index;
  final List<_Node> subs;
  final int min;
  final int max; // -1: no upper bound
}

const List<int> _digit = [0x30, 0x39];
const List<int> _word = [0x30, 0x39, 0x41, 0x5A, 0x5F, 0x5F, 0x61, 0x7A];
const List<int> _space = [0x09, 0x0A, 0x0C, 0x0D, 0x20, 0x20];

/// Reads [s] as code points; an unpaired surrogate reads as U+FFFD.
List<int> _decode(String s) {
  final out = <int>[];
  for (var i = 0; i < s.length; i++) {
    final c = s.codeUnitAt(i);
    if (c >= 0xD800 && c <= 0xDBFF && i + 1 < s.length) {
      final d = s.codeUnitAt(i + 1);
      if (d >= 0xDC00 && d <= 0xDFFF) {
        out.add(0x10000 + ((c - 0xD800) << 10) + (d - 0xDC00));
        i++;
        continue;
      }
    }
    out.add(c >= 0xD800 && c <= 0xDFFF ? 0xFFFD : c);
  }
  return out;
}

final class _Parser {
  _Parser(this.src, this.lim);

  final List<int> src;
  final RegexLimits lim;
  int pos = 0;
  int groups = 0;
  int depth = 0;

  bool get more => pos < src.length;
  int get peek => src[pos];

  RegexError _syntax(int at, String message) =>
      RegexError(RegexErrorKind.syntax, at, message);

  _Node parse() {
    final n = alternation();
    if (more) throw _syntax(pos, 'unmatched )');
    return n;
  }

  _Node alternation() {
    final branches = <_Node>[];
    for (;;) {
      branches.add(sequence());
      if (!more || peek != 0x7C) break;
      pos++;
    }
    return branches.length == 1
        ? branches[0]
        : _Node(_Kind.alt, subs: branches);
  }

  static bool _isQuantifier(int c) =>
      c == 0x2A || c == 0x2B || c == 0x3F || c == 0x7B;

  _Node sequence() {
    final items = <_Node>[];
    while (more) {
      final c = peek;
      if (c == 0x7C || c == 0x29) break;
      var a = atom();
      if (more && _isQuantifier(peek)) {
        if (a.kind == _Kind.begin || a.kind == _Kind.end) {
          throw _syntax(pos, 'missing argument to repetition operator');
        }
        a = quantifier(a);
        if (more && _isQuantifier(peek)) {
          throw _syntax(pos, 'invalid nested repetition operator');
        }
      }
      items.add(a);
    }
    return switch (items.length) {
      0 => _Node(_Kind.empty),
      1 => items[0],
      _ => _Node(_Kind.concat, subs: items),
    };
  }

  _Node atom() {
    final at = pos;
    final c = peek;
    pos++;
    switch (c) {
      case 0x5E: // ^
        return _Node(_Kind.begin);
      case 0x24: // $
        return _Node(_Kind.end);
      case 0x2E: // .
        return _Node(_Kind.klass, ranges: const [0, 0x09, 0x0B, _maxRune]);
      case 0x28: // (
        return group(at);
      case 0x5B: // [
        return klass(at);
      case 0x5C: // \
        final (r, ranges) = escape(at);
        return ranges != null
            ? _Node(_Kind.klass, ranges: ranges)
            : _Node(_Kind.rune, r: r);
      case 0x2A || 0x2B || 0x3F || 0x7B:
        throw _syntax(at, 'missing argument to repetition operator');
      case 0x5D || 0x7D:
        throw _syntax(at, 'unescaped ${String.fromCharCode(c)}');
    }
    return _Node(_Kind.rune, r: c);
  }

  _Node group(int at) {
    if (++depth > regexMaxNesting) {
      throw _syntax(at, 'groups nested deeper than $regexMaxNesting');
    }
    var capture = true;
    if (more && peek == 0x3F) {
      if (pos + 1 >= src.length || src[pos + 1] != 0x3A) {
        throw _syntax(at, 'unsupported group syntax; only (?: is allowed');
      }
      pos += 2;
      capture = false;
    }
    final index = capture ? ++groups : 0;
    final sub = alternation();
    if (!more || peek != 0x29) throw _syntax(at, 'missing )');
    pos++;
    depth--;
    return capture ? _Node(_Kind.capture, index: index, subs: [sub]) : sub;
  }

  _Node quantifier(_Node a) {
    final at = pos;
    final c = peek;
    pos++;
    switch (c) {
      case 0x2A:
        return _Node(_Kind.repeat, max: -1, subs: [a]);
      case 0x2B:
        return _Node(_Kind.repeat, min: 1, max: -1, subs: [a]);
      case 0x3F:
        return _Node(_Kind.repeat, max: 1, subs: [a]);
    }
    const invalid = 'invalid repetition; write {n}, {n,} or {n,m}';
    final lo = count(at);
    if (lo == null) throw _syntax(at, invalid);
    var hi = lo;
    if (more && peek == 0x2C) {
      pos++;
      hi = -1;
      if (more && peek != 0x7D) {
        hi = count(at) ?? (throw _syntax(at, invalid));
      }
    }
    if (!more || peek != 0x7D) throw _syntax(at, invalid);
    pos++;
    if (hi >= 0 && hi < lo) {
      throw _syntax(
        at,
        'invalid repetition: {$lo,$hi} has its maximum below its minimum',
      );
    }
    return _Node(_Kind.repeat, min: lo, max: hi, subs: [a]);
  }

  static bool _isDigit(int c) => c >= 0x30 && c <= 0x39;

  int? count(int at) {
    final start = pos;
    if (pos + 1 < src.length && peek == 0x30 && _isDigit(src[pos + 1])) {
      return null; // no leading zeros
    }
    var n = 0;
    while (more && _isDigit(peek)) {
      n = n * 10 + peek - 0x30;
      if (n > lim.repeat) {
        throw RegexError(
          RegexErrorKind.repeat,
          at,
          'a repeat count above ${lim.repeat}',
        );
      }
      pos++;
    }
    return pos > start ? n : null;
  }

  (int, List<int>?) escape(int at) {
    if (!more) throw _syntax(at, 'trailing backslash');
    final c = peek;
    pos++;
    switch (c) {
      case 0x64: // d
        return (0, _digit);
      case 0x44:
        return (0, _negate(_digit));
      case 0x77: // w
        return (0, _word);
      case 0x57:
        return (0, _negate(_word));
      case 0x73: // s
        return (0, _space);
      case 0x53:
        return (0, _negate(_space));
      case 0x74:
        return (0x09, null);
      case 0x6E:
        return (0x0A, null);
      case 0x72:
        return (0x0D, null);
      case 0x66:
        return (0x0C, null);
      case 0x76:
        return (0x0B, null);
      case 0x78:
        return (hex(at), null);
    }
    if (_isPunct(c)) return (c, null);
    throw _syntax(at, 'invalid escape \\${String.fromCharCode(c)}');
  }

  static int _hexValue(int c) {
    if (c >= 0x30 && c <= 0x39) return c - 0x30;
    if (c >= 0x61 && c <= 0x66) return c - 0x61 + 10;
    if (c >= 0x41 && c <= 0x46) return c - 0x41 + 10;
    return -1;
  }

  int hex(int at) {
    final bad = _syntax(
      at,
      'invalid hexadecimal escape; write \\xHH or \\x{H…}',
    );
    var v = 0;
    if (more && peek == 0x7B) {
      pos++;
      var digits = 0;
      while (more && peek != 0x7D) {
        final d = _hexValue(peek);
        if (d < 0 || digits == 6) throw bad;
        v = v * 16 + d;
        digits++;
        pos++;
      }
      if (!more || digits == 0) throw bad;
      pos++;
    } else {
      for (var i = 0; i < 2; i++) {
        if (!more || _hexValue(peek) < 0) throw bad;
        v = v * 16 + _hexValue(peek);
        pos++;
      }
    }
    if (v > _maxRune || (v >= 0xD800 && v <= 0xDFFF)) {
      throw _syntax(at, '\\x escapes a surrogate or a value beyond U+10FFFF');
    }
    return v;
  }

  static bool _isPunct(int c) =>
      (c >= 0x21 && c <= 0x2F) ||
      (c >= 0x3A && c <= 0x40) ||
      (c >= 0x5B && c <= 0x60) ||
      (c >= 0x7B && c <= 0x7E);

  bool get _closesNext => pos + 1 < src.length && src[pos + 1] == 0x5D;

  _Node klass(int at) {
    var negated = false;
    if (more && peek == 0x5E) {
      negated = true;
      pos++;
    }
    final ranges = <int>[];
    var first = true;
    for (;;) {
      if (!more) throw _syntax(at, 'missing ]');
      if (peek == 0x5D) {
        if (first) {
          throw _syntax(at, r'empty class; escape a literal ] as \]');
        }
        pos++;
        break;
      }
      final itemAt = pos;
      final (lo, set) = point(first: first);
      first = false;
      if (set != null) {
        if (more && peek == 0x2D && !_closesNext) {
          throw _syntax(pos, 'a class escape cannot start a range');
        }
        ranges.addAll(set);
        continue;
      }
      var hi = lo;
      if (more && peek == 0x2D && !_closesNext) {
        pos++;
        if (!more) throw _syntax(at, 'missing ]');
        final (h, hset) = point(first: false);
        if (hset != null) {
          throw _syntax(itemAt, 'a class escape cannot end a range');
        }
        if (h < lo) throw _syntax(itemAt, 'invalid range');
        hi = h;
      }
      ranges.addAll([lo, hi]);
    }
    final n = _normalize(ranges);
    return _Node(_Kind.klass, ranges: negated ? _negate(n) : n);
  }

  (int, List<int>?) point({required bool first}) {
    final at = pos;
    final c = peek;
    if (c == 0x5C) {
      pos++;
      return escape(at);
    }
    if (c == 0x5B) throw _syntax(at, 'unescaped [ in a class');
    if (c == 0x2D && !first && !_closesNext) {
      throw _syntax(
        at,
        'unescaped - in a class; escape it or put it first or last',
      );
    }
    pos++;
    return (c, null);
  }
}

List<int> _normalize(List<int> r) {
  final spans = [for (var i = 0; i < r.length; i += 2) (r[i], r[i + 1])]
    ..sort((a, b) => a.$1.compareTo(b.$1));
  final out = <int>[];
  for (final (lo, hi) in spans) {
    if (out.isNotEmpty && lo <= out.last + 1) {
      if (hi > out.last) out.last = hi;
      continue;
    }
    out.addAll([lo, hi]);
  }
  return out;
}

List<int> _negate(List<int> r) {
  final out = <int>[];
  var next = 0;
  for (var i = 0; i < r.length; i += 2) {
    if (r[i] > next) out.addAll([next, r[i] - 1]);
    next = r[i + 1] + 1;
  }
  if (next <= _maxRune) out.addAll([next, _maxRune]);
  return out;
}

int _sizeOf(_Node n, int limit) {
  int sat(int v) => v < limit + 1 ? v : limit + 1;
  switch (n.kind) {
    case _Kind.empty:
      return 0;
    case _Kind.rune || _Kind.klass || _Kind.begin || _Kind.end:
      return 1;
    case _Kind.capture:
      return sat(_sizeOf(n.subs[0], limit) + 2);
    case _Kind.concat || _Kind.alt:
      var total = 0;
      for (final s in n.subs) {
        total = sat(total + _sizeOf(s, limit));
      }
      return n.kind == _Kind.alt ? sat(total + n.subs.length - 1) : total;
    case _Kind.repeat:
      final s = _sizeOf(n.subs[0], limit);
      if (n.max < 0 && n.min == 0) {
        return sat(s + (_nullable(n.subs[0]) ? 2 : 1));
      }
      if (n.max < 0) return sat(sat((n.min - 1) * s) + s + 1);
      return sat(sat(n.min * s) + sat((n.max - n.min) * (s + 1)));
  }
}

bool _nullable(_Node n) => switch (n.kind) {
  _Kind.empty || _Kind.begin || _Kind.end => true,
  _Kind.rune || _Kind.klass => false,
  _Kind.capture => _nullable(n.subs[0]),
  _Kind.concat => n.subs.every(_nullable),
  _Kind.alt => n.subs.any(_nullable),
  _Kind.repeat => n.min == 0 || _nullable(n.subs[0]),
};

enum _Op { rune, klass, begin, end, save, split, match }

final class _Inst {
  _Inst(
    this.op, {
    this.r = 0,
    this.ranges = const [],
    this.arg = 0,
    this.x = 0,
    this.y = 0,
  });

  final _Op op;
  final int r;
  final List<int> ranges;
  final int arg;
  int x;
  int y;

  bool accepts(int c) {
    if (op == _Op.rune) return c == r;
    var lo = 0, hi = ranges.length ~/ 2;
    while (lo < hi) {
      final mid = (lo + hi) ~/ 2;
      if (c < ranges[2 * mid]) {
        hi = mid;
      } else if (c > ranges[2 * mid + 1]) {
        lo = mid + 1;
      } else {
        return true;
      }
    }
    return false;
  }
}

/// Builds a program back to front, so no jumps are needed.
final class _Emitter {
  final List<_Inst> prog = [];

  int add(_Inst i) {
    prog.add(i);
    return prog.length - 1;
  }

  int emit(_Node n, int next) {
    switch (n.kind) {
      case _Kind.empty:
        return next;
      case _Kind.rune:
        return add(_Inst(_Op.rune, r: n.r, x: next));
      case _Kind.klass:
        return add(_Inst(_Op.klass, ranges: n.ranges, x: next));
      case _Kind.begin:
        return add(_Inst(_Op.begin, x: next));
      case _Kind.end:
        return add(_Inst(_Op.end, x: next));
      case _Kind.capture:
        final end = add(_Inst(_Op.save, arg: 2 * n.index + 1, x: next));
        return add(_Inst(_Op.save, arg: 2 * n.index, x: emit(n.subs[0], end)));
      case _Kind.concat:
        var e = next;
        for (var i = n.subs.length - 1; i >= 0; i--) {
          e = emit(n.subs[i], e);
        }
        return e;
      case _Kind.alt:
        final entries = [for (final s in n.subs) emit(s, next)];
        var e = entries.last;
        for (var i = entries.length - 2; i >= 0; i--) {
          e = add(_Inst(_Op.split, x: entries[i], y: e));
        }
        return e;
      case _Kind.repeat:
        return repeat(n, next);
    }
  }

  int repeat(_Node n, int next) {
    final sub = n.subs[0];
    if (n.max < 0 && n.min == 0) {
      if (_nullable(sub)) {
        return add(_Inst(_Op.split, x: plus(sub, next), y: next));
      }
      final loop = add(_Inst(_Op.split, y: next));
      prog[loop].x = emit(sub, loop);
      return loop;
    }
    if (n.max < 0) {
      var e = plus(sub, next);
      for (var i = 0; i < n.min - 1; i++) {
        e = emit(sub, e);
      }
      return e;
    }
    var e = next;
    for (var i = 0; i < n.max - n.min; i++) {
      e = add(_Inst(_Op.split, x: emit(sub, e), y: next));
    }
    for (var i = 0; i < n.min; i++) {
      e = emit(sub, e);
    }
    return e;
  }

  int plus(_Node sub, int next) {
    final loop = add(_Inst(_Op.split, y: next));
    final body = emit(sub, loop);
    prog[loop].x = body;
    return body;
  }
}

enum _Mode { search, prefix, full }

final class _Queue {
  _Queue(int n) : seen = List<bool>.filled(n, false);

  final List<bool> seen;
  final List<int> pcs = [];
  final List<List<int>> caps = [];

  void reset() {
    seen.fillRange(0, seen.length, false);
    pcs.clear();
    caps.clear();
  }
}

/// A compiled pattern; immutable.
final class Regex {
  Regex._(this._prog, this._start, this.groups);

  /// Parses and compiles [pattern] within [limits]; throws [RegexError].
  factory Regex.compile(String pattern, RegexLimits limits) {
    for (var i = 0, at = 0; i < pattern.length; i++, at++) {
      final c = pattern.codeUnitAt(i);
      final pair =
          c >= 0xD800 &&
          c <= 0xDBFF &&
          i + 1 < pattern.length &&
          (pattern.codeUnitAt(i + 1) & 0xFC00) == 0xDC00;
      if (pair) {
        i++;
      } else if (c >= 0xD800 && c <= 0xDFFF) {
        throw RegexError(
          RegexErrorKind.syntax,
          at,
          'the pattern is not valid Unicode',
        );
      }
    }
    final src = _decode(pattern);
    if (src.length > limits.patternLength) {
      throw RegexError(
        RegexErrorKind.patternLength,
        0,
        'the pattern has ${src.length} code points; the limit is '
        '${limits.patternLength}',
      );
    }
    final p = _Parser(src, limits);
    final tree = p.parse();
    final size = _sizeOf(tree, limits.programSize) + 3;
    if (size > limits.programSize) {
      throw RegexError(
        RegexErrorKind.programSize,
        0,
        'the program would have more than ${limits.programSize} instructions',
      );
    }
    final c = _Emitter();
    final match = c.add(_Inst(_Op.match));
    final end = c.add(_Inst(_Op.save, arg: 1, x: match));
    final start = c.add(_Inst(_Op.save, x: c.emit(tree, end)));
    return Regex._(c.prog, start, p.groups);
  }

  final List<_Inst> _prog;
  final int _start;

  /// The number of capturing groups.
  final int groups;

  /// The number of instructions (schema/pxl/regex.md §3).
  int get size => _prog.length;

  /// Whether the pattern matches anywhere in [s].
  bool hasMatch(String s) => _run(_decode(s), _Mode.search, false) != null;

  /// Whether the pattern matches all of [s].
  bool fullMatch(String s) => _run(_decode(s), _Mode.full, false) != null;

  /// The leftmost-first match in [s]: code-point offsets of the whole match
  /// and of each group, -1 for a group that did not participate; null when
  /// nothing matches.
  List<int>? find(String s) => _run(_decode(s), _Mode.search, true);

  /// The leftmost-first match that starts at the beginning of [s], as
  /// [find] returns it.
  List<int>? prefix(String s) => _run(_decode(s), _Mode.prefix, true);

  List<int>? _run(List<int> input, _Mode mode, bool captures) {
    final ncap = captures ? 2 * (groups + 1) : 0;
    var cur = _Queue(_prog.length), next = _Queue(_prog.length);
    final scratch = List<int>.filled(ncap, -1);
    final stack = <int>[]; // pc, or -(slot + 1) followed by the old value
    List<int>? matched;

    void add(_Queue q, int pc, int pos, List<int> caps) {
      scratch.setAll(0, caps);
      stack
        ..clear()
        ..add(pc);
      while (stack.isNotEmpty) {
        final j = stack.removeLast();
        if (j < 0) {
          scratch[-j - 1] = stack.removeLast();
          continue;
        }
        if (q.seen[j]) continue;
        q.seen[j] = true;
        final i = _prog[j];
        switch (i.op) {
          case _Op.split:
            stack
              ..add(i.y)
              ..add(i.x);
          case _Op.save:
            if (i.arg < ncap) {
              stack
                ..add(scratch[i.arg])
                ..add(-(i.arg + 1));
              scratch[i.arg] = pos;
            }
            stack.add(i.x);
          case _Op.begin:
            if (pos == 0) stack.add(i.x);
          case _Op.end:
            if (pos == input.length) stack.add(i.x);
          case _Op.rune || _Op.klass || _Op.match:
            q.pcs.add(j);
            q.caps.add(List<int>.of(scratch));
        }
      }
    }

    final fresh = List<int>.filled(ncap, -1);
    for (var pos = 0; ; pos++) {
      if (matched == null && (pos == 0 || mode == _Mode.search)) {
        add(cur, _start, pos, fresh);
      }
      next.reset();
      for (var t = 0; t < cur.pcs.length; t++) {
        final i = _prog[cur.pcs[t]];
        if (i.op == _Op.match) {
          if (mode == _Mode.full && pos != input.length) continue;
          if (!captures) return const [];
          matched = cur.caps[t];
          break; // lower-priority threads can only find worse matches
        }
        if (pos < input.length && i.accepts(input[pos])) {
          add(next, i.x, pos + 1, cur.caps[t]);
        }
      }
      if (pos == input.length) break;
      final swap = cur;
      cur = next;
      next = swap;
      if (cur.pcs.isEmpty && (matched != null || mode != _Mode.search)) break;
    }
    return matched;
  }
}
