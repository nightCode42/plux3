// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The action engine of one owner, a page, an embedded view, a plugin or
/// the app (ADR-0039): it starts runs from triggers under each handler's
/// concurrency policy (ACT-003), cancels its runs when its owner is
/// disposed unless they are detached (ACT-004), routes the failures no
/// `onError` handled to the owner's error handlers and shows the rest
/// (ACT-020), traces every run (ACT-030, RT-015) and records its telemetry.
library;

import 'dart:async';
import 'dart:developer' as developer;

import 'package:flutter/foundation.dart';
import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/actions/clock.dart';
import 'package:plux_flutter/src/actions/graph.dart';
import 'package:plux_flutter/src/actions/handlers.dart';
import 'package:plux_flutter/src/actions/run.dart';
import 'package:plux_flutter/src/actions/trace.dart';
import 'package:plux_flutter/src/actions/triggers.dart';
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/navigation/router.dart';
import 'package:plux_flutter/src/render/sections.dart';
import 'package:plux_flutter/src/state/access.dart';

/// Records a telemetry event, as `TelemetryRecorder.record` does.
typedef RecordEvent = void Function(
  String name, {
  Map<String, Object?> fields,
  String route,
  String pluginKey,
});

/// What the runtime gives every engine: the router, `Plux.events`,
/// telemetry, the host's custom actions, the trigger hub, the trace buffer
/// and sync.
final class ActionServices {
  /// Creates the services.
  ActionServices({
    required this.router,
    required this.emit,
    required this.record,
    this.nativeActions = const NoNativeActions(),
    TriggerHub? triggers,
    this.traces,
    this.sync,
    this.clock = const ActionClock(),
  }) : triggers = triggers ?? TriggerHub();

  /// Resolves and opens routes (ADR-0040).
  final PluxRouter router;

  /// Posts a host event (HST-013).
  final void Function(String name, Map<String, Object?> payload) emit;

  /// Records telemetry.
  final RecordEvent record;

  /// The host's custom actions (ACT-060).
  final NativeActions nativeActions;

  /// Dispatches app lifecycle, push, host and data-source events to the
  /// owners whose triggers handle them (ACT-002).
  final TriggerHub triggers;

  /// Keeps the latest run traces (ACT-030), or null.
  final TraceBuffer? traces;

  /// Starts a sync now (`sync`).
  final void Function()? sync;

  /// Time for timers, waits and the timed policies.
  final ActionClock clock;
}

/// What starts a run, for telemetry and traces.
enum RunTrigger {
  /// A widget event or a native slot event.
  event,

  /// A route guard (NAV-009).
  guard,

  /// A page lifecycle event.
  lifecycle,

  /// A timer.
  timer,

  /// A state watcher.
  watch,

  /// The app resumed or paused.
  appLifecycle,

  /// A push notification was opened.
  push,

  /// A host event sent into Plux.
  hostEvent,

  /// A data source loaded or failed.
  data,

  /// An error handler (ACT-020).
  error,
}

/// The concurrency policies of ACT-003.
enum ConcurrencyPolicy {
  /// Every trigger starts a run.
  parallel,

  /// A trigger is ignored while the handler's run is in progress.
  drop,

  /// A trigger cancels the handler's run in progress and starts anew.
  restart,

  /// A trigger waits for the handler's run in progress, in order.
  queue,

  /// A run starts once triggers have stopped for the interval; the newest
  /// trigger's payload wins.
  debounce,

  /// At most one run starts per interval.
  throttle,
}

/// A handler's policy and its interval.
final class RunPolicy {
  /// Creates a policy.
  const RunPolicy(this.kind, [this.interval = Duration.zero]);

  /// Drop: the default of widget events.
  static const RunPolicy drop = RunPolicy(ConcurrencyPolicy.drop);

  /// The policy of [h]. A bundle compiled before runtime 0.3.0 encodes an
  /// absent policy as parallel; unless [honoursParallel] — the bundle lists
  /// `actions.concurrency.v1` — parallel is read as [absent], the
  /// trigger's default (ADR-0039).
  factory RunPolicy.of(
    fbs.Handler h, {
    required bool honoursParallel,
    ConcurrencyPolicy absent = ConcurrencyPolicy.drop,
  }) {
    final interval = Duration(milliseconds: h.intervalMs);
    return switch (h.concurrency) {
      fbs.Concurrency.Parallel when !honoursParallel => RunPolicy(absent),
      fbs.Concurrency.Parallel => const RunPolicy(ConcurrencyPolicy.parallel),
      fbs.Concurrency.Drop => drop,
      fbs.Concurrency.Restart => const RunPolicy(ConcurrencyPolicy.restart),
      fbs.Concurrency.Queue => const RunPolicy(ConcurrencyPolicy.queue),
      fbs.Concurrency.Debounce => RunPolicy(
        ConcurrencyPolicy.debounce,
        interval,
      ),
      fbs.Concurrency.Throttle => RunPolicy(
        ConcurrencyPolicy.throttle,
        interval,
      ),
    };
  }

