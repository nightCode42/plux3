// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Plux pages and shells as go_router routes (NAV-006, ADR-0040): one
/// `GoRoute` for every Plux page, whose redirect runs the page's guards
/// when a URL names it, and a `StatefulShellRoute` per shell of the app
/// document, one branch per tab.
library;

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';
import 'package:plux_flutter/plux_flutter.dart';

/// The routes that show Plux pages in the host's `GoRouter`.
///
/// Plux navigates to a page through `PluxGoRouter`'s delegate, which has
/// already run the page's guards. A URL that names a page, such as
/// `context.go('/plux/account')`, runs them in the route's redirect: the
/// guards' redirect changes the location to its target, and a refusal
/// shows the page's fallback.
///
/// ```dart
/// final plux = PluxGoRoutes();
/// final router = GoRouter(routes: [
///   ...myRoutes,
///   ...plux.routes,
///   plux.shell('main', tabs: ['home', 'settings']),
/// ]);
/// await Plux.initialize(PluxConfig(..., router: PluxGoRouter(router)));
/// ```
final class PluxGoRoutes {
  /// Creates the routes.
  PluxGoRoutes();

  /// The location of every Plux page: `/plux/<route-name>`, with the page's
  /// parameters as query parameters when the location is written by hand.
  static const String prefix = '/plux';

  // The pages whose guards a redirect has run, by location, until the
  // page is built or the next location is redirected.
  final Map<String, PluxRouteSpec> _decided = {};

  /// The route of every Plux page.
  late final GoRoute page = _pageRoute('$prefix/:route');

  /// The routes to add to the host's `GoRouter`: [page].
  List<RouteBase> get routes => [page];

  /// The shell [shell] of the app document as a `StatefulShellRoute`
  /// (NAV-005): one branch per key of [tabs], at `<path>/<tab>`, which
  /// starts at the tab's initial route and keeps its own stack. [path] is
  /// `/<shell>` unless given. The tabs' labels and icons come from the app
  /// document; a tab it does not declare shows the fallback.
  StatefulShellRoute shell(
    String shell, {
    required List<String> tabs,
    String? path,
  }) {
    if (tabs.isEmpty) {
      throw ArgumentError.value(tabs, 'tabs', 'a shell needs a tab');
    }
    final base = path ?? '/$shell';
    return StatefulShellRoute(
      builder: (context, state, navigationShell) => PluxScope(
        child: PluxShell.routed(
          shell,
          body: navigationShell,
          currentIndex: navigationShell.currentIndex,
          onSelect: (i) => navigationShell.goBranch(
            i,
            initialLocation: i == navigationShell.currentIndex,
          ),
        ),
      ),
      navigatorContainerBuilder: (context, navigationShell, children) =>
          IndexedStack(
            index: navigationShell.currentIndex,
            children: [
              for (var i = 0; i < children.length; i++)
                PluxGoBranch(
                  location: '$base/${tabs[i]}',
                  child: Offstage(
                    offstage: i != navigationShell.currentIndex,
                    child: TickerMode(
                      enabled: i == navigationShell.currentIndex,
                      child: children[i],
                    ),
                  ),
                ),
            ],
          ),
      branches: [
        for (final tab in tabs)
          StatefulShellBranch(
            routes: [
              GoRoute(
                path: '$base/$tab',
                builder: (context, state) =>
                    PluxScope(child: PluxShellTab(shell, tab)),
                routes: [_pageRoute('p/:route')],
              ),
            ],
          ),
      ],
    );
  }

  GoRoute _pageRoute(String path) =>
      GoRoute(path: path, redirect: _redirect, pageBuilder: _build);

