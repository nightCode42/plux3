// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// One run of an action graph (ADR-0039): steps in graph order, inputs
/// evaluated by PXL over the run's roots, outputs recorded for later steps,
/// failures routed to `onError` after the step's retries (ACT-006),
/// optimistic updates rolled back with a failed step (ACT-007), `forEach`,
/// `parallel` and `callFlow` run as parts of the same run, bounded by the
/// limits registry (ACT-005) and cancelled with its owner (ACT-004).
/// Errors never escape a run (RT-021).
library;

import 'dart:async';
import 'dart:math' as math;

import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/actions/control.dart';
import 'package:plux_flutter/src/actions/graph.dart';
import 'package:plux_flutter/src/actions/handlers.dart';
import 'package:plux_flutter/src/actions/trace.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/render/values.dart';
import 'package:plux_flutter/src/schema/limit_values.dart';
import 'package:plux_flutter/src/schema/limits.g.dart';
import 'package:plux_flutter/src/schema/registry.g.dart';
import 'package:plux_flutter/src/state/access.dart';

/// The bounds of runs (ACT-005, ACT-003, ACT-030, LIM-001).
final class ActionLimits {
  /// Creates bounds; the bounds not given are the registry's defaults.
  const ActionLimits({
    required this.stepsPerRun,
    required this.stepTimeout,
    required this.runTimeout,
    this.forEachItemsLimit,
    this.queueLengthLimit,
    this.traceStepsLimit,
  });

  /// The bounds the app bundle carries, else the registry defaults.
  factory ActionLimits.of(Map<String, int> limits) {
    int of(PluxLimit l) => limits.valueOf(l);
    return ActionLimits(
      stepsPerRun: of(PluxLimit.actionStepsPerRun),
      stepTimeout: Duration(milliseconds: of(PluxLimit.actionStepTimeout)),
      runTimeout: Duration(milliseconds: of(PluxLimit.actionRunTimeout)),
      forEachItemsLimit: of(PluxLimit.actionForEachItems),
      queueLengthLimit: of(PluxLimit.actionQueueLength),
      traceStepsLimit: of(PluxLimit.actionTraceSteps),
    );
  }

  /// Steps one run may execute, the steps of its flows and loops and its
  /// retries included.
  final int stepsPerRun;

  /// Time one step may take.
  final Duration stepTimeout;

  /// Time one run may take, not counting waits for the user.
  final Duration runTimeout;

  /// `action.forEachItems`, or null for the registry default.
  final int? forEachItemsLimit;

  /// `action.queueLength`, or null for the registry default.
  final int? queueLengthLimit;

  /// `action.traceSteps`, or null for the registry default.
  final int? traceStepsLimit;

  /// Items one `forEach` step may iterate over.
  int get forEachItems =>
      forEachItemsLimit ?? PluxLimit.actionForEachItems.defaultValue;

  /// Triggers a queued handler may hold.
  int get queueLength =>
      queueLengthLimit ?? PluxLimit.actionQueueLength.defaultValue;

  /// Steps one run's trace records.
  int get traceSteps =>
      traceStepsLimit ?? PluxLimit.actionTraceSteps.defaultValue;
}

/// How a run ended.
enum RunOutcome {
  /// Every step it reached succeeded, or a failure was handled.
  ok,

  /// A failure no `onError` handled.
  failed,

  /// Its owner was disposed, or a newer run replaced it.
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

/// A cancellation that a parent cancels too: a run's, or one lane's of a
/// `parallel` step.
final class _Token {
  _Token([this._parent]);

  final _Token? _parent;
  final Completer<void> _done = Completer();

  bool get cancelled => _done.isCompleted || (_parent?.cancelled ?? false);

  void cancel() {
    if (!_done.isCompleted) _done.complete();
  }

