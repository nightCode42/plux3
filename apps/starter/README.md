<!--
SPDX-FileCopyrightText: 2026 Plux contributors
SPDX-License-Identifier: Apache-2.0
-->

# Plux starter

The minimal host app, which the quick start (`DX-001`, P4) will build on: it starts the Plux runtime, shows a published page
inside a native screen, opens it full screen with `Plux.open`, and passes the user's
theme and analytics consent to the runtime. The end-to-end flows (`QA-006`) and the
compatibility matrix (`QA-010`) drive it.

| Path | Contents |
|---|---|
| `lib/main.dart` | `Plux.initialize`, then the app |
| `lib/src/config.dart` | `StarterConfig`: the server and app from `--dart-define`s |
| `lib/src/starter_app.dart` | the home screen: sync status, consent and theme switches, a `PluxView` |
| `integration_test/` | the end-to-end flows; `app_test.dart` runs them on an emulator or simulator |
| `test/e2e_test.dart` | the same flows under `flutter test`, against a server the Go driver starts |
| `assets/plux/` | the baseline `make dev` pulls (not committed) |

Its content is the [starter fixture project](../../schema/testdata/documents/starter):
one plugin, `welcome`, with a page of text, an icon and an image asset.

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
