// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The runtime benchmark (QA-007): one run starts Plux from the embedded
/// fifty-plugin release and measures, in this order,
///
/// 1. `Plux.initialize` with the cached release, first in the process,
///    and the resident memory it adds with the fifty plugins installed
///    (NFR-001, NFR-008) — taken before any page is drawn, because once
///    pages are drawn the memory of Flutter's rendering and the timing of
///    garbage collection move the process's size by more than the
///    runtime's whole overhead;
/// 2. `Plux.initialize` again, [BenchOptions.repeat] times, each after
///    `Plux.dispose`;
/// 3. opening the 300-node catalog page to its first frame, cold and then
///    warm, and that frame's UI-thread time — build, layout and paint of
///    the page (NFR-002, NFR-003);
/// 4. the build and raster times of every frame while the 500-item feed
///    scrolls, whose rows are bound through PXL;
/// 5. the same for the 1,000-item list, scrolled end to end (NFR-004).
///
/// The control — the catalog page written in Flutter — opens after the
/// Plux page. [BenchOptions.scenarios] can limit a run to some of these
/// parts: CI measures them in parallel jobs, each comparing the same parts
/// of the base and the change (ADR-0043).
///
/// The server is unreachable (the discard port of the loopback address),
/// so every run renders what is on the device, as an app start without a
/// network does.
library;

import 'dart:async';
import 'dart:convert';
import 'dart:developer' show Timeline;
import 'dart:io';
import 'dart:math' show max;
import 'dart:ui' show FramePhase;

import 'package:flutter/material.dart';
import 'package:flutter/scheduler.dart';
import 'package:flutter/services.dart';
import 'package:plux_bench_runtime/src/frames.dart';
import 'package:plux_flutter/plux_flutter.dart';

/// The route of the page of 300 nodes (backend/internal/benchproject).
const catalogRoute = 'catalog';

/// The route of the page with the bound list.
const feedRoute = 'feed';

/// The route of the page with the 1,000-item list (NFR-004).
const listRoute = 'list';

/// The text at the end of the catalog page: it is on screen only when
/// Plux rendered the page rather than a fallback.
const catalogEnd = 'bench-end';

/// The first row of the feed.
const feedFirst = 'Story 1';

/// The first row of the list.
const listFirst = 'Item 1';

/// Where the embedded release is, in the app's assets.
const baselineDirectory = 'assets/plux';

/// Starts the runtime; `Plux.initialize` outside tests.
typedef BenchInitialize = Future<PluxStartup> Function(PluxConfig config);

/// A part of a run, in the order a run measures the parts.
enum BenchScenario {
  /// `Plux.initialize`, first and repeated, and the memory it adds.
  startup,

  /// Opening the catalog page, cold and warm.
  open,

  /// The control: the same page written in Flutter.
  native,

  /// Scrolling the feed.
  scroll,

  /// Scrolling the 1,000-item list end to end.
  list,
}

/// What one run does.
@immutable
final class BenchOptions {
  /// Creates options.
  const BenchOptions({
    this.storageDirectory,
    this.output,
    this.repeat = 10,
    this.scrollDistance = 20000,
    this.scrollDuration = const Duration(seconds: 4),
    this.scenarios = const {...BenchScenario.values},
  }) : assert(repeat > 0, 'at least one repetition');