  Future<void> get onCancel {
    final p = _parent;
    return p == null ? _done.future : Future.any([_done.future, p.onCancel]);
  }
}

/// Where a path of steps runs: the graph, its roots and step records, and
/// the `item` and `index` of the loops around it.
final class _Frame {
  _Frame(this.graph, this.roots, this.event, this.extra)
    : steps = {
        for (final s in graph.steps) s.id: ActionRun._record(null, null),
      };

  _Frame.loop(_Frame f, Object? item, int index)
    : graph = f.graph,
      roots = f.roots,
      event = f.event,
      steps = f.steps,
      extra = {...f.extra, 'item': item, 'index': index};

  final ActionGraph graph;
  final Map<String, Object?> Function() roots;
  final Object? event;
  final Map<String, Object?> steps;
  final Map<String, Object?> extra;
}

/// How a path of steps ended.
sealed class _End {
  const _End();
}

final class _Done extends _End {
  const _Done();
}

final class _Stopped extends _End {
  const _Stopped(this.result);
  final Object? result;
}

final class _Failed extends _End {
  const _Failed(this.error, this.step, {this.fatal = false});
  final ActionError error;
  final String? step;

  /// A bound: it ends the run whatever `onError` says (ACT-005).
  final bool fatal;
}

final class _Cancelled extends _End {
  const _Cancelled();
}

/// Thrown by a structural step to end the whole run with [end].
final class _Unwind implements Exception {
  const _Unwind(this.end);
  final _End end;
}

/// One run of [graph].
final class ActionRun {
  /// Creates a run. [roots] gives the owner's PXL roots when a step's
  /// inputs are read; [event] is the trigger's payload; [tracer], when
  /// given, records the steps (ACT-030).
  ActionRun({
    required this.graph,
    required this.roots,
    required StepContext context,
    required this.limits,
    this.event,
    this.tracer,
  }) : scope = RunScope(context.state) {
    this.context = context.forRun(scope);
  }

  /// The graph.
  final ActionGraph graph;

  /// The owner's roots: `params`, `page`, `user`, `flags`, …
  final Map<String, Object?> Function() roots;

  /// What handlers run with, for this run.
  late final StepContext context;

  /// The run's own scope: its optimistic changes (ACT-007).
  final RunScope scope;

  /// The bounds.
  final ActionLimits limits;

  /// The trigger's payload, read as `event`.
  final Object? event;

  /// Records the steps, or null.
  final RunTracer? tracer;

  final _Token _token = _Token();
  final Stopwatch _clock = Stopwatch();
  int _executed = 0;

  /// Whether the run was cancelled.
  bool get cancelled => _token.cancelled;

  /// Cancels the run: its pending step is abandoned and nothing after it
  /// runs (ACT-004).
  void cancel() => _token.cancel();

  late StepContext _ctx = context;
  RunVariables? _vars;

  /// Executes the run. It never throws: every failure is in the result.
  /// The run's variables (STA-001) live until it ends.
  Future<RunResult> execute() async {
    RunVariables? vars;
    try {
      final state = context.state;
      vars = _vars = state is StateAccess ? state.openRun(graph.state) : null;
    } on Object catch (e) {
      return RunResult(
        RunOutcome.failed,
        error: ActionError(
          ActionErrorKind.custom,
          PluxErrorCode.stateWriteRefused,
          'the run variables cannot be opened: $e',
        ),
        steps: 0,
      );
    }
    if (vars != null) _ctx = context.withState(vars.access);
    try {
      final result = await _execute();
      scope.end(succeeded: result.outcome == RunOutcome.ok);
      return result;
    } finally {
      vars?.close();
    }
  }

