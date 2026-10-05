// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Interpolation of the values that animate: numbers and colours
/// (ANI-001, ANI-002).
library;

import 'dart:ui' show Color;

import 'package:plux_flutter/src/pxl/values.dart';

/// Whether [v] is a value that animates: a number or a colour.
bool isAnimatable(Object? v) => v is num || v is PxlColor;

/// Whether [v] animates without being told to: decimals and integers are
/// ambiguous with enum IDs, so only doubles and colours qualify.
bool animatesImplicitly(Object? v) => v is double || v is PxlColor;

/// The value [t] of the way from [a] to [b], or [b] when the two cannot be
/// mixed. Two integers give an integer; [t] may leave 0..1 for curves that
/// overshoot.
Object? lerpValue(Object? a, Object? b, double t) {
  if (a is int && b is int) return (a + (b - a) * t).round();
  if (a is num && b is num) return a + (b - a) * t;
  if (a is PxlColor && b is PxlColor) {
    final c = Color.lerp(_color(a), _color(b), t.clamp(0.0, 1.0));
    return c == null ? b : _pxl(c);
  }
  return b;
}

Color _color(PxlColor c) {
  final v = c.rgba;
  return Color.fromARGB(
    v & 0xff,
    (v >> 24) & 0xff,
    (v >> 16) & 0xff,
    (v >> 8) & 0xff,
  );
}

PxlColor _pxl(Color c) {
  final a = (c.a * 255).round() & 0xff;
  final r = (c.r * 255).round() & 0xff;
  final g = (c.g * 255).round() & 0xff;
  final b = (c.b * 255).round() & 0xff;
  return PxlColor((r << 24) | (g << 16) | (b << 8) | a);
}
