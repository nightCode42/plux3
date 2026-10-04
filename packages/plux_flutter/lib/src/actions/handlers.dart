// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The action handlers of the P4 engine (ADR-0039): one per action this
/// runtime runs, and one that refuses every other action with `PLX-4010`,
/// so the boundary with P5 is one table generated from Appendix D.
library;

import 'dart:async';

import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/data/handlers.dart';
import 'package:plux_flutter/src/data/services.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/schema/registry.g.dart';

/// The phase whose actions this runtime runs: an action whose descriptor
/// names a later phase gets the refusing handler.
const int runtimePhase = 4;

/// What a run may navigate (ADR-0040). The page or component that started
/// the run provides it; every method fails with an [ActionError].
abstract interface class RunNavigator {
  /// Pushes, replaces, pops until or clears and pushes [route] (NAV-005).
  /// [mode] is a `NavigationMode` member; [until] the route `popUntil`
  /// stops at, else [route].
  Future<void> navigate(
    String route,
    Map<String, Object?> params,
    String mode,
    String? until,
  );

  /// Presents [route] as a dialog, or as a bottom sheet when [sheet], and
  /// completes when it closes with its result, in PXL form, typed by the
  /// route's declared result; null when it returns none (NAV-003).
  Future<Object?> present(
    String route,
    Map<String, Object?> params, {
    required bool sheet,
    required bool dismissible,
  });

  /// Pops the page the run belongs to, returning [result] (NAV-003).
  void pop(Object? result);

  /// Selects [tab] of the enclosing shell (NAV-005).
  void switchTab(String tab);
}

/// The custom actions the host registered (ACT-060).
abstract interface class NativeActions {
  /// Calls action [name] with [input]; completes with its output, or fails
  /// with an [ActionError]: `PLX-4202` when it is not registered.
  Future<Object?> call(String name, Map<String, Object?> input);
}

/// No custom action is registered: every call fails with `PLX-4202`.
final class NoNativeActions implements NativeActions {
  /// Creates the lookup.
  const NoNativeActions();

  @override
  Future<Object?> call(String name, Map<String, Object?> input) => Future.error(
    ActionError(
      ActionErrorKind.custom,
      PluxErrorCode.nativeActionNotRegistered,
      'the host registers no custom action "$name"',
    ),
  );
}

/// What a handler runs with.
final class StepContext {
  /// Creates a context.
  const StepContext({
    required this.navigator,
    required this.emit,
    required this.nativeActions,
    this.data,
  });

  /// Navigation for the run's page.
  final RunNavigator navigator;

  /// Posts a host event to `Plux.events` (HST-013).
  final void Function(String name, Map<String, Object?> payload) emit;

  /// The host's custom actions.
  final NativeActions nativeActions;

  /// The data of the run's page, for `apiCall` and `refreshData`
  /// (ADR-0048); null where no data layer runs.
  final DataActions? data;
}

/// What a step produced.
sealed class StepResult {
  const StepResult();
}

/// The step succeeded: its output, and for a branching action the branch
/// taken.
final class StepDone extends StepResult {
  /// Creates the result.
  const StepDone([this.output, this.branch]);

  /// The output, which later steps read as `steps.<id>.output`.
  final Object? output;

  /// The branch taken, or null to follow `onSuccess` or `next`.
  final String? branch;
}

/// The step ended the run, with the graph's [result].
final class StepEnd extends StepResult {
  /// Creates the result.
  const StepEnd([this.result]);

  /// The run's result.
  final Object? result;
}

/// Runs one action.
abstract interface class ActionHandler {
  /// Whether the step waits for the user, as a presented page does: its
  /// time counts against neither the step's bound nor the run's (ADR-0039).
  bool get waitsForUser;

  /// Runs the action with its [inputs] in PXL form, by input name: a
  /// result at once for an action that does its work synchronously, so
  /// the engine skips the timers and cancellation a pending step needs, or
  /// a future. Fails with an [ActionError], thrown or in the future.
  FutureOr<StepResult> run(StepContext c, Map<String, Object?> inputs);
}

/// Every action this runtime does not run: the step fails with `PLX-4010`,
/// which its `onError` can handle.
final class RefusingHandler implements ActionHandler {
  /// Creates the handler for [name].
  const RefusingHandler(this.name, this.phase);

  /// The action.
  final String name;

  /// The phase that delivers it.
  final String phase;

  @override
  bool get waitsForUser => false;

  @override
  StepResult run(StepContext c, Map<String, Object?> inputs) =>
      throw ActionError(
        ActionErrorKind.custom,
        PluxErrorCode.actionsNotAvailable,
        'action $name arrives in $phase; this runtime does not run it',
      );
}

