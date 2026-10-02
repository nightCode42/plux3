<!--
SPDX-FileCopyrightText: 2026 Plux contributors
SPDX-License-Identifier: Apache-2.0
-->

# plux_go_router

Runs [Plux](https://nightcode42.github.io/plux3/) navigation through a
[go_router](https://pub.dev/packages/go_router) `GoRouter`, for apps that host Plux pages
with [`plux_flutter`](https://pub.dev/packages/plux_flutter):

- Plux pages are pushed through the router, at `/plux/<route-name>`;
- a location that names a Plux page runs the page's guards as the route's redirect;
- each shell of the app document is a `StatefulShellRoute`, one stack per tab;
- the router's named `GoRoute`s are native routes plugins can open, once the app's
  native catalogue declares them.

## Usage

```dart
final plux = PluxGoRoutes();
final router = GoRouter(routes: [
  ...myRoutes,
  ...plux.routes,
  plux.shell('main', tabs: ['home', 'settings']),
]);

await Plux.initialize(PluxConfig(
  appId: 'app_…',
  endpoint: Uri.parse('https://plux.example.com'),
  router: PluxGoRouter(router),
));

runApp(PluxScope(child: MaterialApp.router(routerConfig: router)));
```

Plux pages keep their parameters out of the location: the route Plux resolved travels as
`extra`. Deep links open with `Plux.handleDeepLink`, on the router's navigator.

## Licence

Apache-2.0.
