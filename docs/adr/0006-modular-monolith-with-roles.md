# 0006. Modular monolith with deployable roles instead of microservices

- **Status:** Accepted
- **Date:** 2026-09-27
- **Requirements:** `SRV-001`, `SRV-007`, `SRV-008`, `OBS-001`, `OBS-002`, `OBS-003`, `DEP-002`, `DEP-020`, `NFR-023`

## Context and problem

Plux must run both as a single-node installation a self-hoster starts with one command
(`DEP-002`) and as a horizontally scaled deployment serving a million devices (`NFR-023`).
Three workloads have genuinely different shapes: public request serving, background
publishing that touches signing keys, and function execution that must hold no credentials
at all (L-3, L-4). How should the server be decomposed?

## Decision drivers

- One binary a self-hoster can run without an orchestrator (`SRV-001`, `DEP-002`).
- Independent scaling and independent blast radius for the three workloads.
- Hard isolation of signing keys and of the function sandbox, enforceable by deployment.
- Module boundaries strict enough that a later split into services would be mechanical.
- Low operational surface: one image, one configuration file, one set of metrics.

## Considered options

1. **A modular monolith: one Go module, one binary, roles selected by configuration.**
2. Separate services and images per bounded context from the start.
3. One process with no roles at all.

## Decision

Chosen option: **1**, as the specification requires (`SRV-001`).

### Roles

`plux-server` reads `server.roles` from its configuration (Appendix H) and starts the
components of each role in one process. Any subset is valid; `[api, worker, fnrunner]` is
the single-node default.

| Role | Runs | Holds | Exposure |
|---|---|---|---|
| `api` | Connect services, manifest and artifact endpoints, device registration, telemetry ingestion | database, object storage, cache | public, behind ingress |
| `worker` | publish jobs: compile, sign, delta, assets, retention | database, object storage, **signing keys** | internal only |
| `fnrunner` | WASM function sandboxes (P7) | nothing | internal only, mTLS |

The role decides which dependencies are constructed at all. A process without `worker`
never builds a signing client, so a bug in an API handler cannot reach a key (L-3); a
`fnrunner` process is never given a database pool (L-4). This is enforced by construction
in `internal/server`, and by a test that asserts the dependency set of each role.

### Module boundaries

The packages of §6.3 communicate through interfaces defined by the **consumer**, never by
importing another module's storage or transport (L-2). `internal/api` depends on domain
interfaces; domain packages depend on `storage` interfaces; `storage` owns every SQL
statement. An import-boundary test fails the build when a domain package imports
`connectrpc.com/connect`, `net/http` or another domain package's internals. This keeps a
future extraction of, say, `fnrunner` into its own service a matter of adding a transport.

### Lifecycle

Every role registers components with a supervisor that starts them in dependency order and
stops them in reverse. `/livez` reports process health; `/readyz` checks PostgreSQL, object
storage and, where configured, Valkey and the KMS (`SRV-007`). Shutdown stops accepting new
requests, drains in-flight ones and lets running jobs finish or be released back to the
queue within a configurable grace period. Configuration is loaded once, validated in full,
and never re-read at runtime (`SRV-008`); there is no global mutable state and no
import-time side effect (AGENTS.md §8).

### Observability

One OpenTelemetry tracer and one Prometheus registry per process, labelled with the role.
W3C Trace Context is propagated from Studio, the CLI and devices into jobs, so a publish is
one trace from the call to the stored artifacts (`OBS-001`). Metrics are exactly those of
Appendix G, with label cardinality bounded by construction: no metric is labelled with an
identifier of an organisation, app, plugin or device unless Appendix G says so
(`OBS-002`). Logs are `log/slog` JSON with request and trace IDs, through a handler that
drops any attribute marked sensitive (`OBS-003`, `SEC-092`).

## Consequences

- **Positive:** one image and one configuration file for every deployment shape; isolation that a self-hoster gets by running roles in separate processes; no network hop, no serialisation and no distributed transaction inside one publish.
- **Negative:** a bug that crashes the process takes every role in it down on a single-node install; module discipline has to be enforced by tests rather than by the compiler and the network.
- **Follow-up:** `fnrunner` is empty until P7; the mTLS link between `api` and `fnrunner` is specified there.

## Options in detail

### Option 2 — microservices from the start

Would give the isolation by construction, but at a cost this project cannot justify yet:
several images, inter-service contracts, distributed tracing as a necessity rather than a
convenience, and a self-hosted installation that needs an orchestrator. Every boundary that
would matter is already enforced here as a role plus an import rule, and the migration path
stays open.

### Option 3 — one process, no roles

Simplest to operate, but it puts signing keys in the same process as the public API and
gives function sandboxes a database pool, which `SEC-120`, L-3 and L-4 forbid. Rejected.
