# Product Context

What Plux is, who it is for, and the boundaries every design decision must respect. Source: spec §1–§3.

---

## 1. Mission

Plux lets teams build native Flutter screens visually, compile them into signed binary plugins, and ship them to every device without an app-store release — safely enough for regulated industries and fast enough that users never notice.

Its value is measured by two questions:

1. **Are its claims true?** Zero parse cost, tiny deltas, verified updates, hardware-bound requests: each claim has a benchmark or a test, or it is not made.
2. **Can an engineer who did not write it understand it?** Every non-obvious decision is explained in a doc comment, the handbook or an ADR.

## 2. Priorities

When two good options conflict, the one higher in this list wins (spec §1.4):

1. Security and correctness
2. Performance — device first, then server, then Studio
3. Developer experience
4. Operability
5. Feature breadth

## 3. Design goals

| Goal | What it means in practice |
|---|---|
| G1 Performance is a feature | Zero-copy FlatBuffers, pre-compiled expressions, fine-grained rebuilds, section-level deltas; every claim benchmarked in CI (spec §30). |
| G2 High-assurance security by default | DPoP on every request, hardware keys, attestation, signed anti-rollback updates, sandboxed compute, tamper-evident audit. |
| G3 Developer joy | Ten minutes to a Studio-built page on a phone; typed routes; errors with fix-its; live device debugging. |
| G4 No-code or incremental | Generated projects for new apps; one screen or widget at a time for existing apps. |
| G5 Offline-first, resilient, bounded | Everything cached; last known good always runs; failures contained; every resource limited. |
| G6 Correct by construction | Validation and type-checking at publish time, not on a customer's phone. |
| G7 Governed change | Immutable versions, approvals bound to hashes, staged rollouts, kill switches, full audit. |
| G8 Self-hosted by design | Every capability runs on the operator's infrastructure, including air-gapped networks. |
| G9 Standards over invention | RFC 9449, TUF, OpenTelemetry, OpenAPI, ICU/CLDR, WCAG, OWASP, REUSE, SLSA. |
| G10 Readable end to end | ADRs for decisions, tests for requirements, comments for intent. |

## 4. Components

| Component | Role | Phase |
|---|---|---|
| Plux Schema | Versioned document model; single source for validation and code generation | P1 |
| Plux Compiler | Deterministic Go library: validate, type-check, optimise, encode, hash | P1 |
| Plux Server | `api`, `worker`, `fnrunner` roles: documents, publish, releases, devices, functions | P2 |
| `plux_flutter` | Runtime: sync, verify, render, navigate, act, secure | P3 |
| Plux Functions | Sandboxed Go/WASM on server or device | P7 |
| Plux Dev app | Live device preview and debugging | P10 |
| Plux Studio | Visual design, governance and administration | P11 |
| Plux CLI | Developer and CI entry point | P2 onwards |

## 5. Delivery

Sixteen phases (P0–P15), grouped into four milestones (spec §5). The core engine comes first and Studio last, because the contracts the engine fixes — the document model, the bundle format, the release and sync model, the security model — are the hardest to change later. Each phase ends with its Definition of Done (spec §5.3).

## 6. Non-goals

- Downloading native, Dart or JavaScript code to devices.
- Changing an app's primary purpose after store review.
- Hosting end-user data (Plux is not a backend-as-a-service).
- Lazy per-plugin download: all plugins sync at start, so any screen reaches any screen offline.
- Real-time multi-user editing before P14.

The full list with rationale is in spec §3.2.
