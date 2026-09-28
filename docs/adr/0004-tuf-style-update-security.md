# 0004. TUF-style update security with offline root keys

- **Status:** Accepted
- **Date:** 2026-09-27
- **Requirements:** `SEC-050`, `SEC-051`, `SEC-052`, `SEC-055`, `SEC-056`, `SEC-120`, `SEC-121`, `SEC-122`, `REL-031`, `SRV-052`, `BND-005`

## Context and problem

Everything a device renders arrives over the air. An attacker who can sign a release owns
every phone that trusts the signer; an attacker who can only interfere with delivery can
still try to pin devices to an old release with a known flaw, to freeze them on a stale
one, or to combine plugin versions that were never released together. Signing the manifest
with one key answers the first threat and none of the others. What is the update security
model, and how much of it lands in P2?

## Decision drivers

- Compromise of an online key must not be enough to ship a release (`SEC-121`).
- Rollback, freeze and mix-and-match attacks must be detectable by the device (`SEC-055`).
- Keys must rotate without an app-store release (`SEC-121`).
- Every key operation must go through one abstraction with HSM and cloud-KMS backends, and
  never through a file in production (`SEC-120`).
- Algorithms must be named in the metadata so they can be migrated later (`SEC-122`).
- P2 must ship something complete and testable, without pretending the P6 work is done.

## Considered options

1. **TUF roles (root, targets, snapshot, timestamp) over the manifest and bundle hashes, delivered in stages: the `targets` role in P2, the remaining roles, thresholds and rotation in P6.**
2. A single signing key per environment, signing the manifest.
3. Sigstore/cosign-style keyless signing with a transparency log.
4. Notary v2 / ORAS artifact signing.

## Decision

Chosen option: **1**, as the specification requires (`SEC-050`).

### Roles

| Role | Signs | Keys | Phase |
|---|---|---|---|
| `root` | the public keys and thresholds of the other roles | offline, *m* of *n* | P6 |
| `targets` | bundle hashes and the release's contents | online, in KMS or an HSM | **P2** |
| `snapshot` | the version numbers of the current metadata | online | P6 |
| `timestamp` | the current snapshot, with a short expiry | online, frequently re-signed | P6 |

`root` delegates; each role's key signs only its own metadata. Compromising the online
`targets` key does not let an attacker replace `root`'s delegation, and the short-lived
`timestamp` role is what makes a freeze attack visible: a device that sees expired
timestamp metadata knows it is being held back rather than simply up to date.

### What P2 implements

- One Ed25519 `targets` key **per environment** (`GOV-010`), reached only through the
  signing abstraction (`SEC-120`) from the `worker` role (`SRV-052`, L-3). P2 ships two
  backends behind it: HashiCorp Vault Transit, reached over its HTTP API with the standard
  library, and a file backend refused for production environments (`SEC-056`). The
  PKCS#11, AWS KMS, Google Cloud KMS and Azure Key Vault backends that `SEC-120` also names
  are an open decision recorded in the work log: PKCS#11 needs cgo, which the static,
  reproducible build (`CI-006`) forbids in the server binary, and the cloud services cannot
  be exercised in CI without accounts. The recommendation is to add them in P6 with the key
  ceremonies of `SEC-121`, the PKCS#11 one as a separate helper process.
- The signature covers the **bundle hash** (`BND-005`), which already commits to the
  section directory and therefore to every section, so signing one 32-byte value signs the
  whole bundle.
- The manifest (`REL-030`) is signed by the same role and carries the signature, the key
  ID, the algorithm identifier (`SEC-122`), the issue time and the expiry (`REL-031`).
- Public keys are published at a stable endpoint and written into host projects by
  `plux init` and `plux pull` (`CLI-004`), which is what `SEC-051` will verify against.
- Every signature and key ID is stored with the plugin version and the release, so a
  release can be re-verified later without re-signing.

### What P6 adds

