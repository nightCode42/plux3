// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// One run of an action graph (ADR-0039): steps in graph order, inputs
/// evaluated by PXL over the run's roots, outputs recorded for later steps,
/// failures routed to `onError`, bounded by the limits registry, and
/// cancelled with its owner. Errors never escape a run (RT-021).
library;

import 'dart:async';
import 'dart:math' as math;

import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/actions/graph.dart';
import 'package:plux_flutter/src/actions/handlers.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/render/values.dart';
import 'package:plux_flutter/src/schema/limits.g.dart';
import 'package:plux_flutter/src/schema/registry.g.dart';
import 'package:plux_flutter/src/state/access.dart';

/// The bounds of one run (ACT-005, LIM-001).
final class ActionLimits {
  /// Creates bounds.
  const ActionLimits({
    required this.stepsPerRun,
    required this.stepTimeout,
    required this.runTimeout,
  });

  /// The bounds the app bundle carries, else the registry defaults.
  factory ActionLimits.of(Map<String, int> limits) {
    int of(PluxLimit l) => limits[l.key] ?? l.defaultValue;
    return ActionLimits(
      stepsPerRun: of(PluxLimit.actionStepsPerRun),
      stepTimeout: Duration(milliseconds: of(PluxLimit.actionStepTimeout)),
      runTimeout: Duration(milliseconds: of(PluxLimit.actionRunTimeout)),
    );
  }

  /// Steps one run may execute.
  final int stepsPerRun;

  /// Time one step may take.
  final Duration stepTimeout;

  /// Time one run may take, not counting waits for the user.
  final Duration runTimeout;
}

/// How a run ended.
enum RunOutcome {
  /// Every step it reached succeeded, or a failure was handled.
  ok,

  /// A failure no `onError` handled.
  failed,

  /// Its owner was disposed.
  cancelled,
}

/// The end of a run.
final class RunResult {
  /// Creates a result.
  const RunResult(
    this.outcome, {
    this.result,
    this.error,
    this.failingStep,
    required this.steps,
  });

  /// How it ended.
  final RunOutcome outcome;

  /// The graph's result, from `stop`.
  final Object? result;

  /// The unhandled error, when it failed.
  final ActionError? error;

  /// The step whose error ended the run.
  final String? failingStep;

  /// Steps executed.
  final int steps;
}

/// One run of [graph].
final class ActionRun {
  /// Creates a run. [roots] gives the owner's PXL roots when a step's
  /// inputs are read; [event] is the trigger's payload.
  ActionRun({
    required this.graph,
    required this.roots,
    required this.context,
    required this.limits,
    this.event,
  });

  /// The graph.
  final ActionGraph graph;

  /// The owner's roots: `params`, `page`, `user`, `flags`, …
  final Map<String, Object?> Function() roots;

  /// What handlers run with.
  final StepContext context;

  /// The bounds.
  final ActionLimits limits;

  /// The trigger's payload, read as `event`.
  final Object? event;

  final Completer<void> _cancel = Completer();

  /// Whether the run was cancelled.
  bool get cancelled => _cancel.isCompleted;

  /// Cancels the run: its pending step is abandoned and nothing after it
  /// runs (ACT-004).
  void cancel() {
    if (!_cancel.isCompleted) _cancel.complete();
  }

  late StepContext _ctx = context;
  RunVariables? _vars;

  /// Executes the run. It never throws: every failure is in the result.
  /// The run's variables (STA-001) live until it ends.
  Future<RunResult> execute() async {
    RunVariables? vars;
    try {
      vars = _vars = context.state?.openRun(graph.state);
    } on Object catch (e) {
      return _failed(
        ActionError(
          ActionErrorKind.custom,
          PluxErrorCode.stateWriteRefused,
          'the run variables cannot be opened: $e',
        ),
        null,
        0,
      );
    }
    if (vars != null) _ctx = context.withState(vars.access);
    try {
      return await _execute();
    } finally {
      vars?.close();
    }
  }

