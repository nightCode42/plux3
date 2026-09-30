<!--
SPDX-FileCopyrightText: 2026 Plux contributors
SPDX-License-Identifier: Apache-2.0
-->

# plux_flutter

The runtime of [Plux](https://nightcode42.github.io/plux3/), a server-driven UI and
plugin platform for Flutter. Pages designed in Plux Studio are compiled and signed by the
Plux Server; this package syncs every plugin of your app at start with binary deltas,
verifies every bundle before it reads a byte of it, and renders the pages as native
Material and Cupertino widgets.

- **Sync of all plugins at app start**, on a background isolate, with delta updates,
  resumable downloads, atomic activation and a last-known-good fallback.
- **Verification first**: Ed25519-signed manifests, bundle hashes, anti-rollback and a
  FlatBuffers verifier; nothing is parsed before it is verified.
- **Zero-copy rendering** of memory-mapped bundles, with error boundaries around every
  page and component.
- **Offline from the first launch** with a baseline release embedded in the app.
- Theming from the host's theme, the Plux design tokens or both; telemetry by consent.

## Requirements

Flutter 3.47 or later; Android 7.0 (API 24) or later; iOS 15 or later. On Android, the
app's `android/gradle.properties` needs `android.uniquePackageNames=false` (see the host
app guide).

## Usage

```dart
import 'package:flutter/material.dart';
import 'package:plux_flutter/plux_flutter.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  await Plux.initialize(
    PluxConfig(
      appId: '<your app ID>',
      endpoint: Uri.parse('https://plux.example.com'),
    ),
  );
  runApp(
    const MaterialApp(
      home: Scaffold(body: PluxScope(child: PluxView('welcome'))),
    ),
  );
}
```

Write the app's baseline with `plux pull` and list its directories as assets. The
[host app guide](https://nightcode42.github.io/plux3/guides/host-app/) covers setup,
start-up and activation policies, theming, consent and errors; the
[documentation site](https://nightcode42.github.io/plux3/) covers the rest.

## Licence

Apache-2.0. The package includes zstd (BSD-3-Clause); see `LICENSE`.
