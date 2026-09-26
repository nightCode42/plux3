# tools — Agent Notes

Repository tooling: `reqtrace` (spec lint, traceability), `covgate` (coverage floors) and policy checks. Read the root [AGENTS.md](../AGENTS.md), [go-standards.md](../docs/engineering/go-standards.md) and [README.md](README.md) first.

## Rules

- **Standard library only.** No third-party module is ever added to `tools/go.mod`; the tools gate the rest of the repository and must not widen its supply chain.
- **CI gates live here.** Changing what `reqtrace` accepts as evidence, how `covgate` reads `coverage.json`, or what a policy test enforces changes a CI gate: stop and ask (AGENTS.md §7).
- **Deterministic output.** Reports are sorted and contain no timestamps, so reruns produce identical files.
- **Tests first.** Every parsing rule has a table test with a positive and a negative case, including text that merely looks like evidence (IDs inside strings or comments mid-line).
- **Verify against the real repository.** After a change, run `make spec-lint` and `make trace`; a tool that passes its own tests but rejects the actual spec is broken.

## Commands

```bash
make go-check    # lint, vet, test for backend/ and tools/
make spec-lint   # reqtrace lint on docs/requirements.md
make trace       # reqtrace report -strict
```
