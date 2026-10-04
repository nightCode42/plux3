// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The auto_route adapter (ADR-0040, P4 plan A22): Plux navigates through
/// the host's `RootStackRouter`, and plugins open the router's routes as
/// native routes (HST-031).
library;

import 'package:auto_route/auto_route.dart';
import 'package:flutter/widgets.dart';
import 'package:plux_auto_route/src/routes.dart';
import 'package:plux_flutter/plux_flutter.dart';

/// Wraps the host's `RootStackRouter` for `PluxConfig.router`.
///
/// Its routes must include [PluxAutoRoutes.routes]. Plux then pushes its
/// pages through the router, and every route of the host's is a native
/// route plugins can open by the route's name, once the native catalogue
/// declares it. Such a route is opened by its path, so it reads plugins'
/// parameters as path and query parameters; a route that needs typed
/// arguments is registered in `PluxConfig.nativeRoutes`, whose entries
/// win.
///
/// ```dart
/// await Plux.initialize(PluxConfig(..., router: PluxAutoRoute(router)));
/// ```
final class PluxAutoRoute implements PluxRouterAdapter {
  /// Wraps [router].
  PluxAutoRoute(this.router);

  /// The host's router.
  final RootStackRouter router;

  @override
  late final PluxNavigationDelegate delegate = PluxAutoRouteDelegate(router);

  @override
  late final Map<String, PluxNativeRoute<Object?, Object?>> routes = {
    for (final (name, path) in _paths(router.routes, '')) name: _open(path),
  };

  @override
  GlobalKey<NavigatorState> get navigatorKey => router.navigatorKey;

  static Iterable<(String, String)> _paths(
    List<AutoRoute> routes,
    String parent,
  ) sync* {
    for (final r in routes) {
      if (r is RedirectRoute ||
          r.name.startsWith(PluxAutoRoutes.reserved) ||
          r.name.startsWith('#')) {
        continue;
      }
      final path = r.path.startsWith('/')
          ? r.path
          : [
              if (parent != '/') parent,
              r.path,
            ].where((p) => p.isNotEmpty).join('/');
      yield (r.name, path);
      yield* _paths(r.children ?? const [], path);
    }
  }

  /// Opens the route at [path]: its `:name` segments from the parameters of
  /// the same names, and the other parameters that are strings, numbers or
  /// booleans as query parameters.
  PluxNativeRoute<Object?, Object?> _open(
    String path,
  ) => PluxNativeRoute<Object?, Object?>.opened(
    open: (context, params) {
      final used = <String>{};
      final segments = [
        for (final s in path.split('/'))
          if (s.startsWith(':'))
            Uri.encodeComponent(
              '${params[used.add(s.substring(1)) ? s.substring(1) : s] ?? ''}',
            )
          else
            s,
      ];
      final query = {
        for (final MapEntry(:key, :value) in params.entries)
          if (!used.contains(key) &&
              (value is String || value is num || value is bool))
            key: '$value',
      };
      final location = Uri(
        path: segments.join('/'),
        queryParameters: query.isEmpty ? null : query,
      );
      return router.pushPath<Object?>(location.toString());
    },
  );
}

/// The navigation delegate on a `StackRouter` (ADR-0040).
///
/// Pages are pushed on the stack the navigation starts from, the stack of a
/// shell's tab included; the route Plux resolved travels as the route's
/// `args`, so their parameters never appear in the path. Popping uses the
/// navigator, which auto_route follows.
final class PluxAutoRouteDelegate implements PluxNavigationDelegate {
  /// Creates the delegate on [router].
  const PluxAutoRouteDelegate(this.router);

  /// The host's router.
  final RootStackRouter router;

  static const _navigator = PluxNavigatorDelegate();

  (StackRouter, PageRouteInfo<Object?>) _target(
    BuildContext context,
    PluxRouteSpec route,
  ) {
    final branch = PluxAutoBranch.maybeOf(context);
    final stack = branch == null
        ? router
        : StackRouterScope.of(context)?.controller ?? router;
    return (
      stack,
      PageRouteInfo<Object?>(
        branch?.routeName ?? PluxAutoRoutes.name,
        args: route,
        rawPathParams: {'route': route.name},
      ),
    );
  }

  @override
  Future<T?> push<T extends Object?>(
    BuildContext context,
    PluxRouteSpec route,
  ) {
    final (stack, info) = _target(context, route);
    return stack.push<T>(info);
  }

  @override
  Future<T?> replace<T extends Object?>(
    BuildContext context,
    PluxRouteSpec route,
  ) {
    final (stack, info) = _target(context, route);
    return stack.replace<T>(info);
  }

  @override
  Future<T?> clearAndPush<T extends Object?>(
    BuildContext context,
    PluxRouteSpec route,
  ) => router.pushAndPopUntil<T>(
    PageRouteInfo<Object?>(
      PluxAutoRoutes.name,
      args: route,
      rawPathParams: {'route': route.name},
    ),
    predicate: (_) => false,
  );

  @override
  void popUntil(BuildContext context, String name) =>
      _navigator.popUntil(context, name);

  @override
  bool pop(BuildContext context, [Object? result]) =>
      _navigator.pop(context, result);
}
