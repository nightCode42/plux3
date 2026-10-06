<!--
SPDX-FileCopyrightText: 2026 Plux contributors
SPDX-License-Identifier: Apache-2.0
-->

# refapi

The reference backend of the reference apps (`DX-004`): the Plux Bank and Plux Express APIs
their end-to-end flows call. Standard library only; state lives in memory and is seeded the
same way on every start.

It serves **HTTPS and WSS only** — the runtime refuses cleartext data URLs. The certificate is
signed by a CA (ECDSA P-256) that `refapi` generates at start, valid for `localhost`,
`127.0.0.1` and `::1`; the CA's certificate (public) is written to `-ca-out`. The apps trust
it only in end-to-end mode, through `PluxConfig.httpClient` and `PluxConfig.webSocketClient`.

| Flag | Default | Meaning |
|---|---|---|
| `-addr` | `127.0.0.1:0` | Address to listen on |
| `-ca-out` | required | File the CA certificate (PEM) is written to |
| `-track-step` | `2s` | Pause between the status steps of an order's tracking stream |
| `-heartbeat` | `15s` | Interval of the notification stream's heartbeat comments |

Once it is listening it prints exactly one line, `refapi listening on https://HOST:PORT`, and
shuts down on SIGINT or SIGTERM.

## Plux Bank (`/bank`)

Login with `demo` / `demo1234`; every other endpoint needs `Authorization: Bearer <token>`.

| Endpoint | |
|---|---|
| `POST /bank/v1/login` | `{"token","expiresIn"}`; `401` for other credentials |
| `GET /bank/v1/accounts` | `{"items":[{id,name,iban,balanceCents,currency}]}` |
| `POST /bank/v1/transfers` | `201 {"id","status","balanceCents"}`; `422` for an unknown account, an amount that is not positive or is over the balance, a malformed IBAN; idempotent on `Idempotency-Key` |
| `POST /bank/graphql` | The queries `accounts` and `transactions(accountId, first, after)` only — a fixture's server, not a GraphQL engine |
| `GET /bank/v1/notifications` | Server-sent `transfer` events with `id:`; resumes from `Last-Event-ID`; heartbeat comments |

## Plux Express (`/express`, no auth)

| Endpoint | |
|---|---|
| `GET /express/v1/products?cursor=&limit=` | 40 seeded products, cursor pagination (`items`, `next`, `more`) |
| `GET /express/v1/products/{id}` | One product |
| `POST /express/v1/orders` | `201 {"id","status":"placed","totalCents"}` |
| `GET /express/v1/orders/{id}/track` | WebSocket: `placed`, `picked_up`, `on_the_way`, `delivered`, one per `-track-step`, then a normal close |
| `GET /express/v1/courier/jobs` | The courier's jobs |
| `POST /express/v1/courier/jobs/{id}/deliveries` | `201`; requires `Idempotency-Key`, so an outbox replay delivers once |
| `GET /express/v1/courier/jobs/{id}/deliveries` | The deliveries applied, which the flows read to prove it |

Errors are `{"error":{"code","message"}}`.

## In the end-to-end tests

`backend/internal/server/reference_e2e_integration_test.go` builds this module, starts it with
`-ca-out <tmp>/ca.pem -track-step 200ms`, reads the listen line, and passes the CA (base64) and
the URL to the apps' flows as `PLUX_REFAPI_CA` and `PLUX_REFAPI_URL`. Run the module's own tests
with `go test -race ./...` from this directory.
