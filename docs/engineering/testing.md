# Testing

How Plux is tested. The strategy and thresholds are specified in spec §28; this document defines the conventions that make tests consistent, readable and traceable to requirements across Go, Dart and TypeScript.

---

## 1. Principles

- **Tests are the evidence.** A requirement is `DONE` only when an automated test or CI check verifies it (`QA-070`).
- **Deterministic and hermetic.** No real network beyond loopback, no wall-clock dependence, no dependence on execution order. The same commit gives the same result every time.
- **Readable first.** A test documents behaviour; a reader understands the rule without reading the implementation.
- **Never weaken a test to make a change pass.** If a test is wrong, fix it in a separate, explained commit.

## 2. Levels

| Level | Location | Runs |
|---|---|---|
| Unit, property, fuzz (Go) | next to the code, `*_test.go` | every change: `make go-cover` (with `-race`) |
| Unit and widget (Dart) | `packages/<pkg>/test/*_test.dart` | every change: `make dart-cover` |
| Unit (TypeScript) | next to the code, `*.test.ts` | every change: `make studio-cover` |
| Repository policy | `tools/internal/policy` | every change |
| Integration (server with real PostgreSQL, storage, Valkey) | `test/integration/` | from P2, every pull request (`QA-005`) |
| End-to-end (emulators, simulators, device farm) | `test/e2e/` | from P3 (`QA-006`) |
| Compatibility, security, load, layout conformance | `test/compat/`, `test/security/`, `test/load/`, `test/layout-conformance/` | from the phase that introduces them |

## 3. Naming and traceability

Every test that verifies a requirement names it, so the traceability report is generated from code (`QA-071`). `make trace` fails when a `DONE` `MUST` requirement has no evidence, or when evidence cites an unknown or withdrawn ID.

**Go** — the primary requirement in the name, all covered requirements on a `Verifies:` line:

```go
// TestActivationSurvivesCrash checks that a crash mid-activation leaves the
// old release fully active.
// Verifies: SYN-005, QA-009.
func TestActivationSurvivesCrash_SYN_005(t *testing.T) { ... }
```

**Dart** — the ID in square brackets in the test description:

```dart
testWidgets('keeps the last known good release after three crashes [SYN-006]', (tester) async { ... });
```

**TypeScript** — the same bracket form:

```ts
test("rejects contrast below 4.5:1 for body text [A11Y-003]", () => { ... });
```

**CI checks** — a `# Verifies: ID.` comment on the workflow step or Makefile target that enforces the requirement.

Tests with no requirement (helpers, regressions) omit the ID but still describe the behaviour.

## 4. Structure

- Table-driven tests for any behaviour with more than two cases; each case has a name.
- Arrange, act, assert, separated by blank lines.
- Tests that share no mutable state run in parallel (`t.Parallel()` in Go).
- Temporary files go in the test framework's temporary directory, never in the repository.
- Black-box tests of exported behaviour are preferred; white-box tests only when an invariant is not observable from outside.

## 5. Test doubles

- Hand-written fakes of interfaces Plux owns, in a `<package>test` package (Go) or `test/fakes/` (Dart); no generated mocks.
- Every fake passes the same contract tests as the real implementation.
- Third-party code is exercised for real (PostgreSQL in a container, HTTP on loopback), not mocked.

## 6. Property, fuzz and golden tests

- Property tests state an invariant in one sentence; shrunk failures become regular table tests.
- Parsers of untrusted input are fuzzed (`QA-004`); every crash becomes a seed and a regular test.
- Output that must never change silently — compiled bundles, conformance vectors — is checked against committed golden files, updated only with an explicit flag (`go test … -update`) and reviewed as a behaviour change. `make go-determinism` runs these checks, and CI runs it on Linux, macOS and Windows, so every platform produces the same bytes (`CMP-002`).
- Fuzz targets run briefly in every `go test` from their seed corpus, and for `FUZZTIME` each with `make go-fuzz`, which the nightly [fuzz workflow](../../.github/workflows/fuzz.yml) runs for five minutes per target.

## 7. Timing budgets

Performance budgets of the spec (§30) are asserted by tests that run only when a Make target enables them, because timings depend on the machine: `make go-budgets` measures the compiler's (`CMP-050`, `SCH-042`) on CI's reference runner. Benchmarks (`go test -bench`) exist beside them for profiling; results are recorded per phase in [docs/benchmarks/](../benchmarks/README.md).

## 8. Coverage

- Floors (`QA-001`), defined in [coverage.json](../../coverage.json) and enforced by `covgate`: 85% for the compiler, PXL, bundle, delta, DPoP, approval, sync and security packages; 80% for all other Go and Dart code; 70% for Studio.
- Coverage is a floor, not a goal: a test that executes code without asserting behaviour is not coverage.
- Files carrying the generated-code marker (`// Code generated … DO NOT EDIT.`, or flatc's Dart header) are excluded: generated code is verified by regenerating it (`make gen-check`, `CI-003`), and the floors measure hand-written code.
- Floors are never lowered to pass a change (`AGENTS.md` §5).

## 9. Flaky tests

A flaky test is a bug. It is quarantined immediately with a skip that links an issue, then fixed at its root cause — never made to pass with retries or longer sleeps.
