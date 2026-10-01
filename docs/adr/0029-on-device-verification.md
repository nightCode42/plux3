# 0029. On-device verification of manifests and bundles

- **Status:** Accepted
- **Date:** 2026-09-28
- **Requirements:** `SEC-051`, `SEC-052`, `SEC-054`, `SEC-055`, `BND-005`, `BND-006`, `BND-008`, `BND-018`, `SCH-012`, `SYN-011`, `QA-004`

## Context and problem

ADR-0004 decided how releases are signed; the device side of it lands in P3. Everything the
runtime renders arrives over the network or from a file that anything with access to the
device's storage could have changed. The runtime must decide, before it reads a byte of a
section, whether that section is exactly what the release's signer published — and it must
do so on a phone, without blocking the UI, with code that is itself small and auditable.
In what order is what checked, against which keys, with which libraries, and where are the
secrets and counters that verification depends on kept?

## Decision drivers

- Verify before load: nothing unverified is parsed beyond a container header (`SEC-052`).
- Defence in depth: the FlatBuffers verifier runs even on signed sections (`BND-006`).
- Anti-rollback across restarts (`SEC-055`).
- The same algorithms and encodings as the server (Ed25519, SHA-256, RFC 8785), checked by
  shared vectors.
- Hashing and parsing of large inputs off the UI isolate (L-6).
- Small, audited, pure-Dart dependencies; no secret persisted in the clear (AGENTS.md §5).

## Considered options

1. **Verify in Dart: `cryptography` for Ed25519, `crypto` for SHA-256, an in-house RFC 8785 check and a Dart FlatBuffers verifier generated from the same tables as the Go one.**
2. Verify through the platforms' crypto APIs over FFI and platform channels.
3. Verify only at download; trust the store afterwards.

## Decision

Chosen option: **1**.

### Keys (`SEC-051`)

Host apps embed the environment's public keys — `keys.json`, written by `plux pull` (and
`plux init` from P4) — and pass them as `PluxConfig.rootKeys`. In P3 each environment has
one Ed25519 `targets` key (ADR-0004); a manifest is accepted when at least one of its
signatures verifies under an embedded key whose role is `targets` and whose key ID
matches. Root keys, thresholds and rotation signed by the previous root arrive in P6, so
`SEC-051` stays `WIP` until then. Only `ed25519` is accepted; any other algorithm name is
refused, not guessed (`SEC-122`).

### Verification order (`SEC-052`)

**Manifest** (sync isolate), refused as a whole at the first failure:

| # | Check | Failure |
|---|---|---|
| 1 | the response is within `sync.manifestSize`; `signed` is present | `PLX-3001` |
| 2 | `signed` is the RFC 8785 canonical form of itself (parse, re-canonicalise, compare bytes) | `PLX-3001` |
| 3 | one signature verifies under an embedded `targets` key | `PLX-3001` |
| 4 | `type` = `manifest`, `specVersion` = 1, `role` = `targets`; app, environment and channel are the configured ones | `PLX-3001` |
| 5 | `expires` is in the future by the device clock | `PLX-3002` |
| 6 | `releaseSequence` ≥ the highest sequence accepted for this channel | `PLX-3003` |
| 7 | every bundle's required features are supported and `minRuntime` ≤ this runtime | `PLX-3010` |

Only fields of the verified `signed` document are used from then on; the proto fields that
repeat them are ignored. Accepting the manifest raises the stored highest sequence
(`SEC-055`); a rollback arrives as a new, higher sequence (`REL-006`). A failed check keeps
the current release. A device whose clock is ahead of `expires` refuses the manifest and
keeps its release: failing closed is the safe direction.

**Bundle** (sync isolate, before it enters the store):

| # | Check | Failure |
|---|---|---|
| 1 | the container header and directory are well formed (`BundleContainer.parse`) | `PLX-3040` |
| 2 | SHA-256 of bytes 0–15 and the directory equals the signed bundle hash (`BND-005`) | `PLX-3011` after a delta, else `PLX-3040` |
| 3 | every section's SHA-256 equals its directory entry | `PLX-3041` |
| 4 | every section passes the FlatBuffers verifier under the registry's limits (`BND-006`) | `PLX-3042` |
| 5 | the `meta` section's required features are supported; unknown section kinds are skipped unless required (`BND-008`, `BND-018`) | `PLX-3010` |
| 6 | the encrypted flag is clear (confidential bundles arrive in P6) | `PLX-3043` |