Root key ceremonies and *m*-of-*n* thresholds (`SEC-121`), the `snapshot` and `timestamp`
roles and their expiries (`SEC-050`), root rotation accepted only under the previous root
threshold (`SEC-051`), confidential bundles (`SEC-053`), and the device-side verification
order (`SEC-052`) with anti-rollback on the release sequence (`SEC-055`). The metadata
format is defined now so that P2 signatures remain valid when the roles above them arrive:
every signed object carries `spec_version`, `role`, `expires`, `algorithm` and `key_id`,
and unknown fields are preserved rather than dropped.

Until then, `SEC-050`, `SEC-051`, `SEC-053` and `SEC-055` stay `SPEC`, and the work log
records that the P2 signature is a `targets`-only subset — a device in P3 verifies the
manifest signature and every bundle hash, but not yet a role hierarchy.

### Algorithm

Ed25519 (RFC 8032) for signatures and SHA-256 for hashes (`BND-005`), both named
explicitly in the metadata. Not every key service can hold an Ed25519 key — Azure Key
Vault cannot — so ECDSA over P-256 with SHA-256 (`ecdsa-p256-sha256`) is the designated
second algorithm, added together with the first backend that needs it. A verifier accepts
exactly the algorithms named here and rejects any other rather than guessing; crypto
agility is a field, not a rewrite (`SEC-122`). The abstraction exposes
`Sign(ctx, keyRef, message)` and `PublicKey(ctx, keyRef)` and nothing that can export a
private key (L-3).

## Consequences

- **Positive:** the end state is a standard, analysed design rather than a bespoke one; P2 ships a complete, testable signing path; the metadata shape does not change when P6 adds roles.
- **Negative:** until P6 the model protects against forgery and tampering but not against freeze or mix-and-match attacks; two phases touch the same metadata, so P2 must get the field layout right the first time.
- **Follow-up:** the key-ceremony runbook and the rotation procedure are written in P6 with `SEC-121`; `docs/security/` gains the update-security section in P2 and the ceremony in P6.

## Options in detail

### Option 2 — one key per environment

What P2 implements as a first step, and what most over-the-air update systems stop at. It
answers forgery and nothing else: an attacker who controls delivery can still pin a device
to an old release indefinitely, and a compromised online key is a complete compromise with
no offline root to recover under. Insufficient for the regulated deployments Plux targets.

### Option 3 — keyless signing with a transparency log

Excellent for public artifacts built in public CI, which is why `CI-004` uses cosign for
Plux's own release binaries. Wrong for customer releases: it requires every device to reach
a public log and ties customers' private plugin releases to a public identity. Rejected for
the update channel, kept for the supply chain.

### Option 4 — Notary v2 / ORAS

Designed for OCI registries. Plux artifacts are content-addressed objects in object storage
behind a CDN, not registry manifests; adopting it would mean shipping a registry.

## Implementation notes (P2)

- **Where signing happens.** Manifests are signed by the worker, like bundle hashes: a job is
  enqueued in the transaction that promotes a release or changes a channel's switches, and
  the maintenance sweep re-signs any manifest within two days of its seven-day expiry. The
  api role serves the newest stored manifest and never signs (`SRV-052`, L-3).
- **What is signed.** The signed part is the RFC 8785 canonical JSON of the manifest's
  content: `type`, `specVersion`, `role` (`targets`), app, environment, channel, release
  sequence, `issuedAt`, `expires`, the app bundle and each plugin's hash, size, required
  features and minimum runtime, the switches and the experiment assignments. The per-device
  sync plan and the download URLs are served next to it, **not** inside it — unlike the
  abridged example of Appendix B.3, which shows `sync` inside `signed`. Signing a plan per
  device would put a signer in the api role, and a URL may be a short-lived signed URL; the
  plan needs no signature because the device verifies what it rebuilds or downloads against
  the signed bundle hashes (`SYN-011`). This reading is recorded for the maintainer's
  confirmation in the work log.
- **Keys.** The first time the worker signs with an environment's key it records the public
  half, which `GetRootKeys` returns to devices and to `plux pull` (`SEC-051`).
- **Production.** The file backend refuses to sign a bundle or a manifest for an environment
  marked production (`SEC-056`); the development keys therefore never sign a production
  release. The runtime-side rejection lands in P6, so `SEC-056` stays `SPEC`.
