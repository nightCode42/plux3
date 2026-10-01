// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:ui' show FrameTiming;

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_bench_runtime/bench.dart';

FrameTiming timing(int buildStart, int buildFinish, {int raster = 5000}) =>
    FrameTiming(
      vsyncStart: buildStart - 100,
      buildStart: buildStart,
      buildFinish: buildFinish,
      rasterStart: buildFinish + 10,
      rasterFinish: buildFinish + 10 + raster,
      rasterFinishWallTime: 0,
    );

void main() {
  group('BenchOptions.fromEnvironment', () {
    test('takes the defaults for missing and empty variables', () {
      final o = BenchOptions.fromEnvironment(const {'PLUX_BENCH_OUT': ''});
      expect(o.storageDirectory, isNull);
      expect(o.output, isNull);
      expect(o.repeat, 10);
    });

    test('reads every variable', () {
      final o = BenchOptions.fromEnvironment(const {
        'PLUX_BENCH_STORE': '/s',
        'PLUX_BENCH_OUT': '/o.json',
        'PLUX_BENCH_REPEAT': '3',
        'PLUX_BENCH_SCENARIOS': 'startup, scroll',
      });
      expect((o.storageDirectory, o.output, o.repeat), ('/s', '/o.json', 3));
      expect(o.scenarios, {BenchScenario.startup, BenchScenario.scroll});
    });

    test('measures every part unless told otherwise', () {
      expect(BenchOptions.fromEnvironment(const {}).scenarios, {
        ...BenchScenario.values,
      });
      expect(
        BenchOptions.fromEnvironment(const {'PLUX_BENCH_SCENARIOS': ''})
            .scenarios,
        {...BenchScenario.values},
      );
    });

    test('refuses an unknown part', () {
      expect(
        () => BenchOptions.fromEnvironment({
          'PLUX_BENCH_SCENARIOS': 'open,render',
        }),
        throwsA(
          isA<FormatException>().having(
            (e) => e.message,
            'message',
            contains('startup, open, native, scroll'),
          ),
        ),
      );
    });

    test('refuses a repetition count that is not positive', () {
      for (final v in ['0', '-1', 'x']) {
        expect(
          () => BenchOptions.fromEnvironment({'PLUX_BENCH_REPEAT': v}),
          throwsFormatException,
        );
      }
    });
  });

  test('the embedded release is the fifty-plugin benchmark project', () {
    final b = BenchRelease.parse(
      File('assets/plux/baseline.json').readAsStringSync(),
    );
    expect(b.appId, '01f0c450-6c00-7000-8000-000000000001');
    expect(b.routes, hasLength(50));
    expect(b.routes.take(3), [catalogRoute, 'extra01', 'extra02']);
    expect(b.routes, contains(feedRoute));
  });

  group('results', () {
    BenchResult sample() => BenchResult(runtime: '0.1.0', platform: 'linux')
      ..plugins = 50
      ..add('z_ms', 2)
      ..add('a_ms', 1)
      ..add('a_ms', 1.5)
      ..problems.add('PLX-3020');

    test('are JSON with the metrics sorted', () {
      final j = sample().toJson();
      expect(j['benchmark'], 'plux-runtime');
      expect(j['format'], 1);
      expect(j['plugins'], 50);
      expect((j['samples']! as Map).keys, ['a_ms', 'z_ms']);
      expect(jsonEncode(j), contains('"a_ms":[1.0,1.5]'));
    });

    test('are written to the output file as one line', () async {
      final dir = Directory.systemTemp.createTempSync('bench');
      addTearDown(() => dir.deleteSync(recursive: true));
      final out = '${dir.path}/r.json';
      await writeResult(BenchOptions(output: out), sample());
      final text = File(out).readAsStringSync();
      expect(text.trim().split('\n'), hasLength(1));
      expect(jsonDecode(text), sample().toJson());
    });

    test('are printed in parts a device log keeps whole', () async {
      final result = sample();
      for (var i = 0; i < 400; i++) {
        result.add('scroll_ms', i / 7);
      }
      final lines = <String>[];
      await writeResult(const BenchOptions(), result, print: lines.add);
      expect(lines.length, greaterThan(2));
      final parts = [
        for (final (i, l) in lines.indexed)
          l.substring('PLUX_BENCH ${i + 1}/${lines.length} '.length),
      ];
      for (final (i, l) in lines.indexed) {
        expect(l, startsWith('PLUX_BENCH ${i + 1}/${lines.length} '));
      }
      expect(parts.every((p) => p.length <= printedPart), isTrue);
      expect(jsonDecode(parts.join()), result.toJson());
    });
  });

  group('FrameRecorder', () {
    test('finds the frame a post-frame callback followed', () async {
      final r = FrameRecorder()
        ..add([timing(1000, 3000), timing(20000, 26000)]);
      final found = r.frameBefore(27000);
      // Not yet: no later frame has been reported.
      var done = false;
      unawaited(found.then((_) => done = true));
      await Future<void>.delayed(Duration.zero);
      expect(done, isFalse);
      r.add([timing(40000, 41000)]);
      final t = await found;
      expect(frameStart(t), 20000);
      expect(frameEnd(t), 26000);
      expect(r.frames, hasLength(3));
    });

    test('lists the frames that started in a range', () async {
      final r = FrameRecorder()
        ..add([
          timing(1000, 2000),
          timing(5000, 6000),
          timing(9000, 9500),
          timing(12000, 13000),
        ]);
      final frames = await r.between(5000, 9000);
      expect(frames.map(frameStart), [5000, 9000]);
    });

    test('gives up when no later frame comes', () async {
      final r = FrameRecorder()..add([timing(1000, 2000)]);
      await expectLater(
        r.frameBefore(5000, timeout: const Duration(milliseconds: 10)),
        throwsA(isA<TimeoutException>()),
      );
      await expectLater(
        r.between(0, 5000, timeout: const Duration(milliseconds: 10)),
        throwsA(isA<TimeoutException>()),
      );
    });

    test('refuses a time before every frame', () async {
      final r = FrameRecorder()..add([timing(1000, 2000)]);
      await expectLater(r.frameBefore(500), throwsStateError);
    });

    testWidgets('receives the binding\'s timings while attached', (
      tester,
    ) async {
      final r = FrameRecorder()..attach(tester.binding);
      tester.binding.platformDispatcher.onReportTimings!([timing(1000, 2000)]);
      r.detach();
      expect(r.frames, hasLength(1));
    });
  });

  group('the widget tree', () {
    testWidgets('shows a text and has a scrollable list', (tester) async {
      await tester.pumpWidget(
        MaterialApp(
          home: ListView(
            children: [for (var i = 0; i < 100; i++) Text('Row $i')],
          ),
        ),
      );
      final root = tester.binding.rootElement!;
      expect(showsText(root, 'Row 0'), isTrue);
      expect(showsText(root, 'Row 99'), isFalse);
      expect(findScrollable(root), isNotNull);
    });

    testWidgets('has nothing to scroll when all fits', (tester) async {
      await tester.pumpWidget(
        MaterialApp(home: ListView(children: const [Text('one')])),
      );
      expect(findScrollable(tester.binding.rootElement!), isNull);
    });
  });

  testWidgets('the host is a native home screen with a navigator', (
    tester,
  ) async {
    final key = GlobalKey<NavigatorState>();
    await tester.pumpWidget(BenchHost(navigator: key));
    expect(key.currentState, isNotNull);
    expect(find.byType(Scaffold), findsOneWidget);
    expect(find.text('Plux benchmark'), findsOneWidget);
  });

  test('failures say what went wrong', () {
    expect('${const BenchFailure('x')}', 'BenchFailure: x');
  });
}