  Future<String?> _redirect(BuildContext context, GoRouterState state) async {
    // Plux resolved it, through its guards, before pushing it.
    if (state.extra is PluxRouteSpec) return null;
    final key = _key(state.uri);
    if (_decided.containsKey(key)) return null;
    _decided.clear();
    if (!Plux.isInitialized) return null;
    final route = state.pathParameters['route'] ?? '';
    final spec = await Plux.resolveLocation(
      route,
      query: state.uri.queryParameters,
    );
    if (spec.name == route) {
      _decided[key] = spec;
      return null;
    }
    // A guard redirected: the target, resolved through its own guards,
    // under the same base as the route it replaces.
    final path = state.matchedLocation;
    final target = Uri(
      path: '${path.substring(0, path.lastIndexOf('/') + 1)}${spec.name}',
      queryParameters: _text(spec.arguments),
    );
    _decided[_key(target)] = spec;
    return target.toString();
  }

  Page<void> _build(BuildContext context, GoRouterState state) {
    final extra = state.extra is PluxRouteSpec
        ? state.extra! as PluxRouteSpec
        : null;
    return _PluxGoPage(
      // go_router keys a page by its route's pattern when it goes to a
      // location, which every Plux page shares: the key adds the location
      // and the route Plux resolved, so each entry gets its own route.
      key: ValueKey<Object>((state.pageKey.value, state.uri.toString(), extra)),
      route: state.pathParameters['route'] ?? '',
      query: state.uri.queryParameters,
      spec: extra ?? _decided.remove(_key(state.uri)),
    );
  }

  static String _key(Uri uri) {
    final query = uri.queryParameters.entries.toList()
      ..sort((a, b) => a.key.compareTo(b.key));
    return Uri(
      path: uri.path,
      queryParameters: query.isEmpty ? null : Map.fromEntries(query),
    ).toString();
  }

  static Map<String, String>? _text(Object? params) {
    if (params is! Map<String, Object?>) return null;
    final text = <String, String>{
      for (final MapEntry(:key, :value) in params.entries)
        if (value is String || value is num || value is bool) key: '$value',
    };
    return text.isEmpty ? null : text;
  }
}

/// The location of the shell tab a navigator belongs to, so that Plux
/// pages opened from it stay on its stack.
final class PluxGoBranch extends InheritedWidget {
  /// Marks [child] as the tab at [location].
  const PluxGoBranch({super.key, required this.location, required super.child});

  /// The tab's location, `<path>/<tab>`.
  final String location;

  /// The enclosing tab, or null outside a shell.
  static PluxGoBranch? maybeOf(BuildContext context) =>
      context.getInheritedWidgetOfExactType<PluxGoBranch>();

  @override
  bool updateShouldNotify(PluxGoBranch old) => old.location != location;
}

/// A Plux page in go_router's stack. Its route is created once per page
/// key: from the route Plux resolved, or, for a location no guard decision
/// came with, such as a restored one, by resolving it in the page's place.
final class _PluxGoPage extends Page<void> {
  const _PluxGoPage({
    required super.key,
    required this.route,
    required this.query,
    required this.spec,
  }) : super(name: route);

  final String route;
  final Map<String, String> query;
  final PluxRouteSpec? spec;

  @override
  Route<void> createRoute(BuildContext context) {
    final resolved = spec;
    if (resolved != null) {
      return resolved.toRoute<void>(context, settings: this);
    }
    return MaterialPageRoute<void>(
      settings: this,
      builder: (_) => _Resolving(route: route, query: query),
    );
  }
}

final class _Resolving extends StatefulWidget {
  const _Resolving({required this.route, required this.query});

  final String route;
  final Map<String, String> query;

  @override
  State<_Resolving> createState() => _ResolvingState();
}

final class _ResolvingState extends State<_Resolving> {
  late final Future<PluxRouteSpec>? _spec = Plux.isInitialized
      ? Plux.resolveLocation(widget.route, query: widget.query)
      : null;

  @override
  Widget build(BuildContext context) => FutureBuilder<PluxRouteSpec>(
    future: _spec,
    builder: (context, snapshot) =>
        snapshot.data?.builder(context) ?? const SizedBox.shrink(),
  );
}