  /// The policy.
  final ConcurrencyPolicy kind;

  /// The interval of debounce and throttle.
  final Duration interval;
}

/// Runs an owner's error handlers for an error no `onError` handled:
/// true when one handled it (ACT-020).
typedef ErrorRouter = Future<bool> Function(ActionError error);

/// The engine of one owner.
final class ActionHost {
  /// Creates the engine for the page at [route] of [pluginKey]; the app's
  /// engine has an empty route and plugin.
  ActionHost({
    required this.context,
    required this.limits,
    required this.report,
    required this.record,
    required this.route,
    required this.pluginKey,
    this.debug = kDebugMode,
    this.traces,
    this.errors,
    this.presenter,
    this.honoursParallel = false,
  });

  /// What handlers run with: the page's navigation, `Plux.events`, the
  /// host's custom actions, state, flows and time.
  final StepContext context;

  /// The bounds of each run.
  final ActionLimits limits;

  /// Reports a problem.
  final void Function(PluxException error) report;

  /// Records telemetry.
  final RecordEvent record;

  /// The owner's route, for reports and telemetry.
  final String route;

  /// The owner's plugin.
  final String pluginKey;

  /// Whether problems only developers act on are reported (debug builds).
  final bool debug;

  /// Keeps the traces of the runs, or null.
  final TraceBuffer? traces;

  /// The owner's error handlers, after the page's own: the plugin's, then
  /// the app's (ACT-020).
  ErrorRouter? errors;

  /// Shows an error no handler handled: the themed fallback message.
  final void Function(ActionError error)? presenter;

  /// Whether the owner's bundle lists `actions.concurrency.v1`.
  final bool honoursParallel;

  final Map<String, Set<ActionRun>> _active = {};
  final Set<ActionRun> _runs = {};
  final Map<String, List<Completer<bool>>> _queues = {};
  final Map<String, int> _reserved = {};
  final Map<String, (Timer, Completer<RunResult?>)> _debounced = {};
  final Map<String, Duration> _lastStart = {};
  final Set<String> _warned = {};
  final Map<(BundleView, UuidKey), ActionGraph> _graphs = {};
  bool _disposed = false;

  /// Runs in progress that are cancelled with the owner.
  int get running => _runs.length;

  /// Whether the owner is disposed.
  bool get disposed => _disposed;

  /// Fires [handler] of the node at [path], whose graph is in [bundle];
  /// [resolve] evaluates an input value against a run's roots and [roots]
  /// gives the node's roots (ADR-0039). [absent] is the trigger's default
  /// policy for bundles before runtime 0.3.0.
  void fire({
    required fbs.Handler handler,
    required BundleView bundle,
    required String path,
    required Map<String, Object?> Function() roots,
    required Object? Function(fbs.Value? value, Map<String, Object?> roots)
    resolve,
    Object? payload,
    RunTrigger trigger = RunTrigger.event,
    ConcurrencyPolicy absent = ConcurrencyPolicy.drop,
    String? key,
    bool errorHandler = false,
    StateAccess? state,
  }) {
    unawaited(
      fireFor(
        handler: handler,
        bundle: bundle,
        path: path,
        roots: roots,
        resolve: resolve,
        payload: payload,
        trigger: trigger,
        absent: absent,
        key: key,
        errorHandler: errorHandler,
        state: state,
      ),
    );
  }

  /// [fire], completing with the run's result: null when the trigger
  /// started no run.
  Future<RunResult?> fireFor({
    required fbs.Handler handler,
    required BundleView bundle,
    required String path,
    required Map<String, Object?> Function() roots,
    required Object? Function(fbs.Value? value, Map<String, Object?> roots)
    resolve,
    Object? payload,
    RunTrigger trigger = RunTrigger.event,
    ConcurrencyPolicy absent = ConcurrencyPolicy.drop,
    String? key,
    bool errorHandler = false,
    StateAccess? state,
  }) async {
    if (_disposed) return null;
    final graph = this.graph(handler.graph, bundle, path, resolve);
    if (graph == null) return null;
    return start(
      graph,
      roots: roots,
      event: payload,
      key: key ?? '$path#${handler.event}',
      path: path,
      trigger: trigger,
      policy: RunPolicy.of(
        handler,
        honoursParallel: honoursParallel,
        absent: absent,
      ),
      detached: handler.detached,
      errorHandler: errorHandler,
      state: state,
    );
  }

