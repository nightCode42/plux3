// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/plux_flutter.dart';

import '../support/harness.dart';

/// Mixed screens (ADR-0023): `PluxView`s by name in a native screen, with
/// inputs, events and sizing, sharing the app state the host reads,
/// writes and watches through `Plux.state<T>`.
void main() {
  final g = Harness.goldens;
  late Harness h;

  setUp(() async => h = await Harness.create());
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
        home: PluxScope(child: Scaffold(body: home)),
      ),
    );
    await settle(tester);
  }

  Iterable<PluxErrorCode> codes() => h.errors.map((e) => e.code);

  testWidgets(
    'two views on one native screen share an exposed entry: a host write reaches both in the same frame [HST-021] [STA-030] [NAV-004]',
    (tester) async {
      await start(
        tester,
        const Column(
          children: [
            PluxView('mixed'),
            PluxView('counter-card', inputs: {'label': 'Count'}),
          ],
        ),
      );
      expect(find.text('Shared 0'), findsOneWidget);
      expect(find.text('Count 0'), findsOneWidget);
      final counter = Plux.state<int>('counter');
      expect(counter.value, 0);
      expect(await counter.set(3), isTrue);
      await tester.pump();
      expect(find.text('Shared 3'), findsOneWidget);
      expect(find.text('Count 3'), findsOneWidget);
      expect(counter.value, 3);
      expect(codes(), isNot(contains(PluxErrorCode.exposedStateTypeMismatch)));
    },
  );

  testWidgets(
    'watch emits each change; a value of another type, or an entry that is not exposed, is refused with PLX-4203 and changes nothing [STA-030] [HST-021]',
    (tester) async {
      await start(
        tester,
        const PluxView('counter-card', inputs: {'label': 'Count'}),
      );
      final seen = <int?>[];
      final sub = Plux.state<int>('counter').watch().listen(seen.add);
      await Plux.state<int>('counter').set(1);
      await Plux.state<int>('counter').set(2);
      await tester.pump();
      expect(seen, [1, 2]);
      // Not awaited: cancel's future belongs to the root zone, and awaiting
      // it would leave the test's fake async zone.
      unawaited(sub.cancel());

      expect(await Plux.state<Object>('counter').set('three'), isFalse);
      expect(Plux.state<String>('theme').value, isNull);
      expect(await Plux.state<String>('theme').set('dark'), isFalse);
      expect(await Plux.state<int>('missing').set(1), isFalse);
      await tester.pump();
      expect(find.text('Count 2'), findsOneWidget);
      expect(Plux.state<int>('counter').value, 2);
      expect(
        h.errors
            .where((e) => e.code == PluxErrorCode.exposedStateTypeMismatch)
            .map((e) => e.details['state']),
        ['counter', 'theme', 'theme', 'missing'],
      );
    },
  );

  testWidgets(
    'an exposed entry that declares persistence is written and kept, with nothing reported [STA-030] [STA-003]',
    (tester) async {
      await start(
        tester,
        const PluxView('counter-card', inputs: {'label': 'Count'}),
      );
      expect(await Plux.state<int>('visits').set(5), isTrue);
      await tester.pump();
      expect(Plux.state<int>('visits').value, 5);
      final reported = h.errors.where((e) => e.details['state'] == 'visits');
      expect(reported, isEmpty);
    },
  );

  testWidgets(
    'a view shows an exported component by name, its props checked like route parameters [NAV-004] [NAV-007]',
    (tester) async {
      await start(tester, const PluxView('counter-card'));
      expect(find.text('Count 0'), findsNothing);
      expect(codes(), contains(PluxErrorCode.routeParametersInvalid));
    },
  );

  testWidgets(
    'a name that is neither a route nor an exported component shows the fallback with PLX-4100 [NAV-004]',
    (tester) async {
      await start(tester, const PluxView('no-such-name'));
      expect(codes(), contains(PluxErrorCode.routeNotFound));
    },
  );

  testWidgets(
    'an inline page has no route to pop: its pop arrives as a view event with the checked result [NAV-004]',
    (tester) async {
      final events = <PluxViewEvent>[];
      await start(tester, PluxView('mixed', onEvent: events.add));
      await tester.tap(find.text('Close'));
      await tester.pumpAndSettle();
      await settle(tester);
      expect(events.map((e) => (e.name, e.payload)), [('pop', 'closed')]);
      expect(find.text('Shared 0'), findsOneWidget);
    },
  );

  testWidgets(
    'sizing: intrinsic sizes to the content, fixed takes the given size, expand fills bounded constraints and shows the fallback in unbounded ones [NAV-004]',
    (tester) async {
      Size sizeOf(String key) => tester.getSize(find.byKey(ValueKey(key)));
      await start(
        tester,
        Column(
          children: [
            const PluxView(
              'counter-card',
              key: ValueKey('fixed'),
              inputs: {'label': 'Fixed'},
              sizing: PluxViewSizing.fixed(Size(300, 50)),
            ),
            const SizedBox(
              width: 200,
              height: 120,
              child: PluxView(
                'counter-card',
                key: ValueKey('expand'),
                inputs: {'label': 'Expand'},
                sizing: PluxViewSizing.expand,
              ),
            ),
            const Align(
              child: PluxView(
                'counter-card',
                key: ValueKey('intrinsic'),
                inputs: {'label': 'Intrinsic'},
              ),
            ),
            SingleChildScrollView(
              child: Column(
                children: [
                  PluxView(
                    'counter-card',
                    inputs: const {'label': 'Unbounded'},
                    sizing: PluxViewSizing.expand,
                    fallbackBuilder: (_, e) => Text('fallback ${e.code.id}'),
                  ),
                ],
              ),
            ),
          ],
        ),
      );
      expect(sizeOf('fixed'), const Size(300, 50));
      expect(sizeOf('expand'), const Size(200, 120));
      final text = tester.getSize(find.text('Intrinsic 0'));
      expect(sizeOf('intrinsic'), text);
      expect(find.text('Unbounded 0'), findsNothing);
      expect(find.text('fallback PLX-4001'), findsOneWidget);
    },
  );

  testWidgets(
    'Plux.flag reads a feature flag of the active release when it has the type asked for [HST-030]',
    (tester) async {
      await start(tester, const SizedBox());
      expect(Plux.flag<String>('secondLabel'), 'Second');
      expect(Plux.flag<bool>('betaEnabled'), isFalse);
      expect(Plux.flag<int>('secondLabel'), isNull);
      expect(Plux.flag<bool>('missing'), isNull);
    },
  );

  testWidgets(
    'Plux.eventsNamed streams the host events of one name [HST-013]',
    (tester) async {
      await start(tester, const PluxView('home'));
      final confirmed = <Object?>[];
      final sub = Plux.eventsNamed('confirmed')
          .listen((e) => confirmed.add(e.payload));
      addTearDown(sub.cancel);
      Future<void> tap(String label) async {
        await tester.tap(find.text(label).hitTestable());
        await tester.pumpAndSettle();
        await settle(tester);
      }

      await tap('Ask');
      await tap('Yes');
      await tap('Choose');
      await tap('Pick');
      expect(confirmed, [
        {'answer': true},
      ]);
    },
  );
}
