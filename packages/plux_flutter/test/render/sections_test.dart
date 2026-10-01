// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/bundle/container.dart';
import 'package:plux_flutter/src/render/sections.dart';

import '../support/harness.dart';

void main() {
  final gallery = BundleContainer.parse(
    Harness.goldens.bundles['widgets/gallery.pxb']!,
  );
  final pages = gallery.ofKind(SectionKind.page).toList();

  test('the section cache is a bounded LRU keyed by section hash [RT-013]', () {
    final cache = SectionCache(maxEntries: 2, maxBytes: 1 << 30);
    var reads = 0;
    NodeSection get(Section s) => cache.get(s, () {
      reads++;
      return NodeSection.page(s);
    });
    get(pages[0]);
    get(pages[1]);
    get(pages[0]); // a hit makes it the most recent
    expect(reads, 2);
    get(pages[2]); // evicts pages[1]
    expect(cache.length, 2);
    get(pages[0]);
    expect(reads, 3);
    get(pages[1]);
    expect(reads, 4);
    cache.resize(maxEntries: 1, maxBytes: 1 << 30);
    expect(cache.length, 1);
    cache.resize(maxEntries: 8, maxBytes: pages[0].data.length);
    get(pages[0]);
    get(pages[2]);
    expect(cache.length, 1, reason: 'the byte bound evicts all but the newest');
    expect(cache.bytes, pages[2].data.length);
    cache.clear();
    expect((cache.length, cache.bytes), (0, 0));
  });

  test('node sections read strings and nodes in place [RT-010]', () {
    final s = NodeSection.page(pages[0]);
    expect(s.string(0), '');
    expect(s.node(0).widget, isNonZero);
    expect(() => s.string(1 << 20), throwsA(anything));
    expect(() => s.node(1 << 20), throwsA(anything));
    expect(s.bytes, pages[0].data.length);
    final component = gallery.ofKind(SectionKind.component);
    expect(component, isEmpty, reason: 'the gallery component is app-level');
  });

  test('UUIDs and unsigned keys', () {
    final id = Uint8List.fromList(List.generate(16, (i) => 0xf0 + i));
    final key = uuidOfId(id);
    expect(
      compareUnsigned(key.$1, 1),
      1,
      reason: 'the sign bit is the top of an unsigned key',
    );
    expect(compareUnsigned(1, 2), -1);
    expect(compareUnsigned(-1, -1), 0);
  });
}
