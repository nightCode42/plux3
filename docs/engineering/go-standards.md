# Go Standards

How Go code in Plux (`backend/`, `tools/`) is written. The baseline is [Effective Go](https://go.dev/doc/effective_go), the [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments) and the [Google Go Style Guide](https://google.github.io/styleguide/go/); this document records the project-specific decisions on top of them. Formatting and much of this document are enforced by `golangci-lint` ([.golangci.yml](../../.golangci.yml)); the rest is enforced in review.

---

## 1. Principles

1. **Clarity over cleverness.** Prefer the obvious solution, even when it is longer.
2. **The specification drives the code.** Types, names and behaviour follow `docs/requirements.md`.
3. **Explicit over implicit.** Dependencies are passed in, not reached for; behaviour is configured, not inferred.
4. **Small, focused units.** A package has one responsibility; a function does one thing.
5. **Make invalid states unrepresentable** with types and constructors rather than checks everywhere.

## 2. Modules, packages and layering

- Two modules, joined by the root `go.work`: `backend` (server, CLI, compiler, SDK) and `tools` (repository tooling, standard library only). The toolchain is pinned by the `toolchain` line in each `go.mod`.
- `cmd/<binary>` contains only wiring. Command logic sits behind a testable `run(args, stdout, stderr) int` or a component constructor.
- `internal/<module>` holds the implementation, one package per module listed in spec §6.3. Package names are short, lower-case nouns — never `util`, `common`, `helpers` or `misc`.
- Layering (spec §6.7) is one-directional: handlers → domain modules → storage. Only `signing/` touches keys; `fnrunner` imports no storage or signing packages.
- Interfaces are defined where they are **used**, and kept small — usually one to three methods.
- An import cycle is a design error, fixed by restructuring, never by a shared "common" package.

## 3. Naming

- Go conventions: `MixedCaps`; consistent initialisms (`ID`, `URL`, `DPoP`, `JWKS`, `PXL`); no `Get` prefix on getters.
- The spec's vocabulary exactly: `AppRelease`, `PluginVersion`, `Bundle`, `Section`, `Manifest`, `Delta`, `Channel`, `AssuranceLevel`, `Placement`. Never invent a synonym.
- Error reasons are `UPPER_SNAKE_CASE`; their Go identifiers are `Reason<Name>` (see [error-handling.md](error-handling.md)).
- Test names follow [testing.md §3](testing.md#3-naming-and-traceability).

## 4. Comments and documentation

| Element | Rule |
|---|---|
| Package | A `doc.go` with the package comment: responsibility, key invariants, spec sections implemented. |
| Function or method | A doc comment of one to three lines, starting with its name, stating **what** it does. |
| Exported identifiers | A doc comment starting with the name. |
| Struct fields | A comment when meaning, unit or invariant is not obvious (`// in bytes`, `// guarded by mu`). |
| Concurrency | Every type states whether it is safe for concurrent use; every mutex documents what it guards. |
| Spec-driven behaviour | A comment citing the requirement: `// SYN-005: activation is atomic.` |
| Licensing | Every file starts with the SPDX header matching `REUSE.toml`. |

Comments are complete sentences ending with a period (`godot`), explain *why* inside function bodies, and are kept true. Not allowed: commented-out code, `TODO` without an issue, author names or change history, decorative banners.

## 5. Size and complexity

| Limit | Value | Enforced by |
|---|---|---|
| Function length | 80 lines, 50 statements | `funlen` |
| Cognitive complexity | 20 | `gocognit` |
| Nesting | 5 | `nestif` |

A function that exceeds a limit is split, not annotated. Tests are exempt from length limits.

## 6. Errors

- Functions return `error`, never a concrete error pointer.
- Errors crossing a package boundary are wrapped with context: `fmt.Errorf("pkg.Func: %w", err)` (`wrapcheck`).
- Never compare error strings; use `errors.Is` and `errors.As` (`errorlint`).
- Log an error once, at the edge — never log and return.
- From P1 the unified error type of ADR-0018 applies ([error-handling.md](error-handling.md)).

## 7. Context, concurrency and time

- `context.Context` is the first parameter of every function that does I/O, blocks, or crosses a package boundary; deadlines and cancellation are honoured.
- Every goroutine has an owner, a shutdown path and a test proving it does not leak.
- Time and randomness are injected; tests never use `time.Sleep` for correctness.
- No global mutable state, and no `init()` side effects beyond registration. The one exception is a `sync.Once` that works around a dependency's own process-wide state, with a comment naming the upstream defect (`internal/wasmrt`).

## 8. Security

- `gosec` runs in CI; findings are fixed, not suppressed, unless a comment explains why the finding cannot apply.
- File and directory permissions are as tight as the use allows (`0o600`, `0o750`).
- Cryptography uses the standard library or dependencies on the allowlist; never hand-rolled primitives.
- See [security-practices.md](security-practices.md) for the cross-component rules.

## 9. Formatting and spelling

- `gofumpt` and `goimports` with the local prefix `github.com/nightCode42/plux3` (`make go-fmt`).
- Spelling follows British English, matching the specification (`misspell` with the UK locale).
