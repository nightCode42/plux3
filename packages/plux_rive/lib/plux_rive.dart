// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Rive animations for Plux pages (ANI-005, WGT-021).
///
/// Register [PluxRive.slots] as native slots and declare `PluxRive` in the
/// app's native catalogue; pages then use it as any widget:
///
/// ```dart
/// await Plux.initialize(PluxConfig(
///   // ...
///   nativeSlots: {...PluxRive.slots},
/// ));
/// ```
///
/// The slot's props are `url` (an https address of a `.riv` file),
/// `artboard`, `stateMachine`, `inputs` (a map of state machine input names
/// to numbers or booleans), `triggers` (a map of trigger names to any
/// value: the trigger fires whenever its value changes), `fit` and
/// `reduceMotion` (`skip` freezes the animation, `ignore` plays it).
/// Bind `inputs` and `triggers` to Plux state and the state machine follows
/// it. The slot emits `onLoaded` and `onFailed`.
library;

export 'src/inputs.dart' show PluxRiveInputs, StateMachineInputs;
export 'src/plux_rive.dart' show PluxRive, PluxRiveView;
