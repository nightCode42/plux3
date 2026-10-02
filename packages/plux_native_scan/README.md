<!--
SPDX-FileCopyrightText: 2026 Plux contributors
SPDX-License-Identifier: Apache-2.0
-->

# plux_native_scan

Builds the native catalogue of a Flutter host app that uses
[Plux](https://nightcode42.github.io/plux3/): the native routes, native slots and custom
actions plugins may use, by static analysis with the Dart analyzer, without changing the
app's code. `plux native scan` runs it; add it as a dev dependency of the host app.

It reads:

- named `GoRoute`s, auto_route `@RoutePage` classes, and `PluxConfig.nativeRoutes`;
- the constructors of the slot widget classes `plux.yaml` lists;
- `PluxConfig.nativeActions`.

and writes `plux.catalogue.json`, deterministically. Types with no document type are
reported with their location and left out, never guessed.

```sh
dart run plux_native_scan --host 1.4.0+52 --slot MapCard --output plux.catalogue.json
```

## Licence

Apache-2.0.
