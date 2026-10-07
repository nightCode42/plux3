<!--
SPDX-FileCopyrightText: 2026 Plux contributors
SPDX-License-Identifier: Apache-2.0
-->

# Device Trust: Assurance Levels, Software Keys and How Attestation Is Tested

- **Status:** Decided by the maintainer on 2026-10-07 ([plan p6](../plans/p6.md) §2.1, B2,
  B3, B4 and B8). ADR-0012 records the design in milestone S0; the specification changes
  land with it (spec 1.4.0).
- **Requirements:** `SEC-001`, `SEC-002`, `SEC-003`, `SEC-004`, `SEC-007`, `SEC-008`,
  `SEC-029`, `SEC-190`, `QA-008`.

This page records three decisions about how Plux decides how far it trusts a device, and
how that is proved. It is the reference for the code, the tests and the release checklist.
Where the specification is silent or ambiguous, this page says what Plux does and why.

---

## 1. Assurance levels on each platform

`SEC-007` defines four assurance levels. The specification gives Android's evidence for
each level but leaves two gaps on iOS: App Attest has no "basic device integrity" verdict,
and "a clean risk profile" for `AL3` is not defined. Plux maps the evidence as follows.

| Level | Android | iOS |
|---|---|---|
| `AL0` | Nothing verified: the development provider (§3) in a non-production environment, or attestation failed under a policy that still serves the device | The same |
| `AL1` | Play Integrity `PLAY_RECOGNIZED` app integrity verified; the DPoP key may be a software key (§2, `standard` only) | App Attest attestation verified, but the device holds a software DPoP key (§2, `standard` only) |
| `AL2` | `AL1`, plus a Key Attestation chain to Google's roots proving the DPoP key is in TEE or StrongBox, plus the device verdict the profile requires: `MEETS_BASIC_INTEGRITY` under `standard` (the specification's "basic device integrity"), `MEETS_DEVICE_INTEGRITY` under `strict` and `maximum` (setting `androidDeviceVerdictAL2`) | App Attest attestation verified (App ID, environment, nonce, counter) and the DPoP key in the Secure Enclave. A valid App Attest key on genuine Apple hardware is iOS's equivalent of basic device integrity. |
| `AL3` | `AL2`, plus `MEETS_STRONG_INTEGRITY`, plus no RASP finding (`SEC-070`) | `AL2`, plus Apple's fraud-metric receipt for the attested key below the configured threshold, plus no RASP finding |

Rules that hold on both platforms:

- The server computes the level; the device never claims one. The level is written into the
  access token (`SEC-020`) and lowered at once by a RASP report (`SEC-071`) or a failed
  re-attestation (`SEC-006`).
- The `AL3` fraud-metric threshold is a security setting (`riskMetricMaxAL3`), distributed
  like every other setting (plan p6 §5.4). When Apple's receipt service cannot be reached,
  the attestation-outage policy applies (`SEC-009`).
- A page, route, data source or function that requires a level the device lacks is refused
  with `PLX-6002`, never silently degraded.

## 2. Devices without secure hardware

`SEC-001` requires the DPoP key to be generated in secure hardware. `SEC-029` speaks of
"proofs signed by software keys when hardware keys are required", which implies that a
software key can exist. Plux settles this as follows.

1. **The runtime always tries secure hardware first:** StrongBox, then TEE, on Android; the
   Secure Enclave on iOS. A software key is used only when the platform reports that no
   secure hardware can hold a P-256 key (an emulator, a simulator, or a device too old or
   too damaged to have one).
2. **A software key is always flagged.** On Android, Key Attestation shows the key's
   security level as `Software`; on iOS, the device states the key is not in the Secure
   Enclave and App Attest still attests the app. The server records the flag on the device
   record (`keyStorage: software`).
3. **Under `standard`, a software key is accepted and capped at `AL1`.** Such a device can
   sync and run anything that requires `AL0` or `AL1`; it never reaches `AL2`, whatever
   else it proves.
4. **Under `strict` and `maximum`, a software key is refused at registration** with
   `PLX-6001` (reason `KEY_NOT_HARDWARE_BACKED`). The app shows the host's "device not
   supported" message; nothing syncs.
5. **The flag is a profile setting** (`allowSoftwareKeys`), so an operator can refuse
   software keys under `standard` too. It can be tightened from `standard`'s default, never
   loosened under `strict` or `maximum` (`SEC-181`).
