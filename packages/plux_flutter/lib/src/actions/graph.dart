// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Action graphs as the engine runs them (ADR-0039): decoded once from the
/// bundle's actions section, with each step's inputs as readers that
/// evaluate against a run's roots when the step executes.
library;

import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/render/sections.dart';
import 'package:plux_flutter/src/render/values.dart';

/// Reads one step input against the run's roots: `params`, `page`,
/// `event`, `steps`, `user`, … Throws [BindingError] when a binding fails.
typedef InputReader = Object? Function(Map<String, Object?> roots);

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
    this.retry = false,
    this.timeoutMs = 0,
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

  /// Whether the step declares a retry policy (P5, `ACT-006`).
  final bool retry;

  /// The step's own time bound in milliseconds; 0 when it sets none.
  final int timeoutMs;
}

/// A graph: its steps, `steps[0]` first, and its declared output.
final class ActionGraph {
  /// Creates a graph.
  const ActionGraph({
    required this.id,
    required this.steps,
    this.output,
    this.state = const [],
  });

  /// The graph's UUID, for reports and telemetry.
  final String id;

  /// The steps.
  final List<GraphStep> steps;

  /// The type expression of the graph's output, or null.
  final String? output;

  /// The run's variables (STA-001).
  final List<fbs.StateEntry> state;
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
    state: graph.state ?? const [],
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
          retry: s.retry != null,
          timeoutMs: s.timeoutMs,
        ),
    ],
  );
}
