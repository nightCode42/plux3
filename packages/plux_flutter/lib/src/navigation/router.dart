// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Plux's route resolution (ADR-0040): an app-wide name becomes a page of
/// the active release, with how it is presented, how it moves in and the
/// type it returns, or the not-found page (NAV-011); results cross the
/// navigator in their JSON form and are typed again where they arrive
/// (NAV-003).
library;

import 'package:flutter/material.dart';
import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/core/active_release.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/native_catalogue/host.dart';
import 'package:plux_flutter/src/native_catalogue/registration.dart';
import 'package:plux_flutter/src/navigation/delegate.dart';
import 'package:plux_flutter/src/navigation/guards.dart';
import 'package:plux_flutter/src/pxl/types.dart';
import 'package:plux_flutter/src/verify/bundle_verifier.dart';

/// A page a name resolves to.
final class RouteTarget {
  /// Creates a target.
  const RouteTarget({
    required this.page,
    this.presentation = PluxPresentation.page,
    this.transition = PluxTransition.platform,
    this.resultType,
  });

  /// The page.
  final PageRef page;

  /// How its page kind shows it.
  final PluxPresentation presentation;

  /// Its transition.
  final PluxTransition transition;

  /// The type expression of its result, or null when it returns none.
  final String? resultType;
}

/// Builds the content of a routed page: the page host of `PluxView`,
/// marked as owning its route so that `pop` may pop it, and as [guarded]
/// when its guards already decided the entry (NAV-009).
typedef RoutedPageBuilder = Widget Function(
  String route,
  Map<String, Object?> params, {
  required bool guarded,
});

/// A native route checked for opening: its registration, its result type
/// and its parameters in the JSON form host code receives.
typedef NativeEntry = ({
  String name,
  PluxNativeRoute<Object?, Object?> route,
  String? result,
  Map<String, NamedType> types,
  Map<String, Object?> params,
});

/// Builds the fallback of a page of [plugin] that cannot be shown.
typedef RouteFallbackBuilder = Widget Function(
  BuildContext context,
  PluxException error,
  String plugin,
);

/// Resolves names and builds the routes the navigation delegate shows.
final class PluxRouter {
  /// Creates a router over the release [release] gives, reporting with
  /// [report].
  PluxRouter({
    required this.release,
    required this.delegate,
    required this.page,
    required this.types,
    required this.report,
    required this.guards,
    required this.fallback,
    this.notFoundBuilder,
    this.nativeRoutes = const {},
    this.natives = _noNatives,
  });

  static NativeDeclarations? _noNatives() => null;

  /// The host's native routes: those it registers and those its router
  /// adapter discovers (NAV-002, HST-031).
  final Map<String, PluxNativeRoute<Object?, Object?>> nativeRoutes;

  /// The active release's native catalogue, or null before one.
  final NativeDeclarations? Function() natives;

  /// The active release, or null before the first one.
  final ActiveRelease? Function() release;

  /// The seam every navigation goes through.
  final PluxNavigationDelegate delegate;

  /// Builds a routed page.
  final RoutedPageBuilder page;

  /// The named types a page of plugin [plugin] may return: the app's and
  /// the plugin's.
  final Map<String, NamedType> Function(ActiveRelease release, String plugin)
  types;

  /// Reports a problem.
  final void Function(PluxException error) report;

  /// Decides each entry (NAV-009).
  final RouteGuards guards;

  /// Builds the fallback of a refused route.
  final RouteFallbackBuilder fallback;

  /// The host's not-found page, used when the app names none.
  final Widget Function(BuildContext context, String route)? notFoundBuilder;

