# Server API Reference

The Plux Server exposes every capability through one Protocol Buffers
contract in [`proto/plux/v1/`](../../proto), served with ConnectRPC over
gRPC, gRPC-Web and Connect, on HTTP/1.1 and HTTP/2 (`SRV-002`,
[ADR-0005](../adr/0005-connectrpc-and-protobuf.md)). The generated OpenAPI
3.1 description of the same surface is [api/openapi.yaml](api/openapi.yaml),
for integrators who call it as plain HTTP/JSON.

## 1. Services

| Service | Responsibility | Requirements |
|---|---|---|
| `OrgService` | Organisations, teams, memberships, organisation limits | `GOV-001`, `LIM-002` |
| `IdentityService` | Sign-in (built-in and OIDC), second factors (TOTP, WebAuthn), sessions, personal access tokens, CI federation, the device grant, audit | `SEC-100`, `SEC-101`, `SRV-064`, `CLI-002`, `SEC-140` |
| `AppService` | Apps, environments, variables, secrets, channels, trash, app limits | `GOV-010`, `GOV-031`, `SEC-106` |
| `PluginService` | Plugins and editing locks | `SRV-040`–`SRV-042` |
| `DocumentService` | Draft documents, JSON Patch writes, snapshots, validation, export and import | `SRV-030`, `SRV-031`, `SCH-042` |
| `ComponentService` | Components and where each is used | `SCH-041` |
| `TemplateService` | Templates and instantiation with fresh identifiers | `SCH-031` |
| `AssetService` | Uploads, sniffing, variants | `SRV-060`, `AST-003` |
| `PublishService` | Publish jobs with streamed progress | `SRV-050`, `SRV-051` |
| `ReleaseService` | Plugin versions, app releases, promotion, rollback, changelog, compatibility | `REL-001`–`REL-007`, `REL-080`, `REL-081` |
| `ManifestService` | Signed manifest, sync plan, public keys | `REL-020`–`REL-024`, `REL-030`–`REL-033` |
| `DeviceService` | Device registration and installed releases | `GOV-010`, `REL-080` |
| `TokenService` | Short-lived device tokens | `GOV-010`, `SRV-065` |
| `TelemetryService` | Runtime event ingestion and listing | Appendix G.2, `SCH-012` |
| `ControlService` | Kill switches and the mandatory-update flag | `REL-030` |

Services listed in `SRV-003` for later phases — rollouts, experiments,
functions, localisation, approvals, dev sessions, AI, payments, admin —
arrive with their phases.

## 2. Calling conventions

**Organisation.** A call acts in one organisation: the one its token is
bound to, or, for a session, the one named by the `X-Plux-Organization`
header or the request's `organization_id`, which must agree when both are
given. An organisation the caller has no part in is reported as
`not_found`, so its existence is not disclosed (`GOV-001`, `SEC-102`).

**Paging.** Every list call takes `Page` (`page_size`, `page_token`,
`filter`, `order_by`) and returns `PageResult`. An unset or oversized
`page_size` is the registry limit `api.pageSize`. Page tokens are opaque and
integrity-protected: each is bound to the procedure, the caller's scope, the
filter and the ordering it was issued for, authenticated with an HMAC under
an installation key, valid for a day, and rejected elsewhere
(`invalid_argument`, `PLX-1032`). A filter is a conjunction of equalities
on the fields each list documents, `key = "shop" AND production = "true"`;
each list is served in one ordering, which `order_by` may name. A filtered
page may hold fewer items than asked for; the token continues after the
last item read. No endpoint returns an unbounded list (`SRV-004`).

**Idempotency.** Mutating calls accept an `Idempotency-Key` request header
(1 to 128 printable ASCII characters). A repeat with the same key within 24
hours returns the original result, marked `Idempotent-Replayed: true`,
rather than acting twice; the same key with a different request, or while
the first call is still running, is refused (`already_exists`, `PLX-1031`).
A failed call releases its key. Keys belong to the caller — a user, a
token — and results are stored encrypted, because some carry a secret
returned only once. Calls made without a credential (signing in, polling a
device grant) are never replayed: their results are credentials
(`SRV-005`).

**Field masks.** Every list call's `Page` carries a `read_mask`
(`google.protobuf.FieldMask`) that limits each returned item to the named
fields; an empty mask returns whole items (`SRV-004`).

**Rate limits.** Every call, unary or streaming, counts against its client
address, and an authenticated call also against its principal (a user,
token or device). The allowances are registry limits
(`api.requestsPerMinutePerAddress`, `api.requestsPerMinute`,
`api.requestsPerMinutePerDevice`); a refused call returns
`resource_exhausted` with `Retry-After` (`SRV-065`).

