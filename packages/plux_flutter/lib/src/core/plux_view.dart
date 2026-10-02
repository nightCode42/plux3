// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// `PluxView` (HST-001, NAV-004 from P4) and the page host behind it and
/// behind `Plux.open`: a page's lease on the release it renders from
/// (SYN-004), its root error boundary (RT-020), fallbacks for switched-off
/// and failing plugins (RT-022), and its timeline events (RT-015).
library;

import 'dart:async';
import 'dart:developer' as developer;
import 'dart:ui' as ui;

import 'package:flutter/scheduler.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/core/active_release.dart';
import 'package:plux_flutter/src/core/fallback.dart';
import 'package:plux_flutter/src/core/runtime.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/navigation/guards.dart';
import 'package:plux_flutter/src/state/providers.dart';

/// Shows a Plux page, by app-wide route name, inside any widget tree.
final class PluxView extends ConsumerWidget {
  /// Creates a view of [route] with [params].
  const PluxView(
    this.route, {
    super.key,
    this.params = const {},
    this.loadingBuilder,
    this.fallbackBuilder,
  }) : _routed = false,
       _guarded = false;

  /// A view whose page owns its route, so that its `pop` steps pop it; only
  /// the routes Plux builds are (ADR-0040). [guarded] when the route's
  /// guards already decided the entry.
  const PluxView._routed(
    this.route, {
    this.params = const {},
    this._guarded = false,
  }) : loadingBuilder = null,
       fallbackBuilder = null,
       _routed = true;

  /// The page a view's guards entered: its target, decided already.
  const PluxView._entered(
    this.route, {
    required this.params,
    required this.loadingBuilder,
    required this.fallbackBuilder,
    required this._routed,
  }) : _guarded = true;

  final bool _routed;

  /// Whether the route's guards already decided this entry (NAV-009).
  final bool _guarded;

  /// The route name (SCH-025).
  final String route;

  /// The page parameters.
  final Map<String, Object?> params;

  /// Shown while no release is available yet; empty when null.
  final WidgetBuilder? loadingBuilder;

  /// Shown instead of a page that cannot render; when null, the plugin's
  /// or the app's fallback of `PluxConfig` (RT-020, RT-022).
  final Widget Function(BuildContext context, PluxException error)?
  fallbackBuilder;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final rt = ref.watch(pluxRuntimeProvider);
    final release = ref.watch(activeReleaseProvider);
    if (rt == null || release == null) {
      return loadingBuilder?.call(context) ?? const SizedBox.shrink();
    }
    Widget fallback(PluxException e, [String? plugin]) =>
        fallbackBuilder?.call(context, e) ??
        buildFallback(context, rt.config, e, plugin: plugin);
    final PageRef? page;
    try {
      page = release.page(route);
    } on PluxException catch (e) {
      return fallback(e);
    }
    if (page == null) {
      final missing = PluxException(
        PluxErrorCode.routeNotFound,
        'no page has the route $route',
        details: {'route': route},
      );
      rt.reportProblem(missing);
      return fallback(missing);
    }
    var shown = page;
    if (release.disabled(page.plugin)) {
      final message = release.control.message;
      final off = PluxException(
        PluxErrorCode.pluginDisabled,
        message.isEmpty ? 'plugin ${page.plugin} is switched off' : message,
        details: {'plugin': page.plugin, 'reason': 'killSwitch'},
      );
      // The plugin's own fallback page, unless the whole app is switched
      // off; otherwise the app-level fallback (RT-022).
      final PageRef? declared;
      try {
        declared = release.control.appKillSwitch
            ? null
            : release.fallbackPage(page.plugin);
      } on PluxException catch (e) {
        return fallback(e, page.plugin);
      }
      if (declared == null) return fallback(off, page.plugin);
      // The kill switch never opens a guarded page: a declared fallback
      // page with guards or an assurance level, or whose requirements
      // cannot be read yet, gives way to the generic fallback (ADR-0040).
      final needs = RouteGuards.requirementsNow(release, declared);
      if (needs == null ||
          needs.guards.isNotEmpty ||
          needs.assurance > deviceAssurance) {
        return fallback(off, page.plugin);
      }
      shown = declared;
    } else if (!_guarded && RouteGuards.needsDecision(release, page)) {
      // An entry nothing decided yet: an embedded view, a declarative page
      // or a shell's tab runs the route's guards here (NAV-009).
      return _GuardGate(
        key: ValueKey((release.sequence, route)),
        view: this,
        guards: rt.guards,
        fallback: fallback,
      );
    }
    return PluxPageHost(
      key: ValueKey((release.sequence, route, shown.pageKey)),
      runtime: rt,
      page: shown,
      params: params,
      routed: _routed,
      fallback: (e) => fallback(e, shown.plugin),
    );
  }
}

