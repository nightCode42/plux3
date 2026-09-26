# System Invariants

The technical rules Plux must never violate, condensed from the specification with requirement references. When implementing an area, read its section here and the referenced spec sections — the spec remains authoritative.

Phase tags mark when a rule becomes binding.

---

## 1. Determinism and reproducibility

- The compiler produces byte-identical output for the same canonical input and compiler version, on every platform (`CMP-002`). No timestamps, map iteration order or environment data reach a bundle.
- Documents are canonicalised with RFC 8785 before hashing, diffing and storage (`SCH-003`).
- Release binaries are reproducible: pinned toolchain, `-trimpath`, empty build ID, metadata derived only from the commit (`CI-006`). `make go-reproducible` proves it.
- PXL evaluation is pure, deterministic and bounded (`PXL-001`); the same expression gives the same result in Go, Dart and TypeScript (`PXL-007`).

## 2. Contracts evolve additively

- From the first tagged runtime (end of P3), bundle and manifest formats change additively only; needed capabilities are declared in `required_features` so older runtimes refuse cleanly (`BND-000`, `BND-008`, `BND-018`).
- Every document schema change ships with a tested forward migration (`SCH-000`, `SCH-043`).
- The API contract is checked with `buf breaking`; breaking changes need a new version package (`SRV-000`).
- Widget, prop, action and enum IDs in bundles are permanent and never reused (`BND-011`).
- Sections reference each other only by stable IDs, so each can be patched independently (`BND-014`).

## 3. Sync and release

- Devices activate **app releases** — consistent sets of plugin versions — never individual plugins (`REL-002`).
- At start the runtime syncs **all** plugins; nothing is fetched lazily (`SYN-001`).
- Activation is atomic and crash-safe; a device runs either the old or the new release, never a mixture (`SYN-005`).
- The last known good release is kept and restored automatically after repeated failures (`SYN-006`).
- A rollback is a new, higher release sequence; devices refuse lower sequences (`REL-006`, `SEC-055`).
- A release is never swapped under a visible page (`SYN-004`).

## 4. Security

- Verify before load: signatures, expiry, hashes and the FlatBuffers verifier all pass before anything beyond a container header is read (`SEC-052`, `BND-006`).
- Every device request carries a DPoP proof bound to a non-exportable hardware key; tokens are sender-constrained (`SEC-001`, `SEC-020`–`SEC-024`).
- Private keys are used only through the signing abstraction in the `worker` role (`SEC-120`, L-3).
- No downloaded code except PXL bytecode, action graphs and interpreted, sandboxed WebAssembly (`SEC-054`).
- Capabilities are deny-by-default and declared per plugin and function (`SEC-080`, `FN-012`).
- Sensitive values never appear in logs, traces, telemetry or replays (`SCH-012`, `SEC-092`).

## 5. Functions

- Each function runs in exactly one placement, `server` or `device` (`FN-008`).
- Every invocation gets fresh linear memory and hard limits on time, memory, output and calls (`FN-010`, `FN-011`).
- `fnrunner` holds no database credentials or signing keys (`FN-013`).
- Device results are for display only; the server never trusts them (`FN-054`).

## 6. Fault isolation

- A failing page, component, action or native slot never crashes the host app (`RT-020`, `RT-021`, `WGT-033`).
- A plugin that is disabled or fails verification renders its fallback page (`RT-022`).

## 7. Resources

- Every size and count is governed by the limits registry; lower levels can only tighten (`LIM-001`, `LIM-002`).
- Nothing on the Flutter UI isolate does network I/O, decompression, patching or large hashing (L-6).
