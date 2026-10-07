// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Action run traces (ACT-030, ACT-031): every run records its ID, trigger,
/// steps with their start and end, status, inputs, output and errors, into
/// a ring buffer bounded by the limits registry, which `plux_devtools` and
/// tests read through `Plux.diagnostics`. Input and output values are
/// recorded only in debug builds and never when the compiler marked them
/// sensitive; everywhere else a trace holds [PluxStepTrace.redacted].
library;

import 'dart:async';

import 'package:flutter/foundation.dart';

/// How a run or a step ended.
enum PluxTraceStatus {
  /// It succeeded, or its failure was handled.
  ok,

  /// It failed.
  failed,

  /// It was cancelled.
  cancelled,
}

/// One step of a traced run.
@immutable
final class PluxStepTrace {
  /// Creates the trace of a step.
  const PluxStepTrace({
    required this.id,
    required this.action,
    required this.start,
    required this.duration,
    required this.status,
    this.attempts = 1,
    this.inputs = const {},
    this.output,
    this.errorKind,
    this.errorCode,
  });

  /// What a trace holds instead of a value it does not record.
  static const String redacted = '‹redacted›';

  /// The step's ID in its graph.
  final String id;

  /// The action's name.
  final String action;

  /// When the step started, from the start of its run.
  final Duration start;

  /// How long it took, waits for the user and retries included.
  final Duration duration;

  /// How it ended.
  final PluxTraceStatus status;

  /// How often it ran: more than once when it was retried (ACT-006).
  final int attempts;

  /// Its inputs by name; [redacted] where the value is not recorded.
  final Map<String, Object?> inputs;

  /// Its output, or [redacted].
  final Object? output;

  /// The kind of its error, such as `timeout`.
  final String? errorKind;

  /// The registered code of its error, such as `PLX-5001`.
  final String? errorCode;
}

/// One traced run.
@immutable
final class PluxActionTrace {
  /// Creates the trace of a run.
  const PluxActionTrace({
    required this.runId,
    required this.graphId,
    required this.trigger,
    required this.route,
    required this.plugin,
    required this.startedAt,
    required this.duration,
    required this.status,
    required this.steps,
    required this.stepsExecuted,
    this.failingStep,
    this.errorKind,
    this.errorCode,
  });

  /// The run's ID, unique while the runtime runs.
  final int runId;

  /// The graph's UUID.
  final String graphId;

  /// What started the run: `event`, `guard`, `lifecycle`, `timer`, …
  final String trigger;

  /// The route of the page that owns the run; empty for the app's runs.
  final String route;

  /// The plugin whose graph ran; empty for the app's.
  final String plugin;

  /// When the run started.
  final DateTime startedAt;

  /// How long it took.
  final Duration duration;

  /// How it ended.
  final PluxTraceStatus status;

  /// Its steps in the order they started, at most `action.traceSteps`.
  final List<PluxStepTrace> steps;

  /// Every step it executed, including those beyond the recorded ones.
  final int stepsExecuted;

  /// The step whose error ended it.
  final String? failingStep;

  /// The kind of the error that ended it.
  final String? errorKind;

  /// The registered code of the error that ended it.
  final String? errorCode;
}

/// The steps of one run as it records them.
final class RunTracer {
  /// Creates a tracer that keeps at most [maxSteps] steps, with their
  /// values when [values].
  RunTracer({required this.maxSteps, required this.values});

  /// The most steps recorded.
  final int maxSteps;

  /// Whether input and output values are recorded (debug builds).
  final bool values;

  /// The recorded steps.
  final List<PluxStepTrace> steps = [];

  /// Records a step that ran from [start] to [end] of the run's clock.
  void step({
    required String id,
    required String action,
    required Duration start,
    required Duration end,
    required PluxTraceStatus status,
    required Map<String, Object?> inputs,
    required Map<String, int> inputIds,
    required Set<int> redact,
    Object? output,
    String? errorKind,
    String? errorCode,
    int attempts = 1,
  }) {
    if (steps.length >= maxSteps) return;
    final sensitive = redact.isNotEmpty;
    steps.add(
      PluxStepTrace(
        id: id,
        action: action,
        start: start,
        duration: end - start,
        status: status,
        attempts: attempts,
        inputs: {
          for (final e in inputs.entries)
            e.key: values && !redact.contains(inputIds[e.key])
                ? e.value
                : PluxStepTrace.redacted,
        },
        output: values && !sensitive ? output : PluxStepTrace.redacted,
        errorKind: errorKind,
        errorCode: errorCode,
      ),
    );
  }
}

/// The latest run traces, oldest first, at most [capacity].
final class TraceBuffer {
  /// Creates a buffer of [capacity] runs; [values] records input and output
  /// values (debug builds only).
  TraceBuffer({required this.capacity, this.values = kDebugMode});

  /// The most runs kept.
  int capacity;

  /// Whether runs record values.
  final bool values;

  final List<PluxActionTrace> _runs = [];
  final ValueNotifier<List<PluxActionTrace>> _published = ValueNotifier(
    const [],
  );
  int _nextId = 0;
  bool _scheduled = false;

  /// The traces, for `plux_devtools`; published after each run, outside
  /// any frame's build.
  ValueListenable<List<PluxActionTrace>> get listenable => _published;

  /// The traces now, oldest first.
  List<PluxActionTrace> get runs => List.unmodifiable(_runs);

  /// A new run ID.
  int nextRunId() => ++_nextId;

  /// Adds a finished run, dropping the oldest beyond [capacity].
  void add(PluxActionTrace run) {
    _runs.add(run);
    while (_runs.length > capacity && _runs.isNotEmpty) {
      _runs.removeAt(0);
    }
    if (_scheduled) return;
    _scheduled = true;
    scheduleMicrotask(() {
      _scheduled = false;
      _published.value = List.unmodifiable(_runs);
    });
  }
}
