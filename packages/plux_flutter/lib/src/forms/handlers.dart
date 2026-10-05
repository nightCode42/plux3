// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The form actions (STA-020, Appendix D, ADR-0047): `validateForm`,
/// `submitForm` and `resetForm`, on the form the run's scope declares.
library;

import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/actions/handlers.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/forms/form_state.dart';

/// The handlers of the form actions, by action name.
final Map<String, ActionHandler> formHandlers = {
  'validateForm': FunctionHandler((c, i) async {
    final valid = await _form(c, i, 'validateForm').validate();
    return StepDone(valid, valid ? 'valid' : 'invalid');
  }),
  'submitForm': FunctionHandler((c, i) async {
    final form = _form(c, i, 'submitForm');
    final values = await form.submit();
    final run = c.run;
    if (run == null) {
      form.ended(succeeded: true);
    } else {
      run.onEnd((succeeded) => form.ended(succeeded: succeeded));
    }
    return StepDone(values);
  }),
  'resetForm': FunctionHandler((c, i) {
    _form(c, i, 'resetForm').reset();
    return const StepDone();
  }),
};

/// The form the step names; fails with PLX-5351 when no scope in reach
/// declares it.
FormController _form(StepContext c, Map<String, Object?> i, String action) {
  final name = i['form'];
  if (name is! String) {
    throw ActionError.validation('$action: form is not a form name');
  }
  final s = c.state;
  final form = s is FormLookup ? (s as FormLookup).form(name) : null;
  return form ??
      (throw ActionError(
        ActionErrorKind.custom,
        PluxErrorCode.formNotInScope,
        '$action: no form $name is in scope where this run started',
      ));
}
