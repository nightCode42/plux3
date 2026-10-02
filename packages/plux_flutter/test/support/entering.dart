// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:plux_flutter/src/core/active_release.dart';
import 'package:plux_flutter/src/navigation/guards.dart';
import 'package:plux_flutter/src/render/sections.dart';
import 'package:plux_flutter/src/state/providers.dart';

/// What a test renderer does on entering a page: every guard graph allows
/// it, parameters keep their text, and no name is declared. Tests of the
/// guards themselves use the real renderer.
mixin AllowsEveryGuard {
  /// Allows.
  Future<GuardOutcome> runGuard(
    ActiveRelease release,
    PageRef page,
    UuidKey guard,
    Map<String, Object?> params,
    PluxEnvironment? env,
  ) => Future.value(const GuardAllows());

  /// None.
  Set<String> paramNames(ActiveRelease release, PageRef page) => const {};

  /// As they are.
  Map<String, Object?> textParams(
    ActiveRelease release,
    PageRef page,
    Map<String, String> params,
  ) => params;
}