/// The content of a route Plux built: a [PluxView] of [route] whose page
/// owns its route, so that its `pop` steps pop it, [guarded] when its
/// guards already decided the entry (ADR-0040).
PluxView routedPluxView(
  String route,
  Map<String, Object?> params, {
  bool guarded = false,
}) => PluxView._routed(route, params: params, guarded: guarded);

/// Runs the guards of [view]'s route, then shows the page they enter in
/// its place, which may be a redirect's target, or the fallback of the
/// route they refuse (NAV-009). Nothing of the guarded page builds before
/// they decide.
final class _GuardGate extends StatefulWidget {
  const _GuardGate({
    super.key,
    required this.view,
    required this.guards,
    required this.fallback,
  });

  final PluxView view;
  final RouteGuards guards;
  final Widget Function(PluxException e, [String? plugin]) fallback;

  @override
  State<_GuardGate> createState() => _GuardGateState();
}

final class _GuardGateState extends State<_GuardGate> {
  GuardVerdict? _verdict;

  @override
  void initState() {
    super.initState();
    final v = widget.view;
    unawaited(
      widget.guards.decide(v.route, v.params).then((verdict) {
        if (mounted) setState(() => _verdict = verdict);
      }),
    );
  }

  @override
  Widget build(BuildContext context) {
    final v = widget.view;
    return switch (_verdict) {
      null => v.loadingBuilder?.call(context) ?? const SizedBox.shrink(),
      GuardEnter(:final route, :final params) => PluxView._entered(
        route,
        params: params,
        loadingBuilder: v.loadingBuilder,
        fallbackBuilder: v.fallbackBuilder,
        routed: v._routed,
      ),
      GuardRefused(:final reason, :final plugin) => widget.fallback(
        reason,
        plugin,
      ),
    };
  }
}

/// Hosts one page: holds a lease on its release while mounted, checks the
/// page section before first use (BND-006), contains failures and records
/// build and first-frame timing.
final class PluxPageHost extends StatefulWidget {
  /// Creates a host.
  const PluxPageHost({
    super.key,
    required this.runtime,
    required this.page,
    required this.params,
    required this.fallback,
    this.routed = false,
  });

  /// Whether the page owns its route.
  final bool routed;

  /// The runtime.
  final PluxRuntime runtime;

  /// The page.
  final PageRef page;

  /// Its parameters.
  final Map<String, Object?> params;

  /// Builds the fallback for a failure.
  final Widget Function(PluxException error) fallback;

  @override
  State<PluxPageHost> createState() => _PluxPageHostState();
}

final class _PluxPageHostState extends State<PluxPageHost> {
  ActiveRelease? _release;
  PluxException? _failure;
  final Stopwatch _sinceMount = Stopwatch()..start();
  bool _reportedFrame = false;

  /// True while large sections are checked off the UI isolate.
  bool _preparing = false;

  // What render_perf and screen_view report (ADR-0034).
  int? _buildMs;
  int? _firstFrameMs;
  int _frames = 0;
  int _janky = 0;
  Duration _budget = const Duration(microseconds: 16667);
  String _source = '';

  /// Counts the frames drawn while the page is shown, and those whose
  /// build or raster phase missed the display's frame budget.
  void _onTimings(List<ui.FrameTiming> timings) {
    for (final t in timings) {
      _frames++;
      if (t.buildDuration > _budget || t.rasterDuration > _budget) _janky++;
    }
  }

