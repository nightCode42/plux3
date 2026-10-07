# 0012. DPoP with hardware-backed keys plus platform attestation

- **Status:** Accepted (maintainer, 2026-10-07, P6 S0; P6 plan §2.1 B2, B4, B6–B9, B17, B18; §2.2 Q1)
- **Date:** 2026-10-07
- **Requirements:** `SEC-001`–`SEC-009`, `SEC-020`–`SEC-029`, `SEC-056`, `NFR-012`, `NFR-025`

## Context and problem

Until P6 a device registers with `RegisterDevice` and receives a `device_secret`, which it
exchanges for a bearer access token (`IssueDeviceToken`). A bearer token copied from a log,
a proxy or a compromised process is usable anywhere, and nothing proves that the caller is
the genuine app on a sound device. Plux targets MASVS L2 + R and PSD2-grade transactions
(§15), so P6 must bind every request to a key that cannot leave the device, prove the app
and device to the server, and turn that proof into an assurance level that pages, routes,
data sources and functions can require.

Constraints: secure hardware differs per platform (StrongBox, TEE, Secure Enclave) and is
sometimes absent; Play Integrity has a daily quota; self-hosted and air-gapped installations
must not depend on a call to Google per request; the replay cache must not become a single
point of failure; and every claim must be provable in CI (device-trust.md §3).

## Decision drivers

- A stolen token is useless without the device key (`SEC-020`–`SEC-022`).
- The server, not the device, decides the assurance level (`SEC-007`).
- DPoP proof on the device ≤ 15 ms p95 (`NFR-012`); verification ≤ 1 ms p95 (`NFR-025`).
- No call to Google or Apple on the request path; Play Integrity within quota.
- Every value a setting (`SEC-182`), every rule tested by the negative suite (`SEC-029`).

## Considered options

1. **DPoP (RFC 9449) with hardware keys, platform attestation at registration and
   re-attestation, RFC 9068 access tokens.**
2. Mutual TLS with device client certificates (RFC 8705).
3. Keep the device secret and bearer tokens, add attestation on every request.

## Decision

Chosen option: **1**. mTLS client certificates cannot use Android Keystore or Secure
Enclave keys through Flutter's HTTP stacks without bespoke TLS plumbing, break behind TLS-
terminating proxies and CDNs, and give no per-request freshness; option 3 sends attestation
on every request, which exhausts Play Integrity quotas and still leaves bearer tokens.

### Keys (`SEC-001`, Q1, B4)

- The runtime generates a non-exportable P-256 key: Android Keystore (StrongBox, else TEE),
  iOS Secure Enclave. It signs DPoP proofs with ES256.
- StrongBox signing is measured in S1 against `NFR-012`. If it misses 15 ms p95, the DPoP
  key lives in the TEE and StrongBox holds the SCA key (`SEC-027`); both still give `AL2`.
- Without secure hardware the runtime creates a software key and flags it (`key_storage:
  SOFTWARE`). The server accepts it only where `allowSoftwareKeys` is true (`standard`
  default) and caps the device at `AL1`; otherwise registration fails with `PLX-6003`
  (device-trust.md §2).
- A second key-agreement key (ECDH P-256) is created when confidential bundles are on
  (ADR-0055), and an SCA key with user authentication required (ADR-0056).

### Registration (`SEC-005`, B17)

1. `CreateRegistrationChallenge` returns a single-use challenge (TTL
   `registrationChallengeTtl`, ≤ 5 min), stored hashed.
2. The device sends `RegisterAttestedDevice`: its public JWK, key storage, and evidence:
   - **Android:** the Key Attestation chain of the DPoP key, generated with the challenge;
     a Play Integrity standard-request token whose request hash is
     `SHA-256(challenge ‖ jkt)`.
   - **iOS:** an App Attest key ID and attestation object whose `clientDataHash` is
     `SHA-256(challenge ‖ jkt)`.
   - **Development:** the development provider (`SEC-008`), refused by production
     environments with `PLX-6004`.
3. The server verifies, computes the assurance level, stores the device record (ID, `jkt`,
   platform, build, key storage, attestation summary, level) and consumes the challenge.
4. Devices and registrations from before P6 are not migrated: no runtime is published.
   Once the new RPCs exist, `RegisterDevice` and the device-secret `IssueDeviceToken` are
   refused with `PLX-6008`.

