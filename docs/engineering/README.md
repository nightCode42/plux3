# Engineering Handbook

How Plux is built. The [specification](../requirements.md) says *what* the system must do; this handbook says *how* we write, test, secure and deliver it. The binding summary of both is [AGENTS.md](../../AGENTS.md).

| Document | Covers |
|---|---|
| [product-context.md](product-context.md) | Mission, positioning, priorities, components, phases, non-goals |
| [system-invariants.md](system-invariants.md) | Technical rules the system must never violate, with requirement references |
| [security-practices.md](security-practices.md) | Secure-coding rules across all components |
| [go-standards.md](go-standards.md) | Go: layout, naming, comments, size limits, errors, concurrency |
| [dart-standards.md](dart-standards.md) | Dart and Flutter: API design, state, isolates, widgets, documentation |
| [typescript-standards.md](typescript-standards.md) | TypeScript and Studio: strictness, modules, React, Biome |
| [error-handling.md](error-handling.md) | The unified error model and error codes across components |
| [testing.md](testing.md) | Test strategy, conventions per language, traceability, coverage |
| [dependencies.md](dependencies.md) | Dependency policy and allowlists per ecosystem |
| [workflow.md](workflow.md) | Branches, commits, pull requests, definition of done, releases |
| [ci.md](ci.md) | The CI pipeline, the Makefile, git hooks and tool versions |

Terms used across the handbook — bundle, manifest, delta, DPoP, PXL and others — are defined in the [specification glossary](../requirements.md#35-glossary).

Changes to this handbook go through pull requests like code. A rule that no longer serves the project is changed here, explicitly — never ignored quietly.
