// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Action graphs as the engine runs them (ADR-0039): decoded once from the
/// bundle's actions section, with each step's inputs as readers that
/// evaluate against a run's roots when the step executes.
library;

import 'dart:math' as math;

import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/render/sections.dart';
import 'package:plux_flutter/src/render/values.dart';

/// Reads one step input against the run's roots: `params`, `page`,
/// `event`, `steps`, `user`, … Throws [BindingError] when a binding fails.
typedef InputReader = Object? Function(Map<String, Object?> roots);

/// A step's retry policy (ACT-006): how often a failed step runs again,
/// how long it waits before each attempt, and which errors it retries.
final class RetryPolicy {
  /// Creates a policy.
  const RetryPolicy({
    required this.count,
    this.backoffMs = 0,
    this.maxBackoffMs = 0,
    this.jitter = false,
    this.on = const {},
  });

  /// The error kinds retried when a policy names none: the transient ones.
  static const Set<ActionErrorKind> transient = {
    ActionErrorKind.network,
    ActionErrorKind.timeout,
  };

  /// Retries after the first attempt.
  final int count;

  /// The wait before the first retry, in milliseconds; each later wait
  /// doubles it.
  final int backoffMs;

  /// The longest wait, in milliseconds; 0 for no cap.
  final int maxBackoffMs;

  /// Whether each wait is randomised between half and all of its length,
  /// so clients that failed together do not retry together.
  final bool jitter;

  /// The error kinds retried; [transient] when empty. A cancelled step is
  /// never retried.
  final Set<ActionErrorKind> on;

  /// Whether an error of [kind] is retried.
  bool retries(ActionErrorKind kind) =>
      kind != ActionErrorKind.cancelled &&
      (on.isEmpty ? transient : on).contains(kind);

  /// The wait before retry [attempt] (1 for the first retry); [random] is a
  /// value in [0, 1) that jitter scales the wait by.
  Duration backoff(int attempt, double random) {
    var ms = backoffMs * math.pow(2, math.min(attempt - 1, 30)).toDouble();
    if (maxBackoffMs > 0) ms = math.min(ms, maxBackoffMs.toDouble());
    if (jitter) ms = ms / 2 + ms / 2 * random;
    return Duration(milliseconds: ms.round());
  }
}

/// One step of a graph.
final class GraphStep {
  /// Creates a step.
  const GraphStep({
    required this.id,
    required this.action,
    this.inputs = const {},
    this.next = -1,
    this.onSuccess = -1,
    this.onError = -1,
    this.branches = const {},
    this.retry,
    this.timeoutMs = 0,
    this.redact = const {},
  });

  /// The step's ID in its graph, which `steps.<id>` names.
  final String id;

  /// The permanent action ID.
  final int action;

  /// The inputs set, by permanent input ID.
  final Map<int, InputReader> inputs;

  /// The step after this one, or -1.
  final int next;

  /// The step after success, or -1 for [next].
  final int onSuccess;

  /// The step after a failure, or -1: the run fails.
  final int onError;

  /// The named successors, by branch name; -1 ends the run.
  final Map<String, int> branches;

  /// The step's retry policy, or null (ACT-006).
  final RetryPolicy? retry;

  /// The step's own time bound in milliseconds; 0 when it sets none.
  final int timeoutMs;

  /// The permanent IDs of the inputs that read sensitive values: traces
  /// never record them, nor the step's output (ACT-031).
  final Set<int> redact;
}

/// A graph: its steps, `steps[0]` first, and its declared output.
final class ActionGraph {
  /// Creates a graph.
  const ActionGraph({
    required this.id,
    required this.steps,
    this.output,
    this.key = '',
    this.exported = false,
  });

  /// The graph's UUID, for reports and telemetry.
  final String id;

  /// The steps.
  final List<GraphStep> steps;

  /// The type expression of the graph's output, or null.
  final String? output;

  /// The graph's key: a flow's name within its plugin (ACT-061).
  final String key;

  /// Whether other plugins may call the flow.
  final bool exported;
}

/// Decodes [graph], whose strings are in [strings]; [resolve] evaluates an
/// input value against a run's roots.
ActionGraph decodeGraph(
  fbs.Graph graph,
  StringTable strings,
  Object? Function(fbs.Value? value, Map<String, Object?> roots) resolve,
) {
  final out = graph.output;
  return ActionGraph(
    id: graph.id == null ? '' : uuidString(uuidOf(graph.id!)),
    output: out == 0 ? null : strings(out),
    key: graph.key == 0 ? '' : strings(graph.key),
    exported: graph.exported,
    steps: [
      for (final s in graph.steps ?? const <fbs.Step>[])
        GraphStep(
          id: strings(s.id),
          action: s.action,
          inputs: {
            for (final p in s.input ?? const <fbs.Prop>[])
              p.id: (roots) => resolve(p.value, roots),
          },
          next: s.next,
          onSuccess: s.onSuccess,
          onError: s.onError,
          branches: {
            for (final b in s.branches ?? const <fbs.Branch>[])
              strings(b.name): b.step,
          },
          retry: _retry(s.retry),
          timeoutMs: s.timeoutMs,
          redact: {...?s.redact},
        ),
    ],
  );
}

RetryPolicy? _retry(fbs.Retry? r) => r == null
    ? null
    : RetryPolicy(
        count: r.count,
        backoffMs: r.backoffMs,
        maxBackoffMs: r.maxBackoffMs,
        jitter: r.jitter,
        on: {
          for (final k in r.$on ?? const <fbs.ErrorKind>[])
            if (k.value < ActionErrorKind.values.length)
              ActionErrorKind.values[k.value],
        },
      );
