<!--
SPDX-FileCopyrightText: 2026 Plux contributors
SPDX-License-Identifier: Apache-2.0
-->

# Data sources

REST and GraphQL data sources (`DAT-001`, ADR-0048), as the compiler checks them and the
runtime loads them from P5 R4. A source's `config` is validated for the kinds `rest` and
`graphql`; the other kinds keep their P1 behaviour until their milestone. A bundle that
declares a REST or GraphQL source requires the feature `data.v1`, first in runtime 0.3.0.

## Configuration

| Property | Kinds | Meaning |
|---|---|---|
| `baseUrl` | both, required | An app variable of type `string`; every environment gives it an HTTPS URL on a domain the plugin declares (`DAT-003`, `PLX-1170`, `PLX-1171`). The bundle carries the URL of every environment; the runtime uses `PluxConfig.environment`'s. |
| `method` | rest | `GET` (default) or `POST`. |
| `path` | rest (required), graphql | Starts with `/`; `{name}` placeholders take parameters. |
| `query` | graphql, required | The query document, sent with `POST`. |
| `params` | both | Literals or `$expr` bindings in the declaring scope: path placeholders, then the query (`GET`) or a JSON body (`POST`); GraphQL variables. |
| `headers` | both | Literal headers; credentials (`Authorization`, `Cookie`, API keys, tokens) are refused (`PLX-1174`). |
| `auth` | both | Sends the auth delegate's token; one refresh on `401`, then one retry (`HST-010`). |
| `select` | both | A dot path (`items`, `data.0.name`) to the value (`DAT-004`). |
| `responseType`, `transform` | both | Together: the selected value is read as `responseType` and `transform` (PXL over `response`) returns the source's type (`PLX-1172`). |
| `cache` | both | `policy` (`networkOnly`, `cacheFirst`, `networkFirst`, `staleWhileRevalidate`), `ttlSeconds` (required except for `networkOnly`), `key`, `encrypted` (`DAT-010`). Responses of `auth` sources are always encrypted at rest. |
| `pagination` | both | `style` (`cursor`, `page`, `offset`), `pageSize` ≤ `data.pageSize`, `sizeParam`, `cursorParam` and `nextCursor`, `pageParam` and `firstPage`, `offsetParam`, `hasMore` (`DAT-011`, `PLX-1173`). The source's type is a list. |
| `mocks` | both | `empty` (a value of the type) and `error` (`kind`, `status`, `message`); `success` is the source's `mock` (`DAT-080`). Only development bundles carry mocks; release bundles carry none. |
| `operations` | both | Named operations for `apiCall`: `method` and `path` (REST) or `query` (GraphQL), `input` (an object type), `output`, `select`, `headers`, `auth`. |

## At run time

`data.<name>` is `{value, loading, error, hasMore, status}`: a list binds `items` to
`data.<name>.value ?? []`, `status` to `data.<name>.status` and `hasMore` to
`data.<name>.hasMore`, and its `onEndReached` runs `refreshData` with `more: true`. A source
loads when a binding first reads it. `apiCall` runs `"<source>.<operation>"`; `refreshData`
reloads bypassing the cache.

Requests leave from the data isolate only after their host is checked against the plugin's
`networkDomains` over HTTPS; others fail with `PLX-5100` and are reported. Failures are typed
(`PLX-5101`–`5109`) and never carry bodies, query strings or headers. Sizes and time come
from `data.requestSize`, `data.responseSize`, `data.requestTimeout`; the cache keeps within
`data.cacheBytes` and `data.cacheEntries`. Each request records `api_call` with source,
operation, status, duration and bytes. Mock states are selected only in tests and
development builds, never in release builds, whose bundles carry no mocks.

Encrypted cache entries are sealed under a key made for the installation and kept by the
platform's secure storage, like the secure state store's; without one they are not cached
(`PLX-5109`). The `logout` action and `Plux.wipeData` remove every cached response, so the
next user never sees the previous user's; `logout` then calls the auth delegate's
`onLogout`. A source's loads and failures fire its `onLoaded` and `onFailed` triggers.

## Streams, the outbox and transfers

These complete the data layer in P5 R5 ([ADR-0048](../adr/0048-data-layer.md)).

**Streams** (`DAT-012`). A source of kind `websocket` or `sse`, or a GraphQL operation
that subscribes, delivers messages instead of one response. `baseUrl`, `path`, `params`,
`auth` and `select` work as above, over `wss` and `https` only. `subscribe` opens a stream
with its parameters, and `unsubscribe` closes it; a stream also closes with the scope that
opened it. `data.<name>.value` holds the latest message, mapped to the source's type, and
each message fires the source's `onMessage` trigger with it as `event`. A dropped
connection reconnects with capped exponential backoff and jitter (`data.streamBackoffMin`,
`data.streamBackoffMax`) and resubscribes; SSE resumes with `Last-Event-ID`. A message
over `data.streamMessageSize` closes the stream (`PLX-5111`). More than `data.streamsOpen`
open streams per plugin are refused (`PLX-5112`). A WebSocket refused with `401` or `403`
keeps retrying with capped backoff, since `dart:io` does not expose the status; SSE treats
a `4xx` as final (`PLX-5110`). WebSockets connect with the host's
`PluxConfig.webSocketClient` when it gives one.

**The outbox** (`DAT-020`). An operation marked `offlineCapable` that fails with `network`
or `timeout`, or runs while the host has said the network is down
(`Plux.setNetworkAvailable(false)`), is queued instead: its operation, resolved input and an
idempotency key, encrypted in the built-in store. No token is stored. The step succeeds
with a `null` output, and its optimistic state change stays. Entries replay in order,
one at a time per plugin: on app resume, after any successful request, on a backoff timer
(`data.outboxBackoffMin` to `data.outboxBackoffMax`) and when the host reports the network
back. Each attempt sends the same `Idempotency-Key` header, so a server can answer a
repeated mutation without applying it twice. A `2xx` fires the source's `onSynced`
trigger; `409` and `412` fire `onConflict` (`PLX-5122`); another `4xx` fires
`onSyncFailed` (`PLX-5123`); a `5xx`, `network` or `timeout` leaves the entry for the next
replay. A full outbox (`data.outboxEntries`, `data.outboxBytes`) refuses the mutation
(`PLX-5120`). Without the platform's secure storage there is no outbox (`PLX-5121`).
`Plux.wipeData` and `logout` clear it. Mark only operations that are safe to apply later:
the Plux Bank reference app keeps its transfers online.

**Transfers** (`DAT-031`). An upload streams a file, such as one `pickFile` returned, as a
multipart or raw body. A download streams to the app's private storage and returns its
path. Both report progress through `onProgress`, are cancelled with their run, and are
bounded by `data.uploadSize` and `data.downloadSize`: checked against the declared length
first, then counted during the transfer (`PLX-5130`; `PLX-5131` for a file that cannot be
read). A transfer is a step, so it is also bounded by `action.stepTimeout`; raise that
limit for large files.

**Local database sources.** A source of kind `database` watches a query over a local collection
(`DB-006`): see the [database guide](../guides/database.md).
