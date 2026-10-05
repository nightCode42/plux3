// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// A page transition made of a route timeline (NAV-010): the timeline's
/// tracks move the page by its route animation, so a pop plays it backwards.
library;

import 'package:flutter/widgets.dart';
import 'package:plux_flutter/src/animation/registry.dart';
import 'package:plux_flutter/src/animation/timeline_spec.dart';

/// The route props a route timeline animates, by the IDs the compiler
/// assigns: opacity, scale, and the slide as a fraction of the page.
abstract final class RouteProps {
  /// The page's opacity.
  static const int opacity = 1;

  /// The page's scale.
  static const int scale = 2;

  /// The horizontal slide, in page widths.
  static const int slideX = 3;

  /// The vertical slide, in page heights.
  static const int slideY = 4;
}

/// Builds the transition of [timeline] over the route's [animation]
/// (0 at rest below, 1 on top). Under reduce motion, a timeline that skips
/// shows the page at once and one that shortens plays four times as fast
/// (ANI-007).
Widget buildRouteTimeline(
  BuildContext context,
  TimelineSpec timeline,
  Animation<double> animation,
  Widget child,
) {
  final reduce = MediaQuery.maybeDisableAnimationsOf(context) ?? false;
  if (reduce && timeline.reduction == MotionReduction.skip) return child;
  final speed = reduce && timeline.reduction == MotionReduction.shorten
      ? shortenFactor
      : 1.0;
  return AnimatedBuilder(
    animation: animation,
    child: child,
    builder: (context, child) {
      final t = (animation.value * speed).clamp(0.0, 1.0) * timeline.durationUs;
      double? at(int prop) {
        for (final tr in timeline.tracks) {
          if (tr.prop == prop) {
            final v = tr.valueAt(t);
            return v is num ? v.toDouble() : null;
          }
        }
        return null;
      }

      Widget out = child!;
      final slideX = at(RouteProps.slideX), slideY = at(RouteProps.slideY);
      if (slideX != null || slideY != null) {
        out = FractionalTranslation(
          translation: Offset(slideX ?? 0, slideY ?? 0),
          child: out,
        );
      }
      final scale = at(RouteProps.scale);
      if (scale != null) out = Transform.scale(scale: scale, child: out);
      final opacity = at(RouteProps.opacity);
      if (opacity != null) {
        out = Opacity(opacity: opacity.clamp(0.0, 1.0), child: out);
      }
      return out;
    },
  );
}