**Documents as bytes.** Document content is carried as canonical JSON bytes
(`SCH-003`), never as protobuf structures, so a document's hash is a property
of what was stored and extension properties survive a round trip (`SCH-004`).

**Concurrency.** Document writes carry `if_revision`; a mismatch is a
conflict the client resolves by re-reading (`SRV-030`). Writing to a draft
also requires holding the editing lock (`SRV-040`).

**Streaming.** `PublishService.WatchPublish` and `DocumentService.ExportDraft`
are server-streaming; `DocumentService.ImportDraft` and
`AssetService.UploadAsset` are client-streaming, with the first message
carrying the target and no payload.

**Publishing and releases** ([ADR-0020](../adr/0020-build-once-promote-releases.md)).
`Publish` freezes a draft at a revision and returns a `queued` job; the
worker compiles, checks, signs and stores it, and `WatchPublish` streams
its stage, percentage and new diagnostics until it ends `succeeded` with a
version number, `failed` with diagnostics, or `cancelled`. An error, or a
warning not acknowledged with `acknowledge_warnings`, fails the job with
nothing recorded (`PLX-8051`). An empty `plugin_id` publishes the app
bundle; `CreateRelease` takes it under the key `""`. `CreateRelease`
answers with diagnostics and no release when a plugin has no version
(`PLX-8052`), a reference does not resolve, or a version was compiled
against other sources (`PLX-8050`). Publishing, creating a release,
promoting and rolling back need a session that presented a second factor.
Limit listings report measured usage in `LimitUsage.value` where P2
measures it: plugins per app, the newest release's size, pages per plugin
and the newest bundle's size (`LIM-005`).

**Assets** ([ADR-0027](../adr/0027-asset-pipeline.md)). `UploadAsset` is
client-streaming: the first message names the app, the file under
`assets/` and the editing session, and every message carries a chunk. The
file is typed from its bytes, packaged (Lottie becomes dotLottie), stripped
of metadata, scanned when a scanner is configured, and listed in the
app-level draft's `assets/index.json` under the app's lock; an upload under
an existing name replaces the file and keeps its asset ID. Assets belong to
the app, so `plugin_id` must be empty. A raster image comes back with
`processing` `pending`; the worker then adds its WebP and AVIF variants and
sets `ready`, or `failed` with diagnostics. Links in `Artifact.urls` are
signed or CDN URLs valid for fifteen minutes. Refusals: `invalid_argument`
for an unknown or malformed file (`PLX-1005`) or one the scanner rejects
(`PLX-6031`), `resource_exhausted` above `asset.fileSize` or
`asset.imagePixels` (`PLX-1320`).

**Drafts** ([ADR-0015](../adr/0015-single-draft-with-snapshots-and-locks.md)).
Every plugin has one draft, and the app has one for its own documents
(`app.json`, theme, translations, native catalogue, components, templates),
each with its own editing lock (`SRV-042`). A write names the editing
`session` holding the lock — 1 to 128 printable ASCII characters chosen by
the client, one per editor tab — and the `if_revision` it read: `0`
creates, and a stale revision is `aborted` with the current one in the
error's `revision` detail (`SRV-030`). A document with a structural error
is refused whole with `invalid_argument` and its file and JSON Pointer as
details; compiler findings about a structurally valid document come back
as diagnostics. A write refused because the lock is held elsewhere
carries `holder` and `expiresAt`; when the writer is the session a
takeover displaced, its write is kept as an unapplied snapshot named in
`preservedSnapshot`, which the new holder can compare and restore
(`SRV-041`). Snapshot pages run newest first. `ImportDraft` for the whole
app creates the plugins its `plugins/<key>/` directories name, takes each
draft's lock for the session, and deletes documents the import does not
contain; assets are not documents and are uploaded with `AssetService`.

## 3. Errors

A failure returns a Connect code, `google.rpc.ErrorInfo` with the reason and
the domain `plux.dev`, and the Plux error code from Appendix F. Domain code
never constructs transport errors: one interceptor translates registered
reasons at the edge (`SRV-006`, [ADR-0018](../adr/0018-unified-error-model.md)).

