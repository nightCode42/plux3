# 0026. Built-in accounts with TOTP, org-bound credentials and row-level scopes

- **Status:** Accepted
- **Date:** 2026-09-27; revised 2026-09-28 (see [Revision](#revision-2026-09-28))
- **Requirements:** `SEC-100`, `SEC-101`, `SEC-102`, `SEC-106`, `SEC-120`, `SEC-140`, `SRV-004`, `SRV-005`, `SRV-022`, `SRV-064`, `SRV-065`, `GOV-001`, `GOV-010`, `GOV-031`, `CLI-002`

## Context and problem

P2 is the first phase with people and machines calling the server. `SEC-100` requires built-in
accounts with Argon2id **or** OIDC, and TOTP **or** WebAuthn, mandatory for the roles that can
publish, approve, manage keys or manage members. `SEC-101` requires a backend-for-frontend with a
`__Host-` session cookie and CSRF tokens; `SEC-102` deny-by-default authorisation with row-level
security beneath it; `SRV-064` personal access tokens and CI workload-identity federation;
`CLI-002` the OAuth 2.0 device authorization grant; `SEC-140` an append-only audit log. Single
sign-on (`GOV-004`) and WebAuthn step-up (`SEC-103`) are P9 requirements. What does P2 build, and
how do credentials, organisations and row-level security fit together?

## Decision drivers

- Every `MUST` of P2 met without half-building a P9 feature (AGENTS.md §5, Scope).
- One place decides every call (`SEC-102`); the database refuses what the service layer misses.
- No credential usable from a copy of the database (`SEC-101`, `SEC-106`).
- Nothing an attacker can replay: a TOTP code, an idempotent result, a page token.
- A request handler never widens what a transaction may see on a caller's behalf.

## Considered options

1. **Built-in accounts and TOTP now; OIDC and WebAuthn with their P9 requirements.**
2. Built-in accounts, OIDC, TOTP and WebAuthn all in P2.
3. OIDC only, delegating passwords and second factors to an identity provider.

## Decision

Chosen option: **2**, by the maintainer's decision of 2026-09-28, which restores the P2 plan that
named OIDC and WebAuthn (see [Revision](#revision-2026-09-28)). The reasoning first recorded for
option 1 follows, because it still bounds what option 2 builds: option 1 satisfies `SEC-100` as written — its alternatives are joined by "or" —
and leaves `GOV-004` (OIDC and SAML with group mapping and SCIM) and `SEC-103` (WebAuthn step-up)
to P9, where they are specified as a whole. Option 2 would build half of `GOV-004` and a WebAuthn
enrolment nothing in P2 requires; option 3 would make every installation depend on an external
identity provider before P9 specifies how one is integrated.

### Accounts and second factors

- Passwords are Argon2id PHC strings (19 MiB, two passes, one lane — OWASP's server profile),
  12 to 1,024 characters. An unknown address is refused exactly like a wrong password and costs a
  decoy Argon2id computation, so neither the message nor the timing tells them apart.
- `auth.failedSignIns` (limits registry) bounds wrong passwords and codes per account in fifteen
  minutes; a signed-in account is throttled by its ID, a sign-in by its hashed address.
- The first installation administrator is created by `plux-server bootstrap`, which prints a
  one-time invitation and refuses once an administrator exists. Every other account is created
  by `AddMember` with an invitation; nobody chooses another person's password.
- TOTP (RFC 6238, SHA-1, six digits, one step of skew) is one P2 second factor; WebAuthn security
  keys and passkeys are the other (see the revision). The secret is
  sealed with envelope encryption bound to its factor's ID; the last accepted time step is stored,
  so a code works once. A sign-in with a confirmed factor gets a challenge (hashed, five wrong
  codes close it) instead of a session.
- The second factor is enforced **per permission**, not per role: `release.publish`,
  `release.promote` (approval until P9), `keys.manage`, `members.manage`, `secrets.manage` and
  `tokens.manage` need a session that presented one. A person holding such a role therefore cannot
  use it without one, which is what `SEC-100` requires; `auth.studio.mfaRequiredFor` must name at
  least the four capabilities of `SEC-100`, so configuration cannot weaken it.

### Credentials

| Credential | Held by | Stored | Bound to |
|---|---|---|---|
| Session (`plux_ses_…`) | a browser, as `__Host-plux_session` (`Secure`, `HttpOnly`, `SameSite=Strict`, `Path=/`) | SHA-256 | a person; any organisation they belong to, named per call by `X-Plux-Organization` |
| CSRF token (`plux_csrf_…`) | the page, echoed in `X-CSRF-Token` on every cookie call | per session | its session |
| Personal access token (`plux_pat_…`) | the CLI or a script | SHA-256 | one organisation and scopes fixed at creation |
| `plux login` token | the CLI, from the device grant | SHA-256 | one organisation, chosen by the approver |
| CI token | a pipeline, from `ExchangeWorkloadIdentity` | SHA-256 | one organisation and the workload identity's scopes |

A token's scopes are fixed when it is minted and are always a subset of what its creator held at
that moment; at use they are intersected with what the person holds now, so removing a role
removes it from their tokens. Creating a token, approving `plux login` and removing a second factor
need a session that presented a second factor, because each outlives or weakens the session. A
call carrying both a cookie and a bearer token is refused rather than guessed at.

CI federation verifies the provider's RS256/384/512 tokens against keys from its discovery
document, fetched through the SSRF-safe client (`SEC-105`); the jwks URI must lie under the issuer.
The installation trusts issuers, each with an audience and a bounding subject pattern
(`auth.ci.issuers`); an organisation's workload identity must use that audience and a subject
pattern within the bound, and its scopes must be within its creator's.

### Organisations, roles and row-level security

- A call acts in one organisation: the one its token is bound to, or the one a session names in
  `X-Plux-Organization` or the request, which must agree. An organisation the caller has no part in
  is reported as not found, not forbidden.
- P2's roles are owner, admin, developer and viewer, each an explicit list of permissions — no
  inheritance. Organisation-wide roles come from direct memberships; a team's members hold the roles
  the team is granted on single apps (`app_access`), and a user may be granted a role on one app. An
  app-level grant carries only app-scoped permissions.
- Every transaction sets, with `set_config`'s local flag, the organisation, the signed-in user and a
  *scope*. The scope widens exactly two policies, each for one caller: `authentication` lets a token
  be found by its hash before its organisation is known; `installation` reads and writes the audit
  entries that belong to no organisation. The user setting lets a person read their own memberships
  in every organisation, to list them, but change none. Tables that hold no tenant data are named,
  with the reason, in the catalogue test that proves every other table is protected.

### Audit, idempotency and paging

- The audit log is one hash chain per organisation plus one for the installation. An append takes
  a transaction-scoped advisory lock on its chain before reading the last entry, so concurrent
  writers never fork it; a trigger refuses `UPDATE`, `DELETE` and `TRUNCATE`, so the database
  enforces append-only as well as the chain detecting tampering. The client address, user agent and
  request ID come from the request, set once by the API edge.
- Idempotency keys belong to the credential's subject, not to an organisation, and their results
  are sealed with envelope encryption bound to subject and key: some results are secrets returned
  once. A failed call releases its key; a repeat while the first call runs is refused. Calls that
  take no credential are not replayed, because their results are credentials.
- Page tokens carry the position of the last item, authenticated with HMAC-SHA-256 under an
  installation key (sealed in the database, shared by replicas) and bound to the procedure, scope,
  filter and ordering; they expire after a day. Page sizes come from `api.pageSize`.

### Keys

The signing abstraction gains the HashiCorp Vault Transit backend in P2, as ADR-0004 decided:
Ed25519 keys created non-exportable per environment on first use, data keys wrapped by one
AES-256-GCM key, and every signature verified against the published key before it is returned.
Sealed values authenticate a binding — where they are stored — so a ciphertext copied to another
row does not open there.

## Consequences

- **Positive:** every `MUST` of `SEC-100`, `SEC-102`, `SRV-064` and `CLI-002` is met with nothing
  built for P9; no stored value is a usable credential; the database refuses cross-tenant access,
  audit tampering and credential lookups outside their purpose on its own.
- **Negative:** TOTP is phishable; a person who registers a security key is not, and WebAuthn
  step-up (`SEC-103`) makes it mandatory for the most sensitive actions in P9. Resolving a principal
  costs two queries per call. Two sign-in paths and two factor kinds are more to secure than one.
- **Follow-up:** P9 adds SAML, provisioning and group mapping (`GOV-004`), WebAuthn step-up within
  five minutes (`SEC-103`) and
  custom roles (`GOV-002`) behind the same permission catalogue; P11's Studio BFF serves the device
  approval page at `<publicBaseURL>/device` and the browser half of `SEC-101`.

## Options in detail

### Option 1 — built-in accounts and TOTP now

Meets the P2 requirements, keeps P9's identity work in one piece, and has no external dependency
beyond `golang.org/x/crypto` for Argon2id. TOTP is weaker against phishing than WebAuthn, which is
why `SEC-103` makes WebAuthn step-up mandatory for the most sensitive actions in P9.

### Option 2 — everything in P2 (chosen)

Implements OIDC sign-in without the group mapping, SAML and SCIM that `GOV-004` requires alongside
it in P9, and WebAuthn registration without the step-up rules of `SEC-103`. Both are complete for
what `SEC-100` asks of them; P9 extends rather than replaces them. Built in-house on the standard
library (below), so no dependency is added.

### Option 3 — OIDC only

Would satisfy `SEC-100` with no password handling, but every installation, including a developer's
laptop, would need an identity provider before P2's own tests could sign in.

## Revision (2026-09-28)

The maintainer chose option 2: P2 keeps its original plan and ships OpenID Connect sign-in and
WebAuthn second factors beside built-in accounts and TOTP. What is built, and where the line with
P9 runs:

- **OIDC sign-in.** The authorization code flow with PKCE (S256), a nonce and a state, as a
  confidential client (`client_secret_basic`, secret from `PLUX_AUTH_STUDIO_OIDC_CLIENT_SECRET`).
  The provider's endpoints come from its discovery document and must be on the issuer's host; the
  ID token is verified with the same RS256 key-set verifier CI federation uses, for the client ID
  as audience, and its nonce must be the sign-in's. The state is stored hashed and the PKCE
  verifier sealed, for ten minutes, and are used once. A provider identity is linked to an account
  that already exists — every account is created by invitation — on its first sign-in, by the
  address the provider says it verified; the link is by issuer and subject from then on. Signing in
  through the provider accepts a pending invitation. An account with a confirmed second factor is
  still challenged for it. Just-in-time provisioning, group-to-role mapping, SAML and SCIM remain
  `GOV-004`'s, in P9. `auth.studio.allowPasswordLogin` may be false once a provider is configured.
- **WebAuthn.** Security keys and passkeys register as a second factor kind, `webauthn`, and
  answer a sign-in challenge. Plux asks for no attestation ("none") and trusts no authenticator
  make; it verifies the browser's origin, type and challenge, the relying-party ID hash, user
  presence, the signature (ES256, EdDSA or RS256 of at least 2048 bits) and a signature counter
  that must advance where the authenticator keeps one. The relying party is the host of
  `server.publicBaseURL`, which serves Studio's backend-for-frontend; without an https public URL
  security keys are off. The minimal CBOR and COSE decoding this needs is in `internal/auth`,
  bounded in depth and size and fuzzed, rather than a new dependency (`dependencies.md` unchanged).
  Step-up within five minutes for the most sensitive actions remains `SEC-103`'s, in P9.
- **New error code** `PLX-8091` (upstream unavailable): the sign-in provider cannot be reached.

The same decision settled `SEC-120`'s remaining backends: PKCS#11 (as a separate helper process, so
the server binary stays free of cgo for `CI-006`), AWS KMS, Google Cloud KMS and Azure Key Vault
are all supported, arriving in P6 with the key ceremonies of `SEC-121`. P2 ships Vault Transit and
the development file backend.
