// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The navigation of the runs a page starts (ADR-0039, ADR-0040): every
/// navigate, present, pop and switch-tab step goes through the router and
/// the navigation delegate, from the page's own context.
library;

import 'dart:async';

import 'package:flutter/widgets.dart';
import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/actions/engine.dart';
import 'package:plux_flutter/src/actions/handlers.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/navigation/delegate.dart';
import 'package:plux_flutter/src/navigation/router.dart';
import 'package:plux_flutter/src/navigation/shell.dart';
import 'package:plux_flutter/src/pxl/types.dart';

/// Navigation from one page.
final class PageNavigator implements RunNavigator {
  /// Creates the navigator of a page. [routed] says whether the page owns
  /// its route, so that `pop` may pop it; [resultType] is the type its
  /// result must have, and [types] the named types it may use.
  PageNavigator({
    required this.router,
    required this.context,
    required this.route,
    required this.routed,
    required this.resultType,
    required this.types,
  });

  /// The router.
  final PluxRouter router;

  /// The page's build context; navigation fails once it is unmounted.
  final BuildContext Function() context;

  /// The page's route, for reports.
  final String route;

  /// Whether the page owns its route.
  final bool routed;

  /// The type of the page's result, or null.
  final String? resultType;

  /// The named types of the page's bundle and the app's.
  final Map<String, NamedType> Function() types;

  BuildContext _context() {
    final c = context();
    if (!c.mounted) {
      throw const ActionError(
        ActionErrorKind.cancelled,
        PluxErrorCode.navigationRefused,
        'the page that started the run is gone',
      );
    }
    return c;
  }

  /// The route for an action's target, once its guards have decided
  /// (NAV-009): compiled routes are a page or a native route, so a name no
  /// page has is a native route the host did not register (PLX-4200;
  /// registration arrives with ADR-0041).
  Future<PluxRouteSpec> _spec(
    String route,
    Map<String, Object?> params, {
    PluxPresentation? presentation,
    bool dismissible = true,
  }) {
    if (router.target(route) == null) {
      throw ActionError(
        ActionErrorKind.custom,
        PluxErrorCode.nativeRouteNotRegistered,
        'no page has the route $route, and the host registers no native route of that name',
      );
    }
    return router.resolve(
      route,
      _json(params),
      presentation: presentation,
      dismissible: dismissible,
    );
  }

  @override
  Future<void> navigate(
    String route,
    Map<String, Object?> params,
    String mode,
    String? until,
  ) async {
    final d = router.delegate;
    if (mode == 'popUntil') {
      d.popUntil(_context(), until ?? route);
      return;
    }
    final spec = await _spec(route, params);
    final c = _context();
    if (!c.mounted) return;
    switch (mode) {
      case 'replace':
        unawaited(d.replace<Object?>(c, spec));
      case 'clearAndPush':
        unawaited(d.clearAndPush<Object?>(c, spec));
      default:
        unawaited(d.push<Object?>(c, spec));
    }
  }

  @override
  Future<Object?> present(
    String route,
    Map<String, Object?> params, {
    required bool sheet,
    required bool dismissible,
  }) async {
    final target = router.target(route);
    final presentation = sheet
        ? PluxPresentation.bottomSheet
        : target?.presentation == PluxPresentation.fullscreenDialog
        ? PluxPresentation.fullscreenDialog
        : PluxPresentation.dialog;
    final spec = await _spec(
      route,
      params,
      presentation: presentation,
      dismissible: dismissible,
    );
    final json = await router.delegate.push<Object?>(_context(), spec);
    return router.typedResult(route, json);
  }

  @override
  void pop(Object? result) {
    if (!routed) {
      throw navigationRefused(
        'an embedded page has no route of its own to pop',
      );
    }
    // A result of the wrong type is reported and the page pops with none,
    // so the opener never receives a value of another type (NAV-003).
    Object? json;
    try {
      json = PluxRouter.checkResult(result, resultType, types());
    } on ActionError catch (e) {
      router.report(e.toException({'route': route}));
    }
    if (!router.delegate.pop(_context(), json)) {
      throw navigationRefused('there is no route to pop');
    }
  }

  @override
  void switchTab(String tab) {
    final shell = PluxShellScope.maybeOf(_context());
    if (shell == null || !shell.select(tab)) {
      throw navigationRefused('no enclosing shell has the tab $tab');
    }
  }

  static Map<String, Object?> _json(Map<String, Object?> params) => {
    for (final e in params.entries) e.key: toJson(e.value),
  };
}
