# tools

Repository tooling, written in Go with the standard library only.

| Command or package | Purpose | Requirements |
|---|---|---|
| `cmd/reqtrace lint` | Checks the specification: unique IDs, declared areas, phases, cross-references, anchors, withdrawn rows | `QA-073` |
| `cmd/reqtrace report` | Maps every requirement to the tests and CI checks that verify it; `-strict` fails on violations | `QA-070`, `QA-071` |
| `cmd/covgate` | Enforces the coverage floors in `coverage.json` for Go, Dart and Studio | `QA-001` |
| `internal/policy` | Tests repository rules: security policy, Dependabot coverage of every manifest | `SEC-192`, `CI-007` |

Run them through the Makefile: `make spec-lint`, `make trace`, `make go-cover`, `make go-test`. Conventions for citing requirements in tests: [testing.md §3](../docs/engineering/testing.md#3-naming-and-traceability).
