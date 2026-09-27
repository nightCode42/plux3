# Threat Model

The STRIDE threat model required by `SEC-000` is established in P6 and reviewed at the end of every phase from then on. Until P6, each phase records its delta here (DoD-6): what it adds to the attack surface, the threats, and the mitigations with the requirement or test that holds them.

## P1 — Schema and compiler

**Scope.** Project documents (JSON in the Git layout), the compiler and `plux validate`/`build` on developer machines and CI, the bundle format and its Go verifier, PXL programs. Nothing in P1 is networked or signed; bundles are not yet delivered to devices.

**Trust boundaries.** Documents are untrusted input to the compiler: they come from contributors, Studio users and, later, AI assistance. Bundles are untrusted input to every reader, including the runtime from P3.

| STRIDE | Threat | Mitigation | Evidence |
|---|---|---|---|
| Tampering, DoS | A crafted document crashes the compiler, exhausts memory or makes it run for long | Strict I-JSON parser, file-size, depth, string, expression and nesting limits from the registry (`LIM-001`); PXL constant folding bounded by the operation budget; panics recovered as `PLX-2201` | `FuzzCompile`, `FuzzValidatePage`, PXL and JCS fuzz targets, nightly fuzz workflow (`CMP-052`, `QA-004`) |
| Elevation of privilege | Executable code smuggled into a bundle | Only PXL bytecode and action graphs are executable; assets and unknown sections starting with native, script, Dart snapshot or WebAssembly signatures are refused; SVG scripts refused (`SEC-054`, `PLX-1503`) | `TestInvalidProjects` (executable asset, SVG script), `bundle` tests |
| Tampering, DoS | A crafted bundle exploits a reader | Section hashes, bounds and a schema-driven FlatBuffers verifier with depth and table limits before any accessor runs (`BND-006`); PXL programs verified on decode; zstd transport bounded by the declared size (`BND-007`) | `FuzzVerifyPage`, `FuzzRead`, mutation test, Dart decoder over the golden bundles |
| Information disclosure | Secrets or sensitive values end up in bundles, which are readable on devices | Secret-like literals (`PLX-1500`), sensitive state sent to analytics (`PLX-1501`), `http:` URLs (`PLX-1502`); diagnostics never repeat a suspected credential | `TestInvalidProjects`, `TestDiagnosticsDoNotEchoCredentials` |
| Tampering | Two different styles or programs share a 64-bit content ID and one silently replaces the other | A collision is a compile error (`PLX-2202`) | encoder check |
| Spoofing, Repudiation | A modified bundle is taken for a published one | Out of scope in P1: bundle hashes give integrity only. Signing and TUF metadata arrive in P2/P6 (`SEC-050`–`SEC-052`); the runtime never loads unverified bundles | — |
| Tampering (supply chain) | A compromised code generator or dependency | flatc built from source at the pinned tag commit; new dependencies allowlisted by ADR; reproducible builds (`CI-006`); goldens reproduced on three platforms (`CMP-002`) | `go-reproducible`, `go-determinism` |

**Residual risks and follow-ups.** The Dart runtime's FlatBuffers verifier arrives in P3 (`SEC-052`); until then Dart only checks the container. Manifest, DPoP and attestation parsing are fuzzed from P2/P6 (`QA-004`). Regular expressions in PXL (`matches`) arrive in P5 and need a bounded engine.