A baseline bundle is checked the same way against the hash in `baseline.json`, and its
Ed25519 signature over that 32-byte hash — recorded by `plux pull` next to it — must verify
under an embedded key.

**On load** (the files in the store could have been changed after they were verified):
mapping a bundle re-checks its header hash against the release record (a hash of the
header and directory only, bounded by the section count); the **first use** of each
section hashes it and runs the verifier again, and remembers the result by section hash
for the life of the mapping. Sections up to 64 KiB are checked on the UI isolate, larger
ones on a background isolate before the page builds (L-6). This first-use check is the one
bounded piece of hashing on the UI isolate, and its cost is measured in the P3 benchmarks.
A failure marks the plugin as failing verification (`RT-022`) and is reported.

### The Dart FlatBuffers verifier (`BND-006`, `QA-004`)

`make gen` already turns `flatc`'s binary schemas into layout tables for the Go verifier.
`schemagen` writes the same tables as Dart (`lib/src/verify/layout.g.dart`), and a Dart
interpreter mirrors the Go one line for line: offsets, alignment, vtables, strings (in
bounds, NUL-terminated, UTF-8), vectors, nesting depth and a visit count from the limits
registry (`bundle.verifierDepth`, `bundle.verifierTables`). It is tested against every
golden bundle, a mutation corpus shared with Go, and a fuzz test.

### Libraries

- **Ed25519:** `cryptography` 2.9.0 (Apache-2.0), its pure-Dart `DartEd25519`
  implementation selected explicitly so that no platform plugin is involved and results are
  identical everywhere; tested with the RFC 8032 vectors and with signatures produced by
  the P2 signer.
- **SHA-256:** `crypto` 3.0.7 (BSD-3-Clause, the Dart team).
- **RFC 8785:** in house, a canonical serialiser for the JSON subset manifests use, checked
  against the vectors in `schema/testdata/jcs` that the Go implementation passes.

### Secrets and counters on the device

- The **device secret** (P2 `DeviceService`) is never written in the clear (AGENTS.md §5):
  it is encrypted with AES-256-GCM under a key held by the platform — an Android Keystore
  key, and on iOS a Keychain item with `kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly`
  — through a small platform channel in `plux_flutter` with no third-party dependency.
  Access tokens live only in memory. P6 replaces the secret with a hardware-bound key and
  DPoP (`SEC-001`, `SEC-020`).
- The **highest accepted sequence** lives in the release store's pointer file. On a rooted
  device it can be reset along with the store; hardware-backed monotonic counters are P6
  hardening (`SEC-072`).

### Data only, and redaction (`SEC-054`, `SCH-012`)

The runtime has no code path that loads native code, Dart code or scripts from a bundle:
PXL bytecode runs in the VM, action graphs in the P5 executor, and the `wasm` section is
refused until the P7 interpreter exists. Values of fields declared `sensitive` are
replaced by `[redacted]` in every log line, error report and telemetry event the runtime
produces.

## Consequences

- **Positive:** one verification path for downloads, deltas, baselines and loads; the same
  tables drive the Go and Dart verifiers; no native crypto code to maintain.
- **Negative:** pure-Dart Ed25519 is slower than native (a few milliseconds per manifest on
  a phone, off the UI isolate); two dependencies enter every host app; the first-use
  section check costs UI-isolate time, bounded and measured.
- **Follow-up:** P6 adds the root/snapshot/timestamp roles, thresholds, root rotation,
  development-key rejection (`SEC-056`), confidential bundles and hardware-backed storage.

## Options in detail

### Option 2 — platform crypto

Faster primitives and hardware-backed storage, but Ed25519 is not available on every
supported OS version (the Android platform APIs gain it only with Android 13, API 33, and
`RT-002` supports API 24), and two native verification paths would each need their own tests and could disagree. The
platforms are used only where a platform capability is the point: key storage.

### Option 3 — verify only at download

Cheaper at load, but the store is ordinary files; a tampered file would be rendered.
Rejected by `SEC-052` and `BND-006`.
