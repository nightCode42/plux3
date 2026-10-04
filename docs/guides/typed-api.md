<!--
SPDX-FileCopyrightText: 2026 Plux contributors
SPDX-License-Identifier: Apache-2.0
-->

# Typed API and Host Setup Guide

Three commands connect an existing Flutter app to Plux and keep both sides honest:
`plux init` sets the app up as a host, `plux codegen` writes the app's typed Dart API into
it, and `plux native scan` and `sync` tell the server what the host offers plugins. Their
flags are in the [CLI reference](../reference/cli.md).

## 1. Set the app up: `plux init`

Run it in the Flutter app's directory, with the server, organisation, app and environment
(`HST-032`):

```sh
plux init --server https://plux.example.com --org acme --app shop --env production
```

It adds `plux_flutter` to `pubspec.yaml`, and the router adapter when the app uses
`go_router` or `auto_route`; writes `plux.yaml`; writes `lib/plux/plux_options.g.dart`, with
the environment's root public keys embedded (`SEC-051`); and calls
`Plux.initialize(PluxOptions.config())` in `lib/main.dart` when `main` only runs the app,
or prints the lines to add when it does more. A second run changes nothing. In a Plux
project, the same command writes `plux.json` instead.

## 2. Write the typed API: `plux codegen`

```sh
plux codegen --host . ../shop-project
```

It compiles the project, without a server, and writes `lib/plux/plux.g.dart` (`HST-030`).
Misusing a route, a component, an event or a state entry is then a compile error in the
host, not a runtime error. The starter app's file shows each part
([`apps/starter/lib/plux/plux.g.dart`](../../apps/starter/lib/plux/plux.g.dart)):

| Generated | Use |
|---|---|
| `PluxScreens.place(name: 'Harbour')` | `.push(context)` opens the page and completes with its typed result; `.view()` shows it inline; `.page()` lists it in a declarative stack |
| `PluxComponents.counterBadge(label: 'Left')` | an exported component as a `PluxView` with typed props |
| `PluxHostEvents.placeShared` | a stream of `PlaceSharedEvent`s, the plugin's typed events |
| `PluxAppState.counter` | the exposed state entry: `value`, `set`, `watch`, typed |
| `PluxFlags.showTips` | a feature flag with its default |

Run it again after the app document changes; an unchanged API leaves the file untouched.
The output is deterministic, so it is committed like any other source.

## 3. Declare what the host offers: `plux native scan` and `sync`

Plugin pages may place the host's widgets as native slots, open its screens and call its
custom actions, all registered in one place at start-up (`HST-031`):

```dart
PluxConfig(
  // …
  nativeRoutes: {'profile': PluxNativeRoute<String, void>(
    params: (json) => json['name']! as String,
    builder: (context, name) => ProfileScreen(name: name))},
  nativeActions: {'sharePlace': PluxNativeAction<String, bool>(
    input: (json) => json['text']! as String, handler: share)},
  nativeSlots: {'MapCard': PluxNativeSlot((context, slot) => MapCard(title: slot['title']! as String))},
)
```

A custom action may not take a built-in action's name, such as `share`: a step of that name
would run the built-in, so publishing refuses the catalogue (`PLX-1124`). List the slot
widget classes in `plux.yaml`, add `plux_native_scan` as a dev dependency, and run:

```sh
plux native scan          # writes plux.catalogue.json by static analysis (CLI-006)
plux native sync          # uploads it for this host build
```

The scanner reads `go_router` and `auto_route` routes, the registrations above and the slot
classes' constructors, with no change to the host's code. Publishing then checks every
plugin against the catalogues of the host builds a release targets; a build that lacks what
a plugin uses keeps the last release that suits it (`WGT-032`, `REL-080`). Pass the build
string the scan used as `PluxConfig.hostBuild`.
