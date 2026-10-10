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
import 'package:plux_flutter/src/bundle/container.dart';
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/core/active_release.dart';
import 'package:plux_flutter/src/core/fallback.dart';
import 'package:plux_flutter/src/core/runtime.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/navigation/guards.dart';
import 'package:plux_flutter/src/state/providers.dart';

/// An event a [PluxView] passes to its host (ADR-0023): an inline page's
/// `pop`, named `pop`, with its checked result as [payload].
final class PluxViewEvent {
  /// Creates an event.
  const PluxViewEvent(this.name, this.payload);

  /// The event's name: `pop`, or a component's declared event (`on…`).
  final String name;

  /// The payload in the JSON form of its declared type, or null.
  final Object? payload;

  @override
  String toString() => 'PluxViewEvent($name, $payload)';
}

/// How a [PluxView] sizes itself (NAV-004, ADR-0023).
final class PluxViewSizing {
  const PluxViewSizing._(this._mode, [this.size]);

  /// The host gives the view [size].
  const PluxViewSizing.fixed(Size size) : this._(_SizingMode.fixed, size);

  /// The view sizes to its content within the incoming constraints.
  static const intrinsic = PluxViewSizing._(_SizingMode.intrinsic);

  /// The view fills the incoming constraints, which must be bounded; in
  /// unbounded ones it shows its fallback.
  static const expand = PluxViewSizing._(_SizingMode.expand);

  final _SizingMode _mode;

  /// The size a fixed view takes; null otherwise.
  final Size? size;
}

enum _SizingMode { intrinsic, fixed, expand }

/// Shows a Plux page or an exported component, by name, inside any widget
/// tree, never naming a plugin (NAV-004, ADR-0023).
final class PluxView extends ConsumerWidget {
  /// Creates a view of [name]: a route, else an exported component's key.
  /// [inputs] are the page's parameters or the component's props, checked
  /// on entry (PLX-4101).
  const PluxView(
    this.name, {
    super.key,
    this.inputs = const {},
    this.onEvent,
    this.sizing = PluxViewSizing.intrinsic,
    this.loadingBuilder,
    this.fallbackBuilder,
  }) : _routed = false,
       _guarded = false;

  /// A view whose page owns its route, so that its `pop` steps pop it; only
  /// the routes Plux builds are (ADR-0040). [guarded] when the route's
  /// guards already decided the entry.
  const PluxView._routed(
    this.name, {
    this.inputs = const {},
    this._guarded = false,
  }) : loadingBuilder = null,
       fallbackBuilder = null,
       onEvent = null,
       sizing = PluxViewSizing.intrinsic,
       _routed = true;

  /// The page a view's guards entered: its target, decided already.
  const PluxView._entered(
    this.name, {
    required this.inputs,
    required this.onEvent,
    required this.sizing,
    required this.loadingBuilder,
    required this.fallbackBuilder,
    required this._routed,
  }) : _guarded = true;

  final bool _routed;

  /// Whether the route's guards already decided this entry (NAV-009).
  final bool _guarded;

  /// The route name (SCH-025), or the key of an exported component.
  final String name;

  /// The page parameters or the component props.
  final Map<String, Object?> inputs;

  /// Receives the view's events: an inline page's `pop` (ADR-0023). Without
  /// it, an inline page's `pop` step fails, as it has no route to pop.
  final void Function(PluxViewEvent event)? onEvent;

