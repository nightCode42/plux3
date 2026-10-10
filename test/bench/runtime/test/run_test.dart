// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_bench_runtime/bench.dart';
import 'package:plux_flutter/plux_flutter.dart';
// The host run needs the runtime's test hook for the device credential:
// the platform's key store has no platform side under flutter test.
// ignore: implementation_imports
import 'package:plux_flutter/src/core/runtime.dart' show RuntimeOverrides;
// ignore: implementation_imports
import 'package:plux_flutter/src/sync/sync_engine.dart'
    show MemoryCredentialStore, MemorySecretStore;

void main() {
  final binding = LiveTestWidgetsFlutterBinding.ensureInitialized();
  binding.framePolicy = LiveTestWidgetsFlutterBindingFramePolicy.fullyLive;

  testWidgets('a run measures every metric from the embedded release', (
    tester,
  ) async {
    final dir = Directory.systemTemp.createTempSync('plux_bench');
    addTearDown(() => dir.deleteSync(recursive: true));
    final result = await Benchmark(
      BenchOptions(
        storageDirectory: dir.path,
        repeat: 2,
        scrollDuration: const Duration(seconds: 3),
      ),
      binding: binding,
      initialize: (c) => Plux.initializeWith(
        c,
        const RuntimeOverrides(
          credentials: MemoryCredentialStore.new,
          configSecrets: MemorySecretStore.new,
        ),
      ),
    ).run();
    expect(result.plugins, 50);
    expect(
      result.samples.keys,
      containsAll(<String>[
        'initialize_first_ms',
        'initialize_ms',
        'memory_overhead_mib',
        'open_first_frame_cold_ms',
        'open_first_frame_ms',
        'page_frame_ui_ms',
        'page_frame_ui_cold_ms',
        'native_open_first_frame_ms',
        'native_page_frame_ui_ms',
        'scroll_build_ms',
        'scroll_raster_ms',
        'scroll_janky_pct',
        'list_build_ms',
        'list_raster_ms',
        'list_janky_pct',
        'list_max_frame_ms',
      ]),
    );
    expect(result.samples['initialize_ms'], hasLength(2));
  });

  testWidgets('a run limited to some parts measures only those', (
    tester,
  ) async {
    final dir = Directory.systemTemp.createTempSync('plux_bench');
    addTearDown(() => dir.deleteSync(recursive: true));
    final result = await Benchmark(
      BenchOptions(
        storageDirectory: dir.path,
        repeat: 2,
        scenarios: const {BenchScenario.native},
      ),
      binding: binding,
      initialize: (c) => Plux.initializeWith(
        c,
        const RuntimeOverrides(
          credentials: MemoryCredentialStore.new,
          configSecrets: MemorySecretStore.new,
        ),
      ),
    ).run();
    expect(result.samples.keys.toSet(), {
      'native_open_first_frame_ms',
      'native_page_frame_ui_ms',
    });
    expect(result.samples['native_open_first_frame_ms'], hasLength(2));
  });
}
