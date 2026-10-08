// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/plux_flutter.dart';

import 'harness.dart';

/// A way of hosting Plux pages that [delegateSuite] runs against: the
/// plain `Navigator`, a `Navigator` 2.0 pages list, or a router adapter.
final class DelegateHost {
  /// Describes a host.
  const DelegateHost({
    required this.navigatorKey,
    required this.app,
    this.adapter,
  });

  /// The key of the app's root navigator.
  final GlobalKey<NavigatorState> navigatorKey;

  /// The app, showing the routing project's `home` page first.
  final Widget Function() app;

  /// The router adapter, or null for the default delegate.
  final PluxRouterAdapter? adapter;
}

/// Starts the routing conformance project for [host] in widget tests.
final class DelegateRun {
  DelegateRun._(this.h, this.host);

  /// The harness.
  final Harness h;

  /// The host.
  final DelegateHost host;

  /// The host events plugins emitted.
  final List<PluxHostEvent> events = [];

  /// Starts Plux with the routing bundles and pumps the host's app.
  static Future<DelegateRun> start(
    WidgetTester tester,
    Harness h,
    DelegateHost host, {
    PluxAuthDelegate? auth,
    Map<String, PluxNativeRoute<Object?, Object?>> nativeRoutes = const {},
  }) async {
    final run = DelegateRun._(h, host);
    tester.view
      ..physicalSize = const Size(800, 1400)
      ..devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    await tester.runAsync(
      () => h.startFrom(
        Harness.goldens.bundles['routing/routing.pxb']!,
        {'nav': Harness.goldens.bundles['routing/nav.pxb']!},
        router: host.adapter,
        navigatorKey: host.adapter == null ? host.navigatorKey : null,
        authDelegate: auth,
        nativeRoutes: nativeRoutes,
      ),
    );
    final sub = Plux.events.listen(run.events.add);
    addTearDown(sub.cancel);
    await tester.pumpWidget(PluxScope(child: host.app()));
    await run.settled(tester);
    return run;
  }

  /// A context on the root navigator.
  BuildContext get context => host.navigatorKey.currentContext!;

  /// The codes of the problems reported.
  Iterable<PluxErrorCode> get codes => h.errors.map((e) => e.code);

  /// Lets the runtime's work and the router's finish.
  Future<void> settled(WidgetTester tester) async {
    for (var i = 0; i < 3; i++) {
      await settle(tester);
      await tester.pumpAndSettle();
    }
  }

  /// Taps the text [label] and waits for what it starts.
  Future<void> tap(WidgetTester tester, String label) async {
    await tester.tap(find.text(label).hitTestable());
    await settled(tester);
  }
}

/// Every navigation operation of NAV-005, with typed results (NAV-003) and
/// guards (NAV-009), against one way of hosting Plux pages (ADR-0040): the
/// same suite runs for the default delegate, `PluxPage`, `plux_go_router`
/// and `plux_auto_route`.
void delegateSuite(String name, DelegateHost Function() newHost) {
  group('$name, the shared delegate suite', () {
    late Harness h;
    late DelegateHost host;

    setUp(() async {
      h = await Harness.create();
      host = newHost();
    });
    tearDown(() => h.close());

    testWidgets(
      'a navigate step pushes a page by its route name, and pop returns [NAV-005] [NAV-006]',
      (tester) async {
        final run = await DelegateRun.start(tester, h, host);
        expect(find.text('Push detail'), findsOneWidget);
        await run.tap(tester, 'Push detail');
        expect(find.text('Item 42'), findsOneWidget);
        await run.tap(tester, 'Return nothing');
        expect(find.text('Item 42'), findsNothing);
        expect(find.text('Push detail'), findsOneWidget);
      },
    );

    testWidgets(
      'Plux.open completes with the page\'s typed result [NAV-003] [NAV-006]',
      (tester) async {
        final run = await DelegateRun.start(tester, h, host);
        final result = Plux.open<String>(
          run.context,
          'detail',
          params: {'itemId': '7'},
        );
        await run.settled(tester);
        expect(find.text('Item 7'), findsOneWidget);
        await run.tap(tester, 'Return');
        expect(await result, '7');
      },
    );

    testWidgets(
      'a dialog and a bottom sheet return their results to the graph [NAV-003] [NAV-005] [NAV-006]',
      (tester) async {
        final run = await DelegateRun.start(tester, h, host);
        await run.tap(tester, 'Ask');
        expect(find.byType(Dialog), findsOneWidget);
        await run.tap(tester, 'Yes');
        expect(find.byType(Dialog), findsNothing);
        await run.tap(tester, 'Choose');
        expect(find.byType(BottomSheet), findsOneWidget);
        await run.tap(tester, 'Pick');
        expect(find.byType(BottomSheet), findsNothing);
        expect(
          [for (final e in run.events) '${e.name} ${e.payload}'],
          ['confirmed {answer: true}', 'picked {choice: picked}'],
        );
      },
    );

    testWidgets(
      'replace swaps the page; popping the only page is refused with PLX-4102 [NAV-005] [NAV-006]',
      (tester) async {
        final run = await DelegateRun.start(tester, h, host);
        await run.tap(tester, 'Replace with detail');
        expect(find.text('Item r'), findsOneWidget);
        expect(find.text('Push detail'), findsNothing);
        await run.tap(tester, 'Return nothing');
        expect(find.text('Item r'), findsOneWidget);
        expect(run.codes, contains(PluxErrorCode.navigationRefused));
      },
    );

    testWidgets(
      'pop until returns to a named page, and clear and push leaves its page alone [NAV-005] [NAV-006]',
      (tester) async {
        final run = await DelegateRun.start(tester, h, host);
        await run.tap(tester, 'Push detail');
        await run.tap(tester, 'Deeper');
        expect(find.text('Item 42+'), findsOneWidget);
        await run.tap(tester, 'Back to home');
        expect(find.textContaining('Item'), findsNothing);
        await run.tap(tester, 'Clear and push detail');
        expect(find.text('Item c'), findsOneWidget);
        expect(Navigator.of(run.context).canPop(), isFalse);
      },
    );

    testWidgets(
      'guards run before the page is pushed: signed out, account redirects to login [NAV-009] [NAV-006]',
      (tester) async {
        final run = await DelegateRun.start(tester, h, host);
        unawaited(Plux.open<Object?>(run.context, 'account'));
        await run.settled(tester);
        expect(find.text('Login from account'), findsOneWidget);
        unawaited(Plux.open<Object?>(run.context, 'vault'));
        await run.settled(tester);
        expect(find.text('fallback PLX-6002'), findsOneWidget);
      },
    );
  });
}
