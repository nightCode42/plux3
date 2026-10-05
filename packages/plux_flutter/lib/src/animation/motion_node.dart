// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The node that moves (ANI-001, ANI-003, ANI-004, ANI-006, ANI-007): it
/// animates its props implicitly when their bound values change, follows
/// the tracks of the page's timelines, plays enter and exit transitions as
/// its visibility changes, flies as a hero between routes, and drives the
/// timelines that follow its scrolling or dragging.
library;

import 'package:flutter/widgets.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:plux_flutter/src/animation/curves.dart';
import 'package:plux_flutter/src/animation/interpolate.dart';
import 'package:plux_flutter/src/animation/registry.dart';
import 'package:plux_flutter/src/animation/timeline_spec.dart';
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/render/sections.dart';
import 'package:plux_flutter/src/schema/registry.g.dart';

/// What a node's animation needs to know about it.
final class NodeMotion {
  /// Creates the motion of a node.
  const NodeMotion({
    required this.node,
    this.animation,
    this.tracks = const [],
    this.drivers = const [],
    this.opacityProp,
  });

  /// The node's identity.
  final UuidKey node;

  /// The node's own animation, if it declares one.
  final fbs.NodeAnimation? animation;

  /// The timeline tracks that move the node.
  final List<TrackBinding> tracks;

  /// The driven timelines that the node's scrolling or dragging moves.
  final List<TimelineRun> drivers;

  /// The permanent ID of the node's opacity prop, which curves must not
  /// push out of 0..1.
  final int? opacityProp;

  /// The motion of [n], or null when it neither animates nor is animated.
  static NodeMotion? of(fbs.Node n, PluxAnimations? animations) {
    final id = n.id;
    if (id == null) return null;
    final key = uuidOf(id);
    final animation = n.animation;
    final tracks = animations?.tracksOf(key) ?? const <TrackBinding>[];
    final drivers = animations?.drivenBy(key) ?? const <TimelineRun>[];
    if (animation == null && tracks.isEmpty && drivers.isEmpty) return null;
    int? opacity;
    for (final d in widgetDescriptors) {
      if (d.id == n.widget) opacity = d.props['opacity'];
    }
    return NodeMotion(
      node: key,
      animation: animation,
      tracks: tracks,
      drivers: drivers,
      opacityProp: opacity,
    );
  }
}

/// What the node's builder needs from its motion: the prop values to use in
/// place of its own, whether to build it although it is hidden, and the
/// quiet resolution of the values it may animate.
abstract interface class MotionSource {
  /// Whether the node is visible by its own `visible` binding.
  bool get isVisible;

  /// The current values of the props that animate: those of [only], or the
  /// doubles and colours among the node's props.
  Map<int, Object?> animatableTargets(Set<int>? only);

  /// The node's hero tag, or null when it has none.
  String? get heroTag;
}

/// Builds the node's widget with [values] in place of its props'; builds it
/// although hidden when [force] is set, and subscribes to the state it reads
/// when [subscribe] is.
typedef MotionBuild = Widget Function(
  BuildContext context,
  WidgetRef ref,
  Map<int, Object?> values,
  bool force,
  bool subscribe,
  void Function(MotionSource source)? onSource,
);

/// A node with animation (ANI-001–ANI-007).
final class MotionNode extends ConsumerStatefulWidget {
  /// Creates the node.
  const MotionNode({
    required this.motion,
    required this.build,
    this.itemIndex = 0,
    super.key,
  });

  /// The motion.
  final NodeMotion motion;

  /// The item index of the node's template instance, for stagger.
  final int itemIndex;

  /// Builds the node's widget.
  final MotionBuild build;

  @override
  ConsumerState<MotionNode> createState() => _MotionNodeState();
}

