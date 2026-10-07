<!--
SPDX-FileCopyrightText: 2026 Plux contributors
SPDX-License-Identifier: Apache-2.0
-->

# Plux Bank

A reference host app (`DX-004`): a native shell around the published sign-in page. Everything
else is the [`plux_bank` fixture project](../../schema/testdata/documents/plux_bank), one
plugin, `banking`:

| Page | Shows |
|---|---|
| `login` | a validated form; on success the native action `bankSignIn` hands the token to the host, on `401` an inline error |
| `dashboard` | accounts over GraphQL, paginated transactions, a banner fed by server-sent events |
| `transfer` | a form with IBAN, amount (above zero, within the balance) and reference validators; the REST call carries an idempotency key |
| `result` | the amount and new balance, or why the transfer failed |
| `kyc` | `capturePhoto` through `plux_media` (not part of the exit flow) |

English and German translations; security profile `maximum`.

| Path | Contents |
|---|---|
| `lib/main.dart` | `Plux.initialize`, then the app |
| `lib/src/config.dart` | `BankConfig`: the server and app from `--dart-define`s, and the runtime's configuration |
| `lib/src/host.dart` | `BankHost`: the auth delegate holding the token in memory only, and the native action `bankSignIn` |
| `lib/src/reference_ca.dart` | the clients end-to-end builds use to trust the reference API's test CA |
| `lib/plux/plux.g.dart` | the typed API `plux codegen` writes from the fixture (`cd backend/internal/codegen && go generate ./...`) |
| `integration_test/` | the exit flow; `app_test.dart` runs it on an emulator or simulator |
| `test/e2e_test.dart` | the same flow under `flutter test`, against a server the Go driver starts |

The defines are those of the [starter](../starter/README.md), with the route defaulting to `login`,
plus `PLUX_REFAPI_CA` (the reference API's CA, base64 of its PEM) and `PLUX_REFAPI_URL` for the
end-to-end builds. Without `PLUX_REFAPI_CA` the app uses the platform's HTTP client and roots,
as in production.

## Tests

```bash
make e2e-starter                       # runs the starter's and both reference apps' flows against a server built from source
flutter test test/bank_test.dart       # host tests of the configuration, the auth delegate and the CA clients
```

The end-to-end driver (`TestPluxBankAgainstTheServer`) builds [`test/refapi`](../../test/refapi),
publishes the fixture with its environment URLs pointed at it, and runs the flows.
