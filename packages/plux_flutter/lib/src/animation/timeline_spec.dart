// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The timelines of a bundle's timelines section as plain values
/// (ANI-002, ANI-006, NAV-010), and the value of a track at a time.
library;

import 'package:flutter/animation.dart';
import 'package:plux_flutter/src/animation/curves.dart';
import 'package:plux_flutter/src/animation/interpolate.dart';
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/bundle/safe_read.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/pxl/values.dart';
import 'package:plux_flutter/src/render/sections.dart';
import 'package:plux_flutter/src/render/values.dart' show StringTable;

/// What a timeline does when the platform asks to reduce motion (ANI-007).
enum MotionReduction {
  /// Jump to the final state.
  skip,

  /// Play at a quarter of the duration.
  shorten,

  /// Play as declared.
  ignore;

  /// The reduction a bundle's enum names; [skip] for one this runtime does
  /// not know, the safest.
  static MotionReduction of(fbs.ReduceMotion Function() read) =>
      switch (readEnum(read)) {
        fbs.ReduceMotion.Shorten => shorten,
        fbs.ReduceMotion.Ignore => ignore,
        _ => skip,
      };
}

/// What moves a timeline besides time (ANI-006).
enum TimelineDriver {
  /// Time.
  time,

  /// The scroll offset of a node.
  scroll,

  /// The drag of a node.
  drag,
}

/// A keyframe: a value at a time, reached with a curve.
final class KeyframeSpec {
  /// Creates a keyframe.
  const KeyframeSpec(this.atUs, this.value, this.curve);

  /// The time, in microseconds.
  final int atUs;

  /// The value, in PXL form.
  final Object? value;

  /// The curve into this keyframe.
  final Curve curve;
}

/// The keyframes of one prop of one node.
final class TrackSpec {
  /// Creates a track.
  const TrackSpec(this.node, this.prop, this.keyframes);

  /// The node, or null in a route timeline.
  final UuidKey? node;

  /// The permanent prop ID, or the route prop (1 opacity, 2 scale, 3 slideX,
  /// 4 slideY).
  final int prop;

  /// The keyframes in increasing time.
  final List<KeyframeSpec> keyframes;

  /// The value at [tUs] microseconds: the first keyframe's before it, the
  /// last one's after, and in between the previous keyframe's value mixed
  /// into the next one's by the next one's curve.
  Object? valueAt(double tUs) {
    if (keyframes.isEmpty) return null;
    if (tUs <= keyframes.first.atUs) return keyframes.first.value;
    if (tUs >= keyframes.last.atUs) return keyframes.last.value;
    for (var i = 1; i < keyframes.length; i++) {
      final b = keyframes[i];
      if (tUs < b.atUs) {
        final a = keyframes[i - 1];
        final f = (tUs - a.atUs) / (b.atUs - a.atUs);
        return lerpValue(a.value, b.value, b.curve.transform(f));
      }
    }
    return keyframes.last.value;
  }
}

/// A spring that moves a timeline (ANI-006).
final class SpringSpec {
  /// Creates a spring.
  const SpringSpec(this.stiffness, this.damping, this.mass);

  /// The stiffness.
  final double stiffness;

  /// The damping.
  final double damping;

  /// The mass.
  final double mass;
}

/// A timeline of a page (ANI-002).
final class TimelineSpec {
  /// Creates a timeline.
  const TimelineSpec({
    required this.name,
    required this.durationUs,
    required this.tracks,
    this.delayUs = 0,
    this.repeat = 0,
    this.forever = false,
    this.reverse = false,
    this.staggerUs = 0,
    this.reduction = MotionReduction.skip,
    this.route = false,
    this.autoplay = false,
    this.driver = TimelineDriver.time,
    this.driverNode,
    this.driverExtent = 0,
    this.spring,
  });

