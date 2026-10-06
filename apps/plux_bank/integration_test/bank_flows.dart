// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Plux Bank's end-to-end flows (DX-004, QA-006) against a running Plux
/// server that has the `plux_bank` fixture project
/// (schema/testdata/documents/plux_bank) promoted to the configured
/// environment, and the reference backend (test/refapi) its environments
/// name. The same flows run on emulators and simulators
/// (`integration_test/app_test.dart`) and on the development machine
/// against a server the Go driver starts (`test/e2e_test.dart`,
/// `make e2e-starter`).
library;

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_bank/bank.dart';
import 'package:plux_flutter/plux_flutter.dart';

/// The texts on screen, for the message of a wait that timed out.
List<String> _texts() => [
  for (final e in find.byType(Text).evaluate()) ?(e.widget as Text).data,
];

/// Waits in real time until [finder] finds at least [count] widgets,
/// pumping frames; the runtime's isolates answer outside the test's fake
/// clock.
Future<void> pumpUntil(
  WidgetTester tester,
  Finder finder, {
  int count = 1,
  Duration timeout = const Duration(seconds: 60),
  List<Object> reported = const [],
}) async {
  final end = DateTime.now().add(timeout);
  while (finder.evaluate().length < count) {
    if (DateTime.now().isAfter(end)) {
      throw TimeoutException(
        'waiting for $count of $finder; on screen: ${_texts()}; '
        'the runtime reported: $reported',
        timeout,
      );
    }
    await tester.runAsync(
      () => Future<void>.delayed(const Duration(milliseconds: 50)),
    );
    await tester.pump(const Duration(milliseconds: 50));
  }
}

/// Taps [finder] once the screen has settled: a page still sliding in
/// moves its widgets, and a tap aimed at a moving widget can miss it.
Future<void> tapSettled(WidgetTester tester, Finder finder) async {
  await tester.pumpAndSettle();
  await tester.tap(finder);
}

/// Types [text] into the text field labelled [label] and lets the page's
/// `onChanged` handler write it into its form.
Future<void> typeInto(WidgetTester tester, String label, String text) async {
  final field = find.widgetWithText(TextFormField, label);
  await pumpUntil(tester, field);
  await tester.enterText(field, text);
  await tester.pump(const Duration(milliseconds: 100));
}

