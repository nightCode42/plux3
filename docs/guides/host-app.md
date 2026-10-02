# Host App Guide

How a Flutter app hosts Plux pages with the `plux_flutter` runtime: what to add, how the
runtime starts and syncs, and what the host controls. The runtime renders published
pages in Phase 3; navigation between Plux pages, actions and state writes arrive in
later phases (spec §5). The [starter app](../../apps/starter/README.md) is a complete,
tested host to copy from.

## Requirements

| | Minimum |
|---|---|
| Flutter | 3.47; from then on the latest stable and the previous one (`RT-002`, ADR-0038) |
| Android | API 24 (Android 7.0) |
| iOS | 15 |

**Android.** The runtime downloads over HTTP/2 through Play Services Cronet
(`cronet_http`, `SYN-010`), which brings two libraries that declare one namespace. The
Android Gradle plugin 9, which Flutter 3.47's template uses, refuses that unless the
app's `android/gradle.properties` contains:

```properties
android.uniquePackageNames=false
```

The two libraries are Play Services Cronet's `cronet-api` and `cronet-shared`, both
`org.chromium.net`. This is to be reported to `cronet_http` (dart-lang/http); once a
release fixes it, the line is no longer needed and this guide says so.

A release build also needs the `INTERNET` permission in
`android/app/src/main/AndroidManifest.xml` (Flutter's template adds it to debug and
profile builds only):

```xml
<uses-permission android:name="android.permission.INTERNET"/>
```

## 1. Add the runtime and a baseline

Add `plux_flutter` to the app's `pubspec.yaml`. Then, from the app's directory, write the
release your app should start with — its *baseline* — into its assets with the
[CLI](../reference/cli.md):

```bash
plux pull --env production --channel production   # writes assets/plux/
```

`plux pull` writes every bundle with its signature, the asset files the release uses, and
the environment's root public keys (`keys.json`). List its three directories as assets,
because Flutter's asset directories are not recursive:

```yaml
flutter:
  assets:
    - assets/plux/
    - assets/plux/bundles/
    - assets/plux/assets/
```

With a baseline, the first launch renders offline and syncs a delta from it (`SYN-007`).
The runtime verifies the baseline like any download, under the embedded root keys,
before it loads anything (`SEC-052`, [ADR-0029](../adr/0029-on-device-verification.md)).
An app without a baseline waits for its first sync.

## 2. Initialize

Start the runtime before `runApp`:

```dart
import 'package:flutter/material.dart';
import 'package:plux_flutter/plux_flutter.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  final startup = await Plux.initialize(
    PluxConfig(
      appId: '<your app ID>',
      endpoint: Uri.parse('https://plux.example.com'),
      environment: 'production',
    ),
  );
  runApp(MyApp(pluxReady: startup.ready));
}
```

`Plux.initialize` maps the cached release (or imports the baseline on the first launch)
and returns; the sync of every plugin runs on a background isolate (`SYN-001`). What
`PluxConfig` controls:

| Field | Default | Meaning |
|---|---|---|
| `appId`, `endpoint` | required | the Plux app and the server's base URL |
| `environment`, `channel` | `production` | where the app's releases come from |
| `rootKeys` | the baseline's `keys.json` | the root public keys manifests and bundles are verified with (`SEC-051`) |
| `baseline` | `assets/plux` | the asset directory `plux pull` wrote; `null` for none |
| `startup` | `StartupPolicy.useCacheThenSync()` | render what is on the device at once and sync in the background, or `StartupPolicy.blockUntilSynced(timeout)` to wait for the sync first (`SYN-003`) |
| `activation` | `ActivationPolicy.atSafePoint` | when a staged release replaces the active one: `immediate`, `atSafePoint`, `nextLaunch` or `forced`; never under a visible Plux page (`SYN-004`) |
| `downloadParallelism` | 4 | concurrent downloads (`SYN-010`) |
| `themeMode`, `themeSource`, `brand`, `locale` | system, `host`, none, the device's | how pages look; see [Theming](../reference/theming.md) |
| `consent` | `PluxConsent.necessaryOnly` | which telemetry may be sent; see [Telemetry](../reference/telemetry.md) |
| `hostBuild` | empty | your app's build, as devices report it (at most 64 characters) |
| `onError` | none | called with every problem the runtime reports |
| `fallbackBuilder` | `PluxDefaultFallback`: a neutral panel in the theme's colours, the error code in debug builds | what a page or component that cannot render shows instead (`RT-020`, `RT-022`); a `PluxView` can set its own |
| `pluginFallbackBuilders` | none | a fallback per plugin key, used instead of `fallbackBuilder` for that plugin's pages and components |
| `container`, `parentContainer` | Plux's own | share or nest a Riverpod container ([ADR-0008](../adr/0008-riverpod-runtime-state-engine.md)) |
| `navigatorKey` | none | your `MaterialApp`'s navigator key: where deep links and notifications open their pages (below) |

