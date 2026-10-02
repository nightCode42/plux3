// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The action engine of one owner, a page or an embedded view (ADR-0039):
/// it starts runs from triggers, keeps one run per handler at a time,
/// cancels its runs when its owner is disposed, reports the failures no
/// `onError` handled and records each run's telemetry.
library;

import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/actions/graph.dart';
import 'package:plux_flutter/src/actions/handlers.dart';
import 'package:plux_flutter/src/actions/run.dart';
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/navigation/router.dart';
import 'package:plux_flutter/src/render/sections.dart';

/// Records a telemetry event, as `TelemetryRecorder.record` does.
typedef RecordEvent = void Function(
  String name, {
  Map<String, Object?> fields,
  String route,
  String pluginKey,
});

/// What the runtime gives every engine: the router, `Plux.events`,
/// telemetry and the host's custom actions.
final class ActionServices {
  /// Creates the services.
  const ActionServices({
    required this.router,
    required this.emit,
    required this.record,
    this.nativeActions = const NoNativeActions(),
  });

  /// Resolves and opens routes (ADR-0040).
  final PluxRouter router;

  /// Posts a host event (HST-013).
  final void Function(String name, Map<String, Object?> payload) emit;

  /// Records telemetry.
  final RecordEvent record;

  /// The host's custom actions (ACT-060).
  final NativeActions nativeActions;
}

/// What starts a run, for telemetry.
enum RunTrigger {
  /// A widget event or a native slot event.
  event,

  /// A route guard (NAV-009).
  guard,

  /// A deep link or a push payload opening a route.
  link,
}

/// The engine of one page or embedded view.
final class ActionHost {
  /// Creates the engine for the page at [route] of [pluginKey].
  ActionHost({
    required this.context,
    required this.limits,
    required this.report,
    required this.record,
    required this.route,
    required this.pluginKey,
    this.debug = kDebugMode,
  });

  /// What handlers run with: the page's navigation, `Plux.events`, the
  /// host's custom actions.
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

  /// Whether options this runtime ignores are reported (debug builds).
  final bool debug;

  final Set<String> _busy = {};
  final Set<ActionRun> _runs = {};
  final Set<String> _warned = {};
  final Map<(BundleView, UuidKey), ActionGraph> _graphs = {};
  bool _disposed = false;

  /// Runs in progress.
  int get running => _runs.length;

  /// Fires [handler] of the node at [path], whose graph is in [bundle];
  /// [resolve] evaluates an input value against a run's roots and [roots]
  /// gives the node's roots (ADR-0039). A handler whose previous run is
  /// still in progress ignores the trigger (`drop`).
  void fire({
    required fbs.Handler handler,
    required BundleView bundle,
    required String path,
    required Map<String, Object?> Function() roots,
    required Object? Function(fbs.Value? value, Map<String, Object?> roots)
    resolve,
    Object? payload,
  }) {
    if (_disposed) return;
    final id = handler.graph;
    if (id == null) return;
    final key = '$path#${handler.event}';
    _unsupported(handler, key);
    final ActionGraph graph;
    try {
      graph = _graphs.putIfAbsent((bundle, uuidOf(id)), () {
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
      report(_with(e, path));
      return;
    }
    unawaited(start(graph, roots: roots, event: payload, key: key, path: path));
  }

  /// Starts a run of [graph] unless one for [key] is in progress; completes
  /// with its result, or null when the trigger was dropped.
  Future<RunResult?> start(
    ActionGraph graph, {
    required Map<String, Object?> Function() roots,
    required String key,
    String path = '',
    Object? event,
    RunTrigger trigger = RunTrigger.event,
  }) async {
    if (_disposed || !_busy.add(key)) return null;
    for (final s in graph.steps) {
      if (s.retry) _warn('$key/${s.id}', 'step ${s.id} declares a retry');
    }
    final run = ActionRun(
      graph: graph,
      roots: roots,
      context: context,
      limits: limits,
      event: event,
    );
    _runs.add(run);
    final watch = Stopwatch()..start();
    final RunResult result;
    try {
      result = await run.execute();
    } finally {
      _runs.remove(run);
      _busy.remove(key);
    }
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
    return result;
  }

  /// Cancels every run (ACT-004); later triggers are ignored.
  void dispose() {
    _disposed = true;
    for (final r in _runs) {
      r.cancel();
    }
  }

  /// Reports, once per handler in debug builds, the options P4 ignores: a
  /// concurrency policy other than drop or parallel, which is also how the
  /// compiler encodes an absent policy, and detached runs (ADR-0039). Every
  /// policy runs as drop.
  void _unsupported(fbs.Handler h, String key) {
    final policy = h.concurrency;
    if (policy != fbs.Concurrency.Parallel && policy != fbs.Concurrency.Drop) {
      _warn(key, 'its concurrency policy runs as drop in this runtime');
    }
    if (h.detached) {
      _warn('$key/detached', 'a detached run is cancelled with its page');
    }
  }

  void _warn(String key, String what) {
    if (!debug || !_warned.add(key)) return;
    report(
      PluxException(
        PluxErrorCode.actionsNotAvailable,
        'handler $key: $what',
        details: {'node': key, 'route': route, 'plugin': pluginKey},
      ),
    );
  }

  PluxException _with(PluxException e, String path) => PluxException(
    e.code,
    e.message,
    details: {...e.details, 'node': path, 'route': route, 'plugin': pluginKey},
  );
}

/// An [ActionError] for a refused navigation (PLX-4102).
ActionError navigationRefused(String message) => ActionError(
  ActionErrorKind.custom,
  PluxErrorCode.navigationRefused,
  message,
);