  /// Reads the options from the process environment:
  /// `PLUX_BENCH_STORE` (the release store's directory; the platform's
  /// otherwise), `PLUX_BENCH_OUT` (a file for the result; printed
  /// otherwise), `PLUX_BENCH_REPEAT` (repetitions, 10 by default) and
  /// `PLUX_BENCH_SCENARIOS` (the parts to measure, comma-separated; all by
  /// default).
  factory BenchOptions.fromEnvironment(Map<String, String> env) {
    String? get(String k) => (env[k] ?? '').isEmpty ? null : env[k];
    final repeat = get('PLUX_BENCH_REPEAT');
    final n = repeat == null ? 10 : int.tryParse(repeat);
    if (n == null || n < 1) {
      throw FormatException('PLUX_BENCH_REPEAT is not a positive integer', n);
    }
    final names = get('PLUX_BENCH_SCENARIOS');
    final scenarios = <BenchScenario>{...BenchScenario.values};
    if (names != null) {
      scenarios.clear();
      for (final name in names.split(',')) {
        final s = BenchScenario.values.asNameMap()[name.trim()];
        if (s == null) {
          throw FormatException(
            'PLUX_BENCH_SCENARIOS names an unknown part; known: '
            '${BenchScenario.values.map((s) => s.name).join(', ')}',
            name,
          );
        }
        scenarios.add(s);
      }
    }
    return BenchOptions(
      storageDirectory: get('PLUX_BENCH_STORE'),
      output: get('PLUX_BENCH_OUT'),
      repeat: n,
      scenarios: scenarios,
    );
  }

  /// The release store's directory; null for the platform's.
  final String? storageDirectory;

  /// The file the result is written to; null to print it.
  final String? output;

  /// How many times each repeated measurement is taken in a run.
  final int repeat;

  /// How far the feed scrolls, in logical pixels.
  final double scrollDistance;

  /// How long the scroll takes.
  final Duration scrollDuration;

  /// The parts of the run to measure. Plux is started either way.
  final Set<BenchScenario> scenarios;
}

/// The samples of one run, by metric, in the unit the name ends with.
final class BenchResult {
  /// Creates a result.
  BenchResult({required this.runtime, required this.platform});

  /// The runtime's version.
  final String runtime;

  /// The operating system the run was on.
  final String platform;

  /// Samples by metric name.
  final Map<String, List<double>> samples = {};

  /// How many plugins the release holds.
  int plugins = 0;

  /// Codes of the problems the runtime reported, in order.
  final List<String> problems = [];

  /// Adds a sample of [metric].
  void add(String metric, double value) => (samples[metric] ??= []).add(value);

  /// The result as JSON, the format tools/cmd/benchcmp reads.
  Map<String, Object?> toJson() => {
    'benchmark': 'plux-runtime',
    'format': 1,
    'runtime': runtime,
    'platform': platform,
    'plugins': plugins,
    'samples': {for (final k in samples.keys.toList()..sort()) k: samples[k]},
    'problems': problems,
  };
}

/// Why a run could not measure what it should.
final class BenchFailure implements Exception {
  /// Creates a failure.
  const BenchFailure(this.message);

  /// What went wrong.
  final String message;

  @override
  String toString() => 'BenchFailure: $message';
}

/// The embedded release's app and plugin routes, from `baseline.json`.
@immutable
final class BenchRelease {
  /// Creates the description.
  const BenchRelease(this.appId, this.routes);

  /// Reads `baseline.json`; every plugin's entry page has the plugin's key
  /// as its route.
  factory BenchRelease.parse(String json) {
    final j = jsonDecode(json) as Map<String, Object?>;
    final bundles = (j['bundles']! as List<Object?>)
        .cast<Map<String, Object?>>();
    return BenchRelease(j['app']! as String, [
      for (final b in bundles)
        if ((b['plugin']! as String).isNotEmpty) b['plugin']! as String,
    ]);
  }

  /// The app's ID.
  final String appId;

  /// Every plugin's route.
  final List<String> routes;
}

/// The host app the benchmark drives: a native home screen with an app
/// bar and text, as every host shows before the user opens a Plux page —
/// so Flutter's first text layout, which costs the first page drawn in a
/// process tens of milliseconds whoever builds it, is not charged to
/// Plux — and a navigator.
final class BenchHost extends StatelessWidget {
  /// Creates the host.
  const BenchHost({super.key, required this.navigator});

  /// The navigator Plux pages are pushed on.
  final GlobalKey<NavigatorState> navigator;

  @override
  Widget build(BuildContext context) => MaterialApp(
    navigatorKey: navigator,
    debugShowCheckedModeBanner: false,
    home: Scaffold(
      appBar: AppBar(title: const Text('Plux benchmark')),
      body: const Center(child: Text('The benchmark opens Plux pages here.')),
    ),
  );
}

