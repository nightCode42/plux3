# 0060. Signing backends and audit checkpoints

- **Status:** Accepted (maintainer, 2026-10-07, P6 S0; P6 plan §2.1 B12, B18)
- **Date:** 2026-10-07
- **Requirements:** `SEC-120`, `SEC-121`, `SEC-141`, `DEP-040`

## Context and problem

ADR-0026 defined the signing abstraction with file and Vault Transit backends and a
cgo-free design. P6 needs HSM and cloud KMS backends for production and signed checkpoints
over the hash-chained audit log (`SEC-141`).

## Decision drivers

- `plux-server` stays cgo-free and distroless.
- One integration for many products.
- Audit tampering is detectable offline by `plux-server audit verify`.

## Considered options

1. **A PKCS#11 helper process (cgo, `github.com/miekg/pkcs11`) talking to `plux-server`
   over a local socket; cloud HSMs through their PKCS#11 libraries; Vault Transit stays.**
2. Native SDKs per cloud (AWS, Google, Azure) in `plux-server`.

## Decision

Chosen option: **1**.

- **Helper.** `plux-pkcs11-helper` loads the vendor module, exposes `Sign(keyRef, digest)`
  and `PublicKey(keyRef)` over a Unix socket with a length-prefixed protobuf protocol, and
  runs as a sidecar. Only the helper links `miekg/pkcs11`; `signing/` is its only client
  (L-2).
- **Products:** AWS CloudHSM, Google Cloud HSM (`libkmsp11`), Azure Managed HSM, any
  PKCS#11 HSM; tested in CI with SoftHSM2.
- **Audit checkpoints.** A worker job signs `(sequence, entry hash)` of the chain head on a
  schedule with the audit key; `ListAuditCheckpoints` exposes them; `plux-server audit
  verify` recomputes the chain and checks every checkpoint.

## Consequences

- **Positive:** one code path for every HSM; `plux-server` stays static.
- **Negative:** an extra process to deploy where HSMs are used.
- **Follow-up:** S5. `SEC-120`'s list is updated in spec 1.4.0.

## Dependencies

- **`github.com/miekg/pkcs11`** (BSD-3-Clause, cgo): in the helper binary only.
- **SoftHSM2** (BSD-2-Clause): CI test tool only.
