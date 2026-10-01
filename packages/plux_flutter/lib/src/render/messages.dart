// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Formats translated messages written in ICU MessageFormat: simple
/// arguments, `select` and `plural` with exact (`=n`) cases, `#`, and
/// apostrophe quoting. Plural categories use the rule shared by English,
/// German and most European languages (`one` for exactly 1, else
/// `other`); CLDR rules for every locale and locale-aware number, date and
/// money formats arrive with `I18N-001` and `I18N-011` (P8).
library;

/// Formats [pattern] with [args]; an argument that is missing renders as
/// an empty string, and a malformed pattern renders as written.
String formatMessage(String pattern, Map<String, Object?> args) {
  try {
    final p = _Parser(pattern, args);
    final out = p.message(null);
    return p.pos == pattern.length ? out : pattern;
  } on FormatException {
    return pattern;
  }
}

final class _Parser {
  _Parser(this.src, this.args);

  final String src;
  final Map<String, Object?> args;
  int pos = 0;

  bool get _done => pos >= src.length;
  String get _c => src[pos];

  /// A message up to an unmatched `}`; [hash] replaces `#` inside plurals.
  String message(String? hash) {
    final b = StringBuffer();
    while (!_done) {
      final c = _c;
      if (c == '}') break;
      if (c == '{') {
        b.write(argument());
      } else if (c == '#' && hash != null) {
        b.write(hash);
        pos++;
      } else if (c == "'") {
        b.write(quoted());
      } else {
        b.write(c);
        pos++;
      }
    }
    return b.toString();
  }

  /// `''` is an apostrophe; `'{...}'` quotes syntax characters.
  String quoted() {
    pos++;
    if (!_done && _c == "'") {
      pos++;
      return "'";
    }
    if (_done || !'{}#|'.contains(_c)) return "'";
    final end = src.indexOf("'", pos);
    if (end < 0) {
      final rest = src.substring(pos);
      pos = src.length;
      return rest;
    }
    final text = src.substring(pos, end);
    pos = end + 1;
    return text;
  }

  String argument() {
    pos++; // {
    skip();
    final name = word();
    skip();
    if (!_done && _c == '}') {
      pos++;
      return _text(args[name]);
    }
    expect(',');
    skip();
    final kind = word();
    skip();
    if (!_done && _c == '}') {
      pos++;
      return _text(args[name]);
    }
    expect(',');
    final cases = <String, String>{};
    final value = args[name];
    final hash = kind == 'plural' ? _text(value) : null;
    skip();
    if (kind == 'plural' && src.startsWith('offset:', pos)) {
      throw const FormatException('plural offsets are not supported');
    }
    while (!_done && _c != '}') {
      final key = word();
      skip();
      expect('{');
      cases[key] = message(hash);
      expect('}');
      skip();
    }
    expect('}');
    return switch (kind) {
      'plural' =>
        cases['=${_text(value)}'] ??
            cases[value is num && value == 1 ? 'one' : 'other'] ??
            cases['other'] ??
            '',
      'select' => cases[_text(value)] ?? cases['other'] ?? '',
      _ => throw FormatException('unsupported argument type $kind'),
    };
  }

  String word() {
    final start = pos;
    while (!_done && !' ,{}\t\n'.contains(_c)) {
      pos++;
    }
    if (start == pos) throw const FormatException('name expected');
    return src.substring(start, pos);
  }

  void skip() {
    while (!_done && ' \t\n'.contains(_c)) {
      pos++;
    }
  }

  void expect(String c) {
    if (_done || _c != c) throw FormatException('$c expected');
    pos++;
  }

  static String _text(Object? v) => switch (v) {
    null => '',
    double() when v == v.truncateToDouble() && v.abs() < 1e15 =>
      v.toInt().toString(),
    _ => v.toString(),
  };
}
