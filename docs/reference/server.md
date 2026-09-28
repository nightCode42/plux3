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
without `worker` is given only envelope encryption — wrapping and unwrapping
the data keys of stored secrets — and never a signer, so a bug in a handler
cannot sign anything (L-3). With Vault, give an api-only process a token
whose policy allows only `encrypt` and `decrypt` on the wrapping key.
`fnrunner` is refused by the configuration until P7.

## 2. Commands

| Command | Does |
|---|---|
| `plux-server serve` | Runs the configured roles until the process is interrupted |
| `plux-server migrate` | Applies pending migrations and exits, for deployments that migrate in a separate step |
| `plux-server config validate` | Checks a configuration file offline, with no connection to anything (`SRV-008`) |
| `plux-server bootstrap -email <address>` | Creates the first installation administrator and prints a one-time invitation, valid for seven days, with which they set a password; refused once an administrator exists (`SEC-100`, [ADR-0026](../adr/0026-identity-tenancy-and-access.md)) |
| `plux-server seed [-out <dir>\|-]` | For a development installation only: creates an administrator (`dev@plux.localhost`), an organisation and an app, and writes the password, a 30-day CLI token and the IDs to user-only files in `-out` (default `.plux-dev`), or prints them once as `PLUX_DEV_*` lines with `-out -`. Runs once per database; refused when the signing backend may sign for production (`DEP-020`). `make dev` and `make compose-seed` run it once ([Compose stack](../../deploy/compose/README.md)) |
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
  trustedProxies: ["10.0.0.0/8"] # whose X-Forwarded-For is believed
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
  backend: "file"               # file (development only) | vault; PKCS#11 and cloud KMS arrive in P6
  directory: "data/keys"        # the file backend's keys
  vault:                        # HashiCorp Vault Transit (SEC-120)
    address: "https://vault:8200"
    mount: "transit"
    wrapKey: "plux-secrets"     # AES-256-GCM key that wraps data keys (SEC-106)
    # token: set with PLUX_SIGNING_VAULT_TOKEN
  keys:
    targets: "targets"          # prefix of each environment's key, "<prefix>-<environment ID>" (ADR-0004)
auth:
  studio:
    allowPasswordLogin: true    # may be false only when an OIDC provider is configured
    oidc:                       # optional OpenID Connect sign-in (SEC-100)
      issuer: "https://idp.acme.example"
      clientID: "plux-studio"
      redirectURL: "https://plux.acme.example/auth/callback"
      # clientSecret: set with PLUX_AUTH_STUDIO_OIDC_CLIENT_SECRET
    mfaRequiredFor: [publish, approve, keys, members]   # at least these four (SEC-100)
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
limits:                         # installation-level tightenings (LIM-001, LIM-002)
  "bundle.pluginSize": "8MiB"
  "page.nodes": "2000"
  "api.requestSize": "4MiB"
  "api.requestsPerMinutePerAddress": "120"
retention:
  auditYears: 10
  developmentReleaseDays: 90
  snapshotDays: 90              # at least 90 (SRV-031)
  trashDays: 30                 # at least 30 (GOV-031)
assets:
  malwareScanner: ""            # ClamAV clamd, tcp://host:3310 or unix:///path; empty scans nothing (SRV-060)
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
`PLUX_AUTH_STUDIO_OIDC_CLIENT_SECRET`, `PLUX_SIGNING_VAULT_TOKEN`,
`PLUX_OBSERVABILITY_LOG_LEVEL`,
`PLUX_OBSERVABILITY_OTLP_ENDPOINT`.

## 4. Endpoints the api role always serves

