// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/navigation/router.dart';
import 'package:plux_flutter/src/pxl/decimal.dart';
import 'package:plux_flutter/src/pxl/types.dart';

void main() {
  test('a page names its transition; anything else is the platform default [NAV-010]', () {
    expect(PluxTransition.parse('slideLeft'), PluxTransition.slideLeft);
    expect(PluxTransition.parse('sharedAxis'), PluxTransition.sharedAxis);
    expect(PluxTransition.parse(null), PluxTransition.platform);
    expect(PluxTransition.parse('custom'), PluxTransition.platform);
  });

  testWidgets('each presentation shows its route type [NAV-005]', (
    tester,
  ) async {
    late BuildContext context;
    await tester.pumpWidget(
      MaterialApp(
        home: Builder(
          builder: (c) {
            context = c;
            return const SizedBox();
          },
        ),
      ),
    );
    Route<void> route(PluxPresentation p) => PluxRouteSpec(
      name: 'r',
      presentation: p,
      builder: (_) => const SizedBox(),
    ).toRoute<void>(context);
    expect(route(PluxPresentation.page), isA<PluxPageRoute<void>>());
    expect(route(PluxPresentation.dialog), isA<DialogRoute<void>>());
    expect(
      route(PluxPresentation.bottomSheet),
      isA<ModalBottomSheetRoute<void>>(),
    );
    final full = route(PluxPresentation.fullscreenDialog);
    expect(full, isA<MaterialPageRoute<void>>());
    expect((full as MaterialPageRoute<void>).fullscreenDialog, isTrue);
    expect(route(PluxPresentation.page).settings.name, 'r');
  });

  Future<void> show(
    WidgetTester tester,
    PluxTransition transition, {
    TargetPlatform platform = TargetPlatform.iOS,
  }) async {
    final nav = GlobalKey<NavigatorState>();
    await tester.pumpWidget(
      MaterialApp(
        navigatorKey: nav,
        theme: ThemeData(platform: platform),
        home: const Text('home'),
      ),
    );
    nav.currentState!.push(
      PluxPageRoute<void>(
        transition: transition,
        builder: (_) => const Text('next'),
      ),
    );
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 50));
  }

  testWidgets('each transition animates the page in its own way [NAV-010]', (
    tester,
  ) async {
    final expected = {
      PluxTransition.fade: FadeTransition,
      PluxTransition.slideLeft: SlideTransition,
      PluxTransition.slideRight: SlideTransition,
      PluxTransition.slideUp: SlideTransition,
      PluxTransition.slideDown: SlideTransition,
      PluxTransition.scale: ScaleTransition,
      PluxTransition.sharedAxis: Transform,
    };
    for (final MapEntry(key: t, value: type) in expected.entries) {
      await show(tester, t);
      final next = find.text('next');
      expect(
        find.ancestor(of: next, matching: find.byType(type)),
        findsWidgets,
        reason: t.name,
      );
    }
  });

  testWidgets('no transition shows the page at once [NAV-010]', (tester) async {
    await show(tester, PluxTransition.none);
    expect(find.text('next'), findsOneWidget);
    expect(find.text('home'), findsNothing, reason: 'the old page is covered');
  });

  testWidgets(
    'on Android the platform default is the predictive-back transition [NAV-010]',
    (tester) async {
      await show(
        tester,
        PluxTransition.platform,
        platform: TargetPlatform.android,
      );
      expect(
        find.byWidgetPredicate(
          (w) => w.runtimeType.toString().contains('PredictiveBack'),
        ),
        findsWidgets,
      );
    },
  );

  test('a result is checked against the page\'s declared type before it crosses the navigator [NAV-003]', () {
    expect(PluxRouter.checkResult('7', 'string', const {}), '7');
    expect(
      PluxRouter.checkResult(Decimal.tryParse('12.50')!, 'decimal', const {}),
      '12.50',
    );
    expect(PluxRouter.checkResult(null, 'string', const {}), isNull);
    expect(
      () => PluxRouter.checkResult(3, 'string', const {}),
      throwsA(isA<ActionError>()),
    );
    expect(
      () => PluxRouter.checkResult(3, null, const {}),
      throwsA(isA<ActionError>()),
    );
    final item = NamedType.object('Item')
      ..fields!['title'] = const PxlType(PxlKind.string);
    expect(PluxRouter.checkResult({'title': 'a'}, 'Item', {'Item': item}), {
      'title': 'a',
    });
  });
}