  /// The graph [id] of [bundle], decoded once; null, reported, when the
  /// bundle does not hold it.
  ActionGraph? graph(
    fbs.Uuid? id,
    BundleView bundle,
    String path,
    Object? Function(fbs.Value? value, Map<String, Object?> roots) resolve,
  ) {
    if (id == null) return null;
    try {
      return _graphs.putIfAbsent((bundle, uuidOf(id)), () {
        final g = bundle.graph(uuidOf(id));
        if (g == null) {
          throw PluxException(
            PluxErrorCode.bundleMalformed,
            'node $path names a graph its bundle does not hold',
          );
        }
        return decodeGraph(g, bundle.string, resolve);
      });
    } on PluxException catch (e) {
      report(
        PluxException(
          e.code,
          e.message,
          details: {
            ...e.details,
            'node': path,
            'route': route,
            'plugin': pluginKey,
          },
        ),
      );
      return null;
    }
  }

  /// Starts a run of [graph] for the trigger [key] under [policy]
  /// (ACT-003); completes with its result, or null when the policy
  /// started no run. A [detached] run is not cancelled with the owner
  /// (ACT-004). An [errorHandler] run's own failure is not routed again.
  Future<RunResult?> start(
    ActionGraph graph, {
    required Map<String, Object?> Function() roots,
    required String key,
    String path = '',
    Object? event,
    RunTrigger trigger = RunTrigger.event,
    RunPolicy policy = RunPolicy.drop,
    bool detached = false,
    bool errorHandler = false,
    StateAccess? state,
  }) async {
    if (_disposed) return null;
    Future<RunResult?> launch() => _launch(
      graph,
      roots: roots,
      key: key,
      path: path,
      event: event,
      trigger: trigger,
      detached: detached,
      errorHandler: errorHandler,
      state: state,
    );
    switch (policy.kind) {
      case ConcurrencyPolicy.parallel:
        break;
      case ConcurrencyPolicy.drop:
        if (_busy(key)) return null;
      case ConcurrencyPolicy.restart:
        _cancel(key);
      case ConcurrencyPolicy.queue:
        if (_busy(key)) {
          final queue = _queues.putIfAbsent(key, () => []);
          if (queue.length >= limits.queueLength) {
            _warn(
              PluxErrorCode.actionQueueFull,
              'queue/$key',
              'handler $key: its queue holds ${limits.queueLength} triggers',
              always: true,
            );
            return null;
          }
          final turn = Completer<bool>();
          queue.add(turn);
          if (!await turn.future) return null;
          _reserved[key] = (_reserved[key] ?? 1) - 1;
        }
      case ConcurrencyPolicy.debounce:
        final previous = _debounced.remove(key);
        if (previous != null) {
          previous.$1.cancel();
          previous.$2.complete(null);
        }
        final done = Completer<RunResult?>();
        final timer = context.clock.timer(policy.interval, () {
          _debounced.remove(key);
          if (_disposed) return done.complete(null);
          _cancel(key);
          done.complete(launch());
        });
        _debounced[key] = (timer, done);
        return done.future;
      case ConcurrencyPolicy.throttle:
        final now = context.clock.now;
        final last = _lastStart[key];
        if (last != null && now - last < policy.interval) return null;
        _lastStart[key] = now;
    }
    if (_disposed) return null;
    return launch();
  }

  bool _busy(String key) =>
      (_active[key]?.isNotEmpty ?? false) || (_reserved[key] ?? 0) > 0;

  /// Cancels the runs of [key] in progress (restart).
  void _cancel(String key) {
    for (final r in [...?_active[key]]) {
      r.cancel();
    }
  }

  /// Lets the next queued trigger of [key] start.
  void _next(String key) {
    final queue = _queues[key];
    if (queue == null || queue.isEmpty) return;
    _reserved[key] = (_reserved[key] ?? 0) + 1;
    queue.removeAt(0).complete(true);
  }

