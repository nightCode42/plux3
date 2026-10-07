// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The curves a bundle names by permanent ID (ANI-001, ANI-002). The
/// compiler's table lists the same curves in the same order.
library;

import 'package:flutter/animation.dart';

/// The curves by permanent ID minus one: linear, easeIn, easeOut,
/// easeInOut, fastOutSlowIn, decelerate, bounceIn, bounceOut, elasticOut
/// and overshoot.
const List<Curve> animationCurves = [
  Curves.linear,
  Curves.easeIn,
  Curves.easeOut,
  Curves.easeInOut,
  Curves.fastOutSlowIn,
  Curves.decelerate,
  Curves.bounceIn,
  Curves.bounceOut,
  Curves.elasticOut,
  Curves.easeOutBack,
];

/// The curve with permanent [id]; linear for 0 (none named) and for an ID
/// this runtime does not know.
Curve curveById(int id) => id >= 1 && id <= animationCurves.length
    ? animationCurves[id - 1]
    : Curves.linear;
