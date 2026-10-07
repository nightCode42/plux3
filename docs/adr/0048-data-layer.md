# 0048. Data layer: own clients on the runtime's HTTP stack, an encrypted outbox, no new dependency

- **Status:** Accepted (maintainer, 2026-10-04, P5 plan §2.1, B1–B2)
- **Date:** 2026-10-04
- **Requirements:** `DAT-001`, `DAT-003`, `DAT-004`, `DAT-010`, `DAT-011`, `DAT-012`, `DAT-020`, `DAT-030`, `DAT-031`, `DAT-080`, `HST-010`, `SEC-080`, `SCH-012`, `LIM-004`, `ANL-001`, `WGT-012`, `ACT-007`

## Context and problem

P5 brings data sources: REST, GraphQL, WebSocket, Server-Sent Events, local database
queries and static data (`DAT-001`; Plux Function sources are P7's), with mapping to typed
models, caching, pagination, streams, an offline outbox, uploads and downloads, and
design-time mocks. Every request must stay inside the plugin's declared domains
(`DAT-030`, `SEC-080`), use the host's auth delegate (`HST-010`), and run off the UI
isolate (L-6).

The runtime already has an HTTP stack: `http` with `cronet_http` on Android and
`cupertino_http` on iOS, used by sync and telemetry on background isolates
([ADR-0021](0021-sync-all-plugins-at-start.md), [ADR-0034](0034-runtime-telemetry.md)).
The maintainer decided (plan D13, D14, accepted in B2):

- no connectivity package: the outbox replays on app resume, after any successful
  request, on a backoff timer, and when the host calls `Plux.setNetworkAvailable`;
- no client libraries: SSE parsing, GraphQL over HTTP and GraphQL subscriptions over the
  `graphql-transport-ws` protocol are the runtime's own code, on `dart:io`'s `WebSocket`
  and the existing `http` clients.

## Decision drivers

- **Nothing leaves for an undeclared domain** (`DAT-030`): checked before any byte is sent,
  on every redirect too.
- **The UI isolate does no network I/O or large decoding** (L-6).
- **No new dependency in every host app** (D13, D14; the core's size, `RT-061`).
- **Sensitive values are never stored in the clear** (`SCH-012`), and tokens are never
  stored at all.
- **Bounded on the device** (`LIM-004`): cache, outbox, messages and transfers have
  registry limits and degrade gracefully.

## Considered options

1. **Own clients on the existing HTTP stack and `dart:io`**, running in a background
   isolate, with an outbox in the built-in store.
2. **Client libraries**: `dio`, `web_socket_channel`, a GraphQL client package and
   `connectivity_plus`.
3. **Requests on the UI isolate**, decoding moved to `compute` per response.

## Decision

Chosen option: **1.** It reuses the clients that already ship (HTTP/2 through Cronet and
`URLSession`), adds no dependency, and keeps requests, decoding and mapping off the UI
isolate. Option 2 adds four packages to every host app and a second HTTP stack beside the
one sync uses. Option 3 breaks L-6 for every request and pays an isolate start per
response.

### Where requests run

Requests, stream connections, decoding and mapping run in the runtime's network isolate,
with the same clients as sync. Only typed results cross to the UI isolate, where the state
engine ([ADR-0046](0046-state-engine.md)) applies them. The auth delegate is host code on
the UI isolate: the runtime asks it for a token per request and sends the token with the
request; the token is held in memory only.

### Clients and configuration (`DAT-001`, `DAT-003`)

- **REST** over JSON, with the operation's method, path, query, headers and body declared
  in the source and typed.
- **GraphQL** queries and mutations as HTTP POST of the operation and its variables;
  errors in the response's `errors` array are typed errors.
- **Base URLs and non-secret configuration** per environment, from the app document.
  Secrets never appear in bundles: the compiler's secret detection (`PLX-1500`) applies to
  data sources, and calls needing a server-held secret go through a Plux Function (P7) or
  the host's auth delegate (`DAT-003`).
- **Local database and static sources** read the adapter ([ADR-0049](0049-local-persistence.md))
  and the bundle's data, through the same source interface.

### Auth (`HST-010`)

Each request carries the auth delegate's token. A `401` asks the delegate to refresh once
and repeats the request once; a second `401` is an `http(401)` error. The `logout` action
signals the host's delegate, and the host decides whether to wipe Plux data (`DB-008`).

### Domains (`DAT-030`, `SEC-080`)

Before a request leaves, its host is checked against the plugin's declared domains — exact
names, or a `*.` prefix for subdomains — and against the app's approved capabilities
([ADR-0051](0051-device-actions-packages-and-capabilities.md)). Redirects are followed by
the runtime, not the client, so each target is checked the same way. Only `https` and
`wss` are allowed; a debug-only override for local test servers produces a distinct
warning and does not exist in release builds. A blocked request fails with `PLX-5100` and
is reported.

### Mapping (`DAT-004`)

Responses are mapped to the operation's declared type through selectors (paths into the
response) and PXL transforms, compiled at publish. A value that does not fit the type is a
typed mapping error, never a partially filled model.

### Caching (`DAT-010`)

- Policies `networkOnly`, `cacheFirst`, `networkFirst` and `staleWhileRevalidate`, with a
  TTL and a cache key per operation (the source, the operation and its resolved inputs).
- Cached responses live in the built-in store, in memory or persisted; persisted entries
  are encrypted with AES-GCM under the per-installation key when the source asks for it.
  An operation whose output type has a `sensitive` field is cached only in memory or
  encrypted; the compiler refuses a plain persisted cache for it.
- The cache's size is a registry limit; least recently used entries are evicted first.
  `Plux.wipeData` clears a plugin's cache.

### Pagination (`DAT-011`, `WGT-012`)

Cursor, page and offset pagination are declared on the source. A list bound to a paginated
source asks for the next page as its end comes into view, and exposes loading, end and
error states to bindings. Loaded pages are bounded in memory by a registry limit; pages
scrolled far out are dropped and reloaded on return.

### Streams (`DAT-012`)

- **WebSocket** on `dart:io`'s `WebSocket`; **SSE** parsed from a streamed `http` response
  (`text/event-stream`, with `Last-Event-ID` on reconnection); **GraphQL subscriptions**
  over `graphql-transport-ws` on the same WebSocket client.
- Reconnection with exponential backoff and jitter, and resubscription of the active
  subscriptions after reconnecting. A stream belongs to its scope and closes with it.
- Each message is decoded and mapped in the network isolate, then written to state or
  delivered as a data-source event ([ADR-0045](0045-action-engine-completion.md)). A
  message larger than the registry's limit closes the stream with a typed error.
- `subscribe` and `unsubscribe` control streams from graphs.

### The outbox (`DAT-020`)

- A mutation marked `offlineCapable` that fails with `network` or `timeout`, or is issued
  while the host has said the network is unavailable, is written to the outbox: the
  operation, its resolved inputs and an idempotency key, encrypted with AES-GCM under the
  per-installation key in the built-in store. No token is stored; replay asks the auth
  delegate again.
- The idempotency key is generated once per mutation and sent in the `Idempotency-Key`
  header on every attempt, so a server that already applied it can answer without applying
  it twice.
- Entries replay in order per plugin, one at a time, on app resume, after any successful
  request, on a backoff timer and on `Plux.setNetworkAvailable(true)` (D13).
- Results: a 2xx is success; `409` and `412` are a conflict; another 4xx is a failure; a
  5xx, `network` or `timeout` leaves the entry for the next replay. Success, failure and
  conflict are data-source events, so graphs can reconcile state; the optimistic change of
  the original step stays until one of them arrives (`ACT-007`).
- The outbox's entries and bytes are registry limits. A full outbox refuses the new
  mutation with a typed error (`LIM-004`). `Plux.wipeData` clears a plugin's entries.

### Transfers (`DAT-031`)

Uploads stream from a file (for example one `pickFile` returned) as multipart or raw
bodies; downloads stream to the app's private storage. Both report progress as events,
are cancelled with their run, and are bounded by registry size limits, checked against the
declared length before the transfer and counted during it.

### Mocks (`DAT-080`)

A source may declare mocks: static fixtures, examples generated from its schema, or
recorded responses, each with a state (`loading`, `empty`, `error`, `success`). Tests
(Plux Test, [ADR-0052](0052-plux-test-and-import-tools.md)) and debug builds select a state
per source; a release build of the runtime never selects a mock. Selection in the Dev app
and Studio arrives in P10 and P11, so `DAT-080` stays `WIP`.

### Actions and telemetry

`apiCall` and `refreshData` are handlers of the engine (ADR-0045), with optimistic updates
and retries. Each request emits the `api_call` event (Appendix G, `ANL-001`) with the
source, operation, status and duration, behind consent, never a payload.

### Registries

The limits named above (cache, outbox entries and bytes, message size, request and response
size, uploads, downloads, data sources per plugin) and the data layer's error codes in
`PLX-5000`–`5999` are fixed by R4 and R5 in `schema/limits.json` and `schema/errors.json`.

## Consequences

- **Positive.**
  - No new dependency; the HTTP/2 clients that sync proved are reused.
  - Every request passes one domain check, one auth path and one telemetry path.
  - The outbox survives restarts without exposing its contents or any token.
- **Negative.**
  - The runtime owns SSE parsing, the `graphql-transport-ws` protocol and reconnection
    logic, each tested against a local server (`test/refapi`).
  - Without connectivity events the outbox replays on the triggers above; a host that
    knows about connectivity tells Plux through `Plux.setNetworkAvailable`.
  - A server that ignores `Idempotency-Key` may apply a replayed mutation twice; the data
    guide says what servers must do.
- **Follow-up.**
  - R4: clients, auth, domains, mapping, caching, pagination, mocks, `apiCall`,
    `refreshData`.
  - R5: streams, the outbox, transfers.
  - P7: Plux Function sources and the auth delegate for functions.

## Options in detail

### Option 2: client libraries

`dio` would replace the `http` interface sync uses, or run beside it; `web_socket_channel`
wraps the same `dart:io` socket; GraphQL client packages bring their own caches and
normalisation that Plux's typed sources do not need; `connectivity_plus` reports interface
state, not whether the server is reachable, so the outbox would still need its own retry.

### Option 3: requests on the UI isolate

The UI isolate would do socket I/O and parse large responses, against L-6, and spawning an
isolate per response to decode it costs more than keeping one network isolate.
