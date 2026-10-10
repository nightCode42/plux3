// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/data/spec.dart';
import 'package:plux_flutter/src/pxl/vm.dart' show PxlLimits;
import 'package:plux_flutter/src/render/renderer.dart';
import 'package:plux_flutter/src/render/sections.dart';

import '../support/harness.dart';

void main() {
  final g = Harness.goldens;
  late Harness h;
  setUp(() async => h = await Harness.create());
  tearDown(() => h.close());

  /// A source of the strings table below, whose configuration holds
  /// [config]'s entries.
  DataSourceSpec decode(BundleView plugin, Map<String, String> config) {
    final strings = [
      'ledger',
      'int',
      for (final e in config.entries) ...[e.key, e.value],
    ];
    final b = fbs.DataSourceObjectBuilder(
      id: fbs.UuidObjectBuilder(hi: 1, lo: 2),
      name: 0,
      kind: fbs.DataSourceKind.Rest,
      type: 1,
      config: fbs.ValueObjectBuilder(
        kind: fbs.ValueKind.Map,
        entries: [
          for (var i = 0; i < config.length; i++)
            fbs.EntryObjectBuilder(
              key: 2 + 2 * i,
              value: fbs.ValueObjectBuilder(
                kind: fbs.ValueKind.String,
                s: 3 + 2 * i,
              ),
            ),
        ],
      ),
    );
    final bytes = b.toBytes();
    return DataSourceSpec.decode(
      fbs.DataSource(bytes),
      strings: (i) => strings[i],
      plugin: plugin,
      limits: PxlLimits.defaults(),
    );
  }

  testWidgets(
    'a source decodes the assurance level its configuration names [SEC-007]',
    (tester) async {
      await tester.runAsync(
        () => h.startFrom(g.bundles['loan-calculator/demo.pxb']!, {
          'loans': g.bundles['loan-calculator/loans.pxb']!,
        }),
      );
      final rt = h.runtime;
      final plugin = (rt.renderer! as PluxRenderer).view(
        rt.active.value!,
        'loans',
      );
      expect(decode(plugin, {'requiresAssurance': 'AL2'}).requiresAssurance, 2);
      expect(decode(plugin, {'requiresAssurance': 'AL1'}).requiresAssurance, 1);
      for (final level in ['AL0', 'AL9', '']) {
        expect(
          decode(plugin, {'requiresAssurance': level}).requiresAssurance,
          0,
        );
      }
      expect(decode(plugin, {'path': '/x'}).requiresAssurance, 0);
    },
  );
}
