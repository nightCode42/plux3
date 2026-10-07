# 0056. The secure gateway and strong customer authentication

- **Status:** Proposed (P6 plan §2.1 B13, B17; §2.2 Q2)
- **Date:** 2026-10-07
- **Requirements:** `SEC-026`–`SEC-028`, `SEC-030`–`SEC-032`, `SEC-105`, `SEC-106`, `NFR-025`

## Context and problem

DPoP and assurance levels protect requests to the Plux server, but data sources call the
customer's APIs, which cannot verify DPoP, attestation or SCA signatures without
reimplementing Plux. The maintainer decided (B13) that signatures are checked only on the
Plux server, that transaction calls travel only through it, and that every other call is
configurable per data source: through the Plux gateway (default) or direct.

## Decision drivers

- The customer's backend trusts one thing: the Plux server's assertion.
- No SSRF; no bodies in logs; per-device rate limits from the registry.
- ≤ 5 ms p95 added at 1,000 req/s per replica (`NFR-025`).

## Considered options

1. **A gateway in `api`, routing to registered upstreams, adding a signed Plux assertion;
   SCA verified only there.**
2. An SDK for customer backends to verify DPoP and SCA themselves.
3. A separate gateway service.

## Decision

Chosen option: **1**.

- **Routing.** A data source's `route` is `plux` (default) or `direct`. Through the
  gateway, the runtime calls `GatewayService.Call` (unary) or the HTTP route
  `/gateway/v1/{upstream}/…` (streaming bodies, WebSocket, SSE) with DPoP. The gateway
  resolves the upstream registered for the app and environment (`PLX-6060`), applies the
  data source's `requiresAssurance`, the request and response size limits, the timeout,
  and the per-device rate (`gateway.*` limits, `PLX-6064`).
- **Destinations.** Only registered upstreams; the request path is joined to the base URL
  and normalised; private, loopback, link-local and metadata addresses are refused unless
  the upstream allows them (`SEC-105`'s client, re-validated after DNS and redirects;
  `PLX-6061`).
- **Upstream authentication.** mutual TLS, OAuth 2.0 client credentials (tokens cached
  until expiry), or a key held encrypted (`SEC-106`). Device credentials and DPoP headers
  are stripped.
- **Plux assertion.** A short-lived JWT (ES256, per-environment key via `signing/`) in a
  `Plux-Assertion` header: device ID, app, environment, assurance level, verified user
  (if any), `jti`, `iat`, `exp` (60 s), audience = upstream. The JWKS is published.
- **No bodies** are logged, traced or stored; metrics carry upstream, status and duration.
- **User identity (`SEC-026`).** `ExchangeUserToken` (RFC 8693) verifies the host's user
  token against the customer's IdP JWKS and issues a Plux user token bound to the device's
  `jkt`; the gateway forwards verified user claims in the assertion (functions in P7).
- **SCA (`SEC-027`, `SEC-028`, `SEC-032`).** A data-source operation with a `transaction`
  mapping requires route `plux`. Flow: `CreateTransactionChallenge` with the JCS summary
  (amount, currency, payee as displayed) → the device shows it, the user authenticates,
  the SCA key (user-authentication-required: Android `setUserAuthenticationRequired` with
  biometric or device credential via `androidx.biometric`; iOS `.biometryCurrentSet`)
  signs `SHA-256(challenge ‖ summary)` → `Call` with the challenge ID and signature. The
  server verifies the signature with the device's registered SCA key, the single-use
  challenge, and that the request's mapped fields equal the signed summary, then forwards
  with an assertion that carries `sca: {challengeId, summaryHash}`. A signature sent any
  other way is refused (`PLX-6073`); failures `PLX-6070`–`PLX-6072`.
- **Direct calls (Q2)** are allowed per `allowDirectDataSources`; the compiler warns that
  `requiresAssurance` cannot be enforced on them.

## Consequences

- **Positive:** customers integrate by trusting one JWT; SCA is verified in one place.
- **Negative:** the Plux server is in the request path (one hop, availability coupling);
  the maintainer accepts this cost.
- **Follow-up:** S8 with the `NFR-025` benchmark; checkpoint 2.

## Dependencies

- **`androidx.biometric`** (Apache-2.0): the system biometric prompt with
  `CryptoObject`, required to unlock a user-authentication-bound key.

## Options in detail

### Option 2: a verification SDK

Every customer stack would need it in its language; security fixes would depend on
customers updating it.

### Option 3: a separate service

Another deployable and another network hop; the `api` role already terminates DPoP.
