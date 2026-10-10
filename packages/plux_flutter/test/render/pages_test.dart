// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_flutter/src/core/plux_view.dart';

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
    Map<String, Object?> params, {
    bool guarded = false,
    int assurance = 0,
  }) async {
    h.server.assuranceLevel = 'AL$assurance';
    await tester.runAsync(
      () => h.startFrom(g.bundles['loan-calculator/demo.pxb']!, {
        'loans': g.bundles['loan-calculator/loans.pxb']!,
      }),
    );
    // What the first token will say, known before it arrives.
    h.runtime.assurance.value = assurance;
    await tester.pumpWidget(
      MaterialApp(
        home: PluxScope(
          // A view whose guards already decided renders the page at once,
          // so a test of the renderer reaches a guarded page.
          child: guarded
              ? routedPluxView(route, params, guarded: true)
              : PluxView(route, inputs: params),
        ),
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

  testWidgets(
    'a parameter that does not fit its type shows the error fallback and reports PLX-4101 [NAV-007]',
    (tester) async {
      await open(tester, 'result', {'schedule': 'not an object'});
      expect(find.text('fallback PLX-4101'), findsOneWidget);
      final e = h.errors.singleWhere(
        (e) => e.code == PluxErrorCode.routeParametersInvalid,
      );
      expect(e.message, contains('schedule'));
    },
  );

  testWidgets(
    'the calculator requires assurance AL1, so it shows its fallback with PLX-6002 at AL0 [NAV-009] [SEC-007]',
    (tester) async {
      await open(tester, 'loan-calculator', {'productId': 'personal-12m'});
      expect(find.text('fallback PLX-6002'), findsOneWidget);
      final e = h.errors.singleWhere(
        (e) => e.code == PluxErrorCode.assuranceInsufficient,
      );
      expect(e.message, contains('requires assurance AL1'));
    },
  );

  testWidgets(
    'past its guards, the calculator shows its fallback: a state entry without a default is set by actions (P5) [RT-020]',
    (tester) async {
      // AL1 is the level the calculator asks for.
      await open(
        tester,
        'loan-calculator',
        {'productId': 'personal-12m'},
        guarded: true,
        assurance: 1,
      );
      expect(find.text('fallback PLX-4001'), findsOneWidget);
      expect(
        h.errors.map((e) => e.message),
        contains(contains('loans/calculator/8: a binding failed')),
      );
    },
  );
}
