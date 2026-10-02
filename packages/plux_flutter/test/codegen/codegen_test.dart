// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/plux_flutter.dart';

import '../support/harness.dart';
import 'plux_options.g.dart';
import 'routing.g.dart' as routing;
import 'types.g.dart' as types;

/// The libraries `plux codegen` writes (HST-030): `go test
/// ./internal/codegen` generates them from the conformance projects and a
/// project with every type shape; `flutter analyze` compiles them; these
/// tests run them.
void main() {
  group('every type shape', () {
    test('builders pass parameters in their JSON form, and leave optional ones out [HST-030]', () {
      final screen = types.PluxScreens.toString$(
        at: const types.ShopPoint(lat: 1.5),
        tags: {
          'a': [1, 2],
        },
        mode: types.Mode.in$,
        when$: DateTime(2026, 10, 2, 23, 59),
      );
      expect(screen.name, 'to-string');
      expect(screen.params, {
        'at': {'lat': 1.5},
        'mode': 'in',
        'tags': {
          'a': [1, 2],
        },
        'when': '2026-10-02',
      });
      final bare = types.PluxScreens.toString$(
        at: const types.ShopPoint(lat: 0),
        tags: const {},
      );
      expect(bare.params.keys, ['at', 'tags']);
      final view = types.PluxComponents.badge(
        key$: 'k',
        tint: const Color(0xFF112233),
      );
      expect(view.name, 'badge');
      expect(view.inputs, {'key': 'k', 'tint': '#112233FF'});
    });

    test('declared types, enums and host events read and write their JSON form [HST-030] [HST-013]', () {
      final point = types.Point.fromJson({'x': 1, 'y': 2, 'toJson': 'z'});
      expect((point.x, point.y, point.toJson$), (1, 2, 'z'));
      expect(point.toJson(), {'x': 1, 'y': 2, 'toJson': 'z'});
      expect(types.Mode.fromJson('values'), types.Mode.values$);
      expect(types.Mode.name$.json, 'name');
      final done = types.DoneEvent.fromJson({
        'at': '2026-10-02T10:00:00Z',
        'points': [
          {'x': 3, 'y': 4},
        ],
      });
      expect(done.at, DateTime.utc(2026, 10, 2, 10));
      expect(done.points!.single.y, 4);
      expect(
        types.DoneEvent.fromJson({'at': '2026-10-02T10:00:00Z'}).points,
        isNull,
      );
      expect(types.PluxFlagsType.fromJson({'on': true}).toJson(), {'on': true});
    });
  });

  test('plux init configuration embeds the environment and its root keys [HST-032] [SEC-051]', () {
    final config = PluxOptions.config();
    expect(config.appId, '01e0c450-6c00-7000-8000-000000000001');
    expect(config.endpoint, Uri.parse('https://plux.example.com'));
    expect(config.environment, 'production');
    final key = config.rootKeys.single;
    expect((key.keyId, key.algorithm, key.role), ('k1', 'ed25519', 'targets'));
    expect(key.publicKey, [0xab, 0x01]);
  });

  group('the routing project', () {
    final g = Harness.goldens;
    late Harness h;
    late GlobalKey<NavigatorState> navigator;

    setUp(() async {
      h = await Harness.create();
      navigator = GlobalKey();
    });
    tearDown(() => h.close());

    Future<void> start(WidgetTester tester, Widget home) async {
      tester.view
        ..physicalSize = const Size(800, 1400)
        ..devicePixelRatio = 1;
      addTearDown(tester.view.reset);
      await tester.runAsync(
        () => h.startFrom(g.bundles['routing/routing.pxb']!, {
          'nav': g.bundles['routing/nav.pxb']!,
        }),
      );
      await tester.pumpWidget(
        MaterialApp(
          navigatorKey: navigator,
          home: PluxScope(child: Scaffold(body: home)),
        ),
      );
      await settle(tester);
    }

    Future<void> tap(WidgetTester tester, String label) async {
      await tester.tap(find.text(label).hitTestable());
      await tester.pumpAndSettle();
      await settle(tester);
    }

    testWidgets(
      'a generated route opens its page and completes with its typed result [HST-030] [NAV-003]',
      (tester) async {
        await start(tester, const SizedBox());
        final Future<String?> result = routing.PluxScreens.detail(itemId: '7')
            .push(navigator.currentContext!);
        await tester.pumpAndSettle();
        await settle(tester);
        expect(find.text('Item 7'), findsOneWidget);
        await tap(tester, 'Return');
        expect(await result, '7');
      },
    );

    testWidgets(
      'generated components, exposed state, host events and flags are typed views of the runtime [HST-030]',
      (tester) async {
        await start(
          tester,
          Column(
            children: [
              routing.PluxComponents.counterCard(label: 'Count'),
              routing.PluxScreens.home().view(
                sizing: const PluxViewSizing.fixed(Size(800, 1200)),
              ),
            ],
          ),
        );
        expect(find.text('Count 0'), findsOneWidget);
        final counter = routing.PluxAppState.counter;
        final seen = <int?>[];
        final sub = counter.watch().listen(seen.add);
        expect(await counter.set(4), isTrue);
        await tester.pump();
        expect(find.text('Count 4'), findsOneWidget);
        expect(counter.value, 4);
        expect(seen, [4]);
        unawaited(sub.cancel());

        expect(routing.PluxFlags.secondLabel, 'Second');
        expect(routing.PluxFlags.betaEnabled, isFalse);

        final confirmed = <bool>[];
        final events = routing.PluxHostEvents.confirmed.listen(
          (e) => confirmed.add(e.answer),
        );
        addTearDown(events.cancel);
        await tap(tester, 'Ask');
        await tap(tester, 'Yes');
        expect(confirmed, [true]);
      },
    );
  });
}