/// A handler written as a function.
final class _Handler implements ActionHandler {
  const _Handler(this._run, {this.waitsForUser = false});

  final FutureOr<StepResult> Function(StepContext c, Map<String, Object?> i)
  _run;

  @override
  final bool waitsForUser;

  @override
  FutureOr<StepResult> run(StepContext c, Map<String, Object?> inputs) =>
      _run(c, inputs);
}

/// A handler written as a function, for handler tables outside this
/// library, such as the data layer's.
final class FunctionHandler implements ActionHandler {
  /// Creates the handler.
  const FunctionHandler(this._run);

  final FutureOr<StepResult> Function(StepContext c, Map<String, Object?> i)
  _run;

  @override
  bool get waitsForUser => false;

  @override
  FutureOr<StepResult> run(StepContext c, Map<String, Object?> inputs) =>
      _run(c, inputs);
}

/// The handlers of the actions P4 runs, by action name.
final Map<String, ActionHandler> p4Handlers = {
  'condition': _Handler((c, i) {
    final when = i['when'];
    if (when is! bool) {
      throw ActionError.validation('condition: when is not a bool');
    }
    return StepDone(null, when ? 'then' : 'else');
  }),
  'stop': _Handler((c, i) {
    final error = i['error'];
    if (error is String) {
      throw ActionError(
        ActionErrorKind.custom,
        PluxErrorCode.actionCustomError,
        'stop: $error',
      );
    }
    return StepEnd(i['result']);
  }),
  'emitHostEvent': _Handler((c, i) {
    final name = i['event'];
    if (name is! String) {
      throw ActionError.validation('emitHostEvent: event is not a string');
    }
    final payload = i['payload'];
    c.emit(name, payload is Map<String, Object?> ? payload : const {});
    return const StepDone();
  }),
  'navigate': _Handler((c, i) async {
    await c.navigator.navigate(
      _route(i['route'], 'navigate'),
      _params(i['params']),
      enumMember('NavigationMode', i['mode']) ?? 'push',
      i['until'] as String?,
    );
    return const StepDone();
  }),
  'pop': _Handler((c, i) {
    c.navigator.pop(i['result']);
    return const StepEnd();
  }),
  'openDialog': _Handler(
    (c, i) async => StepDone(
      await c.navigator.present(
        _route(i['route'], 'openDialog'),
        _params(i['params']),
        sheet: false,
        dismissible: i['dismissible'] as bool? ?? true,
      ),
    ),
    waitsForUser: true,
  ),
  'openBottomSheet': _Handler(
    (c, i) async => StepDone(
      await c.navigator.present(
        _route(i['route'], 'openBottomSheet'),
        _params(i['params']),
        sheet: true,
        dismissible: i['dismissible'] as bool? ?? true,
      ),
    ),
    waitsForUser: true,
  ),
  'switchTab': _Handler((c, i) {
    final tab = i['tab'];
    if (tab is! String) {
      throw ActionError.validation('switchTab: tab is not a string');
    }
    c.navigator.switchTab(tab);
    return const StepDone();
  }),
  'callNative': _Handler((c, i) async {
    final name = i['action'];
    if (name is! String) {
      throw ActionError.validation('callNative: action is not a string');
    }
    return StepDone(await c.nativeActions.call(name, _params(i['input'])));
  }),
};

String _route(Object? v, String action) => v is String
    ? v
    : throw ActionError.validation('$action: route is not a route name');

Map<String, Object?> _params(Object? v) => switch (v) {
  null => const {},
  final Map<String, Object?> m => m,
  _ => throw const ActionError.validation('params are not an object'),
};

final Map<int, ActionDescriptor> _byId = {
  for (final d in actionDescriptors) d.id: d,
};

/// The descriptor of action [id], or null for an ID this runtime does not
/// know.
ActionDescriptor? actionDescriptor(int id) => _byId[id];

/// The handler of an action: its P4 handler, the data layer's (P5 R4), or
/// the refusing one.
ActionHandler handlerFor(ActionDescriptor d) =>
    p4Handlers[d.name] ??
    dataHandlers[d.name] ??
    RefusingHandler(d.name, d.phase);

/// Whether the descriptor's phase is one this runtime runs.
bool runsInThisRuntime(ActionDescriptor d) =>
    (int.tryParse(d.phase.substring(1)) ?? 99) <= runtimePhase;

/// The member name of a registry enum value: literals carry the permanent
/// value ID, PXL expressions the name. Null for null or an unknown value.
String? enumMember(String enumName, Object? v) {
  if (v is String) return v;
  if (v is! int) return null;
  for (final e in enumDescriptors) {
    if (e.name != enumName) continue;
    for (final m in e.values.entries) {
      if (m.value == v) return m.key;
    }
  }
  return null;
}
