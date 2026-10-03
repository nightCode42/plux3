<!--
SPDX-FileCopyrightText: 2026 Plux contributors
SPDX-License-Identifier: Apache-2.0
-->

# Plux starter

The minimal host app, which the quick start (`DX-001`, P10) will build on: it starts the Plux runtime, shows a published page
inside a native screen, opens it full screen with `Plux.open`, and passes the user's
theme and analytics consent to the runtime. Its mixed screens show both directions of
[ADR-0023](../../docs/adr/0023-mixed-screens-slots-and-plux-view.md): a native screen with
two views of a plugin component that share the counter a native button writes, and a
plugin page holding the app's own map card, whose picks run the page's `navigate` step. It
shows the rest of Phase 4 too: typed routes, components, state, events and flags from the
API `plux codegen` writes; a guarded account page that sends a signed-out user to sign in,
with the app's auth delegate and user context; a deep link and a notification payload
handed to Plux; and a place page that calls the app's native action `sharePlace`, emits the
plugin event the home screen shows, and opens the app's native `profile` screen. The
end-to-end flows (`QA-006`) and the compatibility matrix (`QA-010`) drive it.

| Path | Contents |
|---|---|
| `lib/main.dart` | `Plux.initialize`, then the app |
| `lib/src/config.dart` | `StarterConfig`: the server and app from `--dart-define`s, and the runtime's configuration |
| `lib/src/host.dart` | `StarterHost`: the native route and action, the auth delegate and user context, the navigator and messenger keys |
| `lib/plux/plux.g.dart` | the typed API `plux codegen` writes from the fixture (`make gen` regenerates it) |
| `lib/src/starter_app.dart` | the home screen: sync status, consent and theme switches, a `PluxView`, the mixed screens, sign-in, the guarded account page, a link, a notification, the last shared place and a flagged tip |
| `lib/src/mixed_screen.dart` | a native screen with two typed views of the `counter-badge` component and a button that writes the typed `counter` state |
| `lib/src/map_card.dart` | the app's own map card, registered as the `MapCard` native slot in `lib/src/config.dart` |
| `integration_test/` | the end-to-end flows; `app_test.dart` runs them on an emulator or simulator |
| `test/e2e_test.dart` | the same flows under `flutter test`, against a server the Go driver starts |
| `assets/plux/` | the baseline `make dev` pulls (not committed) |

Its content is the [starter fixture project](../../schema/testdata/documents/starter):
one plugin, `welcome`, with a page of text, an icon and an image asset; the exposed app
state entry `counter`, the exported component `counter-badge`, the pages `places` (with the
`MapCard` slot its native catalogue declares), `place`, `account` (guarded by the graph
`require-sign-in`) and `sign-in`; the native route `profile` and action `sharePlace`; the
host event `placeShared`, the flag `showTips`, the user context's `tier`, the deep-link
scheme `plux-starter` and push payloads.

## Running it

With Docker and an Android emulator, an iOS simulator or a device attached:

```bash
make dev        # the stack with hot reload, the sample apps seeded, then this app under flutter run
make dev-app    # this app alone, against a running `make dev` stack (r: hot reload, R: restart)
```

`make dev` seeds an installation on first run, publishes the starter fixture to its
`starter` app, writes `.dart_defines.json` and pulls the baseline into `assets/plux`
(`make dev-starter`). On Android, `make dev-app` forwards the device's port 8080 to the
machine with `adb reverse`; debug builds allow plain HTTP for it, profile and release
builds do not. Against another server, pass the defines yourself:

```bash
flutter run --dart-define=PLUX_ENDPOINT=https://plux.example.com \
  --dart-define=PLUX_APP_ID=<app id> --dart-define=PLUX_ENVIRONMENT=production
```

| Define | Meaning | Default |
|---|---|---|
| `PLUX_ENDPOINT` | the server's base URL | `http://localhost:8080` |
| `PLUX_APP_ID` | the app's ID | required |
| `PLUX_ENVIRONMENT` | the environment key | `staging` |
| `PLUX_ROUTE` | the page the home screen shows | `welcome` |
| `PLUX_HOST_BUILD` | the host build the device reports | `dev` |
| `PLUX_ROOT_KEYS` | `keyId:<64 hex digits>` root keys, comma-separated | the baseline's `keys.json` |

## Tests

```bash
make e2e-starter    # the flows against a server built from source (PLUX_TEST_DATABASE_URL)
make compat         # the same, across released runtimes and servers (QA-010)
flutter test        # host tests of the configuration and the home screen
flutter test integration_test/app_test.dart --dart-define=...   # on an emulator or simulator
```
