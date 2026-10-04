// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_go_router/plux_go_router.dart';

// The shared suite and harness live with the runtime's tests (ADR-0040).
import '../../plux_flutter/test/support/delegate_suite.dart';
import '../../plux_flutter/test/support/harness.dart';

/// The host's own screen, a named route plugins open as the native route
/// `profile(userId) → bool` of the routing project's catalogue.
GoRoute _profile() => GoRoute(
  name: 'profile',
  path: '/profile/:userId',
  builder: (context, state) => Scaffold(
    body: Column(
      children: [
        Text('Profile ${state.pathParameters['userId']}'),
        TextButton(
          onPressed: () => context.pop(true),
          child: const Text('Done'),
        ),
      ],
    ),
  ),
);

({GoRouter router, PluxGoRoutes plux}) _router({
  String initial = '/plux/home',
}) {
  final plux = PluxGoRoutes();
  final router = GoRouter(
    initialLocation: initial,
    routes: [
      ...plux.routes,
      plux.shell('main', tabs: ['home', 'second']),
      _profile(),
    ],
  );
  addTearDown(router.dispose);
  return (router: router, plux: plux);
}

DelegateHost _host({String initial = '/plux/home'}) {
  final (:router, plux: _) = _router(initial: initial);
  return DelegateHost(
    navigatorKey: router.routerDelegate.navigatorKey,
    adapter: PluxGoRouter(router),
    app: () => MaterialApp.router(routerConfig: router),
  );
}

void main() {
  delegateSuite('go_router', _host);

  group('go_router adapter', () {
    late Harness h;
    setUp(() async => h = await Harness.create());
    tearDown(() => h.close());

    testWidgets(
      'a location that names a page runs its guards as the redirect: account goes to login, with the redirect\'s parameters [NAV-009] [NAV-006]',
      (tester) async {
        final host = _host();
        final run = await DelegateRun.start(tester, h, host);
        final router = (host.adapter! as PluxGoRouter).router;
        router.go('/plux/account');
        await run.settled(tester);
        expect(find.text('Login from account'), findsOneWidget);
        expect(
          router.routerDelegate.currentConfiguration.uri.toString(),
          '/plux/login?from=account',
        );
        router.go('/plux/vault');
        await run.settled(tester);
        expect(find.text('fallback PLX-4102'), findsOneWidget);
        router.go('/plux/count?n=3');
        await run.settled(tester);
        expect(find.text('Count 3'), findsOneWidget);
        expect(
          run.codes,
          isNot(contains(PluxErrorCode.routeParametersInvalid)),
        );
      },
    );

    testWidgets(
      'a shell is a StatefulShellRoute: each tab keeps its stack, and switchTab selects a tab [NAV-005] [NAV-006]',
      (tester) async {
        final host = _host(initial: '/main/home');
        final run = await DelegateRun.start(tester, h, host);
        expect(find.byType(NavigationBar), findsOneWidget);
        expect(find.text('Second'), findsOneWidget);
        await run.tap(tester, 'Push detail');
        expect(find.text('Item 42'), findsOneWidget);
        // Pushed on the tab's own stack: the bar stays.
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
      'the host\'s named routes are native routes: navigate opens one, a presenting step returns its typed result [HST-031] [NAV-002]',
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
        expect(
          run.codes,
          isNot(contains(PluxErrorCode.nativeRouteNotRegistered)),
        );
      },
    );

    testWidgets(
      'deep links open on the router\'s navigator, with no navigatorKey of their own [NAV-008]',
      (tester) async {
        final host = _host();
        final run = await DelegateRun.start(tester, h, host);
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
        () => PluxGoRoutes().shell('main', tabs: const []),
        throwsArgumentError,
      );
    });
  });
}
