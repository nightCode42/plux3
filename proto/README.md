# proto

The API contract. Every Plux Server API is defined here and nowhere else
(`SRV-002`, [ADR-0005](../docs/adr/0005-connectrpc-and-protobuf.md)).

| Path | Contents |
|---|---|
| `plux/v1/common.proto` | Paging, diagnostics, locations, actors, limits, artifacts, audit entries |
| `plux/v1/<service>.proto` | One file per service of `SRV-003` |
| `buf.yaml` | Module, lint rules (`STANDARD`) and breaking-change rules (`FILE`) |
| `buf.gen.yaml` | Code generation with locally built plugins |
| `openapi.base.yaml` | The `info` block of the generated OpenAPI description |

## Generating

`make proto` (also run by `make gen`) formats the contract and writes:

| Output | Location |
|---|---|
| Go messages | `backend/internal/pluxv1/` |
| ConnectRPC handlers and clients | `backend/internal/pluxv1/pluxv1connect/` |
| OpenAPI 3.1 description | `docs/reference/api/openapi.yaml` |

Generated code is committed; `make gen-check` fails when it is stale
(`CI-003`). TypeScript and Dart clients are generated from the same contract
when Studio (P11) and the runtime (P3) need them.

`make install-buf` installs the pinned `buf` and the protoc plugins. They are
built with the project's Go toolchain, so generation needs no network access
and no remote plugin.

## Rules

- `make proto-check` runs `buf lint`, a format check and `buf breaking`
  against the last `backend/v*` tag. A breaking change requires a new version
  package (`v2`) and the deprecation period of `SRV-000`.
- Field numbers are never reused or renumbered; deprecated fields keep their
  slots.
- Documents travel as canonical JSON **bytes**, never as protobuf structures,
  so a document's hash is a property of the bytes (`SCH-003`).
- Every list endpoint takes a `Page` and returns a `PageResult`; nothing
  returns an unbounded list (`SRV-004`).
- Mutating calls accept an `Idempotency-Key` header, not a field (`SRV-005`).

The generated reference lives in [docs/reference/api.md](../docs/reference/api.md).