/// The catalog page of backend/internal/benchproject written in Flutter:
/// the same 300 widgets with the same properties and texts, so its first
/// frame is what Flutter alone costs for the page Plux renders.
final class NativeCatalog extends StatelessWidget {
  /// Creates the page.
  const NativeCatalog({super.key});

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(title: const Text('Catalog')),
    body: SingleChildScrollView(
      child: Column(
        children: [
          for (var i = 0; i < 29; i++)
            Padding(
              padding: const EdgeInsets.all(8),
              child: Row(
                children: [
                  const Icon(Icons.shopping_bag, size: 32),
                  const SizedBox(width: 12),
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(
                          'Product ${i + 1}',
                          style: const TextStyle(fontWeight: FontWeight.w600),
                        ),
                        const Text(
                          'A short description of the product, long enough '
                          'to wrap onto a second line.',
                        ),
                        Text('SKU-${1000 + i}'),
                      ],
                    ),
                  ),
                  Text('€${9 + i}.99'),
                ],
              ),
            ),
          const Padding(
            padding: EdgeInsets.all(16),
            child: Column(
              children: [Text('Revision 1'), Divider(), Text(catalogEnd)],
            ),
          ),
        ],
      ),
    ),
  );
}

/// Runs frames until none is scheduled, at most [limit] of them.
Future<void> settle(SchedulerBinding binding, {int limit = 600}) async {
  for (var i = 0; i < limit; i++) {
    await binding.endOfFrame;
    if (!binding.hasScheduledFrame) return;
  }
  throw const BenchFailure('frames kept coming: nothing settled');
}

/// Whether the tree under [root] shows [text].
bool showsText(Element root, String text) {
  var found = false;
  void visit(Element e) {
    if (found) return;
    final w = e.widget;
    if (w is RichText && w.text.toPlainText() == text) {
      found = true;
      return;
    }
    e.visitChildElements(visit);
  }

  visit(root);
  return found;
}

/// The last vertical scrollable under [root] that can scroll.
ScrollableState? findScrollable(Element root) {
  ScrollableState? found;
  void visit(Element e) {
    if (e is StatefulElement && e.state is ScrollableState) {
      final s = e.state as ScrollableState;
      final p = s.position;
      if (s.widget.axis == Axis.vertical &&
          p.hasContentDimensions &&
          p.maxScrollExtent > 0) {
        found = s;
      }
    }
    e.visitChildElements(visit);
  }

  visit(root);
  return found;
}

/// One benchmark run; [run] measures and returns the samples.
final class Benchmark {
  /// Creates a run of [options] in [binding], starting Plux with
  /// [initialize].
  Benchmark(
    this.options, {
    required this.binding,
    this.initialize = Plux.initialize,
    int Function()? now,
    int Function()? rss,
  }) : _now = now ?? (() => Timeline.now),
       _rss = rss ?? (() => ProcessInfo.currentRss);

  /// What the run does.
  final BenchOptions options;

  /// The app's binding.
  final WidgetsBinding binding;

  /// Starts the runtime.
  final BenchInitialize initialize;

  final int Function() _now;
  final int Function() _rss;
  final FrameRecorder _frames = FrameRecorder();
  final GlobalKey<NavigatorState> _navigator = GlobalKey();

