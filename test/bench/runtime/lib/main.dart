// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:async';
import 'dart:io';

import 'package:flutter/widgets.dart';
import 'package:plux_bench_runtime/bench.dart';

/// Runs the benchmark once, writes its samples and exits: 0 when it
/// measured everything, 1 when a run failed, 2 when it took over five
/// minutes. Build and run it in profile mode (README.md).
Future<void> main() async {
  final binding = WidgetsFlutterBinding.ensureInitialized();
  final watchdog = Timer(const Duration(minutes: 5), () {
    stderr.writeln('plux-bench: the run did not finish in five minutes');
    exit(2);
  });
  try {
    final options = BenchOptions.fromEnvironment(Platform.environment);
    final result = await Benchmark(options, binding: binding).run();
    await writeResult(options, result);
    watchdog.cancel();
    exit(0);
  } on Object catch (e, stack) {
    stderr.writeln('plux-bench: $e\n$stack');
    exit(1);
  }
}
