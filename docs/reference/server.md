# Server Reference

`plux-server` is one Go binary that runs the Plux Server in the roles its
configuration names (`SRV-001`, [ADR-0006](../adr/0006-modular-monolith-with-roles.md)).
This page covers how a process is configured, what it exposes and how it
starts and stops. The API it serves is in [api.md](api.md).

## 1. Roles

| Role | Runs | Holds | Exposure |
|---|---|---|---|
| `api` | The ConnectRPC services, the manifest and artifact endpoints, health and metrics | database, object storage, cache | public, behind ingress |
| `worker` | The publish, delta, asset and retention jobs | database, object storage, signing keys | internal only |
| `fnrunner` | WebAssembly function sandboxes (P7) | nothing | internal only, mTLS |

A process runs any subset; `[api, worker]` is the default and serves a
single-node installation. The role decides what is built at all: a process
without `worker` never constructs a signing client, so a bug in a handler
cannot reach a key (L-3). `fnrunner` is refused by the configuration until
P7.

## 2. Commands

| Command | Does |
|---|---|
| `plux-server serve` | Runs the configured roles until the process is interrupted |
| `plux-server migrate` | Applies pending migrations and exits, for deployments that migrate in a separate step |
| `plux-server config validate` | Checks a configuration file offline, with no connection to anything (`SRV-008`) |
| `plux-server version` | Prints the build version, commit and date |

Every command takes `-config <path>` (default `plux-server.yaml`). Exit
codes are `0` success, `1` the command failed, `2` a usage or
configuration error.

## 3. Configuration

