// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The state actions (STA-002, Appendix D): `setState`, `patchState` and
/// `resetState`, writing through the run's [StateAccess]. A refused write
/// fails the step with its code: `PLX-5301` (validation) for a value of
/// the wrong type, `PLX-5302` otherwise.
library;

import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/actions/handlers.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/state/access.dart';
import 'package:plux_flutter/src/state/scope_state.dart';

/// The handlers of the state actions, by action name.
final Map<String, ActionHandler> stateHandlers = {
  'setState': _StateHandler((s, i, _) => s.write(_path(i), i['value'])),
  'patchState': _StateHandler((s, i, c) {
    final patch = i['patch'];
    if (patch is! Map<String, Object?>) {
      throw const ActionError.validation('patchState: patch is not an object');
    }
    // With optimistic set, the patch is undone when the run fails
    // (ACT-007).
    if (i['optimistic'] == true) c.run?.optimistic.remember(_path(i));
    s.patch(_path(i), patch);
  }),
  'resetState': _StateHandler((s, i, _) => s.reset(_path(i))),
};

String _path(Map<String, Object?> i) {
  final p = i['path'];
  return p is String
      ? p
      : throw const ActionError.validation('the state path is not a string');
}

final class _StateHandler implements ActionHandler {
  const _StateHandler(this._write);

  final void Function(StateAccess s, Map<String, Object?> inputs, StepContext c)
  _write;

  @override
  bool get waitsForUser => false;

  @override
  StepResult run(StepContext c, Map<String, Object?> inputs) {
    final s = c.state;
    if (s is! StateAccess) {
      throw const ActionError(
        ActionErrorKind.custom,
        PluxErrorCode.stateWriteRefused,
        'no state is in scope where this run started',
      );
    }
    try {
      _write(s, inputs, c);
    } on StateWriteException catch (e) {
      throw ActionError(
        e.code == PluxErrorCode.stateWriteTypeMismatch
            ? ActionErrorKind.validation
            : ActionErrorKind.custom,
        e.code,
        e.message,
      );
    }
    return const StepDone();
  }
}