  /// Measures, in the order the library documentation gives.
  Future<BenchResult> run() async {
    final result = BenchResult(
      runtime: PluxRuntimeInfo.version,
      platform: Platform.operatingSystem,
    );
    final baseline = BenchRelease.parse(
      await rootBundle.loadString('$baselineDirectory/baseline.json'),
    );
    if (!baseline.routes.contains(catalogRoute) ||
        !baseline.routes.contains(feedRoute) ||
        !baseline.routes.contains(listRoute)) {
      throw const BenchFailure('the embedded release is not the benchmark');
    }
    result.plugins = baseline.routes.length;
    final config = PluxConfig(
      appId: baseline.appId,
      // The discard port: nothing listens, so the connection is refused.
      endpoint: Uri.parse('http://127.0.0.1:9'),
      baseline: baselineDirectory,
      storageDirectory: options.storageDirectory,
      hostBuild: 'bench',
      onError: (e, _) => result.problems.add(e.code.id),
    );
    _frames.attach(binding);
    try {
      binding.attachRootWidget(
        binding.wrapWithDefaultView(BenchHost(navigator: _navigator)),
      );
      binding.scheduleFrame();
      await settle(binding);
      final rssBefore = _rss();

      final parts = options.scenarios;
      final startup = parts.contains(BenchScenario.startup);
      await _initialize(config, startup ? result : null, 'initialize_first_ms');
      if (startup) {
        result.add('memory_overhead_mib', (_rss() - rssBefore) / (1024 * 1024));
        for (var i = 0; i < options.repeat; i++) {
          await Plux.dispose();
          await _initialize(config, result, 'initialize_ms');
        }
      }

      if (parts.contains(BenchScenario.open)) {
        await _open(catalogRoute, result, 'open_first_frame_cold_ms');
        await _pop();
        for (var i = 0; i < options.repeat; i++) {
          await _open(catalogRoute, result, 'open_first_frame_ms');
          await _pop();
        }
      }

      // The control: the same page written in Flutter, so the report can
      // tell Plux's cost from Flutter's own.
      if (parts.contains(BenchScenario.native)) {
        await _open(null, null, null);
        await _pop();
        for (var i = 0; i < options.repeat; i++) {
          await _open(null, result, 'native_open_first_frame_ms');
          await _pop();
        }
      }

      if (parts.contains(BenchScenario.scroll)) {
        await _scroll(feedRoute, feedFirst, 'scroll', result);
      }
      if (parts.contains(BenchScenario.list)) {
        await _scroll(listRoute, listFirst, 'list', result, endToEnd: true);
      }

      await Plux.dispose();
    } finally {
      _frames.detach();
    }
    final render = result.problems.where((c) => c.startsWith('PLX-4'));
    if (render.isNotEmpty) {
      throw BenchFailure('pages reported problems: ${render.join(', ')}');
    }
    return result;
  }

  /// Starts Plux and, with a [result], records how long it took.
  Future<void> _initialize(
    PluxConfig config,
    BenchResult? result,
    String metric,
  ) async {
    final watch = Stopwatch()..start();
    final startup = await initialize(config);
    watch.stop();
    if (!startup.ready) {
      throw BenchFailure('Plux started without a release: $startup');
    }
    result?.add(metric, watch.elapsedMicroseconds / 1000);
  }

  /// Pushes the Plux page [route] — or, for null, [NativeCatalog] — and,
  /// with a [metric], records the time from the push to the end of the
  /// rasterization of the page's first frame, and that frame's UI-thread
  /// time as `page_frame_ui_ms` (`page_frame_ui_cold_ms` for a cold open,
  /// `native_page_frame_ui_ms` for the native page). A catalog page must
  /// be complete in that first frame.
  Future<void> _open(String? route, BenchResult? result, String? metric) async {
    final context = _navigator.currentContext!;
    final shown = Completer<(int, bool)>();
    final pushed = _now();
    unawaited(
      route == null
          ? _navigator.currentState!.push(
              MaterialPageRoute<void>(builder: (_) => const NativeCatalog()),
            )
          : Plux.open<void>(context, route),
    );
    final catalog = route == null || route == catalogRoute;
    binding.addPostFrameCallback(
      (_) => shown.complete((
        _now(),
        !catalog || showsText(binding.rootElement!, catalogEnd),
      )),
    );
    final (afterFrame, complete) = await shown.future;
    await settle(binding);
    if (!complete) {
      throw BenchFailure(
        'the first frame of ${route ?? 'the native catalog'} does not show '
        'the page',
      );
    }
    if (result == null || metric == null) return;
    final timing = await _frames.frameBefore(afterFrame);
    // The frame timings and Timeline.now must share a clock for the
    // latency below to mean anything: the page's frame handed its scene
    // to the raster thread just before its post-frame callbacks ran.
    if (afterFrame - frameEnd(timing) > 100000 || frameStart(timing) < pushed) {
      throw BenchFailure(
        'frame timings and Timeline.now disagree: pushed at $pushed, '
        'frame built ${frameStart(timing)}–${frameEnd(timing)}, '
        'post-frame callback at $afterFrame',
      );
    }
    final raster = timing.timestampInMicroseconds(FramePhase.rasterFinish);
    result
      ..add(metric, (raster - pushed) / 1000)
      ..add(
        route == null
            ? 'native_page_frame_ui_ms'
            : metric.endsWith('_cold_ms')
            ? 'page_frame_ui_cold_ms'
            : 'page_frame_ui_ms',
        timing.buildDuration.inMicroseconds / 1000,
      );
  }

