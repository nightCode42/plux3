<!--
SPDX-FileCopyrightText: 2026 Plux contributors
SPDX-License-Identifier: Apache-2.0
-->

# Data Guide

How a Plux plugin talks to its backend: REST and GraphQL sources bound to widgets,
mutations, live streams, and mutations that wait for the network. The examples are the
reference apps' documents,
[`plux_bank`](../../schema/testdata/documents/plux_bank) and
[`plux_express`](../../schema/testdata/documents/plux_express), served in tests by
[`test/refapi`](../../test/refapi/README.md). Every property is in the
[data-sources reference](../reference/data-sources.md). The design is
[ADR-0048](../adr/0048-data-layer.md).

## 1. Base URLs, domains and auth

A source's `baseUrl` names an app variable that every environment sets to an HTTPS URL
(`DAT-003`), so the same release talks to staging and production:

```json
"environments": [{"key": "production", "name": "Production",
  "values": {"apiBaseUrl": "https://api.example.com/bank/v1", "graphUrl": "https://api.example.com/bank", "streamUrl": "https://api.example.com/bank/v1"}}]
```

The plugin declares the domains it calls in `capabilities.networkDomains`, and the app
approves them (`SEC-080`). A request to any other domain is blocked and reported
(`DAT-030`). With `"auth": true`, a request carries the host's token from its
`PluxAuthDelegate`; a `401` refreshes it once and retries (`HST-010`). Secrets never go
into documents: credentials in `headers` are refused at publish.

## 2. Read and bind

A source declares its type, where the value is in the response, and how it pages (`DAT-004`,
`DAT-011`). Plux Express's catalogue:

```json
{"name": "products", "kind": "rest", "type": "list<Product>",
 "config": {"baseUrl": "apiBaseUrl", "path": "/products", "select": "items",
   "pagination": {"style": "cursor", "pageSize": 10, "sizeParam": "limit",
                  "cursorParam": "cursor", "nextCursor": "next", "hasMore": "more"}},
 "mock": [{"id": "p-001", "name": "Product 01", "category": "Fruit", "priceCents": 199}]}
```

`data.products` is `{value, loading, error, hasMore, status}`. A list binds `items` to
`data.products.value ?? []` and `status` to `data.products.status`, and shows its
`loading`, `empty` and `error` slots. Its `onEndReached` runs `refreshData` with
`"more": true` for the next page. A GraphQL source is the same, with a `query`; Plux Bank
reads its accounts that way.

`cache` keeps responses with `networkOnly`, `cacheFirst`, `networkFirst` or
`staleWhileRevalidate`, a TTL and optional encryption (`DAT-010`). Responses that need auth
are cached only encrypted.

## 3. Write

Mutations are a source's named `operations`, run by `apiCall` with a typed input. Plux
Bank's transfer:

```json
"operations": {"transfer": {"method": "POST", "path": "/transfers", "auth": true,
                            "input": "TransferInput", "output": "TransferResult"}}
```

```json
{"id": "submit", "action": "submitForm", "input": {"form": "transfer"}, "next": "send"},
{"id": "send", "action": "apiCall",
 "input": {"operation": "api.transfer",
           "input": {"fromAccountId": "acct-001",
                     "toIban": {"$expr": "steps.submit.output.toIban"},
                     "amountCents": {"$expr": "int(mul(decimal(steps.submit.output.amount), 100d))"},
                     "reference": {"$expr": "steps.submit.output.reference"}}},
 "onSuccess": "done", "onError": "failed"}
```

`submitForm` validates the form and outputs its values ([forms guide](forms.md)); the
`apiCall` step's output is the typed result. A failure is typed (`network`, `http` with its
`status`, `timeout`, `validation`), so a page can tell a refused login (`401`) from a lost
connection. A step may declare `retry` and an `optimistic` state change
([actions guide](actions.md)).

## 4. Live data

A `websocket` or `sse` source delivers messages instead of one response (`DAT-012`). Plux
Express follows an order:

```json
{"name": "tracking", "kind": "websocket", "type": "Tracking",
 "config": {"baseUrl": "streamUrl", "path": "/orders/{orderId}/track", "params": {"orderId": ""}}}
```

```json
{"id": "track", "action": "subscribe",
 "input": {"stream": "tracking", "params": {"orderId": {"$expr": "params.orderId"}}}}
```

`data.tracking.value` is the latest message, and the source's `onMessage` trigger runs with
each one. A dropped connection reconnects with backoff and resubscribes. The stream closes
with its page, or with `unsubscribe`.

## 5. Work offline

Mark an operation `offlineCapable` when applying it later is safe (`DAT-020`). Plux
Express's courier confirms deliveries that way:

```json
"confirmDelivery": {"method": "POST", "path": "/courier/jobs/{id}/deliveries",
                    "input": "DeliveryInput", "output": "Delivery", "offlineCapable": true}
```

While the host reports the network down (`Plux.setNetworkAvailable(false)`), or when the
call fails with `network` or `timeout`, the mutation is queued in the encrypted outbox and
the step succeeds. When the network returns, the outbox replays it with the same
`Idempotency-Key`, so the server applies it once. The source's `onSynced`, `onSyncFailed`
and `onConflict` triggers let the page reconcile:

```json
"triggers": {"dataSources": {"courier": {"onSynced": {"steps": [
  {"id": "reload", "action": "refreshData", "input": {"source": "courier"}}]}}}}
```

Payments and transfers are not offline work: Plux Bank's transfer stays online.

## 6. Mocks and imports

A source's `mock` is its `success` state; `mocks` adds `empty` and `error` (`DAT-080`).
Tests and development builds select them, and release bundles carry none.
`plux import openapi` and `plux import graphql` write typed sources and operations from a
backend's description, and `plux mock` serves an OpenAPI document's examples for local
work (`DAT-002`, `TST-004`; [CLI](../reference/cli.md)).