| Path | Answers |
|---|---|
| `GET /livez` | That the process is running. It checks no dependency, so a database outage never restarts a healthy replica. |
| `GET /readyz` | That the process has finished starting **and** every dependency answers: PostgreSQL, the cache, object storage, and from P6 the KMS (`SRV-007`). A failing check is reported as `unavailable` and nothing more, because its message may quote a connection string. |
| `GET /metrics` | The Prometheus metrics of [Appendix G.1](../requirements.md#appendix-g--metrics-and-telemetry-events) (`OBS-002`). |
| `GET /v1/objects/{bundles,deltas}/…` | Bundles and deltas by content address, when object storage has no CDN in front of it (`storage.objects.cdnBaseURL` unset): `Cache-Control: public, max-age=31536000, immutable`, an `ETag` of the hash and range requests (`REL-024`, `DEP-041`). |

Every request body is bounded by the registry limit `api.requestSize`
before a handler sees it (`SEC-104`), and every call — unary or
streaming — is counted against its client's address
(`api.requestsPerMinutePerAddress`, `SRV-065`). The client address is the
immediate peer, unless that peer is listed in `server.trustedProxies`, in
which case `X-Forwarded-For` is read from the right, skipping trusted
hops; a client therefore cannot choose its own address by sending the
header. The same address is recorded in the audit log (`SEC-140`).

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
and row-level security is the second barrier (`SRV-022`, `SEC-102`). Two
more settings widen a few policies, each for one purpose and never on a
caller's behalf: the signed-in user may read their own memberships in every
organisation, and a *scope* lets a token be found by its hash before its
organisation is known (`authentication`) or the installation's own audit
entries be written (`installation`). A test walks the catalogue and fails
on any table that holds tenant data without the policy
([ADR-0026](../adr/0026-identity-tenancy-and-access.md)).

The audit log is one hash chain per organisation and one for the
installation (sign-ins, invitations, second factors). A trigger refuses
any `UPDATE`, `DELETE` or `TRUNCATE`, and appends to one chain are
serialised by an advisory lock, so the chain never forks (`SEC-140`).

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

## 9. Background work

The worker role runs publishes on the `publish` queue: each job, enqueued
with the frozen draft, compiles, checks, signs the bundle hash with the
environment's key and stores the bundle and its source map; only the
worker is given a signer (`SRV-052`). The maintenance sweep also deletes
development releases past `retention.developmentReleaseDays` (`REL-007`).

On the same queue the worker signs manifests and precomputes deltas. A
manifest job is enqueued in the transaction that promotes a release to a
channel or changes its switches; the worker builds the signed part,
signs it with the environment's key and stores it, and the api role
serves the newest one. The file signing backend never signs for an
environment marked production (`SEC-056`). A delta job is enqueued with
every published version: it computes the deltas to it from the plugin's
ten newest versions and from the five bundles most devices hold
(`REL-022`). Any other pair is computed by the api role on the first
request that needs it, once per pair however many requests arrive
together, and stored by the hash of its bytes
([ADR-0003](../adr/0003-section-level-deltas.md)).

The worker role transcodes uploaded raster images on the `asset` queue:
each upload's job, enqueued in the upload's transaction, makes WebP and
AVIF variants at 1×, 2× and 3× with WebAssembly codecs, stores them by
hash and marks the asset ready; content transcoded before is reused, and
an image the codecs refuse is marked failed with a diagnostic
([ADR-0027](../adr/0027-asset-pipeline.md)). Only the worker role compiles
the codecs.

The worker role runs the maintenance sweep hourly and once at start, on
the `maintenance` queue: it purges trash past `retention.trashDays`
(`GOV-031`), deletes draft history older than `retention.snapshotDays`
(`SRV-031`) — first copying into each surviving snapshot whatever of its
state lived only in the history being deleted, so every remaining snapshot
still restores exactly, and never deleting a draft's newest snapshot or a
kept one — then drops document content no longer referenced, forgets
idempotency keys older than a day (`SRV-005`) and
deletes expired sessions, sign-in challenges and device grants. It also
signs a fresh manifest for every channel whose newest one expires within
two days (manifests live seven), deletes expired manifests other than
each channel's newest, expired device tokens and telemetry events older
than 30 days. River's
leader election makes one worker enqueue it however many replicas run.

## 10. Identity

People sign in with built-in accounts or the configured OpenID Connect
provider, and present TOTP codes or WebAuthn security keys as their second
factor ([ADR-0026](../adr/0026-identity-tenancy-and-access.md)). A provider
identity is linked, on its first sign-in, to the invited account with the
address the provider verified. Security keys are registered for the host
of `server.publicBaseURL` as the WebAuthn relying party, and are off when
that is not an https URL. SAML, provisioning and group mapping arrive with
`GOV-004`, and WebAuthn step-up with `SEC-103`, in P9.
The first administrator is created with `plux-server bootstrap`; everyone
else is invited by an organisation's owner. The API's credentials, the
organisation header and the error each refusal returns are in
[api.md](api.md) §4.
