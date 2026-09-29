// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_flutter/src/render/scope.dart';

import '../support/harness.dart';

/// The widget gallery (schema/testdata/documents/widgets): one page per
/// group of widgets, compiled by the Go compiler, rendered here.
void main() {
  final g = Harness.goldens;
  late Harness h;
  setUp(() async => h = await Harness.create());
  tearDown(() => h.close());

  Future<void> open(
    WidgetTester tester,
    String route, {
    Size size = const Size(800, 1400),
    Map<String, Object?> params = const {},
    ThemeData? theme,
  }) async {
    tester.view.physicalSize = size;
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    await tester.runAsync(
      () => h.startFrom(g.bundles['widgets/widgets.pxb']!, {
        'gallery': g.bundles['widgets/gallery.pxb']!,
      }),
    );
    await tester.pumpWidget(
      MaterialApp(
        debugShowCheckedModeBanner: false,
        theme: theme,
        home: PluxScope(child: PluxView(route, params: params)),
      ),
    );
    await settle(tester);
  }

  /// Problems other than the sync that finds no server release.
  List<PluxException> problems() => [
    for (final e in h.errors)
      if (e.code != PluxErrorCode.syncFailed) e,
  ];

  /// The icon nodes report their icons until icon fonts arrive (THM-005).
  List<PluxException> nonIconProblems() => [
    for (final e in problems())
      if (!e.message.contains('prop 1 is not a valid value')) e,
  ];

  group('renders every page of the gallery [WGT-002] [RT-010]', () {
    for (final (route, texts) in [
      (
        'layout',
        [
          'baseline',
          'container',
          'decorated',
          'positioned',
          'wrap 3',
          'transformed',
        ],
      ),
      (
        'material',
        [
          'list tile',
          'choice',
          'badged',
          'expanded child',
          'extended fab',
          'bottom bar',
        ],
      ),
      ('inputs', ['Name', 'checkbox tile', 'radio tile', 'switch tile', 'Day']),
      ('cupertino', ['cupertino', 'section', 'tile', 'Left', 'Right']),
      (
        'text',
        ['Gallery', '3 widgets', 'token colour', 'world!', 'token padding'],
      ),
      (
        'collections',
        [
          'a 0',
          'grid c',
          'page a',
          'empty list',
          'sliver bar',
          'cell b',
          'remaining',
        ],
      ),
      (
        'structure',
        [
          'if then',
          'match two',
          'each x',
          'each y',
          'component title',
          'slot fill',
        ],
      ),
    ]) {
      testWidgets(route, (tester) async {
        await open(tester, route);
        for (final t in texts) {
          expect(find.text(t), findsWidgets, reason: t);
        }
        expect(find.textContaining('fallback'), findsNothing);
        if (route != 'structure') expect(nonIconProblems(), isEmpty);
        await expectLater(
          find.byType(MaterialApp),
          matchesGoldenFile('goldens/$route.png'),
        );
      });
    }
  });

  testWidgets('a page in dark mode uses dark token values [THM-002]', (
    tester,
  ) async {
    await open(tester, 'text', theme: ThemeData.dark());
    Color colour() =>
        tester.widget<Text>(find.text('token colour')).style!.color!;
    expect(colour(), const Color(0xFF6750A4));
    Plux.setThemeMode(ThemeMode.dark);
    await settle(tester);
    expect(colour(), const Color(0xFFD0BCFF));
    await expectLater(
      find.byType(MaterialApp),
      matchesGoldenFile('goldens/text_dark.png'),
    );
  });

  testWidgets('a brand overlay replaces the tokens it defines [THM-003]', (
    tester,
  ) async {
    await open(tester, 'text');
    Plux.setBrand('acme');
    await settle(tester);
    expect(
      tester.widget<Text>(find.text('token colour')).style!.color,
      const Color(0xFFFF5722),
    );
    expect(
      tester
          .widget<Padding>(
            find
                .ancestor(
                  of: find.text('token padding'),
                  matching: find.byType(Padding),
                )
                .first,
          )
          .padding,
      const EdgeInsets.all(12),
      reason: 'a token the brand does not define keeps its value',
    );
  });

  testWidgets('translations, parameters and plural forms [HST-001]', (
    tester,
  ) async {
    await open(tester, 'text', params: {'who': 'Ada'});
    expect(find.text('Ada!'), findsOneWidget);
    expect(find.text('3 widgets'), findsOneWidget);
    final state = pageState(tester, find.text('3 widgets'));
    state.set('count', 1);
    await tester.pump();
    expect(find.text('1 widget'), findsOneWidget);
  });

  testWidgets('a state change rebuilds only the nodes that read it [RT-012]', (
    tester,
  ) async {
    await open(tester, 'text');
    Widget built(String text) => tester.widget(find.text(text));
    final before = built('token padding');
    final state = pageState(tester, find.text('3 widgets'));
    state.set('count', 7);
    await tester.pump();
    expect(find.text('7 widgets'), findsOneWidget);
    expect(identical(built('token padding'), before), isTrue);
  });

  testWidgets(
    'override layers follow the window size class [WGT-010] [BND-016]',
    (tester) async {
      await open(tester, 'text', size: const Size(500, 1400));
      expect(
        tester.widget<Text>(find.text('wide only')).textAlign,
        TextAlign.start,
      );
      await tester.binding.setSurfaceSize(null);
      tester.view.physicalSize = const Size(1000, 1400);
      await settle(tester);
      expect(
        tester.widget<Text>(find.text('wide only')).textAlign,
        TextAlign.end,
      );
    },
  );

  testWidgets('structural nodes follow state and the window [WGT-012]', (
    tester,
  ) async {
    await open(tester, 'structure', size: const Size(400, 1400));
    expect(find.text('compact'), findsOneWidget);
    expect(find.text('hidden'), findsNothing);
    final state = pageState(tester, find.text('if then'));
    state.set('flag', false);
    state.set('mode', 'three');
    state.set('letters', ['p', 'q', 'r']);
    await tester.pump();
    expect(find.text('if else'), findsOneWidget);
    expect(find.text('hidden'), findsOneWidget);
    expect(find.text('match other'), findsOneWidget);
    expect(find.text('each r'), findsOneWidget);
    tester.view.physicalSize = const Size(1000, 1400);
    await settle(tester);
    expect(find.text('expanded'), findsOneWidget);
  });

  testWidgets(
    'inputs keep their value locally and report their events [ADR-0031]',
    (tester) async {
      await open(tester, 'inputs');
      h.errors.clear();
      await tester.tap(find.byType(Checkbox).first);
      await tester.pump();
      expect(
        tester.widget<Checkbox>(find.byType(Checkbox).first).value,
        isFalse,
      );
      await tester.tap(find.byType(Switch).first);
      await tester.pump();
      expect(tester.widget<Switch>(find.byType(Switch).first).value, isFalse);
      await tester.enterText(find.byType(TextField).first, 'Grace');
      await tester.pump();
      expect(find.text('Grace'), findsOneWidget);
      expect(
        h.errors.map((e) => e.code),
        everyElement(PluxErrorCode.actionsNotAvailable),
      );
      expect(
        h.errors,
        isNotEmpty,
        reason: 'each handled event reports PLX-4010',
      );
    },
  );

  testWidgets(
    'buttons with handlers are enabled and fire no action [ADR-0031]',
    (tester) async {
      await open(tester, 'material');
      h.errors.clear();
      await tester.tap(find.text('elevated'));
      await tester.pump();
      expect(h.errors.single.code, PluxErrorCode.actionsNotAvailable);
      expect(
        tester.widget<OutlinedButton>(find.byType(OutlinedButton)).onPressed,
        isNull,
        reason: 'no handler, so disabled',
      );
    },
  );

  testWidgets(
    'a failed image is contained, reported and shows its error slot [RT-020]',
    (tester) async {
      await open(tester, 'structure');
      expect(
        problems().map((e) => e.message),
        contains(contains('an image failed to load')),
      );
      expect(find.text('image failed'), findsOneWidget);
    },
  );

  testWidgets('cupertino inputs keep their value locally', (tester) async {
    await open(tester, 'cupertino');
    await tester.tap(find.byType(CupertinoSwitch));
    await tester.pump();
    expect(
      tester.widget<CupertinoSwitch>(find.byType(CupertinoSwitch)).value,
      isFalse,
    );
  });
}

/// The state of the page showing [finder].
PageStateNotifier pageState(WidgetTester tester, Finder finder) {
  final scope = RenderScope.of(tester.element(finder));
  return Plux.container.read(pageStateProvider(scope.state!).notifier);
}
