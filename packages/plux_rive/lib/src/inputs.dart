// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// The state machine inputs are what ANI-005 binds to Plux state; Rive
// prefers data binding for new files but still runs inputs.
// ignore_for_file: deprecated_member_use

import 'package:rive/rive.dart' as rive;

/// The inputs of a Rive state machine, by name.
abstract interface class StateMachineInputs {
  /// Sets the number input [name]; false when the machine has none.
  bool setNumber(String name, double value);

  /// Sets the boolean input [name]; false when the machine has none.
  bool setBoolean(String name, bool value);

  /// Fires the trigger [name]; false when the machine has none.
  bool fire(String name);
}

/// A Rive [rive.StateMachine] as [StateMachineInputs].
final class _Machine implements StateMachineInputs {
  _Machine(this._machine);

  final rive.StateMachine _machine;

  @override
  bool setNumber(String name, double value) {
    final input = _machine.number(name);
    if (input == null) return false;
    input.value = value;
    return true;
  }

  @override
  bool setBoolean(String name, bool value) {
    final input = _machine.boolean(name);
    if (input == null) return false;
    input.value = value;
    return true;
  }

  @override
  bool fire(String name) {
    final input = _machine.trigger(name);
    if (input == null) return false;
    input.fire();
    return true;
  }
}

/// Keeps a state machine's inputs equal to values the page binds (ANI-005):
/// each apply sets the inputs whose value changed, and fires a trigger when
/// its bound value changes.
final class PluxRiveInputs {
  /// Creates the binding over [target].
  PluxRiveInputs(this.target);

  /// Binds the inputs of a Rive state machine.
  PluxRiveInputs.of(rive.StateMachine machine) : target = _Machine(machine);

  /// The inputs set.
  final StateMachineInputs target;

  final Map<String, Object?> _inputs = {};
  final Map<String, Object?> _triggers = {};
  bool _first = true;

  /// Names that no input of the state machine answers to, from the latest
  /// apply, for developers.
  final Set<String> unknown = {};

  /// Sets what changed since the last apply: numbers and booleans of
  /// [inputs], and the triggers of [triggers] whose value is not the one
  /// they had. A trigger fires only on a change after it was first seen.
  void apply(Map<String, Object?> inputs, Map<String, Object?> triggers) {
    unknown.clear();
    for (final e in inputs.entries) {
      if (!_first && _inputs.containsKey(e.key) && _inputs[e.key] == e.value) {
        continue;
      }
      final v = e.value;
      final ok = switch (v) {
        final num n => target.setNumber(e.key, n.toDouble()),
        final bool b => target.setBoolean(e.key, b),
        _ => false,
      };
      if (!ok) unknown.add(e.key);
    }
    if (!_first) {
      for (final e in triggers.entries) {
        // A trigger seen for the first time sets the baseline.
        if (!_triggers.containsKey(e.key) || _triggers[e.key] == e.value) {
          continue;
        }
        if (!target.fire(e.key)) unknown.add(e.key);
      }
    }
    _inputs
      ..clear()
      ..addAll(inputs);
    _triggers
      ..clear()
      ..addAll(triggers);
    _first = false;
  }
}
