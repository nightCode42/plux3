// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/plux_flutter.dart';

import '../support/harness.dart';

/// Records what the delegate is asked to do, and does it with the plain
/// Navigator.
final class _RecordingDelegate implements PluxNavigationDelegate {
  final List<String> calls = [];
  static const _plain = PluxNavigatorDelegate();

  @override
  Future<T?> push<T extends Object?>(BuildContext c, PluxRouteSpec r) {
    calls.add('push ${r.name} ${r.presentation.name} ${r.transition.name}');
    return _plain.push<T>(c, r);
  }

  @override
  Future<T?> replace<T extends Object?>(BuildContext c, PluxRouteSpec r) {
    calls.add('replace ${r.name}');
    return _plain.replace<T>(c, r);
  }

  @override
  Future<T?> clearAndPush<T extends Object?>(BuildContext c, PluxRouteSpec r) {
    calls.add('clearAndPush ${r.name}');
    return _plain.clearAndPush<T>(c, r);
  }

  @override
  void popUntil(BuildContext c, String name) {
    calls.add('popUntil $name');
    _plain.popUntil(c, name);
  }

  @override
  bool pop(BuildContext c, [Object? result]) {
    calls.add('pop $result');
    return _plain.pop(c, result);
  }
}

final class _Auth implements PluxAuthDelegate {
  _Auth(this.isAuthenticated);

  @override
  bool isAuthenticated;

  @override
  Future<String?> accessToken() async => null;

  @override
  Future<String?> refresh() async => null;

  @override
  void onLogout() {}
}

