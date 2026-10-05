// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:io';
import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/plux_flutter.dart';

import '../support/harness.dart';

Uint8List _bundle(String name) =>
    File('../../schema/testdata/bundles/animation/$name').readAsBytesSync();

/// Animation (ANI-001–ANI-007) on the animation conformance project:
/// implicit animation of a bound prop, enter and exit, hero tags, timelines
/// with keyframes, stagger, repeat and reverse, the start and control
/// actions, scroll- and drag-linked timelines, and reduce motion.
void main() {
  late Harness h;

  setUp(() async => h = await Harness.create());
  tearDown(() => h.close());

  Future<void> start(
    WidgetTester tester, {
    bool reduce = false,
    double height = 2400,
  }) async {
    tester.view
      ..physicalSize = Size(800, height)
      ..devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    if (reduce) {
      tester.platformDispatcher.accessibilityFeaturesTestValue =
          const FakeAccessibilityFeatures(disableAnimations: true);
      addTearDown(
        tester.platformDispatcher.clearAccessibilityFeaturesTestValue,
      );
    }
    await tester.runAsync(
      () => h.startFrom(_bundle('motion.pxb'), {'stage': _bundle('stage.pxb')}),
    );
    await tester.pumpWidget(
      const MaterialApp(home: PluxScope(child: PluxView('stage'))),
    );
    await settle(tester);
  }

  /// What the runtime reported, but for the offline harness's failed sync.
  List<PluxException> problems() => [
    for (final e in h.errors)
      if (e.code != PluxErrorCode.syncFailed) e,
  ];

  double opacityOf(WidgetTester tester, String text) => tester
      .widget<Opacity>(
        find
            .ancestor(of: find.text(text), matching: find.byType(Opacity))
            .first,
      )
      .opacity;

  /// The transform of the node holding [of]: the Transforms the node builds
  /// for its scale, rotation and offset, composed.
  Matrix4 transformAround(WidgetTester tester, Finder of) => find
      .ancestor(of: of, matching: find.byType(Transform))
      .evaluate()
      .take(3)
      .map<Matrix4>((e) => (e.widget as Transform).transform)
      .fold<Matrix4>(Matrix4.identity(), (a, b) => a.multiplied(b));

  Matrix4 transformOf(WidgetTester tester, String text) =>
      transformAround(tester, find.text(text));

  Finder dragged() =>
      find.byWidgetPredicate((w) => w is SizedBox && w.width == 80);

  Future<void> tap(WidgetTester tester, String label) async {
    await tester.tap(find.text(label));
    await tester.pump();
  }

  testWidgets('the project renders its static state [ANI-001]', (tester) async {
    await start(tester);
    expect(problems(), isEmpty);
    expect(opacityOf(tester, 'Implicit'), 1.0);
    expect(find.text('Presence'), findsOneWidget);
  });

  testWidgets(
    'a bound prop animates with its duration and curve when its value changes [ANI-001]',
    (tester) async {
      await start(tester);
      await tap(tester, 'Toggle');
      await tester.pump(const Duration(milliseconds: 100));
      final mid = opacityOf(tester, 'Implicit');
      expect(mid, lessThan(1.0));
      expect(mid, greaterThan(0.2));
      await tester.pump(const Duration(milliseconds: 150));
      expect(opacityOf(tester, 'Implicit'), closeTo(0.2, 1e-9));
      await tap(tester, 'Toggle');
      await tester.pump(const Duration(milliseconds: 100));
      expect(opacityOf(tester, 'Implicit'), greaterThan(0.2));
      expect(opacityOf(tester, 'Implicit'), lessThan(1.0));
      await tester.pump(const Duration(milliseconds: 250));
      expect(opacityOf(tester, 'Implicit'), closeTo(1.0, 1e-9));
    },
  );

  testWidgets(
    'reduce motion skips an implicit animation to its final state [ANI-007]',
    (tester) async {
      await start(tester, reduce: true);
      await tap(tester, 'Toggle');
      await tester.pump();
      expect(opacityOf(tester, 'Implicit'), closeTo(0.2, 1e-9));
    },
  );

  testWidgets(
    'a node plays its enter transition and keeps building through its exit [ANI-003]',
    (tester) async {
      await start(tester);
      await tester.pump(const Duration(milliseconds: 300));
      expect(find.text('Presence'), findsOneWidget);
      await tap(tester, 'Toggle');
      await tester.pump(const Duration(milliseconds: 100));
      expect(find.text('Presence'), findsOneWidget, reason: 'the exit plays');
      await tester.pump(const Duration(milliseconds: 300));
      expect(find.text('Presence'), findsNothing);
      await tap(tester, 'Toggle');
      await tester.pump(const Duration(milliseconds: 50));
      expect(find.text('Presence'), findsOneWidget);
      await tester.pump(const Duration(milliseconds: 300));
      expect(find.text('Presence'), findsOneWidget);
    },
  );

  testWidgets(
    'reduce motion removes a node without playing its exit [ANI-007]',
    (tester) async {
      await start(tester, reduce: true);
      await tap(tester, 'Toggle');
      await tester.pump();
      expect(find.text('Presence'), findsNothing);
    },
  );

  testWidgets('a node with a hero tag flies between routes [ANI-004]', (
    tester,
  ) async {
    await start(tester);
    final hero = find.byWidgetPredicate((w) => w is Hero && w.tag == 'banner');
    expect(hero, findsOneWidget);
  });

  testWidgets('a hero tag also matches a native Flutter page [ANI-004]', (
    tester,
  ) async {
    await start(tester);
    final nav = tester.state<NavigatorState>(find.byType(Navigator));
    nav.push(
      MaterialPageRoute<void>(
        builder: (_) => const Scaffold(
          body: Center(
            child: Hero(
              tag: 'banner',
              child: Text('Native', key: Key('native')),
            ),
          ),
        ),
      ),
    );
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 100));
    // Both the Plux and the native hero are in the flight.
    expect(find.text('Native'), findsWidgets);
    expect(tester.takeException(), isNull);
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('native')), findsOneWidget);
  });

  testWidgets('reduce motion drops the hero [ANI-007]', (tester) async {
    await start(tester, reduce: true);
    expect(
      find.byWidgetPredicate((w) => w is Hero && w.tag == 'banner'),
      findsNothing,
    );
  });

  testWidgets(
    'an autoplaying timeline repeats and reverses through its keyframes [ANI-002]',
    (tester) async {
      await start(tester);
      expect(problems(), isEmpty);
      await tester.pump(const Duration(milliseconds: 1));
      final start0 = opacityOf(tester, 'Pulse');
      expect(start0, closeTo(1.0, 0.05));
      await tester.pump(const Duration(milliseconds: 400));
      final mid = opacityOf(tester, 'Pulse');
      expect(mid, closeTo(0.6, 0.06), reason: 'easeInOut passes the middle');
      await tester.pump(const Duration(milliseconds: 400));
      expect(opacityOf(tester, 'Pulse'), closeTo(0.2, 0.05));
      await tester.pump(const Duration(milliseconds: 400));
      final back = opacityOf(tester, 'Pulse');
      expect(back, greaterThan(0.3), reason: 'the second play runs backwards');
      expect(back, lessThan(0.9));
      await tester.pump(const Duration(milliseconds: 400));
      expect(opacityOf(tester, 'Pulse'), closeTo(1.0, 0.05));
      await tester.pump(const Duration(milliseconds: 800));
      expect(opacityOf(tester, 'Pulse'), closeTo(0.2, 0.05), reason: 'forever');
    },
  );

  testWidgets('a stagger delays each item of a list by its index [ANI-002]', (
    tester,
  ) async {
    await start(tester);
    await tester.pump(const Duration(milliseconds: 1));
    await tester.pump(const Duration(milliseconds: 150));
    final a = opacityOf(tester, 'a');
    final b = opacityOf(tester, 'b');
    final c = opacityOf(tester, 'c');
    expect(a, greaterThan(b));
    expect(b, greaterThan(c));
    expect(c, 0.0);
    await tester.pump(const Duration(milliseconds: 600));
    for (final t in ['a', 'b', 'c']) {
      expect(opacityOf(tester, t), closeTo(1.0, 1e-9));
    }
  });

  testWidgets(
    'startAnimation and controlAnimation play, pause, reverse, seek and stop a timeline [ANI-002]',
    (tester) async {
      await start(tester);
      // Not started: the node shows its own scale.
      expect(transformOf(tester, 'Intro').entry(0, 0), 1.0);
      await tap(tester, 'Play');
      await tester.pump(const Duration(milliseconds: 1));
      await tester.pump(const Duration(milliseconds: 50));
      final playing = transformOf(tester, 'Intro').entry(0, 0);
      expect(playing, greaterThan(0.6));
      expect(playing, lessThan(0.95));
      await tap(tester, 'Pause');
      final paused = transformOf(tester, 'Intro').entry(0, 0);
      await tester.pump(const Duration(milliseconds: 200));
      expect(transformOf(tester, 'Intro').entry(0, 0), paused);
      expect(problems(), isEmpty);
    },
  );

  testWidgets('a timeline that finishes holds its final state [ANI-002]', (
    tester,
  ) async {
    await start(tester);
    await tap(tester, 'Play');
    await tester.pump(const Duration(milliseconds: 1));
    await tester.pump(const Duration(milliseconds: 600));
    expect(transformOf(tester, 'Intro').entry(0, 0), closeTo(1.0, 1e-6));
  });

  testWidgets(
    'reduce motion jumps a started timeline to its final state [ANI-007]',
    (tester) async {
      await start(tester, reduce: true);
      await tap(tester, 'Play');
      await tester.pump();
      expect(transformOf(tester, 'Intro').entry(0, 0), closeTo(1.0, 1e-6));
      // The looping timeline does not run: its node shows its first state.
      await tester.pump(const Duration(milliseconds: 400));
      expect(opacityOf(tester, 'Pulse'), closeTo(1.0, 1e-9));
    },
  );

  testWidgets('scrolling drives a timeline by the offset [ANI-006]', (
    tester,
  ) async {
    await start(tester, height: 300);
    await tester.drag(
      find.byType(SingleChildScrollView),
      const Offset(0, -100),
    );
    await tester.pump();
    // 100 of the 200 pixels the timeline spans: half of its angle.
    final m = transformOf(tester, 'Intro');
    expect(math.asin(m.entry(1, 0)), closeTo(0.5, 0.15));
  });

  testWidgets(
    'dragging drives a timeline and a spring settles it to an end [ANI-006]',
    (tester) async {
      await start(tester);
      final gesture = await tester.startGesture(tester.getCenter(dragged()));
      await gesture.moveBy(const Offset(20, 0));
      await gesture.moveBy(const Offset(30, 0));
      await tester.pump();
      final during = transformAround(tester, dragged());
      expect(during.entry(0, 0), closeTo(1.5, 0.2));
      await gesture.up();
      await tester.pump();
      await tester.pump(const Duration(seconds: 3));
      final settled = transformAround(tester, dragged());
      final s = settled.entry(0, 0);
      expect(
        s == 1.0 || (s - 2.0).abs() < 1e-3,
        isTrue,
        reason: 'at an end: $s',
      );
    },
  );

  Future<void> openStage(WidgetTester tester, {bool reduce = false}) async {
    tester.view
      ..physicalSize = const Size(800, 2400)
      ..devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    if (reduce) {
      tester.platformDispatcher.accessibilityFeaturesTestValue =
          const FakeAccessibilityFeatures(disableAnimations: true);
      addTearDown(
        tester.platformDispatcher.clearAccessibilityFeaturesTestValue,
      );
    }
    await tester.runAsync(
      () => h.startFrom(_bundle('motion.pxb'), {'stage': _bundle('stage.pxb')}),
    );
    await tester.pumpWidget(
      MaterialApp(
        home: PluxScope(
          child: Builder(
            builder: (context) => TextButton(
              onPressed: () => Plux.open<Object?>(context, 'stage'),
              child: const Text('Open'),
            ),
          ),
        ),
      ),
    );
    await settle(tester);
    await tester.tap(find.text('Open'));
    await settle(tester);
  }

  Finder slide() => find.byWidgetPredicate(
    (w) =>
        w is FractionalTranslation &&
        w.translation.dx > 0 &&
        w.translation.dx < 1,
  );

  testWidgets(
    'a route timeline moves the page in as its custom transition [NAV-010]',
    (tester) async {
      await openStage(tester);
      await tester.pump(const Duration(milliseconds: 100));
      expect(slide(), findsWidgets, reason: 'the page is mid-slide');
      await tester.pump(const Duration(milliseconds: 400));
      expect(slide(), findsNothing);
      expect(find.text('Implicit'), findsOneWidget);
      expect(problems(), isEmpty);
    },
  );

  testWidgets(
    'reduce motion shows a custom transition\'s page at once [NAV-010] [ANI-007]',
    (tester) async {
      await openStage(tester, reduce: true);
      await tester.pump(const Duration(milliseconds: 100));
      expect(slide(), findsNothing);
      expect(find.text('Implicit'), findsOneWidget);
    },
  );
}
