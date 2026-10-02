// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/plux_flutter.dart';

import '../support/harness.dart';

/// The routing project's native catalogue (ADR-0041): the custom action
/// `scan(prompt) → string`, the native route `profile(userId) → bool` and
/// the native slot `Counter(label)` with its `onTap(int)` event, used by
/// the home and slots pages.
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
    String home = 'home',
    Map<String, PluxNativeRoute<Object?, Object?>> routes = const {},
    Map<String, PluxNativeSlot> slots = const {},
    Map<String, PluxNativeAction<Object?, Object?>> actions = const {},
  }) async {
    tester.view
      ..physicalSize = const Size(800, 1600)
      ..devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    await tester.runAsync(
      () => h.startFrom(
        g.bundles['routing/routing.pxb']!,
        {'nav': g.bundles['routing/nav.pxb']!},
        nativeRoutes: routes,
        nativeSlots: slots,
        nativeActions: actions,
      ),
    );
    final sub = Plux.events.listen(events.add);
    addTearDown(sub.cancel);
    await tester.pumpWidget(
      MaterialApp(
        navigatorKey: navigator,
        home: PluxScope(child: PluxView(home)),
      ),
    );
    await settle(tester);
  }

  Future<void> tap(WidgetTester tester, String label) async {
    await tester.ensureVisible(find.text(label));
    await tester.tap(find.text(label).hitTestable());
    await tester.pumpAndSettle();
    await settle(tester);
  }

  Iterable<PluxErrorCode> codes() => h.errors.map((e) => e.code);

  PluxNativeAction<Map<String, Object?>, Object?> scan(
    Object? Function(Map<String, Object?> input) answer,
  ) => PluxNativeAction<Map<String, Object?>, Object?>(
    input: (json) => json,
    handler: answer,
  );

  group('custom actions (ACT-060)', () {
    testWidgets(
      'a registered action runs with its checked input, and its typed output reaches the graph [ACT-060]',
      (tester) async {
        final seen = <Map<String, Object?>>[];
        await start(
          tester,
          actions: {
            'scan': scan((input) {
              seen.add(input);
              return 'code-42';
            }),
          },
        );
        await tap(tester, 'Scan');
        expect(seen, [
          {'prompt': 'Scan a code'},
        ]);
        expect(events.map((e) => e.name), ['scanned']);
        expect(events.single.payload, {'code': 'code-42'});
        expect(
          codes(),
          isNot(contains(PluxErrorCode.nativeActionNotRegistered)),
        );
      },
    );

    testWidgets(
      'an output of another type than the catalogue declares fails the step, and never reaches the plugin [ACT-060]',
      (tester) async {
        await start(tester, actions: {'scan': scan((_) => 42)});
        await tap(tester, 'Scan');
        expect(events, isEmpty);
        final e = h.errors.singleWhere(
          (e) => e.code == PluxErrorCode.actionValueInvalid,
        );
        expect(e.message, contains('the output of custom action scan'));
      },
    );

    testWidgets(
      'an exception in the host\'s handler fails the step with PLX-4205, naming only its type [ACT-060] [RT-021]',
      (tester) async {
        await start(
          tester,
          actions: {
            'scan': scan((_) => throw StateError('card 4111 1111 1111 1111')),
          },
        );
        await tap(tester, 'Scan');
        final e = h.errors.singleWhere(
          (e) => e.code == PluxErrorCode.hostCodeFailed,
        );
        expect(e.message, contains('StateError'));
        expect(e.message, isNot(contains('4111')));
        expect(find.text('Push detail'), findsOneWidget);
      },
    );
  });

  group('native routes (NAV-002)', () {
    PluxNativeRoute<String, bool> profile({Object? result = true}) =>
        PluxNativeRoute<String, bool>(
          params: (json) => json['userId']! as String,
          builder: (context, userId) => Scaffold(
            body: Column(
              children: [
                Text('Profile $userId'),
                TextButton(
                  onPressed: () => Navigator.of(context).pop(result),
                  child: const Text('Done'),
                ),
              ],
            ),
          ),
        );

    testWidgets(
      'navigate opens a registered native route with its checked parameters [NAV-002]',
      (tester) async {
        await start(tester, routes: {'profile': profile()});
        await tap(tester, 'Profile');
        expect(find.text('Profile u-7'), findsOneWidget);
        await tap(tester, 'Done');
        expect(find.text('Push detail'), findsOneWidget);
        expect(
          codes(),
          isNot(contains(PluxErrorCode.nativeRouteNotRegistered)),
        );
      },
    );

    testWidgets(
      'a presenting step returns the native route\'s result, typed by the catalogue [NAV-002]',
      (tester) async {
        await start(tester, routes: {'profile': profile()});
        await tap(tester, 'Ask profile');
        expect(find.text('Profile u-8'), findsOneWidget);
        await tap(tester, 'Done');
        expect(events.map((e) => e.name), ['profiled']);
        expect(events.single.payload, {'ok': true});
      },
    );

    testWidgets(
      'a result of another type than the catalogue declares fails the step [NAV-002]',
      (tester) async {
        await start(tester, routes: {'profile': profile(result: 'yes')});
        await tap(tester, 'Ask profile');
        await tap(tester, 'Done');
        expect(events, isEmpty);
        expect(
          h.errors.map((e) => e.message),
          contains(contains('the result of native route profile')),
        );
      },
    );
  });

  group('native slots (WGT-033)', () {
    PluxNativeSlot counter({Object? payload = 3}) => PluxNativeSlot(
      (context, slot) => TextButton(
        onPressed: () => slot.emit('onTap', payload),
        child: Text('${slot['label']}'),
      ),
    );

    testWidgets(
      'a registered slot builds the host widget with its props, and its events start the node\'s graphs [WGT-033]',
      (tester) async {
        await start(tester, home: 'slots', slots: {'Counter': counter()});
        expect(find.text('Taps'), findsOneWidget);
        await tap(tester, 'Taps');
        expect(events.map((e) => e.name), ['tapped']);
        expect(events.single.payload, {'count': 3});
      },
    );

    testWidgets(
      'an event payload of another type than the catalogue declares is reported and dropped [WGT-033]',
      (tester) async {
        await start(
          tester,
          home: 'slots',
          slots: {'Counter': counter(payload: 'three')},
        );
        await tap(tester, 'Taps');
        expect(events, isEmpty);
        expect(
          h.errors.map((e) => e.message),
          contains(contains('event onTap of native slot Counter')),
        );
      },
    );

    testWidgets(
      'a slot the host does not register shows the neutral placeholder and reports PLX-4201 [WGT-033]',
      (tester) async {
        await start(tester, home: 'slots');
        expect(find.text('Taps'), findsNothing);
        expect(codes(), contains(PluxErrorCode.nativeSlotNotRegistered));
      },
    );

    testWidgets(
      'a slot builder that throws is contained by the page\'s boundary and reports PLX-4205 [WGT-033] [RT-020]',
      (tester) async {
        await start(
          tester,
          home: 'slots',
          slots: {
            'Counter': PluxNativeSlot((_, _) => throw StateError('broken')),
          },
        );
        expect(codes(), contains(PluxErrorCode.hostCodeFailed));
        expect(find.textContaining('fallback'), findsOneWidget);
      },
    );
  });
}