  /// The page [name] resolves to in the active release, or null.
  RouteTarget? target(String name) {
    final r = release();
    if (r == null) return null;
    final PageRef? ref;
    try {
      ref = r.page(name);
    } on PluxException catch (e) {
      report(e);
      return null;
    }
    if (ref == null) return null;
    if (ref.section.data.length > SectionGate.uiIsolateLimit &&
        !r.gate.passed(ref.section)) {
      // Large sections are checked off the UI isolate (L-6): until then
      // the page shows as a screen, and its host checks it.
      return RouteTarget(page: ref);
    }
    try {
      r.gate.check(ref.section);
      final p = fbs.Page(ref.section.data);
      final strings = p.strings ?? const <String>[];
      String? at(int i) => i > 0 && i < strings.length ? strings[i] : null;
      return RouteTarget(
        page: ref,
        presentation: switch (p.kind) {
          fbs.PageKind.Dialog => PluxPresentation.dialog,
          fbs.PageKind.BottomSheet => PluxPresentation.bottomSheet,
          fbs.PageKind.FullscreenDialog => PluxPresentation.fullscreenDialog,
          _ => PluxPresentation.page,
        },
        transition: PluxTransition.parse(at(p.transition)),
        resultType: at(p.result),
      );
    } on PluxException {
      // The page host checks the section again and shows its fallback.
      return RouteTarget(page: ref);
    }
  }

  /// The route for [name] with [params]: its page, presented as
  /// [presentation] or as its page kind says; for a name no page has, the
  /// not-found page (NAV-011), reported with `PLX-4100`.
  PluxRouteSpec spec(
    String name,
    Map<String, Object?> params, {
    PluxPresentation? presentation,
    bool dismissible = true,
    bool guarded = false,
  }) {
    final t = target(name);
    if (release() == null) {
      // No release yet: the page waits for the first one, as PluxView
      // does; only a release can say that a route does not exist.
      return PluxRouteSpec(
        name: name,
        arguments: params,
        presentation: presentation ?? PluxPresentation.page,
        dismissible: dismissible,
        builder: (_) => page(name, params, guarded: false),
      );
    }
    if (t != null) {
      return PluxRouteSpec(
        name: name,
        arguments: params,
        presentation: presentation ?? t.presentation,
        transition: t.transition,
        dismissible: dismissible,
        builder: (_) => page(name, params, guarded: guarded),
      );
    }
    report(
      PluxException(
        PluxErrorCode.routeNotFound,
        'no page has the route $name',
        details: {'route': name},
      ),
    );
    return notFound(name);
  }

  /// The not-found page shown for [name]: the app's `navigation.notFound`
  /// route, else the host's builder, else Plux's own page.
  PluxRouteSpec notFound(String name) {
    final declared = release()?.meta('').notFoundRoute;
    if (declared != null && declared.isNotEmpty && target(declared) != null) {
      return PluxRouteSpec(
        name: declared,
        builder: (_) => page(declared, const {}, guarded: false),
      );
    }
    return PluxRouteSpec(
      name: name,
      builder: (c) =>
          notFoundBuilder?.call(c, name) ?? PluxNotFoundPage(route: name),
    );
  }

  /// The route for an entry of [name] with [params], once its guards have
  /// decided (NAV-009): the route they enter, which may be a redirect's
  /// target, or the fallback of the route they refuse, presented as
  /// [presentation] or as the entered page's kind says.
  Future<PluxRouteSpec> resolve(
    String name,
    Map<String, Object?> params, {
    PluxPresentation? presentation,
    bool dismissible = true,
  }) async {
    final verdict = await guards.decide(name, params);
    switch (verdict) {
      case GuardEnter(:final route, :final params):
        return spec(
          route,
          params,
          presentation: presentation,
          dismissible: dismissible,
          guarded: true,
        );
      case GuardRefused(:final route, :final plugin, :final reason):
        return PluxRouteSpec(
          name: route,
          presentation:
              presentation ??
              target(route)?.presentation ??
              PluxPresentation.page,
          dismissible: dismissible,
          builder: (c) => fallback(c, reason, plugin),
        );
    }
  }

  /// Opens [name] for the host (NAV-003): runs its guards, then pushes the
  /// route they enter, or presents it as its page kind says, and completes
  /// with its result in JSON form when it is a [T]; another value is
  /// reported and completes with null.
  Future<T?> open<T extends Object?>(
    BuildContext context,
    String name,
    Map<String, Object?> params,
  ) async {
    final route = await resolve(name, params);
    if (!context.mounted) return null;
    final result = await delegate.push<Object?>(context, route);
    if (result == null || result is T) return result as T?;
    report(
      PluxException(
        PluxErrorCode.actionValueInvalid,
        'route $name returned a ${result.runtimeType}, not a $T',
        details: {'route': name},
      ),
    );
    return null;
  }