  /// Reads [t], whose names are in [strings]. A timeline this runtime
  /// cannot play (an unknown driver, a value that does not animate) throws
  /// `PLX-4302`.
  factory TimelineSpec.read(fbs.Timeline t, StringTable strings) {
    final driver = readEnum(() => t.driver);
    final name = strings(t.name);
    if (driver == null) {
      throw PluxException(
        PluxErrorCode.animationTimelineBroken,
        'timeline $name has a driver this runtime does not know',
      );
    }
    final dn = t.driverNode;
    return TimelineSpec(
      name: name,
      durationUs: t.durationUs,
      delayUs: t.delayUs,
      repeat: t.repeat,
      forever: t.forever,
      reverse: t.reverse,
      staggerUs: t.staggerUs,
      reduction: MotionReduction.of(() => t.reduceMotion),
      route: t.route,
      autoplay: t.autoplay,
      driver: switch (driver) {
        fbs.Driver.Scroll => TimelineDriver.scroll,
        fbs.Driver.Drag => TimelineDriver.drag,
        fbs.Driver.Time => TimelineDriver.time,
      },
      driverNode: dn == null ? null : uuidOf(dn),
      driverExtent: t.driverExtent,
      spring: t.spring == null
          ? null
          : SpringSpec(t.spring!.stiffness, t.spring!.damping, t.spring!.mass),
      tracks: [
        for (final tr in t.tracks ?? const <fbs.Track>[]) _track(tr, name),
      ],
    );
  }

  static TrackSpec _track(fbs.Track tr, String timeline) {
    final node = tr.node;
    return TrackSpec(node == null ? null : uuidOf(node), tr.prop, [
      for (final k in tr.keyframes ?? const <fbs.Keyframe>[])
        KeyframeSpec(k.atUs, _literal(k.value, timeline), curveById(k.curve)),
    ]);
  }

  static Object? _literal(fbs.Value? v, String timeline) {
    final kind = v == null ? null : readEnum(() => v.kind);
    switch (kind) {
      case fbs.ValueKind.Int:
        return v!.i;
      case fbs.ValueKind.Double:
        return v!.d;
      case fbs.ValueKind.Color:
        return PxlColor(((v!.i & 0xffffff) << 8) | ((v.i >> 24) & 0xff));
      default:
        throw PluxException(
          PluxErrorCode.animationTimelineBroken,
          'timeline $timeline has a keyframe that is neither a number nor '
          'a colour',
        );
    }
  }

  /// The name actions use.
  final String name;

  /// One play, in microseconds.
  final int durationUs;

  /// The tracks.
  final List<TrackSpec> tracks;

  /// The delay before the first play.
  final int delayUs;

  /// The plays after the first.
  final int repeat;

  /// Whether it plays until stopped.
  final bool forever;

  /// Whether every second play runs backwards.
  final bool reverse;

  /// The delay per item index.
  final int staggerUs;

  /// How it honours reduce motion.
  final MotionReduction reduction;

  /// Whether it moves a page in or out.
  final bool route;

  /// Whether it plays when its page is shown.
  final bool autoplay;

  /// What moves it.
  final TimelineDriver driver;

  /// The node that drives it.
  final UuidKey? driverNode;

  /// The scroll offset or drag distance that spans it.
  final double driverExtent;

  /// The spring that settles it.
  final SpringSpec? spring;

  /// The local time, in microseconds, of an instance at position [posUs] of
  /// the whole run: the position less the delay and the stagger of item
  /// [index], folded into the play it is in (repeat and reverse).
  double localTime(double posUs, int index) {
    final q = posUs - delayUs - index * staggerUs;
    if (q <= 0) return 0;
    var play = (q / durationUs).floor();
    var u = q - play * durationUs;
    if (!forever && q >= (repeat + 1) * durationUs) {
      play = repeat;
      u = durationUs.toDouble();
    }
    return reverse && play.isOdd ? durationUs - u : u;
  }

  /// The end of the run for items up to [maxIndex]: infinite when it
  /// repeats forever.
  double endUs(int maxIndex) => forever
      ? double.infinity
      : delayUs + maxIndex * staggerUs + (repeat + 1) * durationUs.toDouble();
}