/// Registers the flows. [config] is the app's configuration; the flows are
/// skipped without one. [storage] gives the release store's directory for
/// each launch, null for the platform's, as a device does. [initialize]
/// starts the runtime: `Plux.initialize` on a device, with the platform's
/// key store; the host run replaces the key store, which has no platform
/// side there. The flows start without the app's embedded baseline: their
/// first launch syncs from the server.
void bankFlows({
  required BankConfig? Function() config,
  Future<String?> Function()? storage,
  Future<PluxStartup> Function(PluxConfig) initialize = Plux.initialize,
}) {
  final problems = <PluxException>[];

  /// [pumpUntil], naming what the runtime reported if it times out.
  Future<void> waitFor(WidgetTester tester, Finder finder, {int count = 1}) =>
      pumpUntil(tester, finder, count: count, reported: problems);

  Future<PluxStartup> launch(WidgetTester tester, String? dir) async {
    // A flow that failed left its runtime running.
    await tester.runAsync(Plux.dispose);
    final c = config()!;
    final host = BankHost();
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
    await tester.pumpWidget(BankApp(config: c, startup: startup!, host: host));
    return startup;
  }

  Future<void> stop(WidgetTester tester) async {
    await tester.pumpWidget(const SizedBox());
    await tester.runAsync(Plux.dispose);
  }

  /// Starts the app, waits for the sign-in page and signs in with
  /// [password].
  Future<void> signIn(WidgetTester tester, {required String password}) async {
    final dir = await tester.runAsync(() async => storage?.call());
    final started = await launch(tester, dir);
    expect(started.ready, isTrue, reason: '${started.error}');
    await waitFor(tester, find.text('Welcome back'));
    await typeInto(tester, 'Username', 'demo');
    await typeInto(tester, 'Password', password);
    await tapSettled(tester, find.text('Sign in'));
  }

  final skip = config() == null
      ? 'no server configured: set PLUX_APP_ID (make e2e-starter)'
      : null;

  testWidgets(
    'the exit flow: sign in, see the accounts, make a validated transfer, '
    'read its result, and see the notification and the new balance on the '
    'dashboard [DX-004] [DAT-001] [DAT-011] [DAT-012] [DAT-020] [STA-020]',
    (tester) async {
      problems.clear();
      await signIn(tester, password: 'demo1234');

      // The dashboard: the accounts over GraphQL and the first page of
      // transactions.
      await waitFor(tester, find.text('Your accounts'));
      await waitFor(tester, find.text('Checking'));
      expect(find.text('2500.00 EUR'), findsOneWidget);
      expect(find.text('Savings'), findsOneWidget);
      await waitFor(tester, find.text('Groceries'));

      // The transfer form validates before it sends anything.
      await tapSettled(tester, find.text('New transfer'));
      await waitFor(tester, find.text('Available: 2500.00 EUR'));
      await typeInto(tester, 'Recipient IBAN', 'not an iban');
      await typeInto(tester, 'Amount (EUR)', '12.50');
      await tapSettled(tester, find.text('Send money'));
      await waitFor(tester, find.text('Enter a valid IBAN'));

      // A valid transfer: the result page, then the dashboard.
      await typeInto(tester, 'Recipient IBAN', 'DE44500105175407324931');
      await typeInto(tester, 'Reference', 'Lunch');
      await tapSettled(tester, find.text('Send money'));
      await waitFor(tester, find.text('Transfer completed'));
      expect(find.text('Amount: 12.50 EUR'), findsOneWidget);
      expect(find.text('New balance: 2487.50 EUR'), findsOneWidget);

      await tapSettled(tester, find.text('Back to dashboard'));
      await waitFor(tester, find.text('Your accounts'));
      // The server-sent event of the transfer, and the accounts it
      // refreshed.
      await waitFor(
        tester,
        find.text('Transfer of 12.50 EUR sent. New balance: 2487.50 EUR'),
      );
      await waitFor(tester, find.text('2487.50 EUR'));
      expect(problems, isEmpty, reason: problems.join('\n'));
      await stop(tester);
    },
    skip: skip != null,
    timeout: const Timeout(Duration(minutes: 3)),
  );

  testWidgets(
    'a wrong password shows the error on the sign-in page and signs nobody '
    'in [DX-004] [HST-010]',
    (tester) async {
      problems.clear();
      await signIn(tester, password: 'not the password');
      await waitFor(tester, find.text('Wrong username or password.'));
      expect(find.text('Your accounts'), findsNothing);
      await stop(tester);
    },
    skip: skip != null,
    timeout: const Timeout(Duration(minutes: 3)),
  );

  testWidgets(
    'a transfer over the balance is refused by the form, with its message '
    '[DX-004] [STA-020]',
    (tester) async {
      problems.clear();
      await signIn(tester, password: 'demo1234');
      await waitFor(tester, find.text('Checking'));
      await tapSettled(tester, find.text('New transfer'));
      await waitFor(tester, find.textContaining('Available: '));

      await typeInto(tester, 'Recipient IBAN', 'DE44500105175407324931');
      await typeInto(tester, 'Amount (EUR)', '999999.00');
      await tapSettled(tester, find.text('Send money'));
      await waitFor(
        tester,
        find.text('The amount must be above zero and within your balance'),
      );
      expect(find.text('Transfer completed'), findsNothing);

      // And an amount of zero is no amount either.
      await typeInto(tester, 'Amount (EUR)', '0');
      await tapSettled(tester, find.text('Send money'));
      await waitFor(
        tester,
        find.text('The amount must be above zero and within your balance'),
      );
      expect(problems, isEmpty, reason: problems.join('\n'));
      await stop(tester);
    },
    skip: skip != null,
    timeout: const Timeout(Duration(minutes: 3)),
  );
}
