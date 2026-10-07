# 0058. Encryption at rest and native AES-GCM

- **Status:** Proposed (P6 plan §2.1 B15, supersedes P5's B6 for `strict` and `maximum`)
- **Date:** 2026-10-07
- **Requirements:** `SEC-072`, `SEC-073`, `SEC-053`, `NFR-013`, `DB-002`

## Context and problem

Under `strict` and `maximum`, the database, response cache, outbox, `persisted` state and
key-value store are encrypted. P5 measured pure-Dart AES-GCM (`DartAesGcm`) at ten times
slower than native, too slow for every read and write.

## Decision drivers

- Within 10% of the plain stores in the P5 benchmarks (`NFR-013`).
- Keys never on disk unwrapped; unwrapped once per launch.
- No new third-party crypto unless measurement demands it.

## Considered options

1. **Platform crypto through FFI or platform channels: CryptoKit `AES.GCM` on iOS,
   Conscrypt/`javax.crypto` (ARMv8 AES instructions) on Android.**
2. A vendored, audited C library (e.g. BoringSSL's AES-GCM) compiled by the build hook.
3. Keep pure Dart.

## Decision

Chosen option: **decided by measurement in S7 between 1 and 2, defaulting to 1.** Option 1
adds nothing to the app; option 2 is chosen only if the call overhead of 1 misses the
budget for small records. Option 3 is ruled out by P5's measurement.

- **Keys.** One 256-bit data key per store, generated on the device, wrapped by a secure
  hardware key (Keystore AES key / Secure Enclave ECIES) and stored wrapped; unwrapped once
  per launch and kept in memory, never per read or write. Under `standard` with
  `encryptLocalStores` off, nothing changes (ADR-0049's per-profile wrapping is revised).
- **Format.** Each record: version byte, 12-byte random nonce, ciphertext, tag; the store
  and record key as additional data. SQLCipher stays for `plux_db_drift` (`DB-002`) with
  its key from the same wrap.
- **Off the UI isolate** for bulk work (L-7).

## Consequences

- **Positive:** hardware AES speed; nothing new to ship (option 1).
- **Negative:** two platform implementations; FFI and channel overhead to measure.
- **Follow-up:** S7 measures and records the result in this ADR; then Accepted.
