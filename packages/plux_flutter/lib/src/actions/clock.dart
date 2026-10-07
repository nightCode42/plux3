// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Time as the action engine sees it: the monotonic clock of the timed
/// policies, the timers of triggers, the waits of `delay` and retries, and
/// the jitter of backoffs (ACT-003, ACT-006). Tests substitute a fake, so
/// they drive time and jitter deterministically.
library;

import 'dart:async';
import 'dart:developer' as developer;
import 'dart:math' as math;

/// The engine's clock.
class ActionClock {
  /// Creates the real clock.
  const ActionClock();

  /// The time on a monotonic clock.
  Duration get now => Duration(microseconds: developer.Timeline.now);

  /// Calls [f] once after [d].
  Timer timer(Duration d, void Function() f) => Timer(d, f);

  /// Calls [f] every [d].
  Timer periodic(Duration d, void Function() f) =>
      Timer.periodic(d, (_) => f());

  /// Completes after [d].
  Future<void> sleep(Duration d) => Future<void>.delayed(d);

  /// A value in [0, 1) that scales a backoff's jitter.
  double random() => math.Random().nextDouble();
}
