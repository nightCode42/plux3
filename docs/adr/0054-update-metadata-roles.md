# 0054. Update metadata completed: root, targets, snapshot and timestamp

- **Status:** Accepted (maintainer, 2026-10-07, P6 S0; P6 plan §2.1 B11, B17)
- **Date:** 2026-10-07
- **Requirements:** `SEC-041`, `SEC-050`, `SEC-051`, `SEC-055`, `SEC-056`, `SEC-121`, `SEC-122`, `BND-000`

## Context and problem

ADR-0004 adopted TUF's design; P3 delivered its device half (root keys embedded at build
time, a signed manifest, anti-rollback). P6 completes it: separate roles and keys,
thresholds, expiries, rotation without an app-store release, algorithm agility, and pin
updates through signed metadata.

## Decision drivers

- Compromise of one online key must not allow arbitrary updates; root stays offline.
- Freeze and mix-and-match attacks are detected.
- A device that is offline keeps working; expiry never stops a running app.
- Older runtimes keep working (`BND-000`).

## Considered options

1. **TUF's four roles in Plux's JSON format** (`root.json`, `targets` = the signed
   manifest, `snapshot.json`, `timestamp.json`), Plux-signed, verified by the runtime.
2. Adopt a full TUF client library on the device and server.

## Decision

Chosen option: **1**. No maintained Dart TUF client exists, and the formats need Plux's
JCS canonicalisation and bundle model; the roles and rules are TUF's.

- **Formats.** Each file is `{signed, signatures[{keyid, alg, sig}]}`; `signed` has
  `_type`, `version`, `expires`, `specVersion`. `root` lists keys (`alg`: `ES256`,
  `Ed25519`; open for a post-quantum identifier, `SEC-122`) and each role's key IDs and
  threshold. `snapshot` lists the targets version; `timestamp` lists the snapshot version
  and hash. Schemas in `schema/json/`, vectors in `schema/testdata/manifest/`.
- **Thresholds and expiries** (server settings): `root` 2 of 3 offline keys, 1 year;
  `targets` 30 days; `snapshot` 7 days; `timestamp` 24 hours.
- **Verification on the device** (at sync, before anything else, `SEC-052`): root chain
  (each new root signed by the previous threshold and its own), timestamp, snapshot,
  targets, each signature threshold met, versions never decreasing, expiry checked at
  sync (`PLX-6022`). A failed check keeps the last good release running.
- **Rotation.** Online role keys rotate by a new `root` version; a root key rotates by a
  ceremony (`docs/runbooks/key-ceremony.md`, `SEC-121`).
- **Development keys** are separate; a production runtime refuses metadata or bundles
  signed by them (`PLX-6021`, `SEC-056`).
- **Certificate pins** (`SEC-041`) ship in `PluxConfig.pins` and are replaced only by a
  `pins` entry in signed `root` metadata.
- **Compatibility.** The manifest carries the roles' versions; runtimes before 0.4.0
  ignore them and keep verifying the manifest as before.

## Consequences

- **Positive:** a compromised CDN or online key cannot push or freeze updates unnoticed.
- **Negative:** a timestamp must be re-signed at least daily (a worker job).
- **Follow-up:** S5 builds the signer jobs, the runtime verifier, the ceremony runbook.

## Options in detail

### Option 2: a TUF library

go-tuf exists for the server; there is none for Dart, and its formats would replace the
manifest that P2–P5 built.
