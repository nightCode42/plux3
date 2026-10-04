// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/plux_flutter.dart';

import '../support/delegate_suite.dart';
import '../support/harness.dart';

/// What router adapters build on (ADR-0040): resolving a location, the
/// shell's bar around a router's tab stacks, and a tab's first page.
void main() {
  late Harness h;
  setUp(() async => h = await Harness.create());
  tearDown(() => h.close());

  DelegateHost host(Widget home) {
    final key = GlobalKey<NavigatorState>();
    return DelegateHost(
      navigatorKey: key,
      app: () => MaterialApp(navigatorKey: key, home: home),
    );
  }

  testWidgets(
    'resolveLocation converts a location\'s text by the declared types and runs the guards [NAV-006] [NAV-009]',
    (tester) async {
      await DelegateRun.start(tester, h, host(const SizedBox()));
      PluxRouteSpec? count;
      PluxRouteSpec? account;
      await tester.runAsync(() async {
        count = await Plux.resolveLocation(
          'count',
          query: {'n': '3', 'utm': 'x'},
        );
        account = await Plux.resolveLocation('account');
      });
      expect(count!.name, 'count');
      expect(count!.arguments, {'n': 3});
      expect(account!.name, 'login');
      expect(account!.arguments, {'from': 'account'});
    },
  );

  testWidgets(
    'PluxShell.routed shows the bar around a router\'s body and hands selection to it; PluxShellTab shows a tab\'s first page [NAV-005] [NAV-006]',
    (tester) async {
      var selected = <int>[];
      final run = await DelegateRun.start(
        tester,
        h,
        host(
          StatefulBuilder(
            builder: (context, setState) => PluxShell.routed(
              'main',
              currentIndex: selected.isEmpty ? 0 : selected.last,
              onSelect: (i) => setState(() => selected = [...selected, i]),
              body: PluxShellTab(
                'main',
                selected.isEmpty || selected.last == 0 ? 'home' : 'second',
              ),
            ),
          ),
        ),
      );
      expect(find.byType(NavigationBar), findsOneWidget);
      expect(find.text('Push detail'), findsOneWidget);
      await run.tap(tester, 'Second');
      expect(selected, [1]);
      expect(find.text('Second page'), findsOneWidget);
      await run.tap(tester, 'Home tab');
      expect(selected, [1, 0]);
    },
  );

  testWidgets(
    'a tab or shell the app document lacks shows the fallback and reports PLX-4100 [NAV-005]',
    (tester) async {
      final run = await DelegateRun.start(
        tester,
        h,
        host(const PluxShellTab('main', 'nowhere')),
      );
      expect(find.text('fallback PLX-4100'), findsOneWidget);
      expect(run.codes, contains(PluxErrorCode.routeNotFound));
    },
  );
}