  Future<void> _pop() async {
    _navigator.currentState!.pop();
    await settle(binding);
  }

  /// Scrolls the page [route], whose first row shows [first], and records
  /// every frame drawn meanwhile as `<prefix>_build_ms`,
  /// `<prefix>_raster_ms` and `<prefix>_janky_pct`; with [endToEnd] the
  /// page scrolls to the end of its list at the speed of
  /// [BenchOptions.scrollDistance] over [BenchOptions.scrollDuration], and
  /// the slowest frame, build or raster, is `<prefix>_max_frame_ms`.
  Future<void> _scroll(
    String route,
    String first,
    String prefix,
    BenchResult result, {
    bool endToEnd = false,
  }) async {
    await _open(route, null, null);
    final root = binding.rootElement!;
    if (!showsText(root, first)) {
      throw BenchFailure('the page $route did not render');
    }
    final list = findScrollable(root);
    if (list == null) throw BenchFailure('the page $route does not scroll');
    final distance = endToEnd
        ? list.position.maxScrollExtent
        : options.scrollDistance;
    final duration = endToEnd
        ? options.scrollDuration * (distance / options.scrollDistance)
        : options.scrollDuration;
    final from = _now();
    await list.position.animateTo(
      distance,
      duration: duration,
      curve: Curves.linear,
    );
    final to = _now();
    await settle(binding);
    final frames = await _frames.between(from, to);
    if (frames.length < 10) {
      throw BenchFailure('only ${frames.length} frames while scrolling');
    }
    final hz = binding.platformDispatcher.displays.firstOrNull?.refreshRate;
    final budget = 1e6 / (hz == null || hz <= 0 ? 60 : hz);
    var janky = 0;
    var slowest = 0;
    for (final t in frames) {
      final build = t.buildDuration.inMicroseconds;
      final raster = t.rasterDuration.inMicroseconds;
      if (build > budget || raster > budget) janky++;
      slowest = max(slowest, max(build, raster));
      result
        ..add('${prefix}_build_ms', build / 1000)
        ..add('${prefix}_raster_ms', raster / 1000);
    }
    result.add('${prefix}_janky_pct', 100 * janky / frames.length);
    if (endToEnd) result.add('${prefix}_max_frame_ms', slowest / 1000);
    await _pop();
  }
}

/// The longest part of a printed result: device logs cut longer lines
/// (logcat at about 4,000 bytes).
const printedPart = 800;

/// Writes [result] where [options] say: to the output file as one line of
/// JSON, or printed as `PLUX_BENCH <n>/<count> <part>` lines, which
/// `benchcmp` joins again.
Future<void> writeResult(
  BenchOptions options,
  BenchResult result, {
  void Function(String line) print = print,
}) async {
  final json = jsonEncode(result.toJson());
  final out = options.output;
  if (out != null) {
    await File(out).writeAsString('$json\n', flush: true);
    return;
  }
  final count = (json.length + printedPart - 1) ~/ printedPart;
  for (var i = 0; i < count; i++) {
    final end = (i + 1) * printedPart;
    print(
      'PLUX_BENCH ${i + 1}/$count '
      '${json.substring(i * printedPart, end < json.length ? end : json.length)}',
    );
  }
}
