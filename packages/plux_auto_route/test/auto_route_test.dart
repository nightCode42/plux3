// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:async';

import 'package:auto_route/auto_route.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_auto_route/plux_auto_route.dart';
import 'package:plux_flutter/plux_flutter.dart';

// The shared suite and harness live with the runtime's tests (ADR-0040).
import '../../plux_flutter/test/support/delegate_suite.dart';
import '../../plux_flutter/test/support/harness.dart';

/// The host's own screen, a route plugins open as the native route
/// `profile(userId) → bool` of the routing project's catalogue.
AutoRoute _profile() => NamedRouteDef(
  name: 'profile',
  path: '/profile/:userId',
  builder: (context, data) => Scaffold(
    body: Column(
      children: [
        Text('Profile ${data.params.getString('userId')}'),
        TextButton(
          onPressed: () => context.router.maybePop(true),
          child: const Text('Done'),
        ),
      ],
    ),
  ),
);

DelegateHost _host({String initial = '/plux/home'}) {
  final plux = PluxAutoRoutes();
  final router = RootStackRouter.build(
    routes: [
      ...plux.routes,
      plux.shell('main', tabs: ['home', 'second']),
      _profile(),
    ],
  );
  addTearDown(router.dispose);
  return DelegateHost(
    navigatorKey: router.navigatorKey,
    adapter: PluxAutoRoute(router),
    app: () => MaterialApp.router(
      routerConfig: router.config(
        deepLinkBuilder: (_) => DeepLink.path(initial),
      ),
    ),
  );
}

void main() {
  delegateSuite('auto_route', _host);

  group('auto_route adapter', () {
    late Harness h;
    setUp(() async => h = await Harness.create());
    tearDown(() => h.close());

    testWidgets(
      'a path that names a page runs its guards in the route\'s guard: account shows login, vault its fallback [NAV-009] [NAV-006]',
      (tester) async {
        final host = _host();
        final run = await DelegateRun.start(tester, h, host);
        final router = (host.adapter! as PluxAutoRoute).router;
        unawaited(router.pushPath<void>('/plux/account'));
        await run.settled(tester);
        expect(find.text('Login from account'), findsOneWidget);
        unawaited(router.pushPath<void>('/plux/vault'));
        await run.settled(tester);
        expect(find.text('fallback PLX-4102'), findsOneWidget);
        unawaited(router.pushPath<void>('/plux/count?n=3'));
        await run.settled(tester);
        expect(find.text('Count 3'), findsOneWidget);
      },
    );

    testWidgets(
      'a shell is an AutoTabsRouter: each tab keeps its stack, and switchTab selects a tab [NAV-005] [NAV-006]',
      (tester) async {
        final host = _host(initial: '/main');
        final run = await DelegateRun.start(tester, h, host);
        expect(find.byType(NavigationBar), findsOneWidget);
        await run.tap(tester, 'Push detail');
        expect(find.text('Item 42'), findsOneWidget);
        expect(find.byType(NavigationBar).hitTestable(), findsOneWidget);
        await run.tap(tester, 'Second');
        expect(find.text('Second page').hitTestable(), findsOneWidget);
        await run.tap(tester, 'Home tab');
        expect(find.text('Item 42').hitTestable(), findsOneWidget);
        await run.tap(tester, 'Return nothing');
        expect(find.text('Push detail').hitTestable(), findsOneWidget);
        await run.tap(tester, 'Second tab');
        expect(find.text('Second page').hitTestable(), findsOneWidget);
      },
    );

    testWidgets(
      'the host\'s routes are native routes: navigate opens one, a presenting step returns its typed result [HST-031] [NAV-002]',
      (tester) async {
        final host = _host();
        expect(host.adapter!.routes.keys, ['profile']);
        final run = await DelegateRun.start(tester, h, host);
        await run.tap(tester, 'Profile');
        expect(find.text('Profile u-7'), findsOneWidget);
        await run.tap(tester, 'Done');
        await run.tap(tester, 'Ask profile');
        expect(find.text('Profile u-8'), findsOneWidget);
        await run.tap(tester, 'Done');
        expect(
          [for (final e in run.events) '${e.name} ${e.payload}'],
          ['profiled {ok: true}'],
        );
      },
    );

    testWidgets(
      'deep links open on the router\'s navigator, with no navigatorKey of their own [NAV-008]',
      (tester) async {
        final run = await DelegateRun.start(tester, h, _host());
        expect(
          await Plux.handleDeepLink(
            Uri.parse('https://routing.plux.dev/items/5'),
          ),
          isTrue,
        );
        await run.settled(tester);
        expect(find.text('Item 5'), findsOneWidget);
      },
    );

    test('a shell needs a tab', () {
      expect(
        () => PluxAutoRoutes().shell('main', tabs: const []),
        throwsArgumentError,
      );
    });
  });
}
