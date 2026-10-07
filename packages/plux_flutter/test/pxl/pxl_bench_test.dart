// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/pxl/program.dart';
import 'package:plux_flutter/src/pxl/types.dart';
import 'package:plux_flutter/src/pxl/vm.dart';

/// The evaluation of typical PXL bindings, the measurement of NFR-010
/// (PXL-004: a binding of at most 20 operations in ≤ 2 µs p95 on the
/// mid-tier reference device). The programs are the bytecode the Go
/// compiler wrote for the conformance vectors in `schema/testdata/pxl`,
/// the same bytes the Dart VM runs on a device; each is picked by its
/// source expression, so a vector that changes fails this test instead of
/// changing what it measures. `make bench-pxl` runs it and prints one JSON
/// line; it is not gated here, because the gate is a number from the
/// reference device, and `flutter test` runs the JIT, which is slower than
/// the release build's AOT code.
void main() {
  final vectors = Directory('../../schema/testdata/pxl');

  /// The typical bindings: a vector file and the source expression of one
  /// of its cases, under the name the report gives it.
  const bindings = [
    (
      'concatenation with field reads',
      'runtime.json',
      'currency(price) + " " + string(amount(price))',
    ),
    ('arithmetic with a comparison', 'comparison.json', 'n < x * 3.0'),
    (
      'conditional with a field read',
      'logic.json',
      '!(page.status == null) ? upper(page.status) : "none"',
    ),
    ('null-coalescing field read', 'runtime.json', 'page?.status ?? "none"'),
    ('function call: len', 'runtime.json', 'len(s)'),
    (
      'function call: format',
      'strings.json',
      'format.iban("de89 3704 0044 0532 0130 00")',
    ),
  ];

  const warmup = 2000;
  const samples = 20000;
  const maxOperations = 20;

  test(
    'measures the evaluation of typical bindings [NFR-010] [PXL-004]',
    () {
      final out = <String, Object?>{};
      for (final (name, file, expr) in bindings) {
        final f = jsonDecode(
          File('${vectors.path}/$file').readAsStringSync(),
        ) as Map<String, Object?>;
        final env = PxlEnv.fromJson(f['env']! as Map<String, Object?>);
        final c = (f['cases']! as List<Object?>)
            .cast<Map<String, Object?>>()
            .singleWhere((c) => c['expr'] == expr && c['error'] == null);
        final program = Program.decode(base64.decode(c['program']! as String));
        var operations = 0;
        for (var pc = 0; pc < program.code.length;) {
          pc = program.at(pc)!.next;
          operations++;
        }
        expect(
          operations,
          lessThanOrEqualTo(maxOperations),
          reason: '$name is not a typical binding',
        );
        final inputs =
            (c['inputs'] ?? f['inputs'] ?? const <String, Object?>{})
                as Map<String, Object?>;
        final values = {
          for (final e in inputs.entries)
            e.key: fromJson(env.roots[e.key]!, e.value),
        };
        final limits = PxlLimits.defaults();

        late PxlResult result;
        for (var i = 0; i < warmup; i++) {
          result = evaluate(program, values, limits);
        }
        expect(result, isA<PxlValue>(), reason: name);
        final micros = List<double>.filled(samples, 0);
        final watch = Stopwatch();
        for (var i = 0; i < samples; i++) {
          watch
            ..reset()
            ..start();
          result = evaluate(program, values, limits);
          watch.stop();
          micros[i] = watch.elapsedTicks * 1e6 / watch.frequency;
        }
        expect(result, isA<PxlValue>(), reason: name);
        micros.sort();
        double at(double q) => micros[((micros.length - 1) * q).round()];
        out[name] = {
          'expr': expr,
          'operations': operations,
          'p50_us': double.parse(at(0.5).toStringAsFixed(3)),
          'p95_us': double.parse(at(0.95).toStringAsFixed(3)),
        };
      }
      // ignore: avoid_print
      print(
        jsonEncode({
          'benchmark': 'pxl-bindings',
          'samples': samples,
          'bindings': out,
        }),
      );
    },
    skip: Platform.environment['PLUX_BENCH_PXL'] == null
        ? 'set PLUX_BENCH_PXL=1, or run make bench-pxl'
        : false,
  );
}
