// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The contract between the page host (a page's lease, boundary and
/// timing) and the renderer that turns its section into widgets
/// (ADR-0031).
library;

import 'package:flutter/widgets.dart';
import 'package:plux_flutter/src/core/active_release.dart';
import 'package:plux_flutter/src/navigation/guards.dart';
import 'package:plux_flutter/src/render/sections.dart';
import 'package:plux_flutter/src/state/providers.dart';

/// Builds the widget tree of one page, and runs what entering it needs: its
/// guard graphs and the conversion of text parameters (ADR-0040).
abstract interface class PageRenderer {
  /// Builds [page] of [release] with the route [params]; [routed] says
  /// whether the page owns its route, so that `pop` may pop it. An
  /// embedded page's `pop` reaches [onPop] with its result instead, when
  /// given (ADR-0023). [page] may also be an exported component, whose
  /// props [params] are; its `emitEvent` steps reach [onEvent] with the
  /// payload in its JSON form (SCH-030).
  Widget build(
    BuildContext context,
    ActiveRelease release,
    PageRef page,
    Map<String, Object?> params, {
    bool routed = false,
    void Function(Object? result)? onPop,
    void Function(String event, Object? payload)? onEvent,
  });

  /// Runs guard graph [guard] of [page] over the page's [params], in the
  /// environment [env], and decides with the GuardResult it returns
  /// (NAV-009). [state] gives the roots `app` and `plugin` of a plugin
  /// ('' for the app) a guard may read; without it a guard sees neither.
  Future<GuardOutcome> runGuard(
    ActiveRelease release,
    PageRef page,
    UuidKey guard,
    Map<String, Object?> params,
    PluxEnvironment? env, {
    Map<String, Object?> Function(String plugin)? state,
  });

  /// The parameter names [page] declares.
  Set<String> paramNames(ActiveRelease release, PageRef page);

  /// Text [params], a deep link's or a guard redirect's, converted by the
  /// parameter types of [page] into the form entering it accepts; throws
  /// [FormatException] for a name it does not declare or text that does
  /// not convert.
  Map<String, Object?> textParams(
    ActiveRelease release,
    PageRef page,
    Map<String, String> params,
  );
}
