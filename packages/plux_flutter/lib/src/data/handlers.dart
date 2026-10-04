// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The handlers of `apiCall` and `refreshData` (DAT-001, DAT-011), run on
/// the page's [DataActions] through `StepContext.data`. A failure is an
/// [ActionError] with its kind, so `onError` handles it — and an
/// engine-level optimistic hook (ACT-007) can roll back on it: the handler
/// ignores `optimistic`, which the engine applies around the step.
library;

import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/actions/handlers.dart';
import 'package:plux_flutter/src/data/failure.dart';
import 'package:plux_flutter/src/data/services.dart';

DataActions _data(StepContext c, String action) =>
    c.data ??
    (throw DataFailure.unavailable('$action: this page has no data layer')
        .toActionError());

/// The data actions' handlers, by action name; `handlerFor` adds them to
/// the engine's table.
final Map<String, ActionHandler> dataHandlers = {
  'apiCall': FunctionHandler((c, i) async {
    final op = i['operation'];
    if (op is! String) {
      throw const ActionError.validation('apiCall: operation is not a string');
    }
    final input = i['input'];
    return StepDone(
      await _data(
        c,
        'apiCall',
      ).callOperation(op, input is Map<String, Object?> ? input : const {}),
    );
  }),
  'refreshData': FunctionHandler((c, i) async {
    final source = i['source'];
    if (source is! String) {
      throw const ActionError.validation('refreshData: source is not a string');
    }
    await _data(c, 'refreshData').refresh(source, more: i['more'] == true);
    return const StepDone();
  }),
};
