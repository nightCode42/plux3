// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The control, analytics, sync and component-event actions of P5's
/// milestone R1 (plan §5.1): `switch`, `delay`, `trackEvent`, `sync` and
/// `emitEvent` are handlers; `forEach`, `parallel` and `callFlow` run parts
/// of a graph, so the run executes them itself ([StructuralAction]).
library;

import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/actions/handlers.dart';
import 'package:plux_flutter/src/pxl/values.dart';

/// The actions a run executes itself, because they run other steps.
enum StructuralAction {
  /// Runs the `body` branch once per item (ACT-005).
  forEach,

  /// Runs its lanes concurrently; the first error cancels the others.
  parallel,

  /// Runs a flow (ACT-061).
  callFlow,
}

/// The marker handler of a [StructuralAction]; the run never calls it.
final class StructuralHandler implements ActionHandler {
  /// Creates the marker.
  const StructuralHandler(this.action);

  /// The action.
  final StructuralAction action;

  @override
  bool get waitsForUser => false;

  @override
  StepResult run(StepContext c, Map<String, Object?> inputs) =>
      throw StateError('${action.name} runs in the engine');
}

/// The handlers of R1's actions, by action name.
final Map<String, ActionHandler> controlHandlers = {
  for (final a in StructuralAction.values) a.name: StructuralHandler(a),
  'switch': actionHandler((c, i) {
    final value = i['value'];
    final cases = i['cases'];
    if (value is! String || cases is! List) {
      throw const ActionError.validation('switch: value or cases invalid');
    }
    return StepDone(null, cases.contains(value) ? value : 'default');
  }),
  'delay': actionHandler((c, i) async {
    await c.clock.sleep(durationOf(i['duration'], 'delay'));
    return const StepDone();
  }),
  'trackEvent': actionHandler((c, i) {
    final name = i['name'];
    if (name is! String) {
      throw const ActionError.validation('trackEvent: name is not a string');
    }
    final props = i['props'];
    c.track?.call(name, props is Map<String, Object?> ? props : const {});
    return const StepDone();
  }),
  'sync': actionHandler((c, i) {
    c.sync?.call();
    return const StepDone();
  }),
  'emitEvent': actionHandler((c, i) {
    final emit = c.emitEvent;
    final name = i['event'];
    if (emit == null || name is! String) {
      throw const ActionError.validation(
        'emitEvent runs only in a component handler',
      );
    }
    emit(name, i['payload']);
    return const StepDone();
  }),
};

/// A `duration` input: a PXL duration, or milliseconds.
Duration durationOf(Object? v, String action) => switch (v) {
  PxlDuration(:final millis) when millis >= 0 => Duration(milliseconds: millis),
  final int ms when ms >= 0 => Duration(milliseconds: ms),
  _ => throw ActionError.validation('$action: duration is not a duration'),
};
