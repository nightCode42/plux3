# 0001. One monorepo with per-component toolchains and a top-level Makefile

- **Status:** Accepted
- **Date:** 2026-09-26
- **Requirements:** `CI-001`, `CI-002`, `CI-003`, `CI-006`, `CI-007`, `QA-001`, `QA-070`

## Context and problem

Plux has components in three ecosystems — Go (server, CLI, compiler, tooling), Dart and Flutter (runtime, host apps) and TypeScript on Bun (Studio) — joined by shared contracts in `schema/` and `proto/`. A change to a contract, such as the bundle format, touches the compiler, the runtime and Studio together. How should the code be organised and built so that such a change is one reviewable unit, and so that every component meets the same quality gates?

## Decision drivers

- Contract changes must be atomic across Go, Dart and TypeScript (`BND-000`, `SCH-000`).
- The same checks must run locally, in git hooks and in CI (`CI-002`).
- Each ecosystem keeps its idiomatic tooling; nobody should need a foreign build system to work on one component.
- CI cost stays proportional to what changed (`CI-002`: path-filtered jobs).
- The history and the specification must be readable as one story.

## Considered options

1. **One monorepo, native toolchains per component, a top-level Makefile as the single entry point.**
2. One monorepo with a polyglot build system (Bazel or Pants).
3. One repository per component.

## Decision

Chosen option: **1**. The repository holds every component; each uses its native toolchain — Go modules joined by a committed `go.work`, a Dart pub workspace with one `pubspec.lock`, a Bun workspace with one `bun.lock`. A top-level Makefile delegates to them and is the only entry point for developers, hooks and CI. CI runs one job per toolchain behind path filters, plus repository-wide jobs, and a single **CI OK** gate aggregates them.

## Consequences

- **Positive:** a contract change is one pull request with one review and one CI run; the specification, the handbook and the code version together; each component keeps familiar tooling; `make check` is the complete definition of "green".
- **Negative:** the Makefile must be maintained by hand; CI needs three toolchains; contributors to one component still clone the whole repository.
- **Follow-up:** each new component adds Makefile targets, a CI job, a Dependabot entry and a licence entry (docs/engineering/ci.md §6).

## Options in detail

### Option 1 — monorepo, native toolchains, Makefile

Idiomatic for each ecosystem, simple to understand, well supported by Dependabot, GitHub Actions caches and IDEs. Incremental builds rely on each toolchain's own caching, which is sufficient at this size.

### Option 2 — monorepo with Bazel or Pants

Hermetic, cached, incremental builds across languages. Rejected: Flutter support is immature, the configuration burden is large for a small team, and it would make contributing to any single component harder.

### Option 3 — repository per component

Clear ownership and independent histories. Rejected: contract changes would need coordinated multi-repository releases, the specification would live apart from most code, and cross-component tests would need extra orchestration.
