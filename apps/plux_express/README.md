<!--
SPDX-FileCopyrightText: 2026 Plux contributors
SPDX-License-Identifier: Apache-2.0
-->

# Plux Express

A reference host app (`DX-004`): a native connectivity switch above the published catalogue.
Everything else is the [`plux_express` fixture project](../../schema/testdata/documents/plux_express),
one plugin, `shop`, with no sign-in:

| Page | Shows |
|---|---|
| `catalogue` | a paginated REST list of products |
| `product` | one product and its "Add to cart" button |
| `cart` | the persisted cart (it survives a restart); placing the order |
| `order` | live tracking over a WebSocket: `placed`, `picked_up`, `on_the_way`, `delivered` |
| `courier` | the courier's jobs; a delivery confirmed while the host reports the network gone is queued in the outbox and replayed once when it is back |

| Path | Contents |
|---|---|
| `lib/main.dart` | `Plux.initialize`, then the app |
| `lib/src/config.dart` | `ExpressConfig`: the server and app from `--dart-define`s, and the runtime's configuration |
| `lib/src/host.dart` | `ExpressHost`: the navigator and what the host tells Plux about the network (`Plux.setNetworkAvailable`) |
| `lib/src/reference_ca.dart` | the clients end-to-end builds use to trust the reference API's test CA |
| `lib/plux/plux.g.dart` | the typed API `plux codegen` writes from the fixture (`cd backend/internal/codegen && go generate ./...`) |
| `integration_test/` | the flows; `app_test.dart` runs them on an emulator or simulator |
| `test/e2e_test.dart` | the same flows under `flutter test`, against a server the Go driver starts |

The defines are those of the [starter](../starter/README.md), with the route defaulting to
`catalogue`, plus `PLUX_REFAPI_CA` (the reference API's CA, base64 of its PEM) and `PLUX_REFAPI_URL`
for the end-to-end builds. Without `PLUX_REFAPI_CA` the app uses the platform's HTTP client and
roots, as in production.

## Tests

```bash
make e2e-starter                          # runs the starter's and both reference apps' flows against a server built from source
flutter test test/express_test.dart       # host tests of the configuration, the network switch and the CA clients
```

The end-to-end driver (`TestPluxExpressAgainstTheServer`) builds [`test/refapi`](../../test/refapi),
publishes the fixture with its environment URLs pointed at it, and runs the flows.
