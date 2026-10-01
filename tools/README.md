# tools

Repository tooling, written in Go with the standard library only.

| Command or package | Purpose | Requirements |
|---|---|---|
| `cmd/reqtrace lint` | Checks the specification: unique IDs, declared areas, phases, cross-references, anchors, withdrawn rows | `QA-073` |
| `cmd/reqtrace report` | Maps every requirement to the tests and CI checks that verify it; `-strict` fails on violations | `QA-070`, `QA-071` |
| `cmd/covgate` | Enforces the coverage floors in `coverage.json` for Go, Dart and Studio; files marked as generated are excluded | `QA-001` |
| `cmd/schemagen`, `internal/codegen` | Generates Go, Dart and TypeScript code and reference documents from `schema/` (ADR-0025) | `SCH-001`, `WGT-002`, `LIM-001` |
| `cmd/affected`, `internal/affected` | Decides which CI jobs a change runs, from `ci/affected.json` and the Go and Dart dependency graphs; `make check-changed` runs the same selection locally (ADR-0043) | `CI-002` |
| `internal/policy` | Tests repository rules: security policy, Dependabot coverage of every manifest, bash with pipefail in every workflow, Dart publishing, the runtime benchmark's CI parts measuring every metric | `SEC-192`, `CI-007`, `CI-001`, `CI-005`, `QA-007` |

Run them through the Makefile: `make spec-lint`, `make trace`, `make go-cover`, `make go-test`, `make gen`. Conventions for citing requirements in tests: [testing.md §3](../docs/engineering/testing.md#3-naming-and-traceability).
