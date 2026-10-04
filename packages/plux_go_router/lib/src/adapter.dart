// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The go_router adapter (ADR-0040, P4 plan A22): Plux navigates through
/// the host's `GoRouter`, and plugins open the router's named routes as
/// native routes (HST-031).
library;

import 'package:flutter/widgets.dart';
import 'package:go_router/go_router.dart';
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_go_router/src/routes.dart';

/// Wraps the host's `GoRouter` for `PluxConfig.router`.
///
/// Its routes must include [PluxGoRoutes.routes]. Plux then pushes its
/// pages through the router, and every `GoRoute` with a `name` is a native
/// route plugins can open, once the native catalogue declares it; a
/// `PluxConfig.nativeRoutes` entry of the same name wins.
///
/// ```dart
/// await Plux.initialize(PluxConfig(..., router: PluxGoRouter(router)));
/// ```
final class PluxGoRouter implements PluxRouterAdapter {
  /// Wraps [router].
  PluxGoRouter(this.router);

  /// The host's router.
  final GoRouter router;

  @override
  late final PluxNavigationDelegate delegate = PluxGoRouterDelegate(router);

  @override
  late final Map<String, PluxNativeRoute<Object?, Object?>> routes = {
    for (final (route, params) in _named(router.configuration.routes, const {}))
      route.name!: _open(route.name!, params),
  };

  @override
  GlobalKey<NavigatorState> get navigatorKey =>
      router.routerDelegate.navigatorKey;

  /// The named routes, each with the parameters of its path and its
  /// parents' paths, which `pushNamed` needs.
  static Iterable<(GoRoute, Set<String>)> _named(
    List<RouteBase> routes,
    Set<String> inherited,
  ) sync* {
    for (final r in routes) {
      final params = r is GoRoute
          ? {
              ...inherited,
              for (final m in _param.allMatches(r.path)) m.group(1)!,
            }
          : inherited;
      if (r is GoRoute && r.name != null) yield (r, params);
      yield* _named(r.routes, params);
    }
  }

  static final _param = RegExp(r':(\w+)');

  /// Opens the route [name]: the parameters of its [path] from the
  /// parameters of the same names, the other parameters that are strings,
  /// numbers or booleans as query parameters, and every parameter, in its
  /// JSON form, as `extra`.
  PluxNativeRoute<Object?, Object?> _open(String name, Set<String> path) =>
      PluxNativeRoute<Object?, Object?>.opened(
        open: (context, params) => router.pushNamed<Object?>(
          name,
          pathParameters: {
            for (final p in path)
              if (params[p] != null) p: '${params[p]}',
          },
          queryParameters: {
            for (final MapEntry(:key, :value) in params.entries)
              if (!path.contains(key) &&
                  (value is String || value is num || value is bool))
                key: '$value',
          },
          extra: params,
        ),
      );
}

/// The navigation delegate on a `GoRouter` (ADR-0040).
///
/// Pages are pushed as locations under `/plux`, or under the shell tab the
/// navigation starts from, so they stay on its stack; the route Plux
/// resolved travels as `extra`, so their parameters never appear in the
/// location. Popping uses the navigator, which go_router follows.
final class PluxGoRouterDelegate implements PluxNavigationDelegate {
  /// Creates the delegate on [router].
  const PluxGoRouterDelegate(this.router);

  /// The host's router.
  final GoRouter router;

  static const _navigator = PluxNavigatorDelegate();

  String _location(BuildContext context, PluxRouteSpec route) {
    final name = Uri.encodeComponent(route.name);
    final branch = PluxGoBranch.maybeOf(context);
    return branch == null
        ? '${PluxGoRoutes.prefix}/$name'
        : '${branch.location}/p/$name';
  }

  @override
  Future<T?> push<T extends Object?>(
    BuildContext context,
    PluxRouteSpec route,
  ) => router.push<T>(_location(context, route), extra: route);

  @override
  Future<T?> replace<T extends Object?>(
    BuildContext context,
    PluxRouteSpec route,
  ) => router.pushReplacement<T>(_location(context, route), extra: route);

  /// Goes to [route] with `GoRouter.go`, which replaces the stack; completes
  /// with null, since `go` returns no result and nothing is left below the
  /// route to return one to.
  @override
  Future<T?> clearAndPush<T extends Object?>(
    BuildContext context,
    PluxRouteSpec route,
  ) {
    router.go(
      '${PluxGoRoutes.prefix}/${Uri.encodeComponent(route.name)}',
      extra: route,
    );
    return Future<T?>.value();
  }

  @override
  void popUntil(BuildContext context, String name) =>
      _navigator.popUntil(context, name);

  @override
  bool pop(BuildContext context, [Object? result]) =>
      _navigator.pop(context, result);
}
