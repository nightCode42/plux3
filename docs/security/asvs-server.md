# ASVS 5.0 L2 and API Security Top 10 — Plux Server

The server half of `SEC-109`, checked at the end of P2. Each row names the control, how
Plux meets it, and the evidence. Studio's half (browser controls) is added in P11, so
`SEC-109` stays `WIP` until then. Evidence is a test, a CI check or a configuration file
in this repository; "P6" and "P11" mark controls that belong to a later phase.

## OWASP ASVS 5.0, level 2 — by chapter

| Chapter | Control area | How Plux meets it | Evidence |
|---|---|---|---|
| V1 Encoding and sanitisation | Output encoding, injection | All SQL through `sqlc`-generated, parameterised queries; responses are protobuf or JSON produced by the Connect codecs; no server-side templates | `internal/storage`, `.golangci.yml` (gosec) |
| V2 Validation and business logic | Input validation, anti-automation | Request, JSON depth, string and list limits from the registry (`SEC-104`, `LIM-001`); documents validated against JSON Schema and the compiler before publish; rate limits per address, account and device | `internal/api` limit tests, `internal/compiler` tests |
| V3 Web frontend security | Cookies, CSRF, CSP | `__Host-` cookies, `Secure`, `HttpOnly`, `SameSite=Strict`; CSRF token per session. Nonce CSP and Trusted Types are Studio's (P11) | `internal/api` edge tests, `internal/config` tests |
| V4 API and web service | HTTP methods, content types, GraphQL/WS | ConnectRPC only accepts POST for mutations and declared content types; no GraphQL; streams bounded by the same limits as unary calls | `internal/api` interceptor tests |
| V5 File handling | Uploads, storage, download | Types sniffed from bytes; metadata stripped; dotLottie paths, entry counts and expanded size checked; optional ClamAV scan; objects stored by hash and never executed | `internal/compiler/media` tests, `FuzzMedia` |
| V6 Authentication | Passwords, MFA, lockout | Argon2id; invitation-only accounts; decoy hash; throttling; TOTP (single use per step) and WebAuthn factors; second factor required for sensitive capabilities (`SEC-100`) | `internal/auth` tests |
| V7 Session management | Session lifecycle | Opaque server-side sessions with an absolute expiry (`auth.studio.sessionTTL`, 12 h by default), ended on sign-out; a password change ends every other session. Idle expiry arrives with Studio (P11) | `internal/auth` session tests |
| V8 Authorisation | Access control | Deny by default; RBAC per procedure; per-app grants; PostgreSQL row-level security for every tenant table (`SRV-020`, `SEC-102`) | `internal/api`, `internal/tenancy`, `internal/storage` integration tests |
| V9 Self-contained tokens | JWT and similar | OIDC ID tokens and CI federation tokens verified (algorithm pinned, issuer, audience, expiry, nonce); Plux's own tokens are opaque and stored as hashes | `internal/auth` OIDC and federation tests |
| V10 OAuth and OIDC | Client flows | Authorization code with PKCE, nonce and one-time hashed state; RFC 8628 device grant with `slow_down` | `internal/auth` tests |
| V11 Cryptography | Algorithms, keys, randomness | Ed25519 signatures; AES-GCM envelope sealing bound to the row (`SEC-106`); `crypto/rand` only; only `signing/` touches private keys; the api role cannot sign | `internal/signing` tests (`SEC-120`) |
| V12 Secure communication | TLS | TLS terminated in front of the server (Compose: localhost only; production: load balancer or ingress); outbound calls use TLS with system roots | `deploy/compose/compose.yaml`, `internal/httpx` |
| V13 Configuration | Secrets, dependencies, hardening | Strict configuration refusing unknown keys and unsafe values; secrets only from the environment; allowlisted and vulnerability-scanned dependencies (`govulncheck`); distroless non-root images (`SEC-108`) | `internal/config` tests, CI `go-vuln`, `backend/Dockerfile` |
| V14 Data protection | Sensitive data | Redacting logger, fixed metric labels, sensitive-looking telemetry fields refused, secrets never returned after creation, retention sweeps | `internal/observability`, `internal/telemetry`, `internal/tenancy` tests |
| V15 Secure coding and architecture | Dependencies, SSRF, concurrency | Layering rules (spec §6.7); SSRF-safe client (`SEC-105`); `-race` in CI; fuzz targets for every parser | CI `go-cover`, nightly fuzz |
| V16 Logging and error handling | Security events, errors | Append-only audit chain (`SEC-140`); errors mapped to stable codes, internal failures reported only as an incident ID (`SRV-006`) | `internal/audit`, `internal/api` tests |
| V17 WebRTC | — | Not used | — |

## OWASP API Security Top 10 (2023)

| Risk | How Plux addresses it | Evidence |
|---|---|---|
| API1 Broken object-level authorisation | Row-level security scopes every query to the caller's organisation; resource IDs are checked against per-app grants | `internal/storage` RLS tests, `internal/tenancy` tests |
| API2 Broken authentication | See V6, V7, V9, V10; device tokens limited to the device procedures | `internal/auth`, `internal/device` tests |
| API3 Broken object-property-level authorisation | Read masks and explicit response messages; secrets never returned; the environment's signing key cannot be chosen by the caller | `internal/api` read-mask tests |
| API4 Unrestricted resource consumption | Registry limits on size, count and rate; page size capped (`api.pageSize`); bounded decompression and transcoding | `internal/api`, `delta`, `media` tests |
| API5 Broken function-level authorisation | Every procedure declares its permission; the interceptor refuses before the handler runs | `internal/api` authorisation tests |
| API6 Unrestricted access to sensitive business flows | Publish, approval, key and member changes need their permissions and a second factor (`auth.studio.mfaRequiredFor`, which must keep those four); rate limits on sign-in and device registration | `internal/release`, `internal/auth` tests |
| API7 Server-side request forgery | SSRF-safe client for every outbound call (`SEC-105`) | `internal/httpx` tests |
| API8 Security misconfiguration | Secure defaults; strict configuration; unsafe settings warn; the file signer refuses production | `internal/config`, `internal/signing` tests |
| API9 Improper inventory management | One versioned contract (`proto/plux/v1`) with `buf breaking` in CI; OpenAPI generated from it | CI "API contract" job |
| API10 Unsafe consumption of APIs | Responses from OIDC providers, CI issuers and the scanner are size-limited and validated before use | `internal/auth`, `internal/httpx` tests |
