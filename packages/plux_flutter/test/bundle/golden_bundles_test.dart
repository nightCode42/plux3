// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/bundle/container.dart';
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/pxl/program.dart' as pxl;

/// The bundles the Go compiler writes for the conformance projects
/// (backend/internal/compiler, `-update`).
const _goldens = '../../schema/testdata/bundles';

/// Every golden bundle compiled from a conformance project, by path.
Map<String, Uint8List> _compiled() => {
  for (final f in Directory(
    _goldens,
  ).listSync(recursive: true).whereType<File>())
    if (f.path.endsWith('.pxb') && !f.path.endsWith('sample.pxb'))
      f.path.substring(_goldens.length + 1).replaceAll(r'\', '/'): f
          .readAsBytesSync(),
};

/// The nodes and strings of a page or component section.
(List<fbs.Node>, List<String>) _tree(Section s) => switch (s.kind) {
  SectionKind.page => (fbs.Page(s.data).nodes!, fbs.Page(s.data).strings!),
  _ => (fbs.Component(s.data).nodes!, fbs.Component(s.data).strings!),
};

void main() {
  final bundles = _compiled();

  test('the goldens of the conformance projects are present', () {
    expect(bundles.keys.toList()..sort(), [
      'data/data.pxb',
      'data/shop.pxb',
      'db/db.pxb',
      'db/todo.pxb',
      'features/features.dev.pxb',
      'features/features.pxb',
      'features/tasks.dev.pxb',
      'features/tasks.pxb',
      'forms/forms.pxb',
      'forms/signup.pxb',
      'loan-calculator/demo.pxb',
      'loan-calculator/loans.pxb',
      'routing/nav.pxb',
      'routing/routing.pxb',
      'state/notes.pxb',
      'state/state.pxb',
      'triggers/lab.pxb',
      'triggers/triggers.pxb',
      'widgets/gallery.pxb',
      'widgets/widgets.pxb',
    ]);
  });

  test('decodes every compiled bundle with the generated accessors [QA-003] [BND-001] [BND-012]', () {
    for (final MapEntry(key: path, value: data) in bundles.entries) {
      final b = BundleContainer.parse(data);
      final dev = path.endsWith('.dev.pxb');
      expect(
        b.kind,
        dev
            ? 3
            : (path.contains('/tasks') ||
                      path.contains('/loans') ||
                      path.contains('/nav') ||
                      path.contains('/shop') ||
                      path.contains('/notes') ||
                      path.contains('/lab') ||
                      path.contains('/signup') ||
                      path.contains('/todo') ||
                      path.contains('/gallery')
                  ? 1
                  : 2),
        reason: path,
      );
      expect(b.flags, dev ? 2 : 0, reason: '$path: source-map flag');
      expect(b.ofKind(SectionKind.sourceMap).length, dev ? 1 : 0, reason: path);

      final meta = fbs.Meta(b.ofKind(SectionKind.meta).single.data);
      expect(meta.compilerVersion, 'dev', reason: path);
      expect(meta.schemaVersion, '1.0.0', reason: path);
      // The data and db projects raise data.v1 and db.v1 above their
      // minimum of 0.2.0 (ADR-0048, ADR-0049); every other project uses what runtime 0.3.0 is the
      // first to run: lifecycle handlers and R1's actions, or state writes
      // and stored state (ADR-0045, ADR-0046).
      expect(
        meta.minRuntime,
        path.startsWith('data/') || path.startsWith('db/') ? '0.2.0' : '0.3.0',
        reason: path,
      );
      expect(meta.requiredFeatures, isNotNull, reason: path);
      final features = meta.requiredFeatures!;
      expect(
        features,
        [...features]..sort(),
        reason: '$path: features are sorted',
      );

      for (final s in [
        ...b.ofKind(SectionKind.page),
        ...b.ofKind(SectionKind.component),
      ]) {
        final (nodes, strings) = _tree(s);
        expect(strings.first, '', reason: '$path: string 0 is empty');
        for (final n in nodes) {
          final kinds = [
            n.widget != 0,
            n.component != null,
            n.nativeSlot != 0,
          ].where((k) => k);
          expect(
            kinds.length,
            1,
            reason: '$path: a node is one widget, instance or native slot',
          );
          for (final c in [
            ...?n.children,
            for (final f in [...?n.slots]) ...?f.nodes,
          ]) {
            expect(
              c,
              inInclusiveRange(1, nodes.length - 1),
              reason: '$path: child indices',
            );
          }
          expect(n.hints & ~3, 0, reason: '$path: hints are NodeHints bits');
          if (n.nativeSlot != 0) expect(n.nativeSlot, lessThan(strings.length));
        }
      }

      // Every program of the pxl section passes the Dart decoder's
      // verification (PXL-003).
      for (final s in b.ofKind(SectionKind.pxl)) {
        final programs = fbs.Programs(s.data).programs!;
        expect(programs, isNotEmpty, reason: path);
        for (final p in programs) {
          expect(
            () => pxl.Program.decode(Uint8List.fromList(p.code!)),
            returnsNormally,
            reason: '$path: program ${p.id}',
          );
        }
      }
    }
  });

  test('reads what the optimiser wrote into the features plugin [CMP-022] [CMP-024]', () {
    final b = BundleContainer.parse(bundles['features/tasks.pxb']!);
    final texts = <String>[];
    final natives = <String>[];
    final hints = <int>{};
    for (final s in b.ofKind(SectionKind.page)) {
      final (nodes, strings) = _tree(s);
      for (final n in nodes) {
        if (n.nativeSlot != 0) natives.add(strings[n.nativeSlot]);
        hints.add(n.hints);
        for (final p in [...?n.props]) {
          if (p.value?.kind == fbs.ValueKind.String) {
            texts.add(strings[p.value!.s]);
          }
        }
      }
    }
    expect(texts, contains('Tasks 3'), reason: 'the folded constant');
    expect(
      texts,
      isNot(contains('hidden')),
      reason: 'the node with visible false',
    );
    expect(natives, ['MapView']);
    // Hints are a set of bits; a static repaint boundary would be 3.
    expect(
      hints,
      containsAll([1, 2]),
      reason: 'repaint boundaries and static subtrees',
    );
  });
}
