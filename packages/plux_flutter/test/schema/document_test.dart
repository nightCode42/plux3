// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/schema/document.g.dart';

/// The conformance project shared with the compiler (QA-003).
const _example = '../../schema/testdata/documents/loan-calculator';

/// Decodes and re-encodes a document with the generated type for its kind.
Map<String, Object?> _roundTrip(Map<String, Object?> json) =>
    switch (json['kind']) {
      'app' => AppDocument.fromJson(json).toJson(),
      'plugin' => PluginDocument.fromJson(json).toJson(),
      'page' => PageDocument.fromJson(json).toJson(),
      'component' => ComponentDocument.fromJson(json).toJson(),
      'template' => TemplateDocument.fromJson(json).toJson(),
      'actionGraph' => ActionGraphDocument.fromJson(json).toJson(),
      'nativeCatalogue' => NativeCatalogueDocument.fromJson(json).toJson(),
      'theme' => ThemeDocument.fromJson(json).toJson(),
      'translationKeys' => TranslationKeysDocument.fromJson(json).toJson(),
      'translations' => TranslationsDocument.fromJson(json).toJson(),
      'assetIndex' => AssetIndexDocument.fromJson(json).toJson(),
      final kind => throw StateError('unknown kind $kind'),
    };

void main() {
  test('generated Dart types round-trip every example document [SCH-001]', () {
    final files = Directory(_example)
        .listSync(recursive: true)
        .whereType<File>()
        .where((f) => f.path.endsWith('.json'))
        .toList();
    expect(files, hasLength(greaterThanOrEqualTo(10)));
    for (final file in files) {
      final json = jsonDecode(file.readAsStringSync()) as Map<String, Object?>;
      json.remove(r'$schema');
      expect(_roundTrip(json), json, reason: file.path);
    }
  });

  test('a page decodes into typed fields [SCH-022]', () {
    final page = PageDocument.fromJson(
      jsonDecode(
        File('$_example/plugins/loans/pages/calculator.page.json')
            .readAsStringSync(),
      ) as Object,
    );
    expect(page.pageKind, PageKind.screen);
    expect(page.route, 'loan-calculator');
    final body = page.root.slots!['body']!;
    expect(body.one!.type, 'Padding');
    expect(PageKind.fromJson('dialog'), PageKind.dialog);
    expect(() => PageKind.fromJson('popup'), throwsFormatException);
  });

  test('slot fills hold one node or a list [SCH-023]', () {
    final one = SlotFill.fromJson({'id': 'a', 'type': 'Text'});
    final many = SlotFill.fromJson([
      {'id': 'a', 'type': 'Text'},
      {'id': 'b', 'type': 'Icon'},
    ]);
    expect(one.one!.id, 'a');
    expect(many.many, hasLength(2));
    expect(many.toJson(), isA<List<Object?>>());
    expect(one.toJson(), {'id': 'a', 'type': 'Text'});
  });
}
