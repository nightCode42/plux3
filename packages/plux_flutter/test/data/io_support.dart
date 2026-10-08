// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:async';
import 'dart:math';

import 'package:plux_flutter/src/actions/handlers.dart';

/// A timer that runs only when a test says so.
final class FakeTimer implements Timer {
  /// Creates the timer.
  FakeTimer(this.delay, this._run);

  /// The wait it was asked for.
  final Duration delay;
  final void Function() _run;

  /// Whether it was cancelled.
  bool cancelled = false;

  /// Whether it ran.
  bool fired = false;

  @override
  void cancel() => cancelled = true;

  @override
  bool get isActive => !cancelled && !fired;

  @override
  int get tick => fired ? 1 : 0;

  /// Runs it.
  void fire() {
    fired = true;
    _run();
  }
}

/// A scheduler that keeps its timers for the test to fire.
final class FakeScheduler {
  /// Every timer ever made, oldest first.
  final List<FakeTimer> timers = [];

  /// The delays asked for, oldest first.
  List<Duration> get delays => [for (final t in timers) t.delay];

  /// The timers still waiting.
  List<FakeTimer> get active => [
    for (final t in timers)
      if (t.isActive) t,
  ];

  /// The scheduler function.
  Timer call(Duration after, void Function() run) {
    final t = FakeTimer(after, run);
    timers.add(t);
    return t;
  }

  /// Waits for a timer to be pending, then fires it.
  Future<void> fireNext() async {
    await eventually(() => active.isNotEmpty, 'a timer waits');
    active.first.fire();
  }
}

/// A random source that always gives the top of the range, so jittered
/// waits are exactly the doubled base.
final class TopRandom implements Random {
  @override
  bool nextBool() => true;

  @override
  double nextDouble() => 1.0;

  @override
  int nextInt(int max) => max - 1;
}

/// Polls [condition] until it holds; fails with [what] after a minute.
///
/// The bound only stops a test that hangs: what these tests check is the
/// order of outcomes, not their speed, and with real file I/O under a
/// loaded full-suite run five seconds was not always enough (the DAT-020
/// outbox test failed only under load).
Future<void> eventually(bool Function() condition, String what) async {
  final stop = DateTime.now().add(const Duration(seconds: 60));
  while (!condition()) {
    if (DateTime.now().isAfter(stop)) throw StateError('timed out: $what');
    await Future<void>.delayed(const Duration(milliseconds: 5));
  }
}

/// A navigator that goes nowhere, for step contexts.
final class NoNavigator implements RunNavigator {
  @override
  Future<void> navigate(
    String r,
    Map<String, Object?> p,
    String m,
    String? u,
  ) => Future.value();

  @override
  Future<Object?> present(
    String r,
    Map<String, Object?> p, {
    required bool sheet,
    required bool dismissible,
  }) => Future.value();

  @override
  void pop(Object? result) {}

  @override
  void switchTab(String tab) {}
}