6. **Development builds** use the development provider of §3, not a software key, and are
   refused by production environments whatever their key (`SEC-008`).

The negative suite (`SEC-029`) proves both halves: a software-key proof is refused where a
hardware key is required, and accepted, capped at `AL1`, where the profile allows it.

## 3. How attestation is tested

Real attestation verdicts need things CI does not have:

- **Play Integrity** needs a Google Cloud project, a Play Console app and an install from
  Google Play.
- **App Attest** needs an Apple Developer team and a physical iPhone; the simulator does
  not support it.
- **Key Attestation** on an emulator yields a software-level chain from a test root, never
  Google's roots.

So Plux proves attestation in three layers. Each layer says exactly what it proves.

### 3.1 Layer 1: verifier tests in CI (every pull request)

The server's verifiers are tested against fixed test vectors, committed under
`schema/testdata/attestation/`:

| Vector set | Source | Proves |
|---|---|---|
| Android Key Attestation chains | Chains recorded from real devices once, by the maintainer (§3.3), with the device identifiers removed; plus chains generated by a test CA in the test, for each failure | Chain building to Google's roots, the attestation extension parsing (challenge, security level, verified boot state, bootloader lock, package name, signing certificate digest), and the revocation list check |
| Play Integrity tokens | Tokens encrypted and signed in the test with test keys, in the format Google documents | Local decryption and signature check, the request hash binding the server challenge and the DPoP key thumbprint, verdict mapping per profile, expiry |
| App Attest objects and assertions | Objects recorded once from a real iPhone (§3.3), plus objects generated in the test with a test root | CBOR parsing, the chain to the App Attest root, nonce, App ID, environment, the counter rules, and assertion verification |
| Every failure `SEC-029` lists | Generated in the test | Each one is refused with the expected error |

Recorded vectors carry their expiry. Where a recorded certificate expires, the test pins the
verification time, so the suite stays deterministic (`AGENTS.md` §5, tests).

### 3.2 Layer 2: the development provider in CI (every device job)

The Android and iOS device jobs (emulator, simulator) register through the **development
attestation provider** (`SEC-008`):

- It is a separate provider, named in the evidence, never a missing or skipped check.
- It is accepted only by environments whose type is not `production`; a production
  environment refuses it (`PLX-6001`, reason `DEV_PROVIDER_IN_PRODUCTION`). A test proves
  the refusal.
- A device registered with it gets `AL0`, unless the environment's settings raise it for
  testing, which a production environment cannot do.

This layer proves the whole flow end to end on emulators and simulators: registration,
DPoP on every request, nonces, token refresh, revocation, re-attestation, assurance
enforcement on pages, routes and data sources, and the gateway.

### 3.3 Layer 3: the maintainer's real-device check (once per phase, and before a release)

Some things only real devices prove: a real Play Integrity verdict, a real App Attest
attestation, StrongBox and Secure Enclave keys, RASP detections, screenshot blocking and
overlay filtering. The maintainer runs these on one mid-tier Android phone (a Pixel 6a or
newer, with Google Play) and one iPhone (iOS 17 or newer), as with the P5 reference-device
run.

- **The checklist** is `docs/security/real-device-checklist.md` (milestone S11). Each item
  names its requirement, the steps, the expected result and where the evidence goes.
- **The setup** is a Play Console internal test track, a Google Cloud project linked to it,
  and an Apple Developer team with App Attest enabled. The steps are in the checklist.
- **The evidence** goes to `docs/security/evidence/<date>-<device>.md`: device model, OS
  version, build, results, and the recorded attestation objects (identifiers removed) that
  feed Layer 1's vectors.
- **What stays `WIP` until then:** `SEC-002`, `SEC-003` and `SEC-004` end `DONE` only after
  the check passes on real devices, and `SEC-190`'s MASTG results come from it. Until then,
  each stays `WIP` with this check named.

### 3.4 What each layer can and cannot claim

| Claim | Proved by |
|---|---|
| The verifiers accept valid evidence and refuse every forgery and mismatch | Layer 1 |
| The protocol works end to end on every device request | Layer 2 |
| Production refuses the development provider | Layers 1 and 2 |
| Real Google and Apple evidence verifies, and hardware keys are really hardware | Layer 3 |
| RASP, screenshot blocking and overlay filtering work on real devices | Layer 3 (emulators show part, Layer 2) |

No document, release note or status may claim a Layer 3 result from Layer 1 or Layer 2
evidence.
