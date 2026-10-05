<!--
SPDX-FileCopyrightText: 2026 Plux contributors
SPDX-License-Identifier: Apache-2.0
-->

# plux_lottie

Lottie and dotLottie animations for [Plux](https://nightcode42.github.io/plux3/) pages, as an
optional package of [`plux_flutter`](https://pub.dev/packages/plux_flutter): apps that do not
play Lottie do not pay for the [`lottie`](https://pub.dev/packages/lottie) dependency.

```dart
await Plux.initialize(PluxConfig(
  // ...
  nativeSlots: {...PluxLottie.slots},
));
```

Declare the `PluxLottie` slot in the app's native catalogue (`plux native scan`), then use it
in a page. Props: `url` (https, `.json` or `.lottie`), `animate`, `repeat`, `reverse`,
`progress` (0 to 1, bindable to state), `fit`, `width`, `height`, `reduceMotion`
(`skip` shows the last frame, `shorten`, `ignore`). Event: `onCompleted`.
