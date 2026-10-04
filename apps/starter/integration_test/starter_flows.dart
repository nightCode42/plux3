// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The starter app's end-to-end flows (QA-006) against a running Plux
/// server that has the `starter` fixture project
/// (schema/testdata/documents/starter) promoted to the configured
/// environment. The same flows run on emulators and simulators
/// (`integration_test/app_test.dart`) and on the development machine
/// against a server the Go driver starts (`test/e2e_test.dart`,
/// `make e2e-starter`).
library;

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_starter/starter.dart';

/// The welcome page's text, as the fixture's translations have it.
const welcomeBody =
    'This page was published from the server and rendered natively.';

/// Waits in real time until [finder] finds at least [count] widgets,
/// pumping frames; the runtime's sync isolate answers outside the test's
/// fake clock.
Future<void> pumpUntil(
  WidgetTester tester,
  Finder finder, {
  int count = 1,
  Duration timeout = const Duration(seconds: 60),
}) async {
  final end = DateTime.now().add(timeout);
  while (finder.evaluate().length < count) {
    if (DateTime.now().isAfter(end)) {
      throw TimeoutException('waiting for $count of $finder', timeout);
    }
    await tester.runAsync(
      () => Future<void>.delayed(const Duration(milliseconds: 50)),
    );
    await tester.pump(const Duration(milliseconds: 50));
  }
}

/// Taps [finder] once the screen has settled: a page still sliding in
/// moves its widgets, and a tap aimed at a moving widget can miss it
/// (CI run 37156438886, iOS).
Future<void> tapSettled(WidgetTester tester, Finder finder) async {
  await tester.pumpAndSettle();
  await tester.tap(finder);
}