  /// The native route [name] with [params] checked against the catalogue
  /// before host code sees them (NAV-002, ADR-0041). Throws an
  /// [ActionError]: `PLX-4200` for a route the host does not register,
  /// a `validation` error for parameters the catalogue does not accept.
  NativeEntry checkNative(String name, Map<String, Object?> params) {
    final route = nativeRoutes[name];
    if (route == null) {
      throw ActionError(
        ActionErrorKind.custom,
        PluxErrorCode.nativeRouteNotRegistered,
        'no page has the route $name, and the host registers no native route of that name',
      );
    }
    final d = natives();
    final decl = d?.routes[name];
    if (d == null || decl == null) {
      throw ActionError.validation(
        'the native catalogue declares no route $name',
      );
    }
    return (
      name: name,
      route: route,
      result: decl.result,
      types: d.types,
      params: toHostValues(decl.params, params, d.types, 'native route $name'),
    );
  }

  /// Opens a checked native route; completes with its result as the PXL
  /// value of the catalogue's result type, checked too, or null. A host
  /// failure or a result of another type throws an [ActionError].
  Future<Object?> openNative(
    BuildContext context,
    NativeEntry entry, {
    PluxPresentation presentation = PluxPresentation.page,
  }) async {
    final Object? result;
    try {
      result = await entry.route.openWith(
        context,
        delegate,
        entry.name,
        entry.params,
        presentation: presentation,
      );
    } on ActionError {
      rethrow;
    } on Object catch (e) {
      // Only the exception's type: its message may hold the user's data.
      throw ActionError(
        ActionErrorKind.custom,
        PluxErrorCode.hostCodeFailed,
        'native route ${entry.name} failed with ${e.runtimeType}',
      );
    }
    return fromHostValue(
      entry.result,
      result,
      entry.types,
      'the result of native route ${entry.name}',
    );
  }

  /// The result [json] that route [name] popped with, as the PXL value of
  /// its declared type; null, reported, when it does not have that type.
  Object? typedResult(String name, Object? json) {
    final r = release();
    final t = target(name);
    final type = t?.resultType;
    if (json == null || r == null || t == null || type == null) return null;
    try {
      final all = types(r, t.page.plugin);
      return fromJson(PxlType.parse(type, (n) => all[n]), json);
    } on FormatException catch (e) {
      report(
        PluxException(
          PluxErrorCode.actionValueInvalid,
          'route $name returned a value that is not a $type: ${e.message}',
          details: {'route': name},
        ),
      );
      return null;
    }
  }

  /// [value], a page's result in PXL form, in its JSON form when it has
  /// the page's declared [type]; otherwise an [ActionError] (NAV-003).
  static Object? checkResult(
    Object? value,
    String? type,
    Map<String, NamedType> types,
  ) {
    if (value == null) return null;
    if (type == null) {
      throw const ActionError.validation('the page declares no result type');
    }
    final json = toJson(value);
    try {
      fromJson(PxlType.parse(type, (n) => types[n]), json);
    } on FormatException catch (e) {
      throw ActionError.validation('the result is not a $type: ${e.message}');
    }
    return json;
  }
}

/// Plux's own not-found page (NAV-011): what is shown for an unknown
/// route when neither the app nor the host names one.
final class PluxNotFoundPage extends StatelessWidget {
  /// Creates the page for [route].
  const PluxNotFoundPage({super.key, required this.route});

  /// The unknown route.
  final String route;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Scaffold(
      appBar: AppBar(),
      body: Center(
        child: Semantics(
          label: 'Page not found',
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(Icons.explore_off_outlined, color: theme.disabledColor),
              const SizedBox(height: 12),
              Text('Page not found', style: theme.textTheme.titleMedium),
            ],
          ),
        ),
      ),
    );
  }
}
