# 0055. Confidential bundles: content keys wrapped to device keys, no deltas

- **Status:** Accepted (maintainer, 2026-10-07, P6 S0; P6 plan §2.1 B10)
- **Date:** 2026-10-07
- **Requirements:** `SEC-053`, `SEC-052`, `NFR-013`, `REL-020`

## Context and problem

Some customers must keep page content (texts, flows, business rules in PXL) confidential:
a CDN or a curious user must not be able to read it. `SEC-053` requires AES-256-GCM with a
per-release content key wrapped to a device-held hardware key-agreement key.

## Decision drivers

- Verify before decrypting (`SEC-052`); decrypt off the UI isolate (L-7).
- Only eligible devices receive the key; the key never leaves secure hardware wrapped.
- Decryption ≤ 10 ms per MiB p95 (`NFR-013`).

## Considered options

1. **Per-release content key, sections encrypted, key wrapped per device with ECDH-ES
   (P-256) + HKDF + AES-KW, no deltas.**
2. Encrypted deltas (patch over ciphertext or over plaintext then re-encrypt).
3. Transport encryption only.

## Decision

Chosen option: **1**.

- **Encryption.** At publish, the compiler's output is unchanged; the release step draws a
  random 256-bit content key per release and encrypts each section with AES-256-GCM (a
  unique nonce per section from a counter; the section hash and bundle ID as additional
  data). Hashes in `targets` cover the ciphertext, so verification precedes decryption.
  The content key is stored encrypted under KMS (`SEC-106`).
- **Key delivery.** A device registers a key-agreement public key (ECDH P-256 in secure
  hardware). For eligible devices (assurance ≥ the configured level) the manifest response,
  outside the signed part, carries the content key wrapped with ECDH-ES against that key,
  HKDF-SHA-256 bound to the device ID and release, and AES-KW. Ineligible devices get no
  key and do not sync confidential plugins (`PLX-6050`).
- **No deltas.** Ciphertext does not diff; a changed confidential bundle downloads whole.
  Encrypted deltas may come later with their own ADR.
- **On the device.** Bundles stay encrypted at rest; sections are decrypted into memory
  off the UI isolate, once per launch, on first use, with the platform's hardware AES
  (ADR-0058). Decrypted bytes are never written to disk.
- `confidentialBundles` (off / optional / required) is a profile setting.

## Consequences

- **Positive:** content is opaque to CDN and storage; revocation is per device.
- **Negative:** larger updates for confidential releases; the mmap zero-copy path of
  ADR-0030 does not apply to decrypted sections.
- **Follow-up:** S7, with the `NFR-013` benchmark; ADR-0003, ADR-0029, ADR-0030 revised.

## Options in detail

### Option 2: encrypted deltas

Requires either deterministic encryption (leaks equality) or plaintext patching on the
device, which needs the old plaintext on disk. Deferred.

### Option 3: transport encryption

TLS already exists; it does not protect content at the CDN or on the device.
