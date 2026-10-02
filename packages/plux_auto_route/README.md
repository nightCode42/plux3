<!--
SPDX-FileCopyrightText: 2026 Plux contributors
SPDX-License-Identifier: Apache-2.0
-->

# plux_auto_route

Runs [Plux](https://nightcode42.github.io/plux3/) navigation through an
[auto_route](https://pub.dev/packages/auto_route) `RootStackRouter`, for apps that host
Plux pages with [`plux_flutter`](https://pub.dev/packages/plux_flutter):

- Plux pages are pushed through the router, at `/plux/<route-name>`;
- a path that names a Plux page runs the page's guards in the route's `AutoRouteGuard`;
- each shell of the app document is an `AutoTabsRouter`, one stack per tab;
- the router's routes are native routes plugins can open by name, once the app's native
  catalogue declares them. They are opened by path, so they read plugins' parameters as
  path and query parameters; register a route that needs typed arguments in
  `PluxConfig.nativeRoutes`.

## Usage

```dart
final plux = PluxAutoRoutes();
final router = RootStackRouter.build(routes: [
  ...myRoutes,
  ...plux.routes,
  plux.shell('main', tabs: ['home', 'settings']),
]);

await Plux.initialize(PluxConfig(
  appId: 'app_…',
  endpoint: Uri.parse('https://plux.example.com'),
  router: PluxAutoRoute(router),
));

runApp(PluxScope(child: MaterialApp.router(routerConfig: router.config())));
```

Route names starting with `Plux` are reserved for the routes this package adds.

## Licence

Apache-2.0.