## 3. Show pages

A `PluxView` shows a page inside a native screen; it needs a `PluxScope` above it, the
provider scope of the runtime:

```dart
PluxScope(
  child: PluxView(
    'welcome',
    params: {'name': 'Ada'},
    loadingBuilder: (_) => const Center(child: CircularProgressIndicator()),
    fallbackBuilder: (_, error) => Text('Not available (${error.code.id})'),
  ),
)
```

`Plux.open(context, 'welcome')` pushes a page full screen on the host's navigator and
wraps it in a scope itself. A page that fails is contained by its error boundary and
shows the fallback; the error is reported, never thrown into the app (`RT-020`). Pages
with guards decide before they open: a guard can redirect, for example to a login page,
or show the fallback ([navigation](../reference/navigation.md#4-guards-nav-009)).

### Deep links and notifications

Pass `navigatorKey` to `PluxConfig` and to your `MaterialApp`, then hand Plux the links
and notifications that name its pages:

```dart
final navigatorKey = GlobalKey<NavigatorState>();
await Plux.initialize(PluxConfig(/* … */, navigatorKey: navigatorKey));
runApp(MaterialApp(navigatorKey: navigatorKey, home: const Home()));

// A link the platform delivered, from your link handling:
final opened = await Plux.handleDeepLink(uri);
// The data payload of a notification the user opened, from your push SDK:
await Plux.handlePushPayload(message.data);
```

Both complete with whether a page opened; a link the app document does not map returns
false and is reported (`PLX-4103`), so your app can handle it. The app document's
`navigation.deepLinks` lists the hosts, custom schemes and path patterns; links of the form
`https://<host>/p/<route-name>?…` always work. Plux ships no push SDK: the payload's
`plux` key, or the key the app document's `push` names, holds
`{"route": …, "params": {…}}`.

The platform must deliver those links to your app. For a host `links.example.com` and a
scheme `acme`, an existing Android app adds intent filters to its activity in
`AndroidManifest.xml`:

```xml
<intent-filter android:autoVerify="true">
  <action android:name="android.intent.action.VIEW" />
  <category android:name="android.intent.category.DEFAULT" />
  <category android:name="android.intent.category.BROWSABLE" />
  <data android:scheme="https" android:host="links.example.com" />
</intent-filter>
<intent-filter>
  <action android:name="android.intent.action.VIEW" />
  <category android:name="android.intent.category.DEFAULT" />
  <category android:name="android.intent.category.BROWSABLE" />
  <data android:scheme="acme" />
</intent-filter>
```

An iOS app adds the Associated Domains entitlement `applinks:links.example.com` and the
scheme `acme` under `CFBundleURLTypes` in `Info.plist`. Verified `https` links also need the
site to publish its `assetlinks.json` (Android) and `apple-app-site-association` (iOS).

## 4. Sync, theme and consent at run time

| Call | Does |
|---|---|
| `Plux.sync()` | syncs now; the result is a `SyncResult` and `.progress` streams `SyncEvent`s (`SYN-002`) |
| `Plux.syncEvents` | every sync's events |
| `PluxSyncTile()` | a ready-made settings tile: status and a sync button |
| `Plux.setThemeMode`, `Plux.setBrand`, `Plux.setLocale` | change how pages look |
| `Plux.setConsent(PluxConsent(analytics: true))` | grant or withdraw consent; withdrawing deletes buffered events of that category |
| `Plux.setUserContext`, `Plux.setAuthDelegate` | the signed-in user: attributes the app document's `userContext` declares, read by guards and expressions as `user.<name>`, and `user.authenticated` from the delegate; later phases' data sources take its token |
| `Plux.diagnostics` | reported problems, the active release and the sync status, for tooling |
| `Plux.dispose()` | stops the runtime |

In debug builds, wrap the app in `PluxDevtools` from `plux_devtools` for an overlay with
the sync status, the active release and the log; it renders nothing in release builds.

## 5. Keep it working

- Pull a new baseline before each store release of the app, so a fresh install starts
  close to current.
- Errors carry codes: the [error catalogue](../reference/errors.md) says what each means
  and what to do.
- [Limits](../reference/limits.md) bound what a release may hold on the device; the
  runtime reads them from the signed app bundle.
