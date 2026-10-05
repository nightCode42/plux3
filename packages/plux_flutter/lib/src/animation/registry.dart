// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The page's animations (ANI-002, ANI-006, ANI-007): one run per timeline,
/// owned by the page, which actions start and control and nodes listen to.
library;

import 'dart:math' as math;

import 'package:flutter/foundation.dart';
import 'package:flutter/physics.dart';
import 'package:flutter/scheduler.dart';
import 'package:plux_flutter/src/animation/timeline_spec.dart';
import 'package:plux_flutter/src/render/sections.dart';

/// The commands `controlAnimation` sends.
enum AnimationCommand {
  /// Play forward from the current position.
  play,

  /// Pause at the current position.
  pause,

  /// Play backward from the current position.
  reverse,

  /// Jump to a position.
  seek,

  /// Stop and return to the start.
  stop;

  /// The command named [name], or null.
  static AnimationCommand? named(String? name) =>
      AnimationCommand.values.asNameMap()[name];
}

/// The default spring of a driven timeline that settles.
const SpringSpec defaultSpring = SpringSpec(180, 20, 1);

/// How much faster a timeline plays when reduce motion shortens it.
const double shortenFactor = 4;

/// One timeline playing on a page (ANI-002). It is a clock over the
/// timeline's whole run in microseconds; nodes read the value of their
/// tracks at its position and rebuild when it moves.
final class TimelineRun extends ChangeNotifier {
  /// Creates the run; [reduceMotion] says whether the platform asks for
  /// reduced motion now.
  TimelineRun(this.spec, TickerProvider vsync, this._reduceMotion) {
    _ticker = vsync.createTicker(_tick);
  }

  /// The timeline.
  final TimelineSpec spec;

  final bool Function() _reduceMotion;
  late final Ticker _ticker;
  double _pos = 0;
  int _dir = 1;
  double _speed = 1;
  bool _active = false;
  bool _disposed = false;
  Duration _last = Duration.zero;
  Duration? _springOrigin;
  SpringSimulation? _spring;
  double _springUnit = 1;
  int _maxIndex = 0;

  /// Whether the timeline has started: until then nodes show their own
  /// values, and after [stop] again.
  bool get active => _active;

  /// Whether it is moving.
  bool get playing => _ticker.isActive;

  /// The position in microseconds from the start of the run.
  double get position => _pos;

  /// The end of the run in microseconds.
  double get end => spec.driver == TimelineDriver.time
      ? spec.endUs(_maxIndex)
      : spec.durationUs.toDouble();

  /// Notes that an item at [index] plays the timeline, so that a stagger
  /// across items extends the run (ANI-002).
  void noteIndex(int index) {
    if (index > _maxIndex) _maxIndex = index;
  }

  /// Whether this run follows the user's finger or scrolling.
  bool get driven => spec.driver != TimelineDriver.time;

  /// The value of [track] for the item at [index] now.
  Object? valueOf(TrackSpec track, [int index = 0]) =>
      track.valueAt(spec.localTime(_pos, index));

  bool get _skips => _reduceMotion() && spec.reduction == MotionReduction.skip;

  void _setSpeed() =>
      _speed = _reduceMotion() && spec.reduction == MotionReduction.shorten
      ? shortenFactor
      : 1;

  /// Plays from the start (`startAnimation`). Under reduce motion a
  /// timeline that skips jumps to its final state.
  void start() {
    if (driven) return;
    _spring = null;
    _active = true;
    _dir = 1;
    _pos = 0;
    _setSpeed();
    if (_skips) {
      _ticker.stop();
      _pos = spec.forever ? 0 : end;
      _notify();
      return;
    }
    _run();
    _notify();
  }

  /// Plays on from the current position.
  void play() {
    if (driven) return;
    if (!_active) return start();
    _dir = 1;
    _setSpeed();
    if (_skips) {
      _ticker.stop();
      _pos = spec.forever ? _pos : end;
      return _notify();
    }
    if (!spec.forever && _pos >= end) _pos = 0;
    _run();
  }

  /// Plays backwards from the current position.
  void reverse() {
    if (driven) return;
    if (!_active) {
      _active = true;
      _pos = spec.forever ? 0 : end;
    }
    _dir = -1;
    _setSpeed();
    if (_skips) {
      _ticker.stop();
      _pos = 0;
      return _notify();
    }
    _run();
  }

  /// Pauses.
  void pause() {
    _ticker.stop();
    _spring = null;
  }

  /// Jumps to [position] from the start of the run.
  void seek(Duration position) {
    _ticker.stop();
    _spring = null;
    _active = true;
    _pos = position.inMicroseconds.toDouble().clamp(0, _finiteEnd);
    _notify();
  }

  /// Stops and returns to the start; nodes show their own values again.
  void stop() {
    _ticker.stop();
    _spring = null;
    _active = false;
    _pos = 0;
    _notify();
  }

  /// Moves a driven timeline to [fraction] of its extent (ANI-006).
  void drive(double fraction) {
    if (!driven || _skips) return;
    _ticker.stop();
    _spring = null;
    _active = true;
    _pos = fraction.clamp(0.0, 1.0) * spec.durationUs;
    _notify();
  }