  /// How the view sizes itself.
  final PluxViewSizing sizing;

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
      return _sized(
        rt,
        null,
        loadingBuilder?.call(context) ?? const SizedBox.shrink(),
      );
    }
    Widget fallback(PluxException e, [String? plugin]) =>
        fallbackBuilder?.call(context, e) ??
        buildFallback(context, rt.config, e, plugin: plugin);
    return _sized(rt, fallback, _content(rt, release, fallback));
  }

  /// The content: the page or component [name] resolves to, its guards'
  /// gate, or a fallback.
  Widget _content(
    PluxRuntime rt,
    ActiveRelease release,
    Widget Function(PluxException e, [String? plugin]) fallback,
  ) {
    PageRef? page;
    try {
      // A name is a route first, then an exported component; the
      // routes Plux builds are routes only (ADR-0023).
      page = release.page(name) ?? (_routed ? null : release.component(name));
    } on PluxException catch (e) {
      return fallback(e);
    }
    if (page == null) {
      final missing = PluxException(
        PluxErrorCode.routeNotFound,
        'no page has the route $name, and no component that key',
        details: {'route': name},
      );
      rt.reportProblem(missing);
      return fallback(missing);
    }
    final component = page.section.kind == SectionKind.component;
    var shown = page;
    if (release.disabled(page.plugin)) {
      final message = release.control.message;
      final off = PluxException(
        PluxErrorCode.pluginDisabled,
        message.isEmpty ? 'plugin ${page.plugin} is switched off' : message,
        details: {'plugin': page.plugin, 'reason': 'killSwitch'},
      );
      // The plugin's own fallback page, unless the whole app is switched
      // off or the view shows a component; otherwise the app-level
      // fallback (RT-022).
      final PageRef? declared;
      try {
        declared = release.control.appKillSwitch || component
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
          needs.assurance > rt.assurance.value) {
        return fallback(off, page.plugin);
      }
      shown = declared;
    } else if (!component &&
        !_guarded &&
        RouteGuards.needsDecision(release, page, rt.assurance.value)) {
      // An entry nothing decided yet: an embedded view, a declarative page
      // or a shell's tab runs the route's guards here (NAV-009).
      return _GuardGate(
        key: ValueKey((release.sequence, name)),
        view: this,
        guards: rt.guards,
        fallback: fallback,
      );
    }
    final events = onEvent;
    final host = PluxPageHost(
      key: ValueKey((release.sequence, name, shown.pageKey)),
      runtime: rt,
      page: shown,
      params: inputs,
      routed: _routed,
      onPop: _routed || events == null
          ? null
          : (result) => events(PluxViewEvent('pop', result)),
      onEvent: events == null
          ? null
          : (name, payload) => events(PluxViewEvent(name, payload)),
      fallback: (e) => fallback(e, shown.plugin),
    );
    // A page that asks for an assurance level is checked again whenever
    // the level changes: a page already shown that now asks for more is
    // replaced by its fallback (SEC-007, NAV-009).
    final asks = component
        ? null
        : RouteGuards.requirementsNow(release, shown)?.assurance;
    if (asks == null || asks == 0) return host;
    return _AssuranceGate(
      key: ValueKey((release.sequence, name, 'assurance')),
      runtime: rt,
      route: name,
      plugin: shown.plugin,
      required: asks,
      fallback: fallback,
      child: host,
    );
  }

  /// Applies [sizing] to [child]; an expanding view in unbounded
  /// constraints shows the fallback instead, reported once.
  Widget _sized(
    PluxRuntime? rt,
    Widget Function(PluxException e, [String? plugin])? fallback,
    Widget child,
  ) => switch (sizing._mode) {
    _SizingMode.intrinsic => child,
    _SizingMode.fixed => SizedBox.fromSize(size: sizing.size, child: child),
    _SizingMode.expand => LayoutBuilder(
      builder: (context, constraints) {
        if (constraints.hasBoundedWidth && constraints.hasBoundedHeight) {
          return SizedBox.expand(child: child);
        }
        final e = PluxException(
          PluxErrorCode.nodeBuildFailed,
          'PluxView $name expands, but its constraints are unbounded',
          details: {'route': name},
        );
        rt?.reportProblem(e);
        return SizedBox(
          width: constraints.hasBoundedWidth ? constraints.maxWidth : null,
          height: constraints.hasBoundedHeight ? constraints.maxHeight : null,
          child: fallback?.call(e) ?? const SizedBox.shrink(),
        );
      },
    ),
  };
}

/// The content of a route Plux built: a [PluxView] of [route] whose page
/// owns its route, so that its `pop` steps pop it, [guarded] when its
/// guards already decided the entry (ADR-0040).
PluxView routedPluxView(
  String route,
  Map<String, Object?> params, {
  bool guarded = false,
}) => PluxView._routed(route, inputs: params, guarded: guarded);

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
      widget.guards.decide(v.name, v.inputs).then((verdict) {
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
        inputs: params,
        onEvent: v.onEvent,
        sizing: PluxViewSizing.intrinsic,
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

/// Shows [child] while the device's assurance level is at least [required]
/// and the fallback with `PLX-6002` while it is not, whatever the page was
/// doing when the level fell (SEC-007, NAV-009).
final class _AssuranceGate extends StatefulWidget {
  const _AssuranceGate({
    super.key,
    required this.runtime,
    required this.route,
    required this.plugin,
    required this.required,
    required this.fallback,
    required this.child,
  });

  final PluxRuntime runtime;
  final String route;
  final String plugin;
  final int required;
  final Widget Function(PluxException e, [String? plugin]) fallback;
  final Widget child;

  @override
  State<_AssuranceGate> createState() => _AssuranceGateState();
}

final class _AssuranceGateState extends State<_AssuranceGate> {
  bool _refused = false;

  ValueNotifier<int> get _level => widget.runtime.assurance;

  PluxException _error() => PluxException(
    PluxErrorCode.assuranceInsufficient,
    'route ${widget.route} requires assurance AL${widget.required}, and this '
    'device has AL${_level.value} (SEC-007)',
    details: {'route': widget.route, 'plugin': widget.plugin},
  );

  @override
  void initState() {
    super.initState();
    _refused = _level.value < widget.required;
    if (_refused) widget.runtime.reportProblem(_error());
    _level.addListener(_changed);
  }

  void _changed() {
    final refused = _level.value < widget.required;
    if (refused == _refused) return;
    if (refused) widget.runtime.reportProblem(_error());
    setState(() => _refused = refused);
  }

  @override
  void dispose() {
    _level.removeListener(_changed);
    super.dispose();
  }

  @override
  Widget build(BuildContext context) =>
      _refused ? widget.fallback(_error(), widget.plugin) : widget.child;
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
    this.onPop,
    this.onEvent,
  });

  /// Whether the page owns its route.
  final bool routed;

  /// Receives an embedded page's `pop` result, or null (ADR-0023).
  final void Function(Object? result)? onPop;

  /// Receives a shown component's events (SCH-030), or null.
  final void Function(String event, Object? payload)? onEvent;

  /// Whether the host shows an exported component, which is no screen: no
  /// `screen_view` or `render_perf` is recorded for it.
  bool get _component => page.section.kind == SectionKind.component;

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
    if (!widget._component) widget.runtime.lastRoute = widget.page.route;
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
    if (widget._component) return;
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
        onPop: widget.onPop,
        onEvent: widget.onEvent,
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
