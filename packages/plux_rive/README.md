<!--
SPDX-FileCopyrightText: 2026 Plux contributors
SPDX-License-Identifier: Apache-2.0
-->

# plux_rive

[Rive](https://rive.app) animations for [Plux](https://nightcode42.github.io/plux3/) pages, as an
optional package of [`plux_flutter`](https://pub.dev/packages/plux_flutter): apps that do not
play Rive do not pay for the [`rive`](https://pub.dev/packages/rive) dependency.

```dart
await Plux.initialize(PluxConfig(
  // ...
  nativeSlots: {...PluxRive.slots},
));
```

Declare the `PluxRive` slot in the app's native catalogue (`plux native scan`), then use it in
a page. Props: `url` (https, a `.riv` file), `artboard`, `stateMachine`, `inputs` (state machine
input names to numbers or booleans, bindable to state), `triggers` (names to any value; the
trigger fires when the value changes), `fit`, `reduceMotion` (`skip` freezes, `ignore` plays).
Events: `onLoaded`, `onFailed`.
