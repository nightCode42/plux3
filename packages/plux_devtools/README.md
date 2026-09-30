<!--
SPDX-FileCopyrightText: 2026 Plux contributors
SPDX-License-Identifier: Apache-2.0
-->

# plux_devtools

A debug overlay for Flutter apps that host [Plux](https://nightcode42.github.io/plux3/)
pages with [`plux_flutter`](https://pub.dev/packages/plux_flutter): the sync status with
a sync button, the active release and its plugins, and the problems the runtime reported.
It uses only `plux_flutter`'s public diagnostics and renders nothing in release builds.

## Usage

Wrap the app's content, for example in `MaterialApp.builder`:

```dart
MaterialApp(
  builder: (context, child) => PluxDevtools(child: child!),
  home: const HomeScreen(),
)
```

## Licence

Apache-2.0.
