// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_flutter/src/core/host_events.dart';
import 'package:plux_flutter/src/pxl/decimal.dart';
import 'package:plux_flutter/src/pxl/values.dart';
import 'package:plux_flutter/src/render/renderer.dart';

import '../support/harness.dart';

/// Keeps what passes the typed sink.
final class _Capture implements HostEventSink {
  final List<PluxHostEvent> got = [];

  @override
  bool deliver(PluxHostEvent event) {
    got.add(event);
    return true;
  }
}

/// `Plux.sendEvent` against the app bundle's `hostEvents` declarations
/// (HST-013), on the triggers conformance project: `ping` (toPlux, `n:
/// int`), `priced` (both; a decimal, a date and an optional string) and,
/// for the direction, `taskCompleted` of the features project (toHost).
void main() {
  final g = Harness.goldens;
  late Harness h;
  late _Capture capture;

  setUp(() async => h = await Harness.create());
  tearDown(() => h.close());

  Future<void> start(WidgetTester tester, String app, String plugin) async {
    await tester.runAsync(
      () => h.startFrom(g.bundles['$app/$app.pxb']!, {
        plugin: g.bundles['$app/$plugin.pxb']!,
      }),
    );
    capture = _Capture();
    h.runtime.hostEventSink = TypedHostEvents(
      release: () => h.runtime.active.value,
      types: (r) => (h.runtime.renderer! as PluxRenderer).typesOf(r, ''),
      next: capture,
    );
  }

  PluxErrorCode? lastCode() => h.errors.isEmpty ? null : h.errors.last.code;

  testWidgets(
    'a payload reaches the triggers as the PXL values of its declared types [HST-013]',
    (tester) async {
      await start(tester, 'triggers', 'lab');
      expect(await Plux.sendEvent('ping', {'n': 2}), isTrue);
      expect(capture.got.single.payload, {'n': 2});
      expect(
        await Plux.sendEvent('priced', {
          'amount': '12.50',
          'on': '2026-03-04',
          'note': null,
        }),
        isTrue,
      );
      final payload = capture.got.last.payload;
      expect(payload['amount'], isA<Decimal>());
      expect(payload['on'], isA<PxlDate>());
      expect(payload['note'], isNull);
      expect(h.errors, isEmpty);
    },
  );

  testWidgets(
    'an undeclared event, or one declared toHost, is refused with PLX-5307 [HST-013]',
    (tester) async {
      await start(tester, 'triggers', 'lab');
      expect(await Plux.sendEvent('pong'), isFalse);
      expect(lastCode(), PluxErrorCode.hostEventRefused);
      expect(capture.got, isEmpty);

      await tester.runAsync(h.close);
      h = await tester.runAsync(Harness.create) as Harness;
      await tester.runAsync(
        () => h.startFrom(g.bundles['features/features.pxb']!, {
          'tasks': g.bundles['features/tasks.pxb']!,
        }),
      );
      capture = _Capture();
      h.runtime.hostEventSink = TypedHostEvents(
        release: () => h.runtime.active.value,
        types: (r) => (h.runtime.renderer! as PluxRenderer).typesOf(r, ''),
        next: capture,
      );
      expect(
        await Plux.sendEvent('taskCompleted', {
          'title': 't',
          'priority': 'low',
        }),
        isFalse,
      );
      expect(lastCode(), PluxErrorCode.hostEventRefused);
      expect(capture.got, isEmpty);
    },
  );

  testWidgets(
    'a missing, unknown or mistyped field is refused with PLX-5500, never naming a value [HST-013]',
    (tester) async {
      await start(tester, 'triggers', 'lab');
      for (final payload in <Map<String, Object?>>[
        {},
        {'n': 'secret-value'},
        {'n': 1.5},
        {'n': 1, 'extra': 2},
      ]) {
        h.errors.clear();
        expect(
          await Plux.sendEvent('ping', payload),
          isFalse,
          reason: '$payload',
        );
        expect(lastCode(), PluxErrorCode.hostEventPayloadInvalid);
        expect(h.errors.single.message, isNot(contains('secret-value')));
      }
      h.errors.clear();
      expect(
        await Plux.sendEvent('priced', {'amount': 'abc', 'on': '2026-03-04'}),
        isFalse,
      );
      expect(lastCode(), PluxErrorCode.hostEventPayloadInvalid);
      expect(capture.got, isEmpty);
    },
  );
}
