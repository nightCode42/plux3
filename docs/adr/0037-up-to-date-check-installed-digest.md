# 0037. Up-to-date check: a digest of the installed bundles, and a budget on bodies

- **Status:** Accepted
- **Date:** 2026-10-01
- **Requirements:** `NFR-006`, `REL-031`, `REL-032`, `SEC-020`

## Context and problem

`NFR-006` asked for one request of at most 1 KiB on the wire when nothing changed at app
start. The sync benchmark measured two requests and 6,639 bytes for the fifty-plugin app:

- **The device token.** Device tokens live at most 15 minutes and only in memory
  (`SEC-020`), so every app start asks for one first: 683 bytes.
- **The installed list.** `REL-032` has the manifest request carry every installed bundle's
  hash, so the server can plan deltas: 5,627 bytes for 51 bundles, against a 329-byte
  "not modified" answer.

From P6 every request also carries a DPoP proof header (`SEC-021`), and token issuance an
attestation, so a budget that counts headers would grow with security work that `NFR-006`
is not about.

## Decision drivers

- An unchanged app start costs as little as possible, whatever the number of plugins.
- Deltas stay exact: the plan for a changed manifest still knows what the device holds.
- The budget measures the sync protocol, not authentication, which later phases extend.
- Additive contract change only (`SRV-000`).

## Considered options

1. Keep the list; reword `NFR-006` to the measured size.
2. Send a digest of the installed bundles on the up-to-date check, the list only when asked;
   count the manifest request's bodies.
3. Have the server remember each device's installed bundles and send nothing.

## Decision

Chosen option: **2**, set by the maintainer.

- **The digest.** `GetManifestRequest.installed_digest` is the SHA-256 of one line
  `<key>:<sha256 hex>\n` per installed bundle, sorted by key, the app bundle's key empty. A
  device that holds a manifest's ETag sends the digest instead of `installed`.
- **The answer.** When the ETag matches and the digest is that of exactly the manifest's
  bundles, the server answers "not modified". Otherwise it answers
  `GetManifestResponse.installed_required` without a manifest, and the device asks again
  with its list, from which the server plans the deltas (`REL-032`). A device without an
  ETag sends its list at once.
- **The budget.** `NFR-006` counts the bodies of the one manifest request, both ways: at
  most 1 KiB. Headers, and the device-token request, are not counted.
- **Gates.** The sync benchmark fails when an up-to-date check makes other than one manifest
  request or its bodies exceed 1 KiB; Go and Dart share a test vector for the digest.

## Consequences

- **Positive:** the up-to-date check is one manifest request with 344 bytes of bodies, and
  1,563 bytes on the wire with the token request (was 6,639); its size no longer grows with
  the number of plugins.
- **Negative:** a changed manifest costs one more small round trip (an update of three
  plugins: 18,078 bytes instead of 17,159, within the 10% gate).
- **Follow-up:** none; P6's proof headers fall outside the budget by design.

## Options in detail

### Option 1

No work, but the check grows with every plugin (110 bytes each) and misses the intent of
`NFR-006`.

### Option 2

As decided.

### Option 3

Saves the list on changed manifests too, but makes the answer depend on server-side state
the device cannot see, which breaks when a download fails or a device restores a backup;
the digest keeps the device the source of truth.

## Revision (2026-10-07, P6 plan)

The up-to-date check also carries the security-configuration version the device holds (`config_version`); when it is current, nothing more is sent, so `NFR-006`'s budget holds (ADR-0053).
