// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/actions/engine.dart';
import 'package:plux_flutter/src/actions/graph.dart';
import 'package:plux_flutter/src/actions/handlers.dart';
import 'package:plux_flutter/src/actions/run.dart';
import 'package:plux_flutter/src/schema/registry.g.dart';

import 'engine_test.dart' show FakeNavigator;

/// The engine's cost per step, an early measurement of NFR-011 (ACT-008:
/// ≤ 20 µs per step p95 on the mid-tier reference device, excluding the
/// step's own work). Inputs are literals, so the time is the engine's:
/// reading inputs, dispatching, recording outputs and choosing the next
/// step. Recorded in docs/benchmarks/p4-routing.md, not gated (P4 plan
/// §3.4); `make bench-steps` runs it.
void main() {
  int id(String action) =>
      actionDescriptors.firstWhere((d) => d.name == action).id;
  int input(String action, String name) =>
      actionDescriptors.firstWhere((d) => d.name == action).inputs[name]!;

  const steps = 50;
  const runs = 400;

  ActionGraph chain(GraphStep Function(int i) step) => ActionGraph(
    id: 'bench',
    steps: [for (var i = 0; i < steps; i++) step(i)],
  );

  final kinds = {
    'condition': chain(
      (i) => GraphStep(
        id: 's$i',
        action: id('condition'),
        inputs: {input('condition', 'when'): (_) => true},
        branches: {'then': i + 1 < steps ? i + 1 : -1},
      ),
    ),
    'emitHostEvent': chain(
      (i) => GraphStep(
        id: 's$i',
        action: id('emitHostEvent'),
        inputs: {
          input('emitHostEvent', 'event'): (_) => 'tick',
          input('emitHostEvent', 'payload'): (_) => {'n': i, 'ok': true},
        },
        next: i + 1 < steps ? i + 1 : -1,
      ),
    ),
    'condition reading an earlier output': chain(
      (i) => GraphStep(
        id: 's$i',
        action: id('condition'),
        inputs: {
          input('condition', 'when'): (roots) =>
              i == 0 ||
              ((roots['steps']! as Map)['s${i - 1}'] as Map)['error'] == null,
        },
        branches: {'then': i + 1 < steps ? i + 1 : -1},
      ),
    ),
  };

  test(
    'measures the engine\'s cost per step [NFR-011]',
    () async {
      final host = ActionHost(
        context: StepContext(
          navigator: FakeNavigator(),
          emit: (_, _) {},
          nativeActions: const NoNativeActions(),
        ),
        limits: ActionLimits.of(const {}),
        report: (_) {},
        record: (_, {fields = const {}, route = '', pluginKey = ''}) {},
        route: 'bench',
        pluginKey: 'bench',
        debug: false,
      );
      final out = <String, Object?>{};
      for (final MapEntry(key: kind, value: graph) in kinds.entries) {
        final perStep = <double>[];
        for (var r = 0; r < runs + 50; r++) {
          final w = Stopwatch()..start();
          final res = await host.start(graph, roots: () => const {}, key: 'b');
          w.stop();
          expect(res?.outcome, RunOutcome.ok);
          if (r >= 50) perStep.add(w.elapsedMicroseconds / steps);
        }
        perStep.sort();
        double at(double q) => perStep[((perStep.length - 1) * q).round()];
        out[kind] = {
          'p50_us': double.parse(at(0.5).toStringAsFixed(2)),
          'p95_us': double.parse(at(0.95).toStringAsFixed(2)),
        };
      }
      // ignore: avoid_print
      print(
        jsonEncode({'steps_per_run': steps, 'runs': runs, 'per_step': out}),
      );
    },
    skip: Platform.environment['PLUX_BENCH_STEPS'] == null
        ? 'set PLUX_BENCH_STEPS=1, or run make bench-steps'
        : false,
  );
}
