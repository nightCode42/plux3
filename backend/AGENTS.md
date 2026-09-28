# backend — Agent Notes

The Go module `github.com/nightCode42/plux3/backend`: the Plux Server (`plux-server`, spec §11), the `plux` CLI (§22.1), the compiler (§9) and, from P7, the Functions SDK (§16). Read [go-standards.md](../docs/engineering/go-standards.md) and the root [AGENTS.md](../AGENTS.md) before editing.

## Layout

| Path | Contents | Licence |
|---|---|---|
| `cmd/plux-server/` | Server wiring only: configuration, construction, run, shutdown | AGPL-3.0-only |
| `cmd/plux/` | CLI wiring only | Apache-2.0 |
| `internal/buildinfo/` | Link-time version metadata (`CI-006`) | Apache-2.0 |
| `internal/plxerr/` | Error codes, reasons, diagnostics and the generated catalogue (ADR-0018, `DX-003`) | Apache-2.0 |
| `internal/schema/` | Document model: canonicalisation (`jcs/`), identifiers (`uuid7/`), the limits registry (`limits/`), the widget and action registries (`registry/`), validation and generated types (ADR-0025, ADR-0010) | Apache-2.0 |
| `internal/pxl/` | PXL: parser, checker, standard library, bytecode, VM, folding; exact decimals in `decimal/` (ADR-0009) | Apache-2.0 |
| `internal/bundle/` | Bundle container writer and reader, SHA-256 hashes, the FlatBuffers verifier, zstd transport; flatc accessors in `fbs/` (ADR-0002) | Apache-2.0 |
| `internal/compiler/` | The compiler: pipeline stages, reference graph, optimiser, section encoder, incremental page validation ([compiler.md](../docs/reference/compiler.md)) | Apache-2.0 |
| `internal/pluxv1/` | Generated API messages and ConnectRPC handlers and clients (ADR-0005); never edited by hand | Apache-2.0 |
| `internal/config/` | Server configuration: strict loading, environment overrides, validation (`SRV-008`, Appendix H) | AGPL-3.0-only |
| `internal/observability/` | Logger with redaction, the Prometheus registry of Appendix G, the tracer (`OBS-001`–`OBS-003`) | AGPL-3.0-only |
| `internal/storage/` | PostgreSQL pool, migrations, tenant transactions; sqlc queries in `queries/` generating `dbgen/`; object storage in `objects/`; idempotency keys in `idempotency/`; the integration-test helper in `storagetest/` (ADR-0007) | AGPL-3.0-only |
| `internal/cache/` | The shared, expendable cache: in-memory and Valkey (RESP) backends | AGPL-3.0-only |
| `internal/jobs/` | Durable background work on River (`SRV-024`) | AGPL-3.0-only |
| `internal/httpx/` | The SSRF-safe outbound client and request size guards (`SEC-104`, `SEC-105`) | AGPL-3.0-only |
| `internal/api/` | The ConnectRPC edge: interceptors, authentication, page tokens, idempotent mutation, error translation, and the thin service handlers (`SRV-006`, L-1) | AGPL-3.0-only |
| `internal/auth/` | Accounts, sessions, TOTP, tokens, the device grant, CI federation, RBAC ([ADR-0026](../docs/adr/0026-identity-tenancy-and-access.md)) | AGPL-3.0-only |
| `internal/tenancy/` | Organisations, teams, members, apps, environments, channels, variables, secrets, app access, trash, limit overrides | AGPL-3.0-only |
| `internal/document/` | Drafts, documents with revisions and JSON Patch, snapshots and their retention, editing locks, plugins, import and export in the Git layout, templates, components and usages (ADR-0015) | AGPL-3.0-only |
| `internal/audit/` | The hash-chained, append-only audit log (`SEC-140`) | AGPL-3.0-only |
| `internal/signing/` | The only package that touches keys: file and Vault Transit backends, envelope encryption (`SEC-106`, `SEC-120`, L-3) | AGPL-3.0-only |
| `internal/server/` | Role wiring, health, lifecycle ([server.md](../docs/reference/server.md)) | AGPL-3.0-only |
| `internal/<module>/` | Further server modules as listed in spec §6.3, added phase by phase | per `REUSE.toml` |

Server-only packages are AGPL-3.0-only; packages the CLI or third parties link (compiler, PXL, bundle, schema, SDK) are Apache-2.0 so they can be embedded anywhere (ADR-0022). A package's licence is declared in `REUSE.toml` before its first file is committed, and its files carry the matching SPDX header.

## Invariants

- `cmd/` contains wiring only; every command's logic sits behind a `run(args, stdout, stderr) int` (or a component constructor) so it is testable without exiting the process.
- Build metadata comes only from the commit (`internal/buildinfo`); binaries built from one commit are byte-identical (`make go-reproducible`, `CI-006`).
- Layering rules L-1 to L-7 (spec §6.7) are enforced by package structure: handlers → domain modules → storage; only `signing/` touches keys.
- The compiler performs no I/O except through injected interfaces (L-5) and is deterministic (`CMP-002`).

## Stop and ask before

- Adding a module to `internal/` that is not listed in spec §6.3.
- Changing the licence boundary between AGPL and Apache-2.0 packages.

## Required checks

`make go-check` — formatting, golangci-lint, tidiness, generated code, tests with `-race` and coverage floors, govulncheck, build and reproducibility.