| Connect code | Used for |
|---|---|
| `invalid_argument` | malformed input, failed structural validation |
| `failed_precondition` | the editing lock is held elsewhere (`PLX-8020`), a publish with errors, a state that forbids the call (`PLX-8033`) |
| `permission_denied` | authorisation refused (`PLX-8030`), a second factor needed (`PLX-8011`) |
| `not_found` | the resource does not exist, or the caller may not see it (`PLX-8031`) |
| `already_exists` | a key already taken (`PLX-8032`), an idempotency key reused (`PLX-1031`) |
| `aborted` | revision conflict on a document write |
| `resource_exhausted` | a rate limit or a limits-registry limit (`SRV-065`, `LIM-003`) |
| `unauthenticated` | no or invalid credential (`PLX-8012`) |
| `internal` | an unexpected failure; the response carries an incident ID only |

Findings about documents are **diagnostics**, not errors: a call that
validates returns `repeated Diagnostic` and succeeds, even when the
diagnostics are errors, so every problem is reported at once.

## 4. Authentication

| Caller | Credential |
|---|---|
| Studio | the `__Host-plux_session` cookie (`HttpOnly`, `Secure`, `SameSite=Strict`, `Path=/`) set by `StartPasswordLogin` or `CompleteMfa`, plus the session's CSRF token in `X-CSRF-Token` on **every** call made with it (`SEC-101`) |
| CLI | `Authorization: Bearer plux_pat_…`: a personal access token, or one obtained by `plux login` through the OAuth 2.0 device authorization grant (`CLI-002`) |
| CI | a bearer token exchanged by `ExchangeWorkloadIdentity` from the provider's OpenID Connect identity token, with no long-lived secret (`SRV-064`) |
| Device | `Authorization: Bearer plux_dat_…`: a 15-minute token from `IssueDeviceToken`, exchanged for the `plux_dsec_…` credential `RegisterDevice` returned once; sender-constrained with DPoP and backed by attestation from P6 |

A request carrying both a cookie and a bearer token is refused. The
procedures that take no credential authenticate by what they carry:
`AcceptInvitation`, `StartPasswordLogin`, `CompleteMfa`,
`StartDeviceAuthorization`, `PollDeviceAuthorization`,
`ExchangeWorkloadIdentity`, `RegisterDevice` and `IssueDeviceToken`.

A device token opens exactly four procedures — `GetManifest`,
`GetRootKeys`, `ReportInstalled` and `IngestEvents` — and those need one,
except `GetRootKeys`, which people and CI may also call for an
environment of an app they can read (`plux pull`). Every other procedure
refuses a device token. A device's calls count against its own allowance,
`api.requestsPerMinutePerDevice` (`SRV-065`).

### Manifest and sync plan

`GetManifest` answers with the newest manifest the worker signed for the
device's own app, environment and the requested channel (default
`production`). `signed` holds the RFC 8785 canonical JSON the signatures
cover: type, spec version, role (`targets`), app, environment, channel,
release sequence, issue and expiry times, the app bundle and every
plugin's key, version, bundle hash, size, required features and minimum
runtime, the control switches, and experiment assignments (empty until
P9) (`REL-030`). The typed fields repeat it for convenience; a device
verifies `signed`, never a re-encoding.

The request lists the bundles the device holds; each bundle's `sync` says
`keep`, `delta` (with the hash it applies to, its URL and size) or `full`
(`REL-032`). A delta is offered only when it is at most 60 % of the
compressed bundle (`REL-023`). The plan and the download URLs are **not**
part of `signed`: they depend on the device and on where objects are
hosted, and they need no signature, because the device checks what it
rebuilds or downloads against the signed hashes. The response carries an
`ETag` over the manifest and the installed bundles; sending it back as
`if_none_match` returns `not_modified` and nothing else (`REL-031`).

Authorisation is deny-by-default and evaluated for every call against RBAC
permissions and resource scope, with PostgreSQL row-level security as a
second barrier (`SEC-102`). The roles of P2 are owner, admin, developer and
viewer; a team's members hold the roles the team is granted on single apps.
Publishing, promoting, managing keys, members, secrets and tokens need a
session in which a second factor was presented (`SEC-100`); tokens carry
scopes fixed when they were minted, never more than their creator held
([ADR-0026](../adr/0026-identity-tenancy-and-access.md)).

## 5. Artifacts

Bundles, deltas and assets are **not** returned through this API. They are
content-addressed objects served over plain HTTP from object storage, a CDN
or the server itself, with `Cache-Control: public, max-age=31536000,
immutable` and range support (`SRV-023`, `REL-024`, `DEP-041`). The API
returns their hashes, sizes and URLs; the manifest signs the hashes.
Without a CDN the api role serves bundles and deltas itself at
`GET /v1/objects/<kind>/<xx>/<sha256>`.
