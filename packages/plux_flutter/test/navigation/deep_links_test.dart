// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:math';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/navigation/deep_links.dart';
import 'package:plux_flutter/src/pxl/types.dart';

import '../support/harness.dart';

final class _SignedOut implements PluxAuthDelegate {
  @override
  bool get isAuthenticated => false;

  @override
  Future<String?> accessToken() async => null;

  @override
  Future<String?> refresh() async => null;

  @override
  void onLogout() {}
}

/// Deep links and push payloads of the routing conformance project
/// (NAV-008, ADR-0040): hosts `routing.plux.dev`, scheme `plux-routing`,
/// patterns `/items/{itemId}` → detail, `/account` and `/vault`, push under
/// the default key.
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
    bool withNavigatorKey = true,
    bool loanCalculator = false,
  }) async {
    await tester.runAsync(
      () => loanCalculator
          ? h.startFrom(g.bundles['loan-calculator/demo.pxb']!, {
              'loans': g.bundles['loan-calculator/loans.pxb']!,
            }, navigatorKey: withNavigatorKey ? navigator : null)
          : h.startFrom(
              g.bundles['routing/routing.pxb']!,
              {'nav': g.bundles['routing/nav.pxb']!},
              authDelegate: _SignedOut(),
              navigatorKey: withNavigatorKey ? navigator : null,
            ),
    );
    await tester.pumpWidget(
      MaterialApp(
        navigatorKey: navigator,
        home: const PluxScope(child: Text('host')),
      ),
    );
    await settle(tester);
  }

  fbs.Meta meta() => h.runtime.active.value!.meta('');

  Future<bool> link(WidgetTester tester, String uri) async {
    final opened = Plux.handleDeepLink(Uri.parse(uri));
    await settle(tester);
    await tester.pumpAndSettle();
    return opened;
  }

  Iterable<PluxErrorCode> codes() => h.errors.map((e) => e.code);

  group('resolveDeepLink', () {
    testWidgets(
      'maps the app\'s patterns on its hosts and schemes, and /p/<route> always [NAV-008]',
      (tester) async {
        await start(tester);
        final links = meta().deepLinks;
        LinkTarget? at(String uri) => resolveDeepLink(links, Uri.parse(uri));
        expect(at('https://routing.plux.dev/items/42')?.route, 'detail');
        expect(at('https://routing.plux.dev/items/42')?.params, {
          'itemId': '42',
        });
        expect(at('https://routing.plux.dev/items/42/')?.params, {
          'itemId': '42',
        });
        expect(at('https://routing.plux.dev/items/a%20b')?.params, {
          'itemId': 'a b',
        });
        expect(
          at('https://routing.plux.dev/p/detail?itemId=7')?.route,
          'detail',
        );
        expect(at('https://routing.plux.dev/p/detail?itemId=7')?.params, {
          'itemId': '7',
        });
        // A custom scheme's authority is the path's first segment.
        expect(at('plux-routing://items/9')?.params, {'itemId': '9'});
        expect(at('plux-routing:///items/9')?.params, {'itemId': '9'});
        expect(at('PLUX-ROUTING://account')?.route, 'account');
        // Query parameters fill the others; the path wins over the query.
        expect(
          at('https://routing.plux.dev/items/42?itemId=1&utm_source=mail')
              ?.params,
          {'itemId': '42', 'utm_source': 'mail'},
        );
      },
    );

    testWidgets(
      'maps nothing on another host, over http, under another scheme or for an unknown path [NAV-008]',
      (tester) async {
        await start(tester);
        final links = meta().deepLinks;
        for (final uri in [
          'https://evil.example/items/42',
          'http://routing.plux.dev/items/42',
          'ftp://routing.plux.dev/items/42',
          'other://items/42',
          'https://routing.plux.dev/items',
          'https://routing.plux.dev/items/1/2',
          'https://routing.plux.dev/p',
          'https://routing.plux.dev/',
        ]) {
          expect(resolveDeepLink(links, Uri.parse(uri)), isNull, reason: uri);
        }
        expect(resolveDeepLink(null, Uri.parse('https://x.dev/p/a')), isNull);
      },
    );

    testWidgets('never throws, whatever the link [NAV-008]', (tester) async {
      await start(tester);
      final links = meta().deepLinks;
      final random = Random(7);
      const alphabet = 'ap/{}%2:?=&#.-_ ~itemsXYZ09';
      for (var i = 0; i < 2000; i++) {
        final text = String.fromCharCodes(
          List.generate(
            random.nextInt(40),
            (_) => alphabet.codeUnitAt(random.nextInt(alphabet.length)),
          ),
        );
        final uri = Uri.tryParse('https://routing.plux.dev/$text');
        if (uri != null) resolveDeepLink(links, uri);
      }
    });
  });

  group('resolvePushPayload', () {
    testWidgets(
      'reads {route, params} under the payload key, as an object or as JSON text [NAV-008]',
      (tester) async {
        await start(tester);
        final push = meta().push;
        expect(push?.payloadKey, defaultPushKey);
        expect(
          resolvePushPayload(push, {
            'plux': {
              'route': 'detail',
              'params': {'itemId': '5'},
            },
          })?.params,
          {'itemId': '5'},
        );
        expect(
          resolvePushPayload(push, {
            'plux': '{"route": "detail", "params": {"itemId": "6"}}',
          })?.route,
          'detail',
        );
        expect(
          resolvePushPayload(push, {'plux': '{"route": "x"}'})?.params,
          <String, Object?>{},
        );
        for (final payload in <Map<String, Object?>>[
          {},
          {'other': '{"route": "detail"}'},
          {'plux': 'not json'},
          {'plux': '{"params": {}}'},
          {
            'plux': {'route': 'detail', 'params': 'x'},
          },
        ]) {
          expect(resolvePushPayload(push, payload), isNull, reason: '$payload');
        }
      },
    );

    test('a dotted payload key reaches into nested objects [NAV-008]', () {
      final push = fbs.Push(
        fbs.PushObjectBuilder(enabled: true, payloadKey: 'data.plux').toBytes(),
      );
      expect(
        resolvePushPayload(push, {
          'data': {
            'plux': {
              'route': 'detail',
              'params': {'itemId': '8'},
            },
          },
        })?.params,
        {'itemId': '8'},
      );
      // The key itself wins when the payload has it.
      expect(
        resolvePushPayload(push, {
          'data.plux': {'route': 'home'},
        })?.route,
        'home',
      );
      final off = fbs.Push(fbs.PushObjectBuilder(enabled: false).toBytes());
      expect(
        resolvePushPayload(off, {
          'plux': {'route': 'home'},
        }),
        isNull,
      );
    });

    testWidgets('an app that declares no push resolves no payload [NAV-008]', (
      tester,
    ) async {
      await start(tester, loanCalculator: true);
      expect(meta().push, isNull);
      expect(
        resolvePushPayload(meta().push, {
          'plux': {'route': 'result'},
        }),
        isNull,
      );
    });
  });

  test('fromText converts the literal forms a link carries [NAV-008]', () {
    PxlType t(String s) => PxlType.parse(
      s,
      (n) => n == 'Tier' ? NamedType.enumeration('Tier', ['gold']) : null,
    );
    expect(fromText(t('int'), '42'), 42);
    expect(fromText(t('double'), '2.5'), 2.5);
    expect(fromText(t('bool'), 'true'), true);
    expect(fromText(t('string'), 'x y'), 'x y');
    expect(fromText(t('Tier'), 'gold'), 'gold');
    expect(fromText(t('decimal'), '1.50').toString(), '1.50');
    expect(fromText(t('date'), '2026-10-02').toString(), '2026-10-02');
    for (final (type, text) in [
      ('int', '4.2'),
      ('bool', 'yes'),
      ('Tier', 'silver'),
      ('date', 'tomorrow'),
      ('list<int>', '1'),
    ]) {
      expect(() => fromText(t(type), text), throwsFormatException);
    }
  });

  testWidgets(
    'Plux.handleDeepLink opens the page a link maps to, with its typed parameters [NAV-008]',
    (tester) async {
      await start(tester);
      expect(await link(tester, 'https://routing.plux.dev/items/42'), isTrue);
      expect(find.text('Item 42'), findsOneWidget);
      expect(codes(), isNot(contains(PluxErrorCode.routeParametersInvalid)));
    },
  );

  testWidgets(
    'a link\'s text is converted by its parameter\'s type; text that does not convert shows the error fallback [NAV-008] [NAV-007]',
    (tester) async {
      await start(tester);
      expect(await link(tester, 'https://routing.plux.dev/count/3'), isTrue);
      expect(find.text('Count 3'), findsOneWidget);
      expect(await link(tester, 'https://routing.plux.dev/count/abc'), isTrue);
      expect(find.text('fallback PLX-4101'), findsOneWidget);
    },
  );

  testWidgets(
    'a link\'s query parameters the route does not declare, such as a campaign\'s, are left out [NAV-008] [NAV-007]',
    (tester) async {
      await start(tester);
      expect(
        await link(
          tester,
          'https://routing.plux.dev/p/detail?itemId=7&utm_source=mail',
        ),
        isTrue,
      );
      expect(find.text('Item 7'), findsOneWidget);
      expect(codes(), isNot(contains(PluxErrorCode.routeParametersInvalid)));
    },
  );

  testWidgets(
    'a deep link runs the route\'s guards: signed out, account redirects to login [NAV-008] [NAV-009]',
    (tester) async {
      await start(tester);
      expect(await link(tester, 'plux-routing://account'), isTrue);
      expect(find.text('Login from account'), findsOneWidget);
      expect(await link(tester, 'https://routing.plux.dev/vault'), isTrue);
      expect(find.text('fallback PLX-6002'), findsOneWidget);
    },
  );

  testWidgets(
    'a link nothing maps opens nothing and reports PLX-4103, without its query [NAV-008] [SEC-092]',
    (tester) async {
      await start(tester);
      expect(
        await link(tester, 'https://routing.plux.dev/nowhere?token=abc'),
        isFalse,
      );
      final e = h.errors.singleWhere(
        (e) => e.code == PluxErrorCode.deepLinkUnmapped,
      );
      expect(e.message, contains('https://routing.plux.dev/nowhere'));
      expect(e.message, isNot(contains('abc')));
      expect(find.text('host'), findsOneWidget);
    },
  );

  testWidgets(
    'without PluxConfig.navigatorKey a link opens nothing and reports PLX-4102 [NAV-008]',
    (tester) async {
      await start(tester, withNavigatorKey: false);
      expect(await link(tester, 'https://routing.plux.dev/items/1'), isFalse);
      expect(codes(), contains(PluxErrorCode.navigationRefused));
    },
  );

  testWidgets(
    'Plux.handlePushPayload opens the page a notification names [NAV-008]',
    (tester) async {
      await start(tester);
      final opened = Plux.handlePushPayload({
        'plux': '{"route": "detail", "params": {"itemId": "5"}}',
      });
      await settle(tester);
      await tester.pumpAndSettle();
      expect(await opened, isTrue);
      expect(find.text('Item 5'), findsOneWidget);
      expect(await Plux.handlePushPayload({'other': 1}), isFalse);
      expect(codes(), contains(PluxErrorCode.deepLinkUnmapped));
    },
  );
}
