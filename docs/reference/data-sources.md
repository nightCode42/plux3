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
| `mocks` | both | `empty` (a value of the type) and `error` (`kind`, `status`, `message`); `success` is the source's `mock` (`DAT-080`). |
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
development builds, never in release builds.
