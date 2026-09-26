# Architecture Decision Records

Significant design decisions are recorded here in the [MADR](https://adr.github.io/madr/) format ([template](template.md)). An ADR is immutable once accepted; reversing a decision means writing a new ADR that supersedes it.

Write an ADR when a decision is hard to reverse, affects more than one component, adds or replaces a dependency ([dependencies.md](../engineering/dependencies.md)), changes a `MUST` requirement, or chooses between reasonable alternatives that a future reader would question.

Files are named `NNNN-short-title.md` with a four-digit, never-reused number. The numbers and phases below match spec §32.

## Index

| ADR | Decision | Phase | Status |
|---|---|---|---|
| [0001](0001-monorepo-and-toolchains.md) | Monorepo with per-component toolchains and a top-level Makefile | P0 | Accepted |
| 0002 | FlatBuffers for bundles, in a sectioned container | P1 | Planned |
| 0003 | Section-level deltas with zstd `--patch-from` | P2 | Planned |
| 0004 | TUF-style update security with offline root keys | P2 | Planned |
| 0005 | ConnectRPC and Protocol Buffers for all APIs | P2 | Planned |
| 0006 | Modular monolith with deployable roles instead of microservices | P2 | Planned |
| 0007 | PostgreSQL as system of record and job queue; S3-compatible object storage | P2 | Planned |
| 0008 | Riverpod as the runtime state engine | P3 | Planned |
| 0009 | PXL: a typed expression language compiled to bytecode | P1 | Planned |
| 0010 | Layered widget model instead of one-to-one mirroring of Flutter | P1 | Planned |
| 0011 | Plux Functions: standard Go compiled to WebAssembly; explicit placement | P7 | Planned |
| 0012 | DPoP with hardware-backed keys plus platform attestation | P6 | Planned |
| 0013 | Plux Canvas: TypeScript WebGL2 design surface with a conformance suite | P11 | Planned |
| 0014 | Studio on Bun with a backend-for-frontend; React and shadcn/ui | P11 | Planned |
| 0015 | Single draft with snapshots and exclusive plugin locks | P2 | Planned |
| 0016 | Local database adapter model with Drift as default | P5 | Planned |
| 0017 | AI provider abstraction with structured output and validation-driven repair | P12 | Planned |
| 0018 | Unified error model with registered reasons and Plux error codes | P1 | Planned |
| 0019 | Approvals bound to artifact content hashes | P9 | Planned |
| 0020 | Build-once-promote app releases as the unit of activation | P2 | Planned |
| 0021 | Sync all plugins at app start instead of lazy loading | P3 | Planned |
| [0022](0022-open-core-licensing.md) | Open-core licensing: Apache-2.0 client side, AGPL-3.0 server and Studio, commercial `ee/` | P0 | Accepted |
| 0023 | Mixed native/plugin screens: native slots and `PluxView` with shared exposed state | P4 | Planned |
| 0024 | No-code generated projects and shell-update detection | P4 | Planned |

The decisions for planned ADRs are summarised in spec §32 and §34.1. Each is written in full before or alongside the first implementation that depends on it, and its status is updated here.