  Future<RunResult?> _launch(
    ActionGraph graph, {
    required Map<String, Object?> Function() roots,
    required String key,
    required String path,
    required Object? event,
    required RunTrigger trigger,
    required bool detached,
    required bool errorHandler,
    required StateAccess? state,
  }) async {
    final buffer = traces;
    final tracer = buffer == null
        ? null
        : RunTracer(maxSteps: limits.traceSteps, values: buffer.values);
    final run = ActionRun(
      graph: graph,
      roots: roots,
      context: state == null ? context : context.withState(state),
      limits: limits,
      event: event,
      tracer: tracer,
    );
    (_active[key] ??= {}).add(run);
    if (!detached) _runs.add(run);
    final task = developer.TimelineTask()
      ..start(
        'plux.action',
        arguments: {'graph': graph.id, 'trigger': trigger.name},
      );
    final startedAt = DateTime.now();
    final watch = Stopwatch()..start();
    final RunResult result;
    try {
      result = await run.execute();
    } finally {
      _active[key]?.remove(run);
      _runs.remove(run);
      _next(key);
    }
    watch.stop();
    task.finish(
      arguments: {'result': result.outcome.name, 'steps': result.steps},
    );
    final error = result.error;
    if (error != null) {
      report(
        error.toException({
          'graph': graph.id,
          'step': ?result.failingStep,
          'node': path,
          'route': route,
          'plugin': pluginKey,
        }),
      );
    }
    record(
      'action_run',
      route: route,
      pluginKey: pluginKey,
      fields: {
        'graph_id': graph.id,
        'trigger': trigger.name,
        'duration_ms': watch.elapsedMilliseconds,
        'result': result.outcome.name,
        'steps': result.steps,
        'failing_step': ?result.failingStep,
      },
    );
    if (buffer != null && tracer != null) {
      buffer.add(
        PluxActionTrace(
          runId: buffer.nextRunId(),
          graphId: graph.id,
          trigger: trigger.name,
          route: route,
          plugin: pluginKey,
          startedAt: startedAt,
          duration: watch.elapsed,
          status: switch (result.outcome) {
            RunOutcome.ok => PluxTraceStatus.ok,
            RunOutcome.failed => PluxTraceStatus.failed,
            RunOutcome.cancelled => PluxTraceStatus.cancelled,
          },
          steps: List.unmodifiable(tracer.steps),
          stepsExecuted: result.steps,
          failingStep: result.failingStep,
          errorKind: error?.kind.name,
          errorCode: error?.code.id,
        ),
      );
    }
    if (error != null && !errorHandler && trigger != RunTrigger.guard) {
      await _route(error);
    }
    return result;
  }

  /// Routes an unhandled error to the owner's error handlers, and shows
  /// the fallback message when none handled it (ACT-020).
  Future<void> _route(ActionError error) async {
    var handled = false;
    final router = errors;
    if (router != null) {
      try {
        handled = await router(error);
      } on Object catch (e) {
        report(
          PluxException(
            PluxErrorCode.errorHandlerFailed,
            'routing ${error.code.id}: ${e.runtimeType}',
            details: {'route': route, 'plugin': pluginKey},
          ),
        );
      }
    }
    if (!handled) presenter?.call(error);
  }

  /// Cancels every run that is not detached (ACT-004); later triggers are
  /// ignored, and queued or debounced ones are dropped.
  void dispose() {
    _disposed = true;
    for (final r in [..._runs]) {
      r.cancel();
    }
    for (final q in _queues.values) {
      for (final turn in q) {
        turn.complete(false);
      }
    }
    _queues.clear();
    for (final (timer, done) in _debounced.values) {
      timer.cancel();
      done.complete(null);
    }
    _debounced.clear();
  }

  void _warn(
    PluxErrorCode code,
    String key,
    String what, {
    bool always = false,
  }) {
    if (!(debug || always) || !_warned.add(key)) return;
    report(
      PluxException(
        code,
        what,
        details: {'node': key, 'route': route, 'plugin': pluginKey},
      ),
    );
  }
}

/// An [ActionError] for a refused navigation (PLX-4102).
ActionError navigationRefused(String message) => ActionError(
  ActionErrorKind.custom,
  PluxErrorCode.navigationRefused,
  message,
);

/// The runtime's built-in messages for an error no handler handled, until
/// P8's localisation (ACT-020).
String fallbackMessage(ActionError error) => switch (error.kind) {
  ActionErrorKind.network => 'Check your connection and try again.',
  ActionErrorKind.timeout => 'This is taking too long. Please try again.',
  ActionErrorKind.permission => 'Permission is needed to do this.',
  _ => 'Something went wrong. Please try again.',
};
