# Architecture Decision Records

Significant design decisions are recorded here in the [MADR](https://adr.github.io/madr/) format ([template](template.md)). An ADR is immutable once accepted; reversing a decision means writing a new ADR that supersedes it.

Write an ADR when a decision is hard to reverse, affects more than one component, adds or replaces a dependency ([dependencies.md](../engineering/dependencies.md)), changes a `MUST` requirement, or chooses between reasonable alternatives that a future reader would question.

Files are named `NNNN-short-title.md` with a four-digit, never-reused number. The numbers and phases below match spec §32.

## Index

| ADR | Decision | Phase | Status |
|---|---|---|---|
| [0001](0001-monorepo-and-toolchains.md) | Monorepo with per-component toolchains and a top-level Makefile | P0 | Accepted |
| [0002](0002-flatbuffers-sectioned-bundles.md) | FlatBuffers sections in a hashed container for bundles | P1 | Accepted |
| [0003](0003-section-level-deltas.md) | Section-level deltas with zstd `--patch-from` | P2 | Accepted |
| [0004](0004-tuf-style-update-security.md) | TUF-style update security with offline root keys | P2 | Accepted |
| [0005](0005-connectrpc-and-protobuf.md) | ConnectRPC and Protocol Buffers for all APIs | P2 | Accepted |
| [0006](0006-modular-monolith-with-roles.md) | Modular monolith with deployable roles instead of microservices | P2 | Accepted |
| [0007](0007-postgresql-and-object-storage.md) | PostgreSQL as system of record and job queue; S3-compatible object storage | P2 | Accepted |
| 0008 | Riverpod as the runtime state engine | P3 | Planned |
| [0009](0009-pxl-typed-expression-language.md) | PXL: a typed expression language compiled to bytecode | P1 | Accepted |
| [0010](0010-layered-widget-model.md) | Layered widget model with a descriptor registry | P1 | Accepted |
| 0011 | Plux Functions: standard Go compiled to WebAssembly; explicit placement | P7 | Planned |
| 0012 | DPoP with hardware-backed keys plus platform attestation | P6 | Planned |
| 0013 | Plux Canvas: TypeScript WebGL2 design surface with a conformance suite | P11 | Planned |
| 0014 | Studio on Bun with a backend-for-frontend; React and shadcn/ui | P11 | Planned |
| [0015](0015-single-draft-with-snapshots-and-locks.md) | Single draft with snapshots and exclusive plugin locks | P2 | Accepted |
| 0016 | Local database adapter model with Drift as default | P5 | Planned |
| 0017 | AI provider abstraction with structured output and validation-driven repair | P12 | Planned |
| [0018](0018-unified-error-model.md) | Unified error model with registered codes and reasons | P1 | Accepted |
| 0019 | Approvals bound to artifact content hashes | P9 | Planned |
| [0020](0020-build-once-promote-releases.md) | Build-once-promote app releases as the unit of activation | P2 | Accepted |
| 0021 | Sync all plugins at app start instead of lazy loading | P3 | Planned |
| [0022](0022-open-core-licensing.md) | Open-core licensing: Apache-2.0 client side, AGPL-3.0 server and Studio, commercial `ee/` | P0 | Accepted |
| 0023 | Mixed native/plugin screens: native slots and `PluxView` with shared exposed state | P4 | Planned |
| 0024 | No-code generated projects and shell-update detection | P4 | Planned |
| [0025](0025-document-schema-toolchain.md) | Document schema toolchain: validation, canonicalisation and code generation | P1 | Accepted |
| [0026](0026-identity-tenancy-and-access.md) | Built-in accounts with TOTP, org-bound credentials and row-level scopes | P2 | Accepted |
| [0027](0027-asset-pipeline.md) | Asset pipeline: uploads, WebAssembly image codecs and dotLottie | P2 | Accepted |

The decisions for planned ADRs are summarised in spec §32 and §34.1. Each is written in full before or alongside the first implementation that depends on it, and its status is updated here.
