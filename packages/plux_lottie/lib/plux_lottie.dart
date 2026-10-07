// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Lottie and dotLottie animations for Plux pages (ANI-005, WGT-021).
///
/// Register [PluxLottie.slots] as native slots and declare `PluxLottie` in
/// the app's native catalogue; pages then use it as any widget:
///
/// ```dart
/// await Plux.initialize(PluxConfig(
///   // ...
///   nativeSlots: {...PluxLottie.slots},
/// ));
/// ```
///
/// The slot's props are `url` (an https address of a `.json` or `.lottie`
/// file), `animate` (default true), `repeat` (default true), `reverse`,
/// `progress` (0 to 1, binds the frame to Plux state and stops playback),
/// `fit`, `width`, `height` and `reduceMotion` (`skip`, `shorten` or
/// `ignore`). It emits `onCompleted` when an animation that does not repeat
/// ends.
library;

export 'src/plux_lottie.dart' show PluxLottie, PluxLottieView, PluxReduceMotion;