/// The routing conformance project: its graphs and pages run on the
/// action engine (ADR-0039) and navigate through the router (ADR-0040).
void main() {
  final g = Harness.goldens;
  late Harness h;
  late List<PluxHostEvent> events;
  late GlobalKey<NavigatorState> navigator;

  setUp(() async {
    h = await Harness.create();
    events = [];
    navigator = GlobalKey();
  });
  tearDown(() => h.close());

  Future<void> start(
    WidgetTester tester, {
    Widget home = const PluxView('home'),
    PluxNavigationDelegate? delegate,
    Widget Function(BuildContext, String)? notFound,
    PluxAuthDelegate? auth,
    bool loanCalculator = false,
  }) async {
    tester.view
      ..physicalSize = const Size(800, 1400)
      ..devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    await tester.runAsync(
      () => loanCalculator
          ? h.startFrom(g.bundles['loan-calculator/demo.pxb']!, {
              'loans': g.bundles['loan-calculator/loans.pxb']!,
            }, notFoundBuilder: notFound)
          : h.startFrom(
              g.bundles['routing/routing.pxb']!,
              {'nav': g.bundles['routing/nav.pxb']!},
              navigationDelegate: delegate,
              notFoundBuilder: notFound,
              authDelegate: auth,
            ),
    );
    final sub = Plux.events.listen(events.add);
    addTearDown(sub.cancel);
    await tester.pumpWidget(
      MaterialApp(
        navigatorKey: navigator,
        home: PluxScope(child: home),
      ),
    );
    await settle(tester);
  }

  Future<void> tap(WidgetTester tester, String label) async {
    await tester.tap(find.text(label).hitTestable());
    await tester.pumpAndSettle();
    await settle(tester);
  }

  Iterable<PluxErrorCode> codes() => h.errors.map((e) => e.code);

  BuildContext ctx() => navigator.currentContext!;

  testWidgets(
    'a navigate step pushes a page of the app by its route name, and pop returns [NAV-001] [NAV-005]',
    (tester) async {
      await start(tester);
      await tap(tester, 'Push detail');
      expect(find.text('Item 42'), findsOneWidget);
      await tap(tester, 'Return nothing');
      expect(find.text('Item 42'), findsNothing);
      expect(find.text('Push detail'), findsOneWidget);
      expect(codes(), isNot(contains(PluxErrorCode.actionsNotAvailable)));
    },
  );

  testWidgets(
    'Plux.open opens a page by name only and completes with its typed result [NAV-003]',
    (tester) async {
      await start(tester);
      final result = Plux.open<String>(
        ctx(),
        'detail',
        params: {'itemId': '7'},
      );
      await tester.pumpAndSettle();
      await settle(tester);
      expect(find.text('Item 7'), findsOneWidget);
      await tap(tester, 'Return');
      expect(await result, '7');
    },
  );

  testWidgets(
    'a result of another type than the host asked for is reported and completes with null [NAV-003]',
    (tester) async {
      await start(tester);
      final result = Plux.open<int>(ctx(), 'detail', params: {'itemId': '7'});
      await tester.pumpAndSettle();
      await settle(tester);
      await tap(tester, 'Return');
      expect(await result, isNull);
      expect(codes(), contains(PluxErrorCode.actionValueInvalid));
    },
  );

  testWidgets(
    'openDialog presents a page and its typed result drives a condition [NAV-003] [NAV-005] [HST-013]',
    (tester) async {
      await start(tester);
      await tap(tester, 'Ask');
      expect(find.byType(Dialog), findsOneWidget);
      expect(find.text('Are you sure?'), findsOneWidget);
      await tap(tester, 'Yes');
      expect(find.byType(Dialog), findsNothing);
      await tap(tester, 'Ask');
      await tap(tester, 'No');
      expect(
        [for (final e in events) '${e.name} ${e.payload}'],
        ['confirmed {answer: true}', 'confirmed {answer: false}'],
      );
    },
  );

  testWidgets(
    'openBottomSheet presents a page as a sheet and returns its result [NAV-005]',
    (tester) async {
      await start(tester);
      await tap(tester, 'Choose');
      expect(find.byType(BottomSheet), findsOneWidget);
      await tap(tester, 'Pick');
      expect(events.single.name, 'picked');
      expect(events.single.payload, {'choice': 'picked'});
    },
  );

  testWidgets(
    'an action of a later phase fails with PLX-4010: onError recovers, otherwise the run ends and is reported [RT-021]',
    (tester) async {
      await start(tester);
      await tap(tester, 'Later action handled');
      expect(events.single.name, 'recovered');
      expect(codes(), isNot(contains(PluxErrorCode.actionsNotAvailable)));
      await tap(tester, 'Later action');
      final e = h.errors.singleWhere(
        (e) => e.code == PluxErrorCode.actionsNotAvailable,
      );
      expect(e.details, containsPair('step', 'toast'));
      expect(e.details, containsPair('route', 'home'));
    },
  );

  testWidgets(
    'stop with an error, an unregistered custom action and an unregistered native route are reported, never thrown [RT-021] [NAV-002]',
    (tester) async {
      await start(tester);
      await tap(tester, 'Stop with error');
      await tap(tester, 'Scan');
      await tap(tester, 'Profile');
      expect(
        codes(),
        containsAll([
          PluxErrorCode.actionCustomError,
          PluxErrorCode.nativeActionNotRegistered,
          PluxErrorCode.nativeRouteNotRegistered,
        ]),
      );
      expect(find.text('Push detail'), findsOneWidget);
    },
  );

  testWidgets(
    'replace and clear-and-push change the stack; popping the only route is refused with PLX-4102 [NAV-005]',
    (tester) async {
      await start(tester);
      await tap(tester, 'Replace with detail');
      expect(find.text('Item r'), findsOneWidget);
      expect(find.text('Push detail'), findsNothing);
      await tap(tester, 'Return nothing');
      expect(find.text('Item r'), findsOneWidget);
      expect(codes(), contains(PluxErrorCode.navigationRefused));
    },
  );

  testWidgets(
    'clear and push leaves the pushed page alone on the stack [NAV-005]',
    (tester) async {
      await start(tester);
      await tap(tester, 'Push detail');
      await tap(tester, 'Deeper');
      expect(find.text('Item 42+'), findsOneWidget);
      expect(Navigator.of(ctx()).canPop(), isTrue);
      navigator.currentState!.popUntil((r) => r.isFirst);
      await tester.pumpAndSettle();
      await tap(tester, 'Clear and push detail');
      expect(find.text('Item c'), findsOneWidget);
      expect(navigator.currentState!.canPop(), isFalse);
    },
  );

  testWidgets('pop until returns to a named route [NAV-005]', (tester) async {
    final delegate = _RecordingDelegate();
    await start(
      tester,
      delegate: delegate,
      home: Builder(
        builder: (context) => FilledButton(
          onPressed: () => unawaited(Plux.open<Object?>(context, 'home')),
          child: const Text('Open home'),
        ),
      ),
    );
    await tap(tester, 'Open home');
    await tap(tester, 'Push detail');
    await tap(tester, 'Deeper');
    await tap(tester, 'Back to home');
    expect(find.text('Push detail'), findsOneWidget);
    expect(find.textContaining('Item'), findsNothing);
    expect(delegate.calls, contains('popUntil home'));
  });

  testWidgets(
    'every navigation goes through the delegate, with the route, its presentation and its transition [NAV-006] [NAV-010]',
    (tester) async {
      final delegate = _RecordingDelegate();
      await start(tester, delegate: delegate);
      await tap(tester, 'Push detail');
      await tap(tester, 'Return nothing');
      await tap(tester, 'Ask');
      await tap(tester, 'Yes');
      await tap(tester, 'Choose');
      await tap(tester, 'Pick');
      expect(delegate.calls, [
        'push detail page slideLeft',
        'pop null',
        'push confirm dialog platform',
        'pop true',
        'push sheet bottomSheet platform',
        'pop picked',
      ]);
    },
  );

  testWidgets(
    'a shell keeps one stack per tab, and switchTab selects a tab [NAV-005] [NAV-006]',
    (tester) async {
      await start(tester, home: const PluxShell('main'));
      expect(find.byType(NavigationBar), findsOneWidget);
      // The second tab's label binds a flag of the app document.
      expect(find.text('Second'), findsOneWidget);
      await tap(tester, 'Push detail');
      expect(find.text('Item 42'), findsOneWidget);
      await tap(tester, 'Second');
      expect(find.text('Second page').hitTestable(), findsOneWidget);
      await tap(tester, 'Home tab');
      expect(find.text('Item 42').hitTestable(), findsOneWidget);
      await tap(tester, 'Return nothing');
      await tap(tester, 'Second tab');
      expect(find.text('Second page').hitTestable(), findsOneWidget);
    },
  );

  testWidgets('switchTab outside a shell is refused with PLX-4102 [NAV-005]', (
    tester,
  ) async {
    await start(tester);
    await tap(tester, 'Second tab');
    expect(codes(), contains(PluxErrorCode.navigationRefused));
  });

  testWidgets(
    'user.authenticated is the auth delegate\'s answer, false without one [HST-010]',
    (tester) async {
      final auth = _Auth(true);
      await start(tester, auth: auth);
      await tap(tester, 'Who');
      expect(events.map((e) => e.name), ['signedIn']);
      auth.isAuthenticated = false;
      await tap(tester, 'Who');
      expect(events, hasLength(1));
    },
  );

  testWidgets(
    'an unknown route opens the app\'s not-found page and reports PLX-4100 [NAV-011]',
    (tester) async {
      await start(tester);
      unawaited(Plux.open<Object?>(ctx(), 'nowhere'));
      await tester.pumpAndSettle();
      await settle(tester);
      expect(find.text('No such page'), findsOneWidget);
      expect(
        h.errors
            .where((e) => e.code == PluxErrorCode.routeNotFound)
            .single
            .details,
        containsPair('route', 'nowhere'),
      );
    },
  );

  testWidgets(
    'without one in the app, the host\'s not-found page, else Plux\'s own [NAV-011]',
    (tester) async {
      await start(
        tester,
        loanCalculator: true,
        notFound: (_, route) => Text('host lost $route'),
        home: const SizedBox(),
      );
      unawaited(Plux.open<Object?>(ctx(), 'nowhere'));
      await tester.pumpAndSettle();
      expect(find.text('host lost nowhere'), findsOneWidget);
    },
  );

  testWidgets(
    'Plux\'s own not-found page when neither the app nor the host names one [NAV-011]',
    (tester) async {
      await start(tester, loanCalculator: true, home: const SizedBox());
      unawaited(Plux.open<Object?>(ctx(), 'nowhere'));
      await tester.pumpAndSettle();
      expect(find.byType(PluxNotFoundPage), findsOneWidget);
      expect(codes(), contains(PluxErrorCode.routeNotFound));
    },
  );

  testWidgets(
    'a missing or unknown parameter shows the error fallback and reports PLX-4101 [NAV-007]',
    (tester) async {
      await start(tester);
      unawaited(Plux.open<Object?>(ctx(), 'detail'));
      await tester.pumpAndSettle();
      await settle(tester);
      expect(find.text('fallback PLX-4101'), findsOneWidget);
      navigator.currentState!.pop();
      await tester.pumpAndSettle();
      unawaited(
        Plux.open<Object?>(
          ctx(),
          'detail',
          params: {'itemId': '1', 'extra': 2},
        ),
      );
      await tester.pumpAndSettle();
      await settle(tester);
      expect(find.text('fallback PLX-4101'), findsOneWidget);
      expect(
        h.errors.where((e) => e.code == PluxErrorCode.routeParametersInvalid),
        hasLength(2),
      );
    },
  );

  testWidgets('a Navigator 2.0 pages list can hold Plux routes [NAV-006]', (
    tester,
  ) async {
    await start(
      tester,
      home: Builder(
        builder: (_) => Navigator(
          pages: [
            const MaterialPage<void>(child: Text('host page')),
            Plux.pageFor<void>('detail', params: {'itemId': 'p'}),
          ],
          onDidRemovePage: (_) {},
        ),
      ),
    );
    expect(find.text('Item p'), findsOneWidget);
  });
}
