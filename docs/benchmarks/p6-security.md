<!--
SPDX-FileCopyrightText: 2026 Plux contributors
SPDX-License-Identifier: Apache-2.0
-->

# P6 — Device security

Results of Phase 6's benchmarks, with their method (DoD-4, `QA-007`). The target is set for
the mid-tier reference device, which CI does not have: `NFR-012` stays `WIP` until the
maintainer runs the device benchmark there (plan p6 B16). The host numbers below come from
this repository's cloud container (Intel Xeon @ 2.10 GHz, 4 cores; 2026-10-08), so they say
how the Dart side behaves, not whether a phone meets the target.

| Requirement | Target (reference device) | Command |
|---|---|---|
| `NFR-012`, `SEC-021` | DPoP proof creation ≤ 15 ms, p95 | `make bench-dpop` (host), the device command below (reference device) |

## DPoP proof creation (`NFR-012`)

**What is measured.** One call of `DpopProofs.proof` from start to the finished compact JWS:
the header and payload JSON, base64url, the SHA-256 of the access token for `ath`, the
`jti` from a secure generator, and the signature. Each proof carries a 384-byte access token
and a nonce, as a request to the server does.

**Host benchmark.** `make bench-dpop` runs `packages/plux_flutter/test/security/dpop_bench_test.dart`
on the Dart VM (JIT) after 100 warm-up proofs, and times 1,000 proofs with a stopwatch, in two
variants:

- *Instant sign*: a key store whose `sign` returns at once. The time is the Dart side's:
  JSON, base64url, hashing and `ath`.
- *Software keys*: `SoftwareDeviceKeys`, P-256 in pure Dart. An upper bound for the signature
  on a platform with no secure hardware, and not what a phone runs: the runtime uses
  `PlatformDeviceKeys`.

| Variant | p50 (ms) | p95 (ms) | p99 (ms) |
|---|---:|---:|---:|
| Instant sign (Dart side only) | 0.080 | 0.743 | 1.442 |
| Software keys (pure-Dart P-256) | 7.82 | 11.686 | 17.637 |

The Dart side costs under 1 ms at p95 on JIT, so the 15 ms budget is spent by the platform's
signature (Keystore or Secure Enclave round trip over the platform channel). Even pure-Dart
P-256 on this machine stays under the target at p95, but a phone's CPU is slower, which is
why the device run decides.

## Reference device

**Method.** `apps/starter/integration_test/dpop_bench_test.dart` creates a key with the real
`PlatformDeviceKeys` (StrongBox or the TEE on Android, the Secure Enclave on iOS; the
platform's software keystore on an emulator or simulator), signs 200 proofs after 20 warm-up
proofs, prints p50, p95 and p99 as JSON, and deletes the key. It fails when p95 is over 15 ms
only with `PLUX_BENCH_ASSERT=1`; CI emulators do not set it and only report. It is not part
of any CI job. The JSON names the key's storage (`key_storage`), which the result must
record: StrongBox decides `SEC-001`'s choice of key storage for the DPoP key by this
measurement.

**Command** (from `apps/starter`):

```bash
flutter test integration_test/dpop_bench_test.dart -d <device> \
    --dart-define=PLUX_BENCH_ASSERT=1
```

`flutter test` builds the app in debug mode, so the Dart side runs JIT, which is slower than
the AOT code a release app runs; the result is a pessimistic bound for the target.

| Device | OS | Key storage | p50 (ms) | p95 (ms) | p99 (ms) | Date |
|---|---|---|---:|---:|---:|---|
| _to be run by the maintainer_ | | | | | | |
