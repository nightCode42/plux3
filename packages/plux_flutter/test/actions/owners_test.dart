// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/plux_flutter.dart';

import '../support/harness.dart';

/// The app's and the plugins' triggers, the error-handler chain and
/// component events on the triggers conformance project, end to end.
void main() {
  final g = Harness.goldens;
  late Harness h;

  setUp(() async => h = await Harness.create());
  tearDown(() => h.close());

  Future<void> start(WidgetTester tester, Widget home) async {
    tester.view
      ..physicalSize = const Size(800, 1600)
      ..devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    await tester.runAsync(
      () => h.startFrom(g.bundles['triggers/triggers.pxb']!, {
        'lab': g.bundles['triggers/lab.pxb']!,
      }),
    );
    await tester.pumpWidget(MaterialApp(home: PluxScope(child: home)));
    await settle(tester);
  }

  Future<void> tap(WidgetTester tester, String label) async {
    await tester.tap(find.text(label));
    await settle(tester);
  }

  testWidgets(
    'a host event runs the app\'s trigger; the plugin\'s and the page\'s watchers follow the state it writes [HST-013] [ACT-002]',
    (tester) async {
      await start(tester, const PluxView('home'));
      expect(find.text('pings 0 seen 0 mirror 0'), findsOneWidget);
      expect(await Plux.sendEvent('ping', {'n': 2}), isTrue);
      await settle(tester);
      expect(find.text('pings 2 seen 2 mirror 2'), findsOneWidget);
      expect(await Plux.sendEvent('pong'), isFalse);
      expect(
        h.errors.map((e) => e.code),
        contains(PluxErrorCode.hostEventRefused),
      );
    },
  );

  testWidgets(
    'an error no step handles goes to the page\'s, then the plugin\'s, then the app\'s handler [ACT-020]',
    (tester) async {
      await start(tester, const PluxView('home'));
      expect(find.text('errors 0 0'), findsOneWidget);
      await tap(tester, 'fail');
      // The plugin's handler counts it and fails, so the app's runs too.
      expect(find.text('errors 1 1'), findsOneWidget);
    },
  );

  testWidgets(
    'emitEvent reaches the component instance\'s handler; a view of the component passes it to onEvent [SCH-030]',
    (tester) async {
      await start(tester, const PluxView('home'));
      await tap(tester, 'hit');
      expect(find.text('got 7'), findsOneWidget);

      final events = <PluxViewEvent>[];
      await tester.pumpWidget(
        MaterialApp(
          home: PluxScope(
            child: Scaffold(body: PluxView('badge', onEvent: events.add)),
          ),
        ),
      );
      await settle(tester);
      await tap(tester, 'hit');
      expect([for (final e in events) (e.name, e.payload)], [('onHit', 7)]);
    },
  );

  testWidgets(
    'a run a component started is cancelled when the component goes [ACT-004]',
    (tester) async {
      await start(tester, const PluxView('home'));
      await tap(tester, 'slow');
      await tap(tester, 'hide');
      expect(find.text('slow'), findsNothing);
      final runs = h.runtime.traces.runs;
      expect(
        runs.where((r) => r.status == PluxTraceStatus.cancelled),
        hasLength(1),
      );
      await tester.pump(const Duration(minutes: 2));
      await settle(tester);
      expect(find.text('got 0'), findsOneWidget);
    },
  );
}
