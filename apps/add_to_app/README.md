<!--
SPDX-FileCopyrightText: 2026 Plux contributors
SPDX-License-Identifier: Apache-2.0
-->

# Add-to-app

The Plux runtime inside a Flutter module embedded in native apps (`HST-033`, plan p4
§5.10):

| Path | What it is |
|---|---|
| [`plux_module/`](plux_module/lib/plux_module.dart) | The Flutter module: it asks its host for the runtime's settings, starts Plux, opens the pages the host asks for, and asks the host for its native screens |
| [`android_host/`](android_host/app/src/main/kotlin/dev/plux/addtoapp/host/HostApp.kt) | A Kotlin app with one cached `FlutterEngine`: pages full screen in a `FlutterActivity`, or below a native header in a `FlutterFragment`; UiAutomator tests in `androidTest` |
| [`ios_host/`](ios_host/HostApp/PluxHost.swift) | A Swift app with one `FlutterEngine` and one `FlutterViewController`, embedded with CocoaPods: pages full screen or below a native header, always in that view controller, since a new one on the running engine starts without the accessibility tree; XCUITests |

The module and a host talk on the method channel `dev.plux/host`:

| Method | Called by | Does |
|---|---|---|
| `config` | the module | returns the runtime's settings: `endpoint`, `appId`, `environment`, `hostBuild`, `rootKeys` |
| `open` | the host | opens the Plux page at `route`; when it pops, the module closes the host's screen with `SystemNavigator.pop` |
| `openNative` | the module | shows the host's native screen behind the native route `host-settings`, and answers once it closes |

## Run the flows

`TestAddToAppAgainstTheServer` (`make e2e-starter`) starts a Plux server built from source,
publishes the starter fixture with a page that opens `host-settings`, pulls its baseline
into `plux_module/assets/plux`, and publishes an update. On the development machine the
module's Dart test plays the host; in the device jobs the hosts' UI tests run on the
emulator and the simulator ([test/e2e](../../test/e2e/README.md)). Each flow runs in a
process of its own: offline from the baseline, then the update and the native screen.

## Build a host by hand

Run `flutter pub get` in `plux_module` first: it writes the platform glue the hosts include
(`.android/include_flutter.groovy`, `.ios/Flutter/podhelper.rb`).

- **Android:** build with the Gradle wrapper it writes into the module,
  `../plux_module/.android/gradlew :app:assembleDebug` in `android_host`, or open
  `android_host` in Android Studio. Start it with the runtime's settings as extras:
  `adb shell am start -n dev.plux.addtoapp.host/.MainActivity -e endpoint
  http://10.0.2.2:8080 -e appId <app-id> -e environment staging`.
- **iOS:** run `pod install` in `ios_host` and open `HostApp.xcworkspace`. The project is
  committed as it is before `pod install`, which adds the pods to it: leave that change out
  of commits. Set `PLUX_ENDPOINT`, `PLUX_APP_ID` and `PLUX_ENVIRONMENT` in the scheme's
  environment.