  /// Settles a driven timeline to its nearest end with its spring, carrying
  /// [velocity] in fractions of the extent per second (ANI-006).
  void settle(double velocity) {
    if (!driven || _skips) return;
    final unit = spec.durationUs.toDouble();
    final at = _pos / unit;
    final target = (at + velocity * 0.1) >= 0.5 ? 1.0 : 0.0;
    if (_reduceMotion()) {
      _pos = target * unit;
      return _notify();
    }
    _animateTo(target, unit, velocity);
  }

  /// Animates the position to [target] fractions of [unit] with the
  /// timeline's spring.
  void _animateTo(double target, double unit, double velocity) {
    final s = spec.spring ?? defaultSpring;
    _springUnit = unit;
    _spring = SpringSimulation(
      SpringDescription(
        mass: s.mass,
        stiffness: s.stiffness,
        damping: s.damping,
      ),
      _pos / unit,
      target,
      velocity,
    );
    _springOrigin = null;
    _last = Duration.zero;
    _ticker.stop();
    _ticker.start();
  }

  double get _finiteEnd {
    final e = end;
    return e.isFinite ? e : spec.durationUs.toDouble();
  }

  void _run() {
    final s = spec.spring;
    if (s != null && !spec.forever && spec.driver == TimelineDriver.time) {
      final unit = end;
      _animateTo(_dir > 0 ? 1.0 : 0.0, unit, 0);
      return;
    }
    _spring = null;
    _last = Duration.zero;
    _ticker.stop();
    _ticker.start();
  }

  void _tick(Duration elapsed) {
    final sim = _spring;
    if (sim != null) {
      final origin = _springOrigin ??= elapsed;
      final t = (elapsed - origin).inMicroseconds / 1e6;
      _pos = sim.x(t) * _springUnit;
      if (sim.isDone(t)) {
        _pos = sim.x(t).roundToDouble() * _springUnit;
        _pos = _pos.clamp(0, _finiteEnd);
        _spring = null;
        _ticker.stop();
      }
      return _notify();
    }
    final dt = (elapsed - _last).inMicroseconds * _speed * _dir;
    _last = elapsed;
    _pos += dt;
    final e = end;
    if (_dir > 0 && _pos >= e) {
      _pos = e;
      _ticker.stop();
    } else if (_dir < 0 && _pos <= 0) {
      _pos = 0;
      _ticker.stop();
    }
    _notify();
  }

  void _notify() {
    if (!_disposed) notifyListeners();
  }

  @override
  void dispose() {
    _disposed = true;
    _ticker.dispose();
    super.dispose();
  }
}

/// A track bound to the run that plays it.
final class TrackBinding {
  /// Binds [track] to [run].
  const TrackBinding(this.run, this.track);

  /// The run.
  final TimelineRun run;

  /// The track.
  final TrackSpec track;
}

/// The animations of a page: a [TimelineRun] per timeline, found by name
/// for actions and by node for the nodes they move (ANI-002). Actions reach
/// it as a service of the page's engine.
final class PluxAnimations {
  /// Creates the runs of [timelines], ticking with [vsync]; [reduceMotion]
  /// reads the platform's setting when an animation starts (ANI-007).
  PluxAnimations({
    required Iterable<TimelineSpec> timelines,
    required TickerProvider vsync,
    required bool Function() reduceMotion,
  }) {
    for (final t in timelines) {
      if (t.route) continue;
      final run = TimelineRun(t, vsync, reduceMotion);
      _runs[t.name] = run;
      for (final tr in t.tracks) {
        final node = tr.node;
        if (node != null) {
          (_byNode[node] ??= []).add(TrackBinding(run, tr));
        }
      }
      final driver = t.driverNode;
      if (driver != null && t.driver != TimelineDriver.time) {
        (_drivers[driver] ??= []).add(run);
      }
    }
  }

  final Map<String, TimelineRun> _runs = {};
  final Map<UuidKey, List<TrackBinding>> _byNode = {};
  final Map<UuidKey, List<TimelineRun>> _drivers = {};

  /// The names of the page's timelines.
  Iterable<String> get names => _runs.keys;

  /// The run of timeline [name], or null.
  TimelineRun? run(String name) => _runs[name];

  /// The tracks that move node [node].
  List<TrackBinding> tracksOf(UuidKey node) =>
      _byNode[node] ?? const <TrackBinding>[];

  /// The driven timelines that [node] scrolls or drags.
  List<TimelineRun> drivenBy(UuidKey node) =>
      _drivers[node] ?? const <TimelineRun>[];

  /// Starts the timelines that play when the page is shown.
  void autoplay() {
    for (final r in _runs.values) {
      if (r.spec.autoplay) r.start();
    }
  }

  /// Applies [command] to timeline [name]; false when the page has none.
  bool control(String name, AnimationCommand command, Duration? position) {
    final r = _runs[name];
    if (r == null) return false;
    switch (command) {
      case AnimationCommand.play:
        r.play();
      case AnimationCommand.pause:
        r.pause();
      case AnimationCommand.reverse:
        r.reverse();
      case AnimationCommand.seek:
        r.seek(position ?? Duration.zero);
      case AnimationCommand.stop:
        r.stop();
    }
    return true;
  }

  /// Releases the tickers.
  void dispose() {
    for (final r in _runs.values) {
      r.dispose();
    }
  }
}

/// Clamps [v] to 0..1 for the props whose value must stay there, such as
/// an opacity, whatever the curve does.
Object? clampedFor(String? prop, Object? v) =>
    prop == 'opacity' && v is num ? math.min(1.0, math.max(0.0, v)) : v;
