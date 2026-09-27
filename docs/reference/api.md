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
| `IdentityService` | Sign-in (built-in and OIDC), MFA, sessions, personal access tokens, CI federation, the device grant, audit | `SEC-100`, `SEC-101`, `SRV-064`, `CLI-002`, `SEC-140` |
| `AppService` | Apps, environments, variables, secrets, channels, trash, app limits | `GOV-010`, `GOV-031`, `SEC-106` |
| `PluginService` | Plugins and editing locks | `SRV-040`–`SRV-042` |
| `DocumentService` | Draft documents, JSON Patch writes, snapshots, validation, export and import | `SRV-030`, `SRV-031`, `SCH-042` |
| `ComponentService` | Components and where each is used | `SCH-041` |
| `TemplateService` | Templates and instantiation with fresh identifiers | `SCH-031` |
| `AssetService` | Uploads, sniffing, variants | `SRV-060`, `AST-003` |
| `PublishService` | Publish jobs with streamed progress | `SRV-050`, `SRV-051` |
| `ReleaseService` | Plugin versions, app releases, promotion, rollback, changelog, compatibility | `REL-001`–`REL-007`, `REL-080`, `REL-081` |
| `ManifestService` | Signed manifest, sync plan, public keys | `REL-030`–`REL-033` |
| `DeviceService` | Device registration and installed releases | `REL-080` |
| `TokenService` | Short-lived device tokens | `SRV-064` |
| `TelemetryService` | Runtime event ingestion | Appendix G.2 |
| `ControlService` | Kill switches and the mandatory-update flag | `REL-030` |

Services listed in `SRV-003` for later phases — rollouts, experiments,
functions, localisation, approvals, dev sessions, AI, payments, admin —
arrive with their phases.

## 2. Calling conventions

**Paging.** Every list call takes `Page` (`page_size`, `page_token`,
`filter`, `order_by`) and returns `PageResult`. Page tokens are opaque and
integrity-protected: each is bound to the filter and ordering it was issued
for, authenticated with an HMAC under a server key, and rejected elsewhere.
No endpoint returns an unbounded list (`SRV-004`).

**Idempotency.** Mutating calls accept an `Idempotency-Key` request header.
A repeat with the same key within 24 hours returns the original result rather
than acting twice (`SRV-005`).

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

## 3. Errors

A failure returns a Connect code, `google.rpc.ErrorInfo` with the reason and
the domain `plux.dev`, and the Plux error code from Appendix F. Domain code
never constructs transport errors: one interceptor translates registered
reasons at the edge (`SRV-006`, [ADR-0018](../adr/0018-unified-error-model.md)).

| Connect code | Used for |
|---|---|
| `invalid_argument` | malformed input, failed structural validation |
| `failed_precondition` | the editing lock is held elsewhere (`PLX-8020`), a publish with errors |
| `permission_denied` | authorisation refused (`PLX-8030`) |
| `not_found` | the resource does not exist, or the caller may not see it |
| `aborted` | revision conflict on a document write |
| `resource_exhausted` | a rate limit or a limits-registry limit (`SRV-065`, `LIM-003`) |
| `unauthenticated` | no or invalid credential |
| `internal` | an unexpected failure; the response carries an incident ID only |

Findings about documents are **diagnostics**, not errors: a call that
validates returns `repeated Diagnostic` and succeeds, even when the
diagnostics are errors, so every problem is reported at once.

## 4. Authentication

| Caller | Credential |
|---|---|
| Studio | a `__Host-` prefixed, `HttpOnly`, `Secure`, `SameSite=Strict` session cookie plus a CSRF token on state-changing calls (`SEC-101`) |
| CLI | a personal access token, or one obtained by `plux login` through the OAuth 2.0 device authorization grant (`CLI-002`) |
| CI | a token exchanged from the provider's OIDC identity token, with no long-lived secret (`SRV-064`) |
| Device | a short-lived token from `TokenService`; sender-constrained with DPoP from P6 |

Authorisation is deny-by-default and evaluated for every call against RBAC
permissions and resource scope, with PostgreSQL row-level security as a
second barrier (`SEC-102`).

## 5. Artifacts

Bundles, deltas and assets are **not** returned through this API. They are
content-addressed objects served over plain HTTP from object storage, a CDN
or the server itself, with `Cache-Control: public, max-age=31536000,
immutable` and range support (`SRV-023`, `REL-024`, `DEP-041`). The API
returns their hashes, sizes and URLs; the manifest signs the hashes.
