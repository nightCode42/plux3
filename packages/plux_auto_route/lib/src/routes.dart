// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Plux pages and shells as auto_route routes (NAV-006, ADR-0040): one
/// route for every Plux page, whose guard runs the page's guards when a
/// path names it, and an `AutoTabsRouter` per shell of the app document,
/// one nested stack per tab.
library;

import 'package:auto_route/auto_route.dart';
import 'package:flutter/material.dart';
import 'package:plux_flutter/plux_flutter.dart';

/// The routes that show Plux pages in the host's `RootStackRouter`.
///
/// Plux navigates to a page through `PluxAutoRoute`'s delegate, which has
/// already run the page's guards. A path that names a page, such as
/// `router.pushPath('/plux/account')`, runs them in the route's
/// `AutoRouteGuard`: the page shows what they decide, in the route's
/// place, a redirect's target and a refusal's fallback included.
///
/// ```dart
/// final plux = PluxAutoRoutes();
/// final router = RootStackRouter.build(routes: [
///   ...myRoutes,
///   ...plux.routes,
///   plux.shell('main', tabs: ['home', 'settings']),
/// ]);
/// await Plux.initialize(PluxConfig(..., router: PluxAutoRoute(router)));
/// ```
final class PluxAutoRoutes {
  /// Creates the routes.
  PluxAutoRoutes();

  /// The path of every Plux page: `/plux/<route-name>`, with the page's
  /// parameters as query parameters when the path is written by hand.
  static const String prefix = '/plux';

  /// The name of the route of every Plux page outside a shell.
  static const String name = 'PluxRoute';

  /// The prefix of the names of the routes this package adds; the host's
  /// own route names must not start with it.
  static const String reserved = 'Plux';

  /// The route of every Plux page.
  late final AutoRoute page = _pageRoute(name, '$prefix/:route');

  /// The routes to add to the host's router: [page].
  List<AutoRoute> get routes => [page];

  /// The shell [shell] of the app document as an `AutoTabsRouter` (NAV-005):
  /// one tab per key of [tabs], at `<path>/<tab>`, each a nested stack
  /// that starts at the tab's initial route. [path] is `/<shell>` unless
  /// given. The tabs' labels and icons come from the app document; a tab
  /// it does not declare shows the fallback.
  AutoRoute shell(String shell, {required List<String> tabs, String? path}) {
    if (tabs.isEmpty) {
      throw ArgumentError.value(tabs, 'tabs', 'a shell needs a tab');
    }
    String tabName(String tab) => '$reserved:$shell:$tab';
    return NamedRouteDef(
      name: '$reserved:$shell',
      path: path ?? '/$shell',
      builder: (context, data) => AutoTabsRouter(
        routes: [for (final t in tabs) PageRouteInfo(tabName(t))],
        builder: (context, child) {
          final tabsRouter = AutoTabsRouter.of(context, watch: true);
          return PluxScope(
            child: PluxShell.routed(
              shell,
              body: child,
              currentIndex: tabsRouter.activeIndex,
              onSelect: tabsRouter.setActiveIndex,
            ),
          );
        },
      ),
      children: [
        for (final t in tabs)
          NamedRouteDef(
            name: tabName(t),
            path: t,
            builder: (context, data) => PluxAutoBranch(
              routeName: '${tabName(t)}:page',
              child: const AutoRouter(),
            ),
            children: [
              NamedRouteDef(
                name: '${tabName(t)}:root',
                path: '',
                builder: (context, data) =>
                    PluxScope(child: PluxShellTab(shell, t)),
              ),
              _pageRoute('${tabName(t)}:page', 'p/:route'),
            ],
          ),
      ],
    );
  }

  static AutoRoute _pageRoute(String name, String path) => NamedRouteDef(
    name: name,
    path: path,
    guards: const [_PluxGuard()],
    type: RouteType.custom(customRouteBuilder: _route),
    builder: (context, data) => _Resolving(
      route: data.params.getString('route', ''),
      query: _text(data.queryParams.rawMap),
    ),
  );

  // The route Plux resolved, with its presentation and transition; a page
  // restored without one resolves in its place.
  static Route<T> _route<T>(
    BuildContext context,
    Widget child,
    AutoRoutePage<T> page,
  ) {
    final spec = page.routeData.args;
    if (spec is! PluxRouteSpec) {
      return MaterialPageRoute<T>(settings: page, builder: _follow);
    }
    return PluxRouteSpec(
      name: spec.name,
      builder: _follow,
      presentation: spec.presentation,
      transition: spec.transition,
      dismissible: spec.dismissible,
      arguments: spec.arguments,
    ).toRoute<T>(context, settings: page);
  }

  // The content of the route's current page: auto_route keys a page by its
  // route's name, so a replace updates the route in place with the next
  // page, as its own routes follow theirs.
  static Widget _follow(BuildContext context) {
    final page = ModalRoute.settingsOf(context);
    if (page is! AutoRoutePage) return const SizedBox.shrink();
    final spec = page.routeData.args;
    return spec is PluxRouteSpec ? spec.builder(context) : page.child;
  }

  static Map<String, String> _text(Map<String, dynamic> raw) => {
    for (final MapEntry(:key, :value) in raw.entries)
      if (value != null) key: '$value',
  };
}

/// Runs a Plux page's guards when a path, rather than Plux, opens it.
final class _PluxGuard extends AutoRouteGuard {
  const _PluxGuard();

  @override
  Future<void> onNavigation(
    NavigationResolver resolver,
    StackRouter router,
  ) async {
    // Plux resolved it, through its guards, before pushing it.
    if (resolver.route.args is PluxRouteSpec || !Plux.isInitialized) {
      resolver.next();
      return;
    }
    final spec = await Plux.resolveLocation(
      resolver.route.params.getString('route', ''),
      query: PluxAutoRoutes._text(resolver.route.queryParams.rawMap),
    );
    resolver.overrideNext(args: spec);
  }
}

/// The name of the Plux page route of the shell tab a stack belongs to, so
/// that Plux pages opened from it stay on its stack.
final class PluxAutoBranch extends InheritedWidget {
  /// Marks [child] as the tab whose Plux page route is [routeName].
  const PluxAutoBranch({
    super.key,
    required this.routeName,
    required super.child,
  });

  /// The tab's Plux page route.
  final String routeName;

  /// The enclosing tab, or null outside a shell.
  static PluxAutoBranch? maybeOf(BuildContext context) =>
      context.getInheritedWidgetOfExactType<PluxAutoBranch>();

  @override
  bool updateShouldNotify(PluxAutoBranch old) => old.routeName != routeName;
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