/// Registers the flows. [config] is the app's configuration; the flows
/// are skipped without one. [storage] gives the release store's directory
/// for each launch, null for the platform's, as a device does.
/// [initialize] starts the runtime: `Plux.initialize` on a device, with
/// the platform's key store; the host run replaces the key store, which
/// has no platform side there. The flows start without the app's embedded
/// baseline: their first launch syncs from the server, and a baseline
/// `make dev` pulled into the app belongs to another installation.
void starterFlows({
  required StarterConfig? Function() config,
  Future<String?> Function()? storage,
  Future<PluxStartup> Function(PluxConfig) initialize = Plux.initialize,
}) {
  final problems = <PluxException>[];

  late StarterHost host;

  Future<PluxStartup> launch(WidgetTester tester, String? dir) async {
    final c = config()!;
    host = StarterHost();
    final startup = await tester.runAsync(
      () => initialize(
        c.toPluxConfig(
          host: host,
          storageDirectory: dir,
          onError: (e, _) => problems.add(e),
          baseline: null,
        ),
      ),
    );
    await tester.pumpWidget(
      StarterApp(config: c, startup: startup!, host: host),
    );
    return startup;
  }

  // Scrolls the home screen to the tile [key] and taps it.
  Future<void> tapTile(WidgetTester tester, String key) async {
    final tile = find.byKey(ValueKey(key));
    // The home screen's list is the first scrollable; plugin pages may
    // hold others.
    await tester.scrollUntilVisible(
      tile,
      200,
      scrollable: find.byType(Scrollable).first,
    );
    await tester.tap(tile);
  }

  Future<void> stop(WidgetTester tester) async {
    await tester.pumpWidget(const SizedBox());
    await tester.runAsync(Plux.dispose);
  }

  final skip = config() == null
      ? 'no server configured: set PLUX_APP_ID (make e2e-starter)'
      : null;

  testWidgets(
    'a first launch syncs the published release, renders its page inside '
    'the host and full screen, and a relaunch starts from the cache '
    '[QA-006] [SYN-003] [HST-001]',
    (tester) async {
      problems.clear();
      final dir = await tester.runAsync(() async => storage?.call());
      // First launch: nothing on the device, so start-up waits for the
      // first sync (SYN-003).
      final first = await launch(tester, dir);
      expect(first.ready, isTrue, reason: '${first.error}');
      await pumpUntil(tester, find.text(welcomeBody));
      expect(find.text('Welcome to Plux'), findsOneWidget);
      // The icon comes from the font the server subset (THM-005), the
      // logo from the asset file the device synced (AST-001).
      await pumpUntil(tester, find.bySemanticsLabel('Hello'));
      await pumpUntil(tester, find.bySemanticsLabel('Plux logo'));

      // The host opens the same page full screen (HST-001, Plux.open).
      // Once the route's transition ends the home screen below it is
      // offstage, and a device may draw the new page only after that, so
      // offstage copies count too.
      await tester.tap(find.byKey(const ValueKey('open-page')));
      await pumpUntil(
        tester,
        find.text(welcomeBody, skipOffstage: false),
        count: 2,
      );
      // The pushed route is on screen once its app bar's back button is.
      await pumpUntil(tester, find.byType(BackButton));
      await tester.pageBack();
      await tester.pumpAndSettle();

      // The host's controls reach the runtime.
      await tester.tap(find.text('Share usage analytics'));
      await tester.tap(find.text('Dark theme'));
      await tester.pumpAndSettle();
      final again = await tester.runAsync(() async => Plux.sync());
      expect(again!.outcome, SyncOutcome.upToDate);
      expect(problems, isEmpty, reason: problems.join('\n'));
      await stop(tester);

      // A relaunch renders from the release on the device at once.
      final second = await launch(tester, dir);
      expect(second.sequence, first.sequence);
      await pumpUntil(tester, find.text(welcomeBody));
      await stop(tester);
    },
    skip: skip != null,
    timeout: const Timeout(Duration(minutes: 3)),
  );

  testWidgets(
    'mixed screens: two plugin views share the counter the host writes, and '
    "a native slot's event in a plugin page runs its navigate action "
    '[HST-021] [NAV-004] [WGT-033] [STA-030]',
    (tester) async {
      problems.clear();
      final dir = await tester.runAsync(() async => storage?.call());
      final started = await launch(tester, dir);
      expect(started.ready, isTrue, reason: '${started.error}');
      await pumpUntil(tester, find.text(welcomeBody));

      // A native screen with two views of the exported component.
      await tester.tap(find.byKey(const ValueKey('open-mixed')));
      await pumpUntil(tester, find.text('Left: 0'));
      expect(find.text('Right: 0'), findsOneWidget);
      await tapSettled(tester, find.byKey(const ValueKey('add-one')));
      await pumpUntil(tester, find.text('Left: 1'));
      expect(find.text('Right: 1'), findsOneWidget);
      await pumpUntil(tester, find.text('Native view of the counter: 1'));
      await tester.pumpAndSettle();
      await tester.pageBack();
      await tester.pumpAndSettle();

      // A plugin page holding the host's map card.
      await tester.tap(find.byKey(const ValueKey('open-places')));
      await pumpUntil(tester, find.text('Harbour'));
      await tapSettled(tester, find.text('Harbour'));
      await pumpUntil(tester, find.text('Place: Harbour'));
      expect(problems, isEmpty, reason: problems.join('\n'));
      await stop(tester);
    },
    skip: skip != null,
    timeout: const Timeout(Duration(minutes: 3)),
  );

  testWidgets(
    'typed routes, a guard, a deep link, a notification, a native route '
    'and action, and a plugin event [NAV-002] [NAV-008] [NAV-009] '
    '[HST-010] [HST-011] [HST-013] [HST-030] [ACT-060]',
    (tester) async {
      problems.clear();
      final dir = await tester.runAsync(() async => storage?.call());
      final started = await launch(tester, dir);
      expect(started.ready, isTrue, reason: '${started.error}');
      await pumpUntil(tester, find.text(welcomeBody));

      // Signed out, the account page's guard sends the user to sign in.
      await tapTile(tester, 'open-account');
      await pumpUntil(
        tester,
        find.text('Sign in on the home screen to see your account.'),
      );
      await tester.pumpAndSettle();
      await tester.pageBack();
      await tester.pumpAndSettle();
      // Signed in, the guard lets the page open, which reads the plan.
      await tapTile(tester, 'sign-in');
      await tester.pump();
      await tapTile(tester, 'open-account');
      await pumpUntil(tester, find.text('Plan: pro'));
      await tester.pumpAndSettle();
      await tester.pageBack();
      await tester.pumpAndSettle();

      // A link opens a plugin page, whose buttons call the host's native
      // action, emit a host event and open the host's native route.
      await tapTile(tester, 'open-link');
      await pumpUntil(tester, find.text('Place: Lighthouse'));
      await tapSettled(tester, find.text('Share'));
      await pumpUntil(tester, find.text('Shared: Place: Lighthouse'));
      await tapSettled(tester, find.text('Profile'));
      await pumpUntil(tester, find.text('Profile of Lighthouse'));
      // Past the transitions, so one back button is on screen.
      await tester.pumpAndSettle();
      await tester.pageBack();
      await pumpUntil(tester, find.text('Place: Lighthouse'));
      await tester.pumpAndSettle();
      await tester.pageBack();
      await pumpUntil(tester, find.text('Last shared: Lighthouse'));

      // A notification's payload opens its page.
      await tapTile(tester, 'open-notification');
      await pumpUntil(tester, find.text('Place: Beach'));
      await tester.pumpAndSettle();
      await tester.pageBack();
      await tester.pumpAndSettle();
      expect(problems, isEmpty, reason: problems.join('\n'));
      await stop(tester);
    },
    skip: skip != null,
    timeout: const Timeout(Duration(minutes: 3)),
  );
}
