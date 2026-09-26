// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/bundle/container.dart';
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart';

/// The golden bundle written by the Go tests of backend/internal/bundle.
Uint8List _golden() =>
    File('../../schema/testdata/bundles/sample.pxb').readAsBytesSync();

void main() {
  test('reads the golden bundle written by Go with the generated accessors [BND-001] [BND-003] [BND-012]', () {
    final data = _golden();
    final b = BundleContainer.parse(data);
    expect(b.kind, 1);
    expect(b.flags, 0);
    expect(b.sections.map((s) => s.kind), [
      SectionKind.meta,
      SectionKind.page,
      SectionKind.page,
      SectionKind.strings,
    ]);
    for (final s in b.sections) {
      expect(
        s.data.offsetInBytes % 8,
        0,
        reason: 'sections are 8-byte aligned views',
      );
      // A view, not a copy: a write through the input shows in the section.
      final before = s.data[0];
      data[s.data.offsetInBytes] ^= 0xff;
      expect(s.data[0], before ^ 0xff);
      data[s.data.offsetInBytes] ^= 0xff;
    }
    final meta = Meta(b.ofKind(SectionKind.meta).single.data);
    expect(meta.kind, BundleKind.Plugin);
    expect(meta.key, 'loans');
    expect(meta.requiredFeatures, ['pxl.v1']);
    final page = Page(b.ofKind(SectionKind.page).first.data);
    expect(page.strings, ['', 'Loan', 'amount', 'medium', 'home']);
    expect(page.nodes!.first.widget, 7);
    expect(page.nodes!.first.props!.length, 2);
    expect(page.nodes![1].testId, 4);
  });

  test('rejects malformed containers', () {
    final good = _golden();
    Uint8List edit(void Function(Uint8List d) f) =>
        Uint8List.fromList(good)..apply(f);
    for (final (name, data) in [
      ('empty', Uint8List(0)),
      ('magic', edit((d) => d[0] = 0x58)),
      ('version', edit((d) => d[4] = 2)),
      ('kind', edit((d) => d[6] = 9)),
      ('flags', edit((d) => d[8] = 4)),
      ('count', edit((d) => d[12] = 200)),
      ('section kind', edit((d) => d[48 + 16] = 0)),
      ('order', edit((d) => d[48 + 72 + 16] = 0x01)),
      ('misaligned', edit((d) => d[48 + 20]++)),
      ('trailing', Uint8List.fromList([...good, 0])),
    ]) {
      expect(
        () => BundleContainer.parse(data),
        throwsA(isA<MalformedBundle>()),
        reason: name,
      );
    }
    expect(
      const MalformedBundle('x').toString(),
      'PLX-3040 BUNDLE_MALFORMED: x',
    );
  });
}

extension on Uint8List {
  void apply(void Function(Uint8List d) f) => f(this);
}