  Future<RunResult> _execute() async {
    // The engine's own time: waits for the user are not counted.
    _clock.start();
    final end = graph.steps.isEmpty
        ? const _Done()
        : await _path(_Frame(graph, roots, event, const {}), 0, _token);
    _clock.stop();
    switch (end) {
      case _Done():
        scope.optimistic.commit();
        return RunResult(RunOutcome.ok, steps: _executed);
      case _Stopped(:final result):
        scope.optimistic.commit();
        return RunResult(RunOutcome.ok, result: result, steps: _executed);
      case _Failed(:final error, :final step):
        scope.optimistic.rollback();
        return RunResult(
          RunOutcome.failed,
          error: error,
          failingStep: step,
          steps: _executed,
        );
      case _Cancelled():
        scope.optimistic.rollback();
        return RunResult(RunOutcome.cancelled, steps: _executed);
    }
  }

  /// Runs the steps of [f] from [start] until a step has no successor.
  Future<_End> _path(_Frame f, int start, _Token token) async {
    final steps = f.graph.steps;
    var i = start;
    while (i >= 0) {
      if (token.cancelled) return const _Cancelled();
      if (i >= steps.length) {
        return const _Failed(
          ActionError(
            ActionErrorKind.custom,
            PluxErrorCode.bundleMalformed,
            'a step names a successor outside its graph',
          ),
          null,
          fatal: true,
        );
      }
      final step = steps[i];
      final bound = _bound(step);
      if (bound != null) return bound;
      final descriptor = actionDescriptor(step.action);
      final handler = descriptor == null
          ? RefusingHandler('${step.action}', 'a later phase')
          : handlerFor(descriptor);
      final started = tracer == null ? Duration.zero : _clock.elapsed;
      StepResult? result;
      ActionError? error;
      Map<String, Object?> inputs = const {};
      var attempts = 1;
      OptimisticLog? optimistic;
      for (;;) {
        try {
          inputs = _inputs(f, step, descriptor);
          // A device operation the plugin does not declare is blocked
          // before it reaches the device (SEC-080).
          final device = _ctx.device;
          if (device != null && descriptor != null) {
            device.guard.admit(device.plugin, descriptor.name, inputs);
          }
          optimistic ??= _optimistic(descriptor, inputs);
          final work = handler is StructuralHandler
              ? _structural(handler.action, f, step, inputs, token)
              : handler.run(
                  step.branches.isEmpty
                      ? _ctx
                      : _ctx.forStep(step.branches.keys),
                  inputs,
                );
          if (work is StepResult) {
            // Done synchronously: nothing to bound or cancel.
            result = work;
          } else if (handler.waitsForUser) {
            _clock.stop();
            result = await _orCancel(work, token);
            _clock.start();
          } else if (handler is StructuralHandler) {
            result = await work;
          } else {
            result = await _orCancel(_timed(work, step), token);
          }
          error = null;
        } on _Unwind catch (u) {
          optimistic?.rollback();
          return u.end;
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
        if (!_clock.isRunning) _clock.start();
        if (token.cancelled) {
          optimistic?.rollback();
          return const _Cancelled();
        }
        final retry = step.retry;
        if (error == null ||
            retry == null ||
            attempts > retry.count ||
            !retry.retries(error.kind)) {
          break;
        }
        // Another attempt: it waits, then counts as a step (ACT-005).
        final wait = retry.backoff(attempts, context.clock.random());
        attempts++;
        if (!await _sleep(wait, token)) return const _Cancelled();
        final bound = _bound(step);
        if (bound != null) {
          optimistic?.rollback();
          return bound;
        }
      }
      tracer?.step(
        id: step.id,
        action: descriptor?.name ?? '${step.action}',
        start: started,
        end: _clock.elapsed,
        status: error == null ? PluxTraceStatus.ok : PluxTraceStatus.failed,
        inputs: inputs,
        inputIds: descriptor?.inputs ?? const {},
        redact: step.redact,
        output: result is StepDone ? result.output : null,
        errorKind: error?.kind.name,
        errorCode: error?.code.id,
        attempts: attempts,
      );
      if (error != null) {
        optimistic?.rollback();
        f.steps[step.id] = _record(null, error.toPxl());
        if (step.onError >= 0) {
          i = step.onError;
          continue;
        }
        return _Failed(error, step.id);
      }
      optimistic?.commit();
      switch (result!) {
        case StepEnd(:final result):
          return _Stopped(result);
        case StepDone(:final output, :final branch):
          f.steps[step.id] = _record(output, null);
          i = branch != null
              ? step.branches[branch] ?? -1
              : step.onSuccess >= 0
              ? step.onSuccess
              : step.next;
      }
    }
    return const _Done();
  }

  /// Counts a step against the run's bounds; the end of the run when one
  /// is exceeded (ACT-005).
  _End? _bound(GraphStep step) {
    if (++_executed > limits.stepsPerRun) {
      _executed--;
      return _Failed(
        ActionError(
          ActionErrorKind.custom,
          PluxErrorCode.actionStepLimitExceeded,
          'the run executed more than ${limits.stepsPerRun} steps',
        ),
        step.id,
        fatal: true,
      );
    }
    if (_clock.elapsed >= limits.runTimeout) {
      _executed--;
      return _Failed(
        ActionError(
          ActionErrorKind.timeout,
          PluxErrorCode.actionTimeout,
          'the run took longer than ${limits.runTimeout.inMilliseconds} ms',
        ),
        step.id,
        fatal: true,
      );
    }
    return null;
  }

  /// Bounds a pending step by the step timeout, its own `timeoutMs` and
  /// the run's remaining time.
  Future<StepResult> _timed(Future<StepResult> work, GraphStep step) {
    final remaining = limits.runTimeout - _clock.elapsed;
    var bound = limits.stepTimeout < remaining ? limits.stepTimeout : remaining;
    if (step.timeoutMs > 0) {
      bound = Duration(
        milliseconds: math.min(bound.inMilliseconds, step.timeoutMs),
      );
    }
    return work.timeout(
      bound,
      onTimeout: () => throw ActionError(
        ActionErrorKind.timeout,
        PluxErrorCode.actionTimeout,
        'step ${step.id} took longer than ${bound.inMilliseconds} ms',
      ),
    );
  }

  /// Applies a step's `optimistic` map of state entries before it runs, as
  /// `apiCall` declares (ACT-007); null when the step has none.
  OptimisticLog? _optimistic(
    ActionDescriptor? descriptor,
    Map<String, Object?> inputs,
  ) {
    final changes = inputs['optimistic'];
    if (changes is! Map<String, Object?> || changes.isEmpty) return null;
    final log = OptimisticLog(context.state);
    try {
      for (final e in changes.entries) {
        log.apply(e.key, e.value);
      }
    } on Object {
      log.rollback();
      rethrow;
    }
    return log;
  }

  /// The step's inputs by name, in PXL form.
  Map<String, Object?> _inputs(
    _Frame f,
    GraphStep step,
    ActionDescriptor? descriptor,
  ) {
    if (step.inputs.isEmpty) return const {};
    final names = <int, String>{
      if (descriptor != null)
        for (final e in descriptor.inputs.entries) e.value: e.key,
    };
    final vars = _vars;
    final scope = {
      ...f.roots(),
      ...f.extra,
      'event': f.event,
      'steps': f.steps,
      if (vars != null) 'run': vars.values,
    };
    return {
      for (final e in step.inputs.entries)
        names[e.key] ?? '${e.key}': toPxl(e.value(scope)),
    };
  }

  /// Runs a step that runs other steps.
  Future<StepResult> _structural(
    StructuralAction action,
    _Frame f,
    GraphStep step,
    Map<String, Object?> inputs,
    _Token token,
  ) => switch (action) {
    StructuralAction.forEach => _forEach(f, step, inputs, token),
    StructuralAction.parallel => _parallel(f, step, inputs, token),
    StructuralAction.callFlow => _callFlow(inputs, token),
  };

  /// Runs the `body` branch once per item, in order (ACT-005).
  Future<StepResult> _forEach(
    _Frame f,
    GraphStep step,
    Map<String, Object?> inputs,
    _Token token,
  ) async {
    final items = inputs['items'];
    if (items is! List<Object?>) {
      throw const ActionError.validation('forEach: items is not a list');
    }
    if (items.length > limits.forEachItems) {
      throw ActionError(
        ActionErrorKind.validation,
        PluxErrorCode.actionForeachLimitExceeded,
        'forEach has ${items.length} items, above ${limits.forEachItems}',
      );
    }
    final body = step.branches['body'] ?? -1;
    if (body >= 0) {
      for (var n = 0; n < items.length; n++) {
        _settle(await _path(_Frame.loop(f, items[n], n), body, token));
      }
    }
    return const StepDone();
  }

  /// Runs the lanes concurrently and joins them; the first error cancels
  /// the others and fails the step.
  Future<StepResult> _parallel(
    _Frame f,
    GraphStep step,
    Map<String, Object?> inputs,
    _Token token,
  ) async {
    final lanes = inputs['lanes'];
    if (lanes is! List<Object?>) {
      throw const ActionError.validation('parallel: lanes is not a list');
    }
    final group = _Token(token);
    final failed = Completer<_End>();
    Future<void> lane(int start) async {
      final end = await _path(f, start, group);
      if (end is _Done || failed.isCompleted) return;
      // The first error or stop ends the lanes still running.
      group.cancel();
      failed.complete(end);
    }

    final all = Future.wait([
      for (final name in lanes)
        if (step.branches[name] case final start? when start >= 0) lane(start),
    ]);
    await Future.any([all, failed.future]);
    await all;
    if (failed.isCompleted) _settle(await failed.future);
    if (token.cancelled) throw const _Unwind(_Cancelled());
    return const StepDone();
  }

  /// Runs a flow with its typed input and returns its output (ACT-061).
  Future<StepResult> _callFlow(
    Map<String, Object?> inputs,
    _Token token,
  ) async {
    final name = inputs['flow'];
    if (name is! String) {
      throw const ActionError.validation('callFlow: flow is not a name');
    }
    final target = context.flows.flow(name);
    if (target == null) {
      throw ActionError(
        ActionErrorKind.custom,
        PluxErrorCode.flowNotFound,
        'the active release holds no flow "$name"',
      );
    }
    final input = inputs['input'];
    final params = input is Map<String, Object?>
        ? input
        : const <String, Object?>{};
    Map<String, Object?> roots() => {...target.roots(), 'params': params};
    if (target.graph.steps.isEmpty) return const StepDone();
    final end = await _path(
      _Frame(target.graph, roots, null, const {}),
      0,
      token,
    );
    return switch (end) {
      _Done() => const StepDone(),
      // A flow's stop ends the flow, with its result as the step's output.
      _Stopped(:final result) => StepDone(result),
      _ => _settle(end),
    };
  }

  /// What a structural step does with the end of a path it ran: nothing
  /// when it ran to its end; it fails with the path's error, which its own
  /// `onError` can handle; a bound, a stop or a cancellation ends the run.
  StepResult _settle(_End end) => switch (end) {
    _Done() => const StepDone(),
    _Failed(:final error, fatal: false) => throw error,
    _ => throw _Unwind(end),
  };

  /// Waits [d]; false when the run was cancelled first.
  Future<bool> _sleep(Duration d, _Token token) async {
    if (d <= Duration.zero) return !token.cancelled;
    final done = await _orCancel(
      context.clock.sleep(d).then((_) => const StepDone()),
      token,
    );
    return done != null;
  }

  /// Completes with the step's result, or null when the run is cancelled
  /// first.
  Future<StepResult?> _orCancel(Future<StepResult> work, _Token token) {
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
    token.onCancel.then((_) {
      if (!done.isCompleted) done.complete(null);
    }).ignore();
    return done.future;
  }

  static Map<String, Object?> _record(Object? output, Object? error) => {
    'output': output,
    'error': error,
  };
}
