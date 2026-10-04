# Host App Guide

How a Flutter app hosts Plux pages with the `plux_flutter` runtime: what to add, how the
runtime starts and syncs, and what the host controls. The runtime renders published pages
(Phase 3), and navigates between them and the host's screens, guards them and runs the
actions of Phase 4; the remaining actions and plugin-side state writes arrive in Phase 5
(spec §5). The [starter app](../../apps/starter/README.md) is a complete, tested host to
copy from. Plugin authors' side of navigation is in the [routing guide](routing.md);
`plux init`, `plux codegen` and the native catalogue in the
[typed API guide](typed-api.md); store projects for no-code apps in the
[no-code apps guide](no-code-apps.md).

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

From the app's directory, `plux init` sets it up: it adds `plux_flutter` (and the router
adapter of a `go_router` or `auto_route` app) to `pubspec.yaml`, writes `plux.yaml` and
`lib/plux/plux_options.g.dart` with your environment's root keys, and calls
`Plux.initialize` in `main.dart` when `main` only runs the app
([CLI reference](../reference/cli.md)):

```bash
plux init --server https://plux.example.com --org my-org --app my-app --env production
```

Then write the release your app should start with — its *baseline* — into its assets:

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

Start the runtime before `runApp`. `plux init` writes this for you with
`PluxOptions.config()`; by hand:

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

A `PluxView` shows a page, or a component a plugin exports, inside a native screen, by
name only ([ADR-0023](../adr/0023-mixed-screens-slots-and-plux-view.md)); it needs a
`PluxScope` above it, the provider scope of the runtime:

```dart
PluxScope(
  child: PluxView(
    'welcome',                       // a route, else an exported component's key
    inputs: {'name': 'Ada'},         // the page's parameters or the component's props
    onEvent: (event) => debugPrint('${event.name} ${event.payload}'),
    sizing: PluxViewSizing.intrinsic,
    loadingBuilder: (_) => const Center(child: CircularProgressIndicator()),
    fallbackBuilder: (_, error) => Text('Not available (${error.code.id})'),
  ),
)
```

- **Inputs** are checked on entry like route parameters (`PLX-4101`).
- **Events.** An embedded page has no route of its own, so its `pop` arrives as a
  `PluxViewEvent` named `pop`, with the page's checked result.
- **Sizing.** `PluxViewSizing.intrinsic` (the default) sizes the view to its content,
  `PluxViewSizing.fixed(size)` gives it a size, and `PluxViewSizing.expand` fills the
  incoming constraints, which must be bounded: in a scrollable it shows the fallback and
  reports `PLX-4001`.
- Any number of views share one runtime, its state and its caches; each has its own error
  boundary. A name that is neither a route nor an exported component reports `PLX-4100`.