Configuration is one YAML file plus environment variables
([Appendix H.1](../requirements.md#appendix-h--configuration-reference)).
The file is read strictly: an unknown key is an error, a repeated key is an
error, and a section that belongs to a later phase is refused by name with
the phase it arrives in, rather than being silently accepted.

```yaml
server:
  roles: [api, worker]          # api | worker  (fnrunner arrives in P7)
  listen: ":8080"
  publicBaseURL: "https://plux.acme.example"
  shutdownGrace: "30s"
  maxRequestSize: "8MiB"        # SEC-104
database:
  url: "postgres://plux@db:5432/plux?sslmode=verify-full"
  maxConnections: 50
  migrateOnStart: true          # SRV-021
objectStorage:
  backend: "filesystem"         # filesystem | s3
  directory: "data/objects"
  # For s3: endpoint, region, bucket, pathStyle, accessKeyID, secretAccessKey
  cdnBaseURL: ""                # empty: the server signs URLs or serves the bytes (DEP-041)
  signedURLTTL: "15m"
cache:
  backend: "memory"             # memory (single-node only) | valkey
  valkeyURL: "rediss://valkey:6379"
signing:
  backend: "file"               # file (development only); the KMS backends arrive in P6
  directory: "data/keys"
  keys:
    targets: "targets"          # signs bundle hashes and manifests (ADR-0004)
auth:
  studio:
    allowPasswordLogin: true
    oidc: { issuer: "", clientID: "", redirectURL: "" }
    mfaRequiredFor: [publish, approve, keys, members]
    sessionTTL: "12h"
  device:
    accessTokenTTL: "5m"
    refreshTokenTTL: "720h"
  ci:
    tokenTTL: "1h"
    issuers:
      - issuer: "https://token.actions.githubusercontent.com"
        audience: "plux"
        subjectPattern: "repo:acme/*:ref:refs/heads/main"
observability:
  logLevel: "info"              # debug | info | warn | error
  logFormat: "json"             # json | text
  otlpEndpoint: ""              # empty disables trace export
  traceSampleRatio: 1.0
telemetry:
  store: "postgres"             # clickhouse arrives in P9
  maxEventsPerRequest: 500
limits:                         # installation-level tightenings (LIM-001, LIM-002)
  "bundle.pluginSize": "8MiB"
  "page.nodes": "2000"
retention:
  auditYears: 10
  developmentReleaseDays: 90
  snapshotDays: 90              # at least 90 (SRV-031)
  trashDays: 30                 # at least 30 (GOV-031)
```

**Limits** name keys of the registry, `schema/limits.json`
([limits.md](limits.md)). A value may only **tighten** the default and may
never exceed the hard maximum (`LIM-002`); sizes take a unit such as
`8MiB`, everything else is a plain number. An unknown key is an error, so a
typo cannot silently do nothing.

**Environment variables** override the file. Only credentials and the
handful of values a container is told at start-up are read, and an empty
variable is treated as unset:

`PLUX_SERVER_ROLES`, `PLUX_SERVER_LISTEN`, `PLUX_SERVER_PUBLIC_BASE_URL`,
`PLUX_DATABASE_URL`, `PLUX_OBJECT_STORAGE_ENDPOINT`,
`PLUX_OBJECT_STORAGE_BUCKET`, `PLUX_OBJECT_STORAGE_ACCESS_KEY_ID`,
`PLUX_OBJECT_STORAGE_SECRET_ACCESS_KEY`, `PLUX_CACHE_VALKEY_URL`,
`PLUX_AUTH_STUDIO_OIDC_CLIENT_SECRET`, `PLUX_OBSERVABILITY_LOG_LEVEL`,
`PLUX_OBSERVABILITY_OTLP_ENDPOINT`.

## 4. Endpoints the api role always serves

| Path | Answers |
|---|---|
| `GET /livez` | That the process is running. It checks no dependency, so a database outage never restarts a healthy replica. |
| `GET /readyz` | That the process has finished starting **and** every dependency answers: PostgreSQL, the cache, object storage, and from P6 the KMS (`SRV-007`). A failing check is reported as `unavailable` and nothing more, because its message may quote a connection string. |
| `GET /metrics` | The Prometheus metrics of [Appendix G.1](../requirements.md#appendix-g--metrics-and-telemetry-events) (`OBS-002`). |

Every request body is bounded by `server.maxRequestSize` before a handler
sees it (`SEC-104`).

## 5. Starting and stopping

Components start in dependency order and stop in the reverse order.
On shutdown the process stops reporting ready **before** the listener
closes, so a load balancer drains it first; in-flight requests and running
jobs then have `server.shutdownGrace` to finish (`SRV-007`).

Migrations run on start under a PostgreSQL advisory lock when
`database.migrateOnStart` is set, so several replicas starting at once
converge without racing (`SRV-021`). Every migration is expand-only: it may
add and backfill, never drop or retype, because an N-1 binary is still
running during a rolling upgrade (`DEP-030`). A test refuses a migration
that breaks that rule, and another refuses one that was edited after it was
applied.

## 6. Storage

PostgreSQL is the system of record (`SRV-020`). Every table holding tenant
data carries `organization_id` and has row-level security bound to
`plux_current_organization()`, which the pool sets per transaction from the
authenticated principal; authorisation happens in the service layer first,
and row-level security is the second barrier (`SRV-022`, `SEC-102`).

Bundles, deltas, assets and exports are content-addressed objects under
`<kind>/<first two hex digits>/<sha256>` (`SRV-023`). Two backends serve
them: `s3` for deployments, and `filesystem` for development. With a CDN
configured the server hands out the CDN URL; otherwise it signs a short
URL, or serves the bytes itself with range support (`DEP-041`, `REL-024`).

## 7. Observability

Logs are structured JSON with the request ID, the trace and span IDs and
the process's role and version. Attributes whose name suggests a secret —
anything containing `password`, `token`, `secret`, `credential`,
`authorization`, `cookie`, `session`, `signature`, `url` and the rest —
and values that declare themselves sensitive are replaced with
`[redacted]` before the handler sees them (`OBS-003`, `SEC-092`).

Traces follow W3C Trace Context from Studio, the CLI and devices through
the api and worker roles (`OBS-001`); with no `otlpEndpoint` the tracer is
a no-op that still propagates context, so nothing in the server needs to
know whether tracing is on.

## 8. Outbound requests

Every server-side fetch of a user-supplied URL goes through the SSRF-safe
client: private, loopback, link-local — including the cloud metadata
address — carrier-grade NAT, multicast and documentation ranges are
refused, and the address is checked again after DNS resolution and after
every redirect, so a name that resolves differently the second time cannot
reach an internal service (`SEC-105`).