### Verifiers (`SEC-002`–`SEC-004`, B7)

- **Key Attestation:** chain built with `crypto/x509` to Google's attestation roots
  (pinned, committed with their expiry); the attestation extension parsed with
  `encoding/asn1`: challenge, attestation and Keymaster security levels, verified boot
  state, bootloader lock, package name and signing-certificate digests; the revocation
  list fetched by the worker on a schedule and cached, never on the request path.
- **Play Integrity, verified locally:** the token is a JWE (A256KW, A256GCM) wrapping a
  JWS (ES256). The server decrypts and verifies it with the decryption and verification
  keys from the Play Console, held as secrets (`SEC-106`). Google's decode API is not used,
  so an air-gapped installation verifies with no call out. Checked: package name, request
  hash, timestamp within the window, `appRecognitionVerdict == PLAY_RECOGNIZED`, the
  device verdict the profile requires.
- **App Attest:** the CBOR attestation object decoded (ADR dependency below); the x5c chain
  to Apple's App Attest root; nonce; App ID (`teamID.bundleID`) hash; `aaguid` for the
  environment; counter 0; the credential public key's hash equals the key ID. Assertions:
  signature over `SHA-256(authenticatorData ‖ clientDataHash)`, App ID, and a counter that
  strictly increases (stored per device, updated with a compare-and-set).
- **Fraud-metric receipts** (iOS `AL3`) are refreshed by the worker, never on the request
  path; an unreachable receipt service follows the outage policy (`SEC-009`).

### Assurance levels (`SEC-007`, B8)

As in device-trust.md §1. The level is computed on registration, re-attestation and RASP
reports, written into the access token, and lowered at once by a RASP report or a failed
re-attestation. A requirement the device does not meet is refused with `PLX-6002`.

### Re-attestation and revocation (`SEC-006`)

- Re-attestation every `reattestationInterval` (24 h), on app update (host build change),
  on a risk signal, or when the server answers a refresh with `PLX-6007`.
- `RevokeDevice` marks the device revoked; every later request fails with `PLX-6006` and
  the device must register again. Revocation is audited.
- Attestation outage: `attestationOutageGrace` keeps the last known level for the grace
  period; `0` fails closed with `PLX-6009` (`SEC-009`).

### Tokens (`SEC-020`, `SEC-025`, B6)

- RFC 9068 JWT access tokens, ES256, signed through the signing abstraction (`signing/`,
  layering L-2), with `iss`, `aud`, `sub` (device ID), `client_id` (app), `env`, `al`,
  `cnf.jkt`, `iat`, `exp`, `jti`. Lifetime `accessTokenLifetime` (5 min, max 15).
- `RefreshDeviceToken` needs a fresh DPoP proof by the bound key. iOS adds an App Attest
  assertion on every refresh (local and cheap). Android adds a Play Integrity token only at
  re-attestation, or always when `androidRefreshRequiresIntegrity` is set; in between the
  hardware-bound DPoP key is the proof of possession, which keeps Play Integrity within its
  daily quota. There are no refresh tokens.

### DPoP (`SEC-021`, `SEC-022`, `SEC-024`)

- Every device request carries `Authorization: DPoP <token>` and a `DPoP` proof
  (`typ: dpop+jwt`, ES256, public `jwk`; `htm`, `htu`, `iat`, `jti`, `ath`, `nonce`).
- One HTTP middleware for every device route verifies, in order of cost: header shape;
  token signature and expiry; `cnf.jkt` equals the proof key's RFC 7638 thumbprint
  (`PLX-6013`); proof signature; `htm`, normalised `htu`; `iat` within `dpopIatWindow`;
  `ath`; nonce; then the replay cache. Failures map to `PLX-6010`–`PLX-6015`.
- Nonces are stateless: `HMAC(server nonce key, epoch)` with the epoch advancing every
  `dpopNonceRotation` (≤ 5 min); the current and previous epoch are accepted. A missing or
  stale nonce yields `use_dpop_nonce` with a fresh `DPoP-Nonce` header (`PLX-6012`).

### Replay cache (`SEC-023`, B9)

- `jti` (scoped by `jkt`) is stored in Valkey with `SET NX PX`, TTL the `iat` window plus
  skew, shared by every `api` replica. The in-house Valkey client gains Sentinel failover
  and replica support; each replica watches its health.
