// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:convert';
import 'dart:io';
import 'dart:math';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/bundle/container.dart';
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/bundle/safe_read.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/schema/limits.g.dart';
import 'package:plux_flutter/src/verify/flatbuffers_verifier.dart';

const _bundles = '../../schema/testdata/bundles';

final _depth = PluxLimit.bundleVerifierDepth.defaultValue;
final _visits = PluxLimit.bundleVerifierTables.defaultValue;

void _verify(int kind, Uint8List data, {int? depth, int? visits}) =>
    verifySection(
      kind,
      data,
      maxDepth: depth ?? _depth,
      maxVisits: visits ?? _visits,
    );

bool _accepts(int kind, Uint8List data) {
  try {
    _verify(kind, data);
    return true;
  } on PluxException catch (e) {
    expect(e.code, PluxErrorCode.sectionVerificationFailed);
    return false;
  }
}

Map<String, BundleContainer> _goldens() => {
  for (final f in Directory(
    _bundles,
  ).listSync(recursive: true).whereType<File>())
    if (f.path.endsWith('.pxb'))
      f.path.substring(_bundles.length + 1).replaceAll(r'\', '/'):
          BundleContainer.parse(f.readAsBytesSync()),
};

/// Reads a page the way the renderer does — enum fields through
/// [readEnum] — touching every node, prop, value and string; an accepted
/// buffer must never make this throw.
void _readPage(Uint8List data) {
  final page = fbs.Page(data);
  final strings = page.strings ?? const <String>[];
  for (final s in strings) {
    s.length;
  }
  void value(fbs.Value? v, int depth) {
    if (v == null || depth > 8) return;
    readEnum(() => v.kind);
    v.i;
    v.d;
    v.s;
    v.unscaled?.length;
    v.uuid?.hi;
    for (final item in v.items ?? const <fbs.Value>[]) {
      value(item, depth + 1);
    }
    for (final e in v.entries ?? const <fbs.Entry>[]) {
      value(e.value, depth + 1);
    }
  }

  for (final n in page.nodes ?? const <fbs.Node>[]) {
    n.widget;
    n.id?.lo;
    n.children?.length;
    n.testId;
    n.hints;
    for (final p in n.props ?? const <fbs.Prop>[]) {
      value(p.value, 0);
    }
    for (final s in n.slots ?? const <fbs.SlotFill>[]) {
      s.nodes?.length;
    }
    for (final o in n.overrides ?? const <fbs.Override>[]) {
      readEnum(() => o.kind);
      for (final p in o.props ?? const <fbs.Prop>[]) {
        value(p.value, 0);
      }
    }
    value(n.visible, 0);
  }
}

void main() {
  final goldens = _goldens();

  test('accepts every section of every golden bundle [BND-006]', () {
    var n = 0;
    for (final MapEntry(key: name, value: b) in goldens.entries) {
      for (final s in b.sections) {
        expect(_accepts(s.kind, s.data), isTrue, reason: '$name ${s.kind}');
        n++;
      }
    }
    expect(n, greaterThan(40));
  });

  test('agrees with the Go verifier on the shared mutation corpus [BND-006] [QA-004]', () {
    final json = jsonDecode(
      File('../../schema/testdata/verifier/vectors.json').readAsStringSync(),
    ) as Map<String, Object?>;
    final cases = (json['cases']! as List<Object?>)
        .cast<Map<String, Object?>>();
    expect(cases.length, greaterThan(300));
    var accepted = 0;
    for (final c in cases) {
      final b = goldens[c['bundle']]!;
      final s = b.sections[c['section']! as int];
      expect(s.kind, c['kind']);
      final data = Uint8List.fromList(s.data);
      for (final e in (c['edits']! as List<Object?>).cast<List<Object?>>()) {
        data[e[0]! as int] = e[1]! as int;
      }
      final ok = _accepts(s.kind, data);
      expect(
        ok,
        c['valid'],
        reason: '${c['bundle']} #${c['section']} ${c['edits']}',
      );
      if (ok) accepted++;
      if (ok && s.kind == SectionKind.page) _readPage(data);
    }
    expect(accepted, greaterThan(0));
  });

  test('enforces the depth and visit limits', () {
    final page = goldens.values
        .expand((b) => b.ofKind(SectionKind.page))
        .first
        .data;
    expect(
      () => _verify(SectionKind.page, page, depth: 1),
      throwsA(isA<PluxException>()),
    );
    expect(
      () => _verify(SectionKind.page, page, visits: 3),
      throwsA(isA<PluxException>()),
    );
  });

  test('rejects wrong identifiers, sizes and kinds', () {
    final meta = goldens.values
        .expand((b) => b.ofKind(SectionKind.meta))
        .first
        .data;
    expect(_accepts(SectionKind.page, meta), isFalse);
    expect(_accepts(SectionKind.meta, Uint8List(4)), isFalse);
    expect(_accepts(99, meta), isFalse);
  });

  test('a page buffer the verifier accepts always reads safely [QA-004]', () {
    final pages = goldens.values
        .expand((b) => b.ofKind(SectionKind.page))
        .map((s) => s.data)
        .toList();
    final rng = Random(1006);
    var accepted = 0;
    for (var i = 0; i < 5000; i++) {
      final data = Uint8List.fromList(pages[rng.nextInt(pages.length)]);
      for (var k = rng.nextInt(3) + 1; k > 0; k--) {
        data[8 + rng.nextInt(data.length - 8)] = rng.nextInt(256);
      }
      if (_accepts(SectionKind.page, data)) {
        accepted++;
        _readPage(data);
      }
    }
    expect(accepted, greaterThan(100));
  });

  test('validates UTF-8 as Go does', () {
    bool v(List<int> b) => validUtf8(Uint8List.fromList(b), 0, b.length);
    expect(v(utf8.encode('héllo €𐍈')), isTrue);
    expect(v([0xC0, 0x80]), isFalse, reason: 'overlong');
    expect(v([0xED, 0xA0, 0x80]), isFalse, reason: 'surrogate');
    expect(v([0xF4, 0x90, 0x80, 0x80]), isFalse, reason: 'above U+10FFFF');
    expect(v([0xE2, 0x82]), isFalse, reason: 'truncated');
    expect(v([0x80]), isFalse, reason: 'continuation byte');
    expect(v([0xF8, 0x80, 0x80, 0x80, 0x80]), isFalse);
  });
}