`Plux.open(context, 'welcome')` pushes a page full screen on the host's navigator and
wraps it in a scope itself. A page that fails is contained by its error boundary and
shows the fallback; the error is reported, never thrown into the app (`RT-020`). Pages
with guards decide before they open: a guard can redirect, for example to a login page,
or show the fallback ([navigation](../reference/navigation.md#4-guards-nav-009)).

**Typed routes.** `plux codegen <project-dir>` writes `lib/plux/plux.g.dart`, your app's
typed API (`HST-030`): `PluxScreens.loanCalculator(productId: 'p-12').push(context)`
completes with the page's typed result, `PluxComponents`, `PluxHostEvents`,
`PluxAppState` and `PluxFlags` are typed views of the rest, and misuse is a compile error.
Run it again after the app document changes.

### Native routes, slots and actions

Plugins can use what your app already has: its screens as **native routes**, its widgets
as **native slots** inside plugin pages, and its functions as **custom actions**. Register
them once, in `PluxConfig`, without changing them:

```dart
PluxConfig(
  /* … */
  nativeRoutes: {
    'profile': PluxNativeRoute<ProfileParams, bool>(
      params: ProfileParams.fromJson,
      builder: (context, p) => ProfileScreen(userId: p.userId),
    ),
  },
  nativeSlots: {
    'MapCard': PluxNativeSlot(
      (context, slot) => MapCard(
        zoom: slot['zoom']! as double,
        onPan: (offset) => slot.emit('onPan', offset),
      ),
    ),
  },
  nativeActions: {
    'openScanner': PluxNativeAction<ScanIn, String>(
      input: ScanIn.fromJson,
      handler: (scan) => scanner.scan(prompt: scan.prompt),
    ),
  },
)
```

Plugins compile against your app's native catalogue (`plux native scan` writes it), and
every value crossing into your code is checked against it first, and every value your
code returns before a plugin sees it. A route's screen returns its result with
`Navigator.pop`. Something a plugin uses that your build does not register fails safely
(`PLX-4200`–`PLX-4202`), and an exception in your code is reported with its type only
(`PLX-4205`).

Build the catalogue and upload it for each build you ship:

```sh
plux native scan                  # writes plux.catalogue.json
plux native sync -C ../my-plux-project   # uploads it for the pubspec.yaml version
```

`plux native scan` needs `plux_native_scan` as a dev dependency of your app; until it is
published, depend on it from this repository (a `git` dependency with `path:
packages/plux_native_scan`). List your slot widgets in `plux.yaml` and pass the build to `PluxConfig` as `hostBuild`
(the version in `pubspec.yaml`, such as `1.4.0+52`, unless you choose another). A release
that uses something a build lacks is flagged at publish, and that build's devices keep the
newest release they can run ([CLI reference](../reference/cli.md)).

### Shared state and plugin events

An app document `state` entry marked `exposed` is shared by your code and every plugin
view and slot on screen ([ADR-0023](../adr/0023-mixed-screens-slots-and-plux-view.md)).
Address it by name:

```dart
final cart = Plux.state<int>('cartCount');
cart.value;                                   // the current value
await cart.set(3);                            // every view reading it rebuilds in the same frame
cart.watch().listen((count) => badge(count)); // one value per change
```

- A write is checked against the entry's declared type. A value of another type, a name
  the app does not expose, or a write before the first release is active is refused:
  `set` completes with `false`, the entry keeps its value and `PLX-4203` is reported.
- Values cross in their JSON form, as inputs do: strings, numbers, booleans, lists and
  maps; `DateTime`, `Duration` and `Color` are accepted where the type asks for them.
- In Phase 4 plugins read exposed state; their own writes (`setState`) and persistence
  arrive in P5. An exposed entry that declares a persistence is kept in memory and
  reported once in debug builds (`PLX-4010`).

Plugins send your app typed events with `emitHostEvent`, declared in the app document's
`hostEvents`. `Plux.events` streams them all, and `Plux.eventsNamed('checkout')` those of
one name. `Plux.flag<bool>('newCheckout')` reads a feature flag of the active release, as
plugins read `flags.newCheckout`.

### Apps that use go_router or auto_route

Add Plux's routes to your router and pass the router through its adapter; your existing
routes become native routes plugins can open, with no registration
([navigation](../reference/navigation.md) §7):

```dart
final plux = PluxGoRoutes();
final router = GoRouter(routes: [
  ...myRoutes,
  ...plux.routes,
  plux.shell('main', tabs: ['home', 'settings']),
]);
await Plux.initialize(PluxConfig(..., router: PluxGoRouter(router)));
```

With `auto_route`, use `PluxAutoRoutes` and `PluxAutoRoute(router)` from `plux_auto_route`
the same way. Deep links then open on the router's navigator, with no `navigatorKey`.

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

### Native Android and iOS apps (add-to-app)

An app written in Kotlin or Swift embeds Plux through a Flutter module (`HST-033`).
[`apps/add_to_app`](../../apps/add_to_app/README.md) is a working example with both hosts
and their UI tests; in outline:

1. Create the module (`flutter create -t module`), add `plux_flutter`, and pull a baseline
   into its assets as in §1. Its `main` runs its widget tree at once — a `MaterialApp`
   whose `navigatorKey` it also gives `PluxConfig`, wrapped in a `PluxScope` once Plux has
   started — and then starts Plux, with settings the host passes over a method channel or
   that the module is built with: an Android `FlutterActivity` draws nothing until
   Flutter's first frame, so that frame should not wait on the start. It leaves semantics to
   the platform, which turns them on as each native view attaches when a screen reader
   runs: a `SemanticsBinding.instance.ensureSemantics()` handle held by the module keeps a
   view attached later from receiving the semantics tree.
2. Let the native side ask for pages by route: the module's channel handler calls
   `Plux.open(navigatorKey.currentContext!, route)` and, once the popped page has left the
   screen, `SystemNavigator.pop()`, which finishes a `FlutterActivity`, takes a
   `FlutterFragment`'s activity back and dismisses a `FlutterViewController`. The page
   leaves the widget tree when its exit transition ends (`TransitionRoute.completed`,
   through a `NavigatorObserver`), and that takes frames, which the engine stops once the
   host's screen closes: a page still mounted would keep a staged release from activating.
3. Register the host's native screens as `PluxNativeRoute.opened` routes that ask the host
   over the same channel, so plugin pages open them with `navigate`.
4. **Android:** `flutter pub get` in the module writes `.android/include_flutter.groovy`.
   Apply it in the host's `settings.gradle.kts`, depend on `project(":flutter")`, and set
   `android.newDsl=false`, `android.builtInKotlin=false` and
   `android.uniquePackageNames=false` in `gradle.properties`, as Flutter's own templates do.
   Create one `FlutterEngine`, run the module's entry point, put it in
   `FlutterEngineCache`, and open pages with `FlutterActivity.withCachedEngine(id)` or a
   `FlutterFragment.withCachedEngine(id).shouldAutomaticallyHandleOnBackPressed(true)`.
5. **iOS:** load the module's `.ios/Flutter/podhelper.rb` in the `Podfile`, call
   `install_all_flutter_pods` with `use_frameworks!` and `flutter_post_install` in
   `post_install`, then `pod install`. Set `ENABLE_USER_SCRIPT_SANDBOXING = NO`, so the
   module's build scripts run. Create one `FlutterEngine`, `run()` it and
   `GeneratedPluginRegistrant.register(with:)`, then create one
   `FlutterViewController(engine:nibName:bundle:)` and show every page in it, presented or
   as a child. The engine drops its accessibility bridge when its view controller changes
   and does not rebuild it while semantics stay on, so VoiceOver would find nothing on a
   second view controller (Flutter 3.47).

The runtime keeps the engine's state while the app runs, so a release that sync staged
activates once no Plux page is open, as in a Flutter app.

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