- If Valkey is unavailable: under `maximum` (`replayCacheFallback: failClosed`) requests
  are refused with `PLX-6015`; under `standard` and `strict` each replica falls back to a
  bounded in-memory cache (`dpop.replayCacheEntries`) with the narrower window
  `dpopIatWindowFallback` and raises a distinct alert. Requests are never accepted unchecked.

### Development and production keys (`SEC-056`)

Production environments refuse the development provider and development-signed tokens; the
token signing key is per environment type.

## Consequences

- **Positive:** stolen tokens and recorded requests are useless; assurance is computed on
  evidence; no per-request call to Google or Apple; quota-friendly on Android.
- **Negative:** every device request does two ECDSA verifications and one cache write;
  registration is a two-step flow; the old device-secret path is removed.
- **Follow-up:** S1 measures StrongBox; S2 builds the verifiers and the vectors
  (device-trust.md §3.1); S3 builds tokens, DPoP, nonces and the replay cache with the
  negative suite (`SEC-029`); the real-device check (Layer 3) before `SEC-002`–`SEC-004`
  are `DONE`.

## Dependencies (dependencies.md §1)

- **`github.com/go-jose/go-jose/v4`** (Apache-2.0, maintained, pure Go): JWS for tokens and
  DPoP proofs, JWE for Play Integrity. Writing JOSE by hand risks algorithm-confusion
  bugs; go-jose restricts accepted algorithms per call.
- **`github.com/fxamacker/cbor/v2`** (MIT, fuzzed, used by WebAuthn libraries), or an
  in-house decoder: App Attest's object is a small, fixed CBOR map (`fmt`, `attStmt`,
  `authData`). S2 decides by size and safety; the in-house option is preferred if it stays
  under ~300 lines with fuzz tests, since it parses only the fields needed.
- **`com.google.android.play:integrity`** (Play terms, not open source; accepted by the
  maintainer as for ML Kit, B18): the standard integrity request on Android.
- No dependency for Key Attestation (`crypto/x509`, `encoding/asn1`), the Valkey client,
  or iOS (DeviceCheck, CryptoKit, Security).

## Options in detail

### Option 2: mutual TLS with device certificates

Strong binding, but Flutter's Cronet and URLSession clients cannot present Keystore or
Secure Enclave keys without platform channels per request; CDNs and TLS-terminating load
balancers lose the binding; no nonce or per-request freshness.

### Option 3: bearer tokens plus attestation per request

Simple, but Play Integrity's quota and latency rule it out, and tokens stay replayable.

## Revision (2026-10-07, P6 S2: decisions taken while building the verifiers)

- **Token key (maintainer):** the token signing key is a dedicated ES256 key per
  environment class held by `signing/`, reached by the `api` role through a sign-only
  capability (ADR-0006, Revision). Tokens carry `typ: at+jwt` and a `kid`; a production
  environment refuses a development-class token and the reverse (`SEC-056`).
- **CBOR:** in-house. The decoder the WebAuthn code already had (ADR-0026) moved to
  `internal/cbor`, bounded and fuzzed, and App Attest uses it; `fxamacker/cbor` is not a
  dependency.
- **Binding:** Play Integrity's `requestHash` is base64url(SHA-256(challenge ‖ `jkt`)) and
  App Attest's `clientDataHash` is SHA-256(challenge ‖ `jkt`), so both bind the evidence to
  the registration challenge and to the DPoP key. Android Key Attestation attests the DPoP
  key itself: the chain's leaf key must equal the registered key.
- **Roots (maintainer):** Google's two Key Attestation roots and Apple's App Attestation
  root are committed unmodified under `backend/internal/attest/*/roots/` and embedded; a
  test pins each SHA-256 fingerprint. `.gitignore`, `AGENTS.md` and the security practices
  name this as the one exception to "no certificates in the repository".
- **Input bounds (maintainer):** the size and count bounds of the verifiers are registry
  limits (`LIM-001`): `attest.keyAttestationChainCerts`, `attest.playIntegrityTokenBytes`,
  `attest.appAttestObjectBytes`, `dpop.proofBytes`, `dpop.jtiBytes`.
- **Sentinel:** the Valkey client follows Sentinel failover to the promoted replica; it
  never reads from replicas, since rate-limit counters and the replay cache need the
  master's view.
