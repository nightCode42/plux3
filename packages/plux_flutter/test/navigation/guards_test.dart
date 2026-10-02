// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_flutter/src/navigation/guards.dart';

import '../support/harness.dart';

final class _Auth implements PluxAuthDelegate {
  _Auth(this.isAuthenticated);

  @override
  bool isAuthenticated;

  @override
  Future<String?> accessToken() async => 'secret-access-token';

  @override
  Future<String?> refresh() async => 'secret-refreshed-token';

  @override
  void onLogout() {}
}

/// The guarded pages of the routing conformance project (NAV-009,
/// ADR-0040): an authentication guard with a redirect, a feature-flag
/// guard, an assurance level, guards that redirect in a cycle, and a
/// moved page redirecting twice.
void main() {
  final g = Harness.goldens;
  late Harness h;
  late GlobalKey<NavigatorState> navigator;

  setUp(() async {
    h = await Harness.create();
    navigator = GlobalKey();
  });
  tearDown(() => h.close());

  Future<void> start(
    WidgetTester tester, {
    Widget home = const Text('host'),
    PluxAuthDelegate? auth,
    PluxConsent consent = PluxConsent.necessaryOnly,
  }) async {
    await tester.runAsync(
      () => h.startFrom(
        g.bundles['routing/routing.pxb']!,
        {'nav': g.bundles['routing/nav.pxb']!},
        authDelegate: auth,
        navigatorKey: navigator,
        consent: consent,
      ),
    );
    await tester.pumpWidget(
      MaterialApp(
        navigatorKey: navigator,
        home: PluxScope(child: home),
      ),
    );
    await settle(tester);
  }

  Future<void> open(WidgetTester tester, String route) async {
    Plux.open<Object?>(navigator.currentContext!, route).ignore();
    await settle(tester);
    await tester.pumpAndSettle();
  }

  Iterable<PluxException> refusals() =>
      h.errors.where((e) => e.code == PluxErrorCode.navigationRefused);

  testWidgets(
    'signed out, the authentication guard redirects to login with its parameters [NAV-009] [HST-010]',
    (tester) async {
      await start(tester, auth: _Auth(false));
      await open(tester, 'account');
      expect(find.text('Login from account'), findsOneWidget);
      expect(find.text('Account'), findsNothing);
      expect(refusals(), isEmpty, reason: 'a redirect is not a refusal');
    },
  );

  testWidgets(
    'signed in, the authentication guard allows the page [NAV-009] [HST-010]',
    (tester) async {
      await start(tester, auth: _Auth(true));
      await open(tester, 'account');
      expect(find.text('Account'), findsWidgets);
      expect(find.textContaining('Login from'), findsNothing);
    },
  );

  testWidgets(
    'without an auth delegate the user is signed out, so the guard redirects [NAV-009]',
    (tester) async {
      await start(tester);
      await open(tester, 'account');
      expect(find.text('Login from account'), findsOneWidget);
    },
  );

  testWidgets(
    'a page that asks for an assurance level above AL0 fails closed until P6 [NAV-009] [SEC-007]',
    (tester) async {
      await start(tester, auth: _Auth(true));
      await open(tester, 'vault');
      expect(find.text('Vault'), findsNothing);
      expect(find.text('fallback PLX-4102'), findsOneWidget);
      expect(refusals().single.message, contains('requires assurance AL1'));
    },
  );

  testWidgets(
    'a feature-flag guard reads the flag defaults and takes its fallback [NAV-009]',
    (tester) async {
      await start(tester);
      await open(tester, 'beta');
      expect(find.text('Beta'), findsNothing);
      expect(find.text('fallback PLX-4102'), findsOneWidget);
      expect(refusals().single.message, contains('a guard refused it'));
    },
  );

  testWidgets(
    'guards that redirect in a cycle are refused, not followed forever [NAV-009]',
    (tester) async {
      await start(tester);
      Plux.setUserContext(
        const PluxUser(id: 'u-1', attributes: {'tier': 'loop'}),
      );
      await open(tester, 'ping');
      expect(find.text('fallback PLX-4102'), findsOneWidget);
      // Refused at the first route visited again.
      expect(
        refusals().single.message,
        'route ping: its guards redirect in a cycle: ping → pong → ping',
      );
    },
  );

  testWidgets(
    'the same guards allow when their condition does not hold [NAV-009] [HST-011]',
    (tester) async {
      await start(tester);
      Plux.setUserContext(
        const PluxUser(id: 'u-1', attributes: {'tier': 'gold'}),
      );
      await open(tester, 'ping');
      expect(find.text('Ping'), findsWidgets);
    },
  );

  testWidgets(
    'a guard may not navigate: its navigation step fails, so the guard fails closed [NAV-009]',
    (tester) async {
      await start(tester);
      await open(tester, 'trap');
      expect(find.text('Trap'), findsNothing);
      expect(find.text('fallback PLX-4102'), findsOneWidget);
      expect(refusals().last.message, contains('cannot navigate'));
    },
  );

  testWidgets(
    'a redirect whose text parameters do not convert to the target\'s types is refused [NAV-009]',
    (tester) async {
      await start(tester);
      final rt = h.runtime;
      final guards = RouteGuards(
        release: () => rt.active.value,
        run: (_, _, _, _) async => const GuardRedirects('count', {'n': 'abc'}),
        convert: rt.renderer!.textParams,
        report: h.errors.add,
      );
      final verdict = await tester.runAsync(() => guards.decide('account', {}));
      expect(verdict, isA<GuardRefused>());
      expect(
        (verdict! as GuardRefused).reason.message,
        contains('do not convert'),
      );
      final ok = await tester.runAsync(
        () => RouteGuards(
          release: () => rt.active.value,
          run: (_, page, _, _) async => page.route == 'count'
              ? const GuardAllows()
              : const GuardRedirects('count', {'n': '3'}),
          convert: rt.renderer!.textParams,
          report: h.errors.add,
        ).decide('account', {}),
      );
      final entered = ok! as GuardEnter;
      expect(entered.route, 'count');
      expect(entered.params, {'n': 3});
    },
  );

  testWidgets(
    'redirects chain: a moved page redirects to account, whose guard redirects to login [NAV-009]',
    (tester) async {
      await start(tester, auth: _Auth(false));
      await open(tester, 'old');
      expect(find.text('Login from account'), findsOneWidget);
      expect(find.text('Old'), findsNothing);
    },
  );

  testWidgets(
    'an embedded PluxView runs the guards and shows the page they enter in its place [NAV-009] [NAV-004]',
    (tester) async {
      await start(
        tester,
        home: const Scaffold(body: PluxView('account')),
        auth: _Auth(false),
      );
      expect(find.text('Login from account'), findsOneWidget);
    },
  );

  testWidgets(
    'a declarative PluxPage runs the guards too [NAV-009] [NAV-006]',
    (tester) async {
      await tester.runAsync(
        () => h.startFrom(g.bundles['routing/routing.pxb']!, {
          'nav': g.bundles['routing/nav.pxb']!,
        }),
      );
      await tester.pumpWidget(
        MaterialApp(
          home: PluxScope(
            child: Navigator(
              pages: [Plux.pageFor<void>('vault')],
              onDidRemovePage: (_) {},
            ),
          ),
        ),
      );
      await settle(tester);
      expect(find.text('Vault'), findsNothing);
      expect(find.text('fallback PLX-4102'), findsOneWidget);
    },
  );

  testWidgets(
    'each guard run is recorded as action_run with the guard trigger, under analytics consent [NAV-009] [ANL-001]',
    (tester) async {
      await start(
        tester,
        auth: _Auth(true),
        consent: const PluxConsent(analytics: true),
      );
      await open(tester, 'account');
      await tester.runAsync(() => h.runtime.flushTelemetry());
      final runs = [
        for (final e in h.server.events)
          if (e['name'] == 'action_run')
            jsonDecode(utf8.decode(base64.decode(e['fields']! as String)))
                as Map<String, Object?>,
      ];
      expect(runs.map((f) => f['trigger']), contains('guard'));
      expect(runs.firstWhere((f) => f['trigger'] == 'guard')['result'], 'ok');
    },
  );

  testWidgets(
    'the user context reaches guards as declared and typed; undeclared or ill-typed attributes are reported by name only, and no attribute or token reaches telemetry [HST-011] [HST-010] [SEC-092]',
    (tester) async {
      await start(
        tester,
        auth: _Auth(true),
        consent: const PluxConsent(analytics: true),
      );
      Plux.setUserContext(
        const PluxUser(
          id: 'pseudonym-77',
          attributes: {
            'tier': 'loop',
            'email': 'someone@example.com',
            'plan': 'undeclared-plan-value',
          },
        ),
      );
      await open(tester, 'ping'); // reads user.tier: the cycle is refused
      expect(find.text('fallback PLX-4102'), findsOneWidget);
      final invalid = h.errors.where(
        (e) => e.code == PluxErrorCode.userContextInvalid,
      );
      expect(invalid, hasLength(1), reason: 'once per user context');
      expect(invalid.single.message, contains('plan is not declared'));
      expect(invalid.single.message, isNot(contains('undeclared-plan-value')));
      await open(tester, 'account');
      await tester.pumpWidget(const SizedBox()); // pages report on leaving
      await tester.runAsync(() => h.runtime.flushTelemetry());
      expect(h.server.events, isNotEmpty);
      final sent = [
        for (final e in h.server.events) ...[
          jsonEncode(e),
          if (e['fields'] case final String f) utf8.decode(base64.decode(f)),
        ],
      ].join('\n');
      for (final secret in [
        'someone@example.com',
        'pseudonym-77',
        'undeclared-plan-value',
        'secret-access-token',
        'secret-refreshed-token',
      ]) {
        expect(sent, isNot(contains(secret)), reason: secret);
      }
      // Every event type the guards and pages sent was checked.
      expect(
        h.server.events.map((e) => e['name']).toSet(),
        containsAll(['session_start', 'screen_view', 'action_run', 'error']),
      );
    },
  );
}