  Future<RunResult> _execute() async {
    final steps = <String, Object?>{
      for (final s in graph.steps) s.id: _record(null, null),
    };
    // The engine's own time: waits for the user are not counted.
    final clock = Stopwatch()..start();
    var executed = 0;
    var i = graph.steps.isEmpty ? -1 : 0;
    while (i >= 0) {
      if (cancelled) return _cancelled(executed);
      if (i >= graph.steps.length) {
        return _failed(
          const ActionError(
            ActionErrorKind.custom,
            PluxErrorCode.bundleMalformed,
            'a step names a successor outside its graph',
          ),
          null,
          executed,
        );
      }
      final step = graph.steps[i];
      // Bounds end the run whatever onError says (ACT-005).
      if (++executed > limits.stepsPerRun) {
        return _failed(
          ActionError(
            ActionErrorKind.custom,
            PluxErrorCode.actionStepLimitExceeded,
            'the run executed more than ${limits.stepsPerRun} steps',
          ),
          step.id,
          executed - 1,
        );
      }
      final remaining = limits.runTimeout - clock.elapsed;
      if (remaining <= Duration.zero) {
        return _failed(_runTimeout(), step.id, executed - 1);
      }
      final descriptor = actionDescriptor(step.action);
      final handler = descriptor == null
          ? RefusingHandler('${step.action}', 'a later phase')
          : handlerFor(descriptor);
      StepResult? result;
      ActionError? error;
      try {
        final inputs = _inputs(step, descriptor, steps);
        final work = handler.run(_ctx, inputs);
        if (work is StepResult) {
          // Done synchronously: nothing to bound or cancel.
          result = work;
        } else if (handler.waitsForUser) {
          clock.stop();
          result = await _orCancel(work);
          clock.start();
        } else {
          var bound = limits.stepTimeout < remaining
              ? limits.stepTimeout
              : remaining;
          if (step.timeoutMs > 0) {
            bound = Duration(
              milliseconds: math.min(bound.inMilliseconds, step.timeoutMs),
            );
          }
          result = await _orCancel(
            work.timeout(
              bound,
              onTimeout: () => throw ActionError(
                ActionErrorKind.timeout,
                PluxErrorCode.actionTimeout,
                'step ${step.id} took longer than ${bound.inMilliseconds} ms',
              ),
            ),
          );
        }
      } on ActionError catch (e) {
        error = e;
      } on BindingError catch (e) {
        error = ActionError.validation('step ${step.id}: ${e.message}');
      } on PluxException catch (e) {
        error = ActionError(ActionErrorKind.custom, e.code, e.message);
      } on Object catch (e) {
        error = ActionError(
          ActionErrorKind.custom,
          PluxErrorCode.actionCustomError,
          'step ${step.id}: $e',
        );
      }
      if (!clock.isRunning) clock.start();
      if (cancelled) return _cancelled(executed);
      if (error != null) {
        steps[step.id] = _record(null, error.toPxl());
        if (step.onError >= 0) {
          i = step.onError;
          continue;
        }
        return _failed(error, step.id, executed);
      }
      switch (result!) {
        case StepEnd(:final result):
          return RunResult(RunOutcome.ok, result: result, steps: executed);
        case StepDone(:final output, :final branch):
          steps[step.id] = _record(output, null);
          i = branch != null
              ? step.branches[branch] ?? -1
              : step.onSuccess >= 0
              ? step.onSuccess
              : step.next;
      }
    }
    return RunResult(RunOutcome.ok, steps: executed);
  }

  /// The step's inputs by name, in PXL form.
  Map<String, Object?> _inputs(
    GraphStep step,
    ActionDescriptor? descriptor,
    Map<String, Object?> steps,
  ) {
    if (step.inputs.isEmpty) return const {};
    final names = <int, String>{
      if (descriptor != null)
        for (final e in descriptor.inputs.entries) e.value: e.key,
    };
    final vars = _vars;
    final scope = {
      ...roots(),
      'event': event,
      'steps': steps,
      if (vars != null) 'run': vars.values,
    };
    return {
      for (final e in step.inputs.entries)
        names[e.key] ?? '${e.key}': toPxl(e.value(scope)),
    };
  }

  /// Completes with the step's result, or null when the run is cancelled
  /// first.
  Future<StepResult?> _orCancel(Future<StepResult> work) {
    final done = Completer<StepResult?>();
    unawaited(
      work.then(
        (r) {
          if (!done.isCompleted) done.complete(r);
        },
        onError: (Object e, StackTrace s) {
          if (!done.isCompleted) done.completeError(e, s);
        },
      ),
    );
    _cancel.future.then((_) {
      if (!done.isCompleted) done.complete(null);
    }).ignore();
    return done.future;
  }

  ActionError _runTimeout() => ActionError(
    ActionErrorKind.timeout,
    PluxErrorCode.actionTimeout,
    'the run took longer than ${limits.runTimeout.inMilliseconds} ms',
  );

  RunResult _failed(ActionError e, String? step, int executed) => RunResult(
    RunOutcome.failed,
    error: e,
    failingStep: step,
    steps: executed,
  );

  RunResult _cancelled(int executed) =>
      RunResult(RunOutcome.cancelled, steps: executed);

  static Map<String, Object?> _record(Object? output, Object? error) => {
    'output': output,
    'error': error,
  };
}
