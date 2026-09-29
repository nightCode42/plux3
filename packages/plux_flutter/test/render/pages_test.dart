// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/plux_flutter.dart';

import '../support/harness.dart';

/// The pages of the loan-calculator conformance project.
void main() {
  final g = Harness.goldens;
  late Harness h;
  setUp(() async => h = await Harness.create());
  tearDown(() => h.close());

  Future<void> open(
    WidgetTester tester,
    String route,
    Map<String, Object?> params,
  ) async {
    await tester.runAsync(
      () => h.startFrom(g.bundles['loan-calculator/demo.pxb']!, {
        'loans': g.bundles['loan-calculator/loans.pxb']!,
      }),
    );
    await tester.pumpWidget(
      MaterialApp(
        home: PluxScope(child: PluxView(route, params: params)),
      ),
    );
    await settle(tester);
  }

  testWidgets(
    'renders the result page with a typed parameter [HST-001] [RT-010]',
    (tester) async {
      await open(tester, 'result', {
        'schedule': {
          'monthlyPayment': '4535.42',
          'months': 12,
          'totalInterest': '4425.04',
        },
      });
      expect(find.text('Your schedule'), findsOneWidget);
      expect(find.text('4535.42'), findsOneWidget);
      expect([
        for (final e in h.errors)
          if (e.code != PluxErrorCode.syncFailed) e,
      ], isEmpty);
    },
  );

  testWidgets('a parameter that does not fit its type is reported', (
    tester,
  ) async {
    await open(tester, 'result', {'schedule': 'not an object'});
    expect(
      h.errors.map((e) => e.message),
      contains(contains('parameter schedule')),
    );
  });

  testWidgets(
    'the calculator shows its fallback: a state entry without a default is set by actions (P5) [RT-020]',
    (tester) async {
      await open(tester, 'loan-calculator', {'productId': 'personal-12m'});
      expect(find.text('fallback PLX-4001'), findsOneWidget);
      expect(
        h.errors.map((e) => e.message),
        contains(contains('loans/calculator/8: a binding failed')),
      );
    },
  );
}