final class _MotionNodeState extends ConsumerState<MotionNode>
    with TickerProviderStateMixin {
  AnimationController? _implicit;
  AnimationController? _presence;
  Map<int, Object?> _from = const {};
  Map<int, Object?> _to = const {};
  bool _first = true;
  bool? _wasVisible;
  bool _exiting = false;
  Curve _implicitCurve = Curves.linear;

  fbs.NodeAnimation? get _anim => widget.motion.animation;

  bool get _reduce => MediaQuery.maybeDisableAnimationsOf(context) ?? false;

  MotionReduction get _reduction =>
      MotionReduction.of(() => _anim!.reduceMotion);

  @override
  void initState() {
    super.initState();
    for (final b in widget.motion.tracks) {
      b.run.noteIndex(widget.itemIndex);
    }
  }

  @override
  void dispose() {
    _implicit?.dispose();
    _presence?.dispose();
    super.dispose();
  }

  /// Runs [controller] for [duration] under the node's reduce-motion
  /// setting: not at all when it skips, a quarter of the time when it
  /// shortens (ANI-007). Returns whether motion plays.
  Duration? _scaled(Duration duration) {
    if (!_reduce) return duration;
    return switch (_reduction) {
      MotionReduction.skip => null,
      MotionReduction.shorten => duration ~/ shortenFactor.toInt(),
      MotionReduction.ignore => duration,
    };
  }

  // ── Implicit animation ───────────────────────────────────────────────────

  Map<int, Object?> _implicitNow() {
    final c = _implicit;
    if (c == null || _to.isEmpty) return const {};
    final t = c.isAnimating ? _implicitCurve.transform(c.value) : 1.0;
    if (!c.isAnimating) return const {};
    return {
      for (final e in _to.entries)
        e.key: lerpValue(_from[e.key] ?? e.value, e.value, t),
    };
  }

  void _retarget(MotionSource source) {
    final a = _anim;
    if (a == null || a.durationUs <= 0) return;
    final only = a.props?.isEmpty ?? true ? null : a.props!.toSet();
    final next = source.animatableTargets(only);
    if (_first) {
      _to = next;
      _from = next;
      return;
    }
    var changed = next.length != _to.length;
    for (final e in next.entries) {
      if (_to[e.key] != e.value) changed = true;
    }
    if (!changed) return;
    final now = _implicitNow();
    _from = {
      for (final e in next.entries) e.key: now[e.key] ?? _to[e.key] ?? e.value,
    };
    _to = next;
    final total = Duration(microseconds: a.durationUs + a.delayUs);
    final run = _scaled(total);
    final c = _implicit ??= AnimationController(vsync: this)
      ..addStatusListener((_) {
        if (mounted) setState(() {});
      });
    if (run == null) {
      c.stop();
      _from = next;
      return;
    }
    c.duration = run;
    final lead = a.delayUs / (a.durationUs + a.delayUs);
    _implicitCurve = Interval(lead, 1, curve: curveById(a.curve));
    c.forward(from: 0);
  }

  // ── Presence ─────────────────────────────────────────────────────────────

  void _presenceChanged(bool visible) {
    final a = _anim;
    final enter = a?.enter, exit = a?.exit;
    final was = _wasVisible;
    _wasVisible = visible;
    if (enter == null && exit == null) return;
    final c = _presence ??= AnimationController(vsync: this, value: 1)
      ..addStatusListener(_presenceStatus);
    if (visible && (was == null || !was) && enter != null) {
      final d = _scaled(Duration(microseconds: enter.durationUs));
      _exiting = false;
      if (d == null) {
        c.value = 1;
        return;
      }
      c.duration = d;
      c.forward(from: 0);
    } else if (!visible && was == true && exit != null) {
      final d = _scaled(Duration(microseconds: exit.durationUs));
      if (d == null) {
        _exiting = false;
        c.value = 0;
        return;
      }
      _exiting = true;
      c.reverseDuration = d;
      c.reverse(from: 1);
    }
  }

  void _presenceStatus(AnimationStatus status) {
    if (status == AnimationStatus.dismissed && _exiting) {
      if (mounted) setState(() => _exiting = false);
    }
  }

  Widget _present(Widget child) {
    final c = _presence;
    final a = _anim;
    if (c == null || a == null) return child;
    return AnimatedBuilder(
      animation: c,
      child: child,
      builder: (context, child) {
        final spec = c.status == AnimationStatus.reverse || _exiting
            ? a.exit
            : a.enter;
        if (spec == null || c.value == 1) return child!;
        final curve = curveById(spec.curve);
        final p = curve.transform(c.value);
        return _transition(
          spec.kind,
          p,
          c.status == AnimationStatus.reverse || _exiting,
          child!,
        );
      },
    );
  }

  Widget _transition(
    fbs.TransitionKind kind,
    double p,
    bool exit,
    Widget child,
  ) {
    final out = 1 - p;
    Offset slide(Offset d) => exit ? d * out : -d * out;
    switch (kind) {
      case fbs.TransitionKind.Scale:
        return Transform.scale(
          scale: p,
          child: Opacity(opacity: p.clamp(0.0, 1.0), child: child),
        );
      case fbs.TransitionKind.SlideUp:
        return _slid(slide(const Offset(0, -1)), p, child);
      case fbs.TransitionKind.SlideDown:
        return _slid(slide(const Offset(0, 1)), p, child);
      case fbs.TransitionKind.SlideLeft:
        return _slid(slide(const Offset(-1, 0)), p, child);
      case fbs.TransitionKind.SlideRight:
        return _slid(slide(const Offset(1, 0)), p, child);
      case fbs.TransitionKind.Fade:
      case fbs.TransitionKind.None:
        return Opacity(opacity: p.clamp(0.0, 1.0), child: child);
    }
  }

  Widget _slid(Offset offset, double p, Widget child) => FractionalTranslation(
    translation: offset,
    child: Opacity(opacity: p.clamp(0.0, 1.0), child: child),
  );

  // ── Timelines ────────────────────────────────────────────────────────────

  Map<int, Object?> _timelineValues() {
    final out = <int, Object?>{};
    for (final b in widget.motion.tracks) {
      if (!b.run.active) continue;
      final v = b.run.valueOf(b.track, widget.itemIndex);
      out[b.track.prop] = b.track.prop == widget.motion.opacityProp
          ? clampedFor('opacity', v)
          : v;
    }
    return out;
  }

  Widget _driven(Widget child) {
    var out = child;
    for (final run in widget.motion.drivers) {
      out = switch (run.spec.driver) {
        TimelineDriver.scroll => NotificationListener<ScrollNotification>(
          onNotification: (n) {
            run.drive(n.metrics.pixels / run.spec.driverExtent);
            return false;
          },
          child: out,
        ),
        TimelineDriver.drag => GestureDetector(
          behavior: HitTestBehavior.translucent,
          onPanUpdate: (d) {
            final delta = d.delta.dx.abs() > d.delta.dy.abs()
                ? d.delta.dx
                : d.delta.dy;
            final unit = run.spec.durationUs;
            run.drive(run.position / unit + delta / run.spec.driverExtent);
          },
          onPanEnd: (e) {
            final v = e.velocity.pixelsPerSecond;
            final speed = v.dx.abs() > v.dy.abs() ? v.dx : v.dy;
            run.settle(speed / run.spec.driverExtent);
          },
          child: out,
        ),
        TimelineDriver.time => out,
      };
    }
    return out;
  }

  @override
  Widget build(BuildContext context) {
    MotionSource? source;
    final motion = widget.motion;
    // The first build also resolves the node, to read its visibility, the
    // state it reads and the values that animate.
    final wasExiting = _exiting;
    var built = widget.build(
      context,
      ref,
      {..._implicitNow(), ..._timelineValues()},
      _exiting,
      true,
      (s) => source = s,
    );
    final s = source;
    if (s != null) {
      _presenceChanged(s.isVisible);
      _retarget(s);
      if (_exiting && !wasExiting) {
        // The node just became hidden: it stays built while it exits.
        built = widget.build(
          context,
          ref,
          _timelineValues(),
          true,
          false,
          null,
        );
      }
    }
    _first = false;
    final listenables = <Listenable>[
      for (final b in motion.tracks) b.run,
      ?_implicit,
    ];
    Widget out = built;
    if (listenables.isNotEmpty) {
      final merged = Listenable.merge(listenables);
      out = AnimatedBuilder(
        animation: merged,
        builder: (context, _) {
          final values = {..._implicitNow(), ..._timelineValues()};
          if (values.isEmpty) return built;
          return widget.build(context, ref, values, _exiting, false, null);
        },
      );
    }
    out = _present(out);
    out = _driven(out);
    return _hero(out, s);
  }

  Widget _hero(Widget child, MotionSource? source) {
    final tag = source?.heroTag;
    if (tag == null || tag.isEmpty) return child;
    if (_reduce && _reduction == MotionReduction.skip) return child;
    return Hero(tag: tag, child: child);
  }
}