  @override
  void initState() {
    super.initState();
    _source = widget.runtime.lastRoute;
    widget.runtime.lastRoute = widget.page.route;
    SchedulerBinding.instance.addTimingsCallback(_onTimings);
    final release = _release = widget.runtime.mount();
    if (release == null) return;
    try {
      // Sections above 64 KiB are checked on a background isolate before
      // the page builds, smaller ones on first use (ADR-0029, L-6).
      final pending = release.gate.prepare([
        ...release.bundle(widget.page.plugin).container.sections,
        ...release.bundle('').container.sections,
      ]);
      if (pending != null) {
        _preparing = true;
        unawaited(
          pending.then(
            (_) {
              if (mounted) setState(() => _preparing = false);
            },
            onError: (Object e) {
              if (!mounted) return;
              setState(() {
                _preparing = false;
                _fail(
                  e is PluxException
                      ? e
                      : PluxException(PluxErrorCode.bundleMalformed, '$e'),
                );
              });
            },
          ),
        );
        return;
      }
      release.gate.check(widget.page.section);
    } on PluxException catch (e) {
      _fail(e);
    }
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final hz = View.maybeOf(context)?.display.refreshRate ?? 60;
    _budget = Duration(microseconds: (1e6 / (hz > 0 ? hz : 60)).round());
  }

  @override
  void dispose() {
    SchedulerBinding.instance.removeTimingsCallback(_onTimings);
    _recordView();
    final r = _release;
    if (r != null) widget.runtime.unmount(r);
    super.dispose();
  }

  /// Records how long the page was shown and, once it drew a frame, how
  /// it performed (ANL-001, RT-015). Only routes and numbers: never the
  /// page's parameters or state.
  void _recordView() {
    final t = widget.runtime.telemetry;
    final route = widget.page.route;
    t.record(
      'screen_view',
      route: route,
      pluginKey: widget.page.plugin,
      fields: {
        'source_route': _source,
        'duration_ms': _sinceMount.elapsedMilliseconds,
      },
    );
    final first = _firstFrameMs;
    if (first == null || _failure != null) return;
    int? nodes;
    try {
      nodes = fbs.Page(widget.page.section.data).nodes?.length;
    } on Object {
      nodes = null;
    }
    t.record(
      'render_perf',
      route: route,
      pluginKey: widget.page.plugin,
      fields: {
        'build_ms': ?_buildMs,
        'first_frame_ms': first,
        'frames': _frames,
        'janky_frame_pct': _frames == 0
            ? 0.0
            : double.parse((100 * _janky / _frames).toStringAsFixed(1)),
        'node_count': ?nodes,
      },
    );
  }

  void _fail(PluxException e) {
    _failure = e;
    widget.runtime.failure(e).ignore();
  }

  @override
  Widget build(BuildContext context) {
    final release = _release;
    final renderer = widget.runtime.renderer;
    if (_failure != null) return widget.fallback(_failure!);
    if (_preparing) return const SizedBox.shrink();
    if (release == null || renderer == null) {
      return widget.fallback(
        const PluxException(PluxErrorCode.syncFailed, 'no release to render'),
      );
    }
    final task = developer.TimelineTask()
      ..start('plux.page.build', arguments: {'route': widget.page.route});
    final built = Stopwatch()..start();
    Widget child;
    try {
      child = renderer.build(
        context,
        release,
        widget.page,
        widget.params,
        routed: widget.routed,
      );
    } on Object catch (e, stack) {
      final error = e is PluxException
          ? e
          : PluxException(
              PluxErrorCode.nodeBuildFailed,
              'page ${widget.page.route}: $e',
              details: {'route': widget.page.route},
            );
      developer.log('$error', name: 'plux', error: e, stackTrace: stack);
      _fail(error);
      child = widget.fallback(error);
    } finally {
      task.finish();
      _buildMs ??= built.elapsedMilliseconds;
    }
    if (!_reportedFrame) {
      _reportedFrame = true;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        _firstFrameMs = _sinceMount.elapsedMilliseconds;
        developer.Timeline.instantSync(
          'plux.page.firstFrame',
          arguments: {
            'route': widget.page.route,
            'ms': '${_sinceMount.elapsedMilliseconds}',
          },
        );
        widget.runtime.firstFrame();
      });
    }
    return child;
  }
}
