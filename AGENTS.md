# AGENTS.md — Working Agreement for Plux

This file is the binding working agreement for every contributor — human or AI agent — on this repository. It is loaded at the start of every session and takes precedence over habits, defaults and memory of earlier conversations. Details live in the [engineering handbook](docs/engineering/README.md); this file holds the rules and tells you where to look.

---

## 1. What this project is

**Plux is a server-driven UI and plugin platform for Flutter.** Developers design apps, plugins, pages and actions in Plux Studio; the Plux Server validates, compiles and signs them into FlatBuffers bundles with binary deltas; the `plux_flutter` runtime syncs every plugin at app start, verifies it, and renders it as native widgets. Logic that declarative actions cannot express runs as Plux Functions — Go compiled to WebAssembly, on the server or on the device. Every device request is bound to a hardware key with DPoP and backed by platform attestation.

The project is judged on two things: whether its claims are **provably true** — every requirement verified by a test, every performance number by a benchmark — and whether an experienced engineer can **read it end to end and understand why it is built the way it is**. When goals conflict, resolve them in this order (spec §1.4):

1. **Security and correctness** — never traded away.
2. **Performance** — on the device first, then the server, then Studio.
3. **Developer experience** — for the developers building with Plux.
4. **Operability** — observability, upgrades, recovery.
5. **Feature breadth.**

| Component | Path | Technology | Licence |
|---|---|---|---|
| Plux Server (`plux-server`), CLI (`plux`), compiler | `backend/` | Go, ConnectRPC, PostgreSQL | Server: AGPL-3.0-only; CLI, compiler, SDK: Apache-2.0 |
| Repository tooling (`reqtrace`, `covgate`, policy checks) | `tools/` | Go, standard library only | Apache-2.0 |
| Flutter runtime and optional packages | `packages/` | Dart, Flutter, Riverpod | Apache-2.0 |
| Host apps (Dev app, reference apps) | `apps/` | Flutter | Apache-2.0 |
| Plux Studio | `studio/` | Bun, TypeScript, React | AGPL-3.0-only |
| Contracts: JSON Schema, widget descriptors, FlatBuffers, conformance vectors | `schema/`, `proto/` | — | Apache-2.0 |
| Enterprise edition (from P9) | `ee/` | — | Commercial |

Licensing per path is defined in [REUSE.toml](REUSE.toml) and [ADR-0022](docs/adr/0022-open-core-licensing.md). Product context, goals and non-goals: [product-context.md](docs/engineering/product-context.md).

---

## 2. Session protocol — surviving context loss

Context is finite. Rules, specification details and project state must always be **re-read from this repository**, never reconstructed from memory of an earlier conversation or a summary.

**At the start of every session, and after any context compaction or resumption:**

1. Re-read this file.
2. Read [docs/WORKLOG.md](docs/WORKLOG.md) for the current phase, focus, active branch and hand-off notes.
3. Run `git status` and `git branch --show-current`, and confirm they match the work log.
4. Before touching any area, read the documents listed for it in the routing table (§4), including the `AGENTS.md` of the component you are changing.

**During work:** if you are not certain you have read a rule or a spec section *in this session*, read it again. Guessing is not permitted.

**At the end of every task:**

1. Update [docs/WORKLOG.md](docs/WORKLOG.md): what changed, what was verified, what is next, and any open question.
2. Report to the maintainer using the format in §9.

---

## 3. Sources of truth

When sources disagree, the higher one wins. If the conflict is real rather than a stale document, **stop and ask** — never resolve it silently.

1. [docs/requirements.md](docs/requirements.md) — the specification (`SRS-PLUX-001`). Requirement IDs (e.g. `SYN-005`) are the vocabulary of this project.
2. [docs/adr/](docs/adr/README.md) — accepted Architecture Decision Records.
3. This file, the component `AGENTS.md` files, and the [engineering handbook](docs/engineering/README.md).
4. Existing code. Code that contradicts 1–3 is a bug, not a precedent.

Per-requirement implementation status lives **only** in the `Status` column of `docs/requirements.md` (spec §4.3). No other file restates it.

### Keeping documents and code in agreement

Documents and code must never drift apart. When an implementation cannot, or should not, follow a document exactly — the spec, an ADR, the handbook, a component `AGENTS.md`, or a doc comment — **stop and present the divergence to the maintainer before continuing**:

1. **The conflict** — the document, section or requirement ID, what it says, and what the implementation needs instead.
2. **Option A: update the document** — the exact wording change and why the implementation is right.
3. **Option B: change the implementation** — what following the document costs.
4. **Recommendation** — which option, and the trade-off.

The maintainer decides. The chosen document change lands in the **same pull request** as the code, and every other document that restates the same rule is updated with it, so no stale copy remains. A change to a `MUST` requirement also gets an ADR (spec, Document Control). `make spec-lint` must pass after every edit to the specification.

---

## 4. Routing table — read before you start

| If the task touches… | Read first |
|---|---|
| Anything at all | This file, `docs/WORKLOG.md`, the current phase in spec §5 |
| Go code (`backend/`, `tools/`) | `backend/AGENTS.md`, [go-standards.md](docs/engineering/go-standards.md) |
| Dart or Flutter code (`packages/`, `apps/`) | `packages/AGENTS.md`, [dart-standards.md](docs/engineering/dart-standards.md) |
| Studio (`studio/`) | `studio/AGENTS.md`, [typescript-standards.md](docs/engineering/typescript-standards.md), spec §21 |
| Contracts (`schema/`, `proto/`, bundle format) | `schema/README.md`, spec §7–§9, [system-invariants.md](docs/engineering/system-invariants.md) §2 |
| Errors and error codes | [error-handling.md](docs/engineering/error-handling.md), spec Appendix F |
| Security in any form | [security-practices.md](docs/engineering/security-practices.md), spec §15 |
| Tests of any kind | [testing.md](docs/engineering/testing.md), spec §28 |
| Adding or upgrading a dependency | [dependencies.md](docs/engineering/dependencies.md) |
| Git, branches, commits, PRs, releases | [workflow.md](docs/engineering/workflow.md) |
| CI, the Makefile, hooks, tool versions | [ci.md](docs/engineering/ci.md) |
| Sync, releases, deltas | spec §10, system-invariants §3 |
| Functions | spec §16, system-invariants §5 |
| Limits and quotas | spec §30.4 |
| Licensing, `ee/` | [ADR-0022](docs/adr/0022-open-core-licensing.md), `REUSE.toml` |
| A design choice not covered above | `docs/adr/`, then ask |

---

## 5. Hard rules

These are not guidelines. A change that breaks one is not mergeable.

### Scope

- **Stay inside the current phase** (spec §5, `docs/WORKLOG.md`). Nothing from a later phase is half-implemented; a later-phase field or feature is either absent or rejected explicitly.
- Every change implements identified requirement IDs, or is an explicitly agreed tooling, documentation or maintenance change.

### Architecture

- The layering rules L-1 to L-7 (spec §6.7) hold: handlers contain no business logic; only `signing/` touches private keys; `fnrunner` never receives database credentials or signing capabilities; the compiler is a pure library; nothing on the Flutter UI isolate does network I/O, decompression, patching or large hashing.
- **Determinism:** the compiler produces byte-identical output for the same input (`CMP-002`); release builds are reproducible (`CI-006`). No wall clock, randomness, map iteration order or environment data may reach an artifact.
- **Verify before load:** the runtime never parses anything beyond a container header before its signature and hashes are verified (`SEC-052`).
- **Additive evolution only** for the bundle format, manifest, document schema and API once their phase is tagged (`BND-000`, `SCH-000`, `SRV-000`).
- **No downloaded executable code** except PXL bytecode, action graphs and sandboxed WebAssembly run by an interpreter (`SEC-054`).
- **Deny by default:** capabilities, permissions, network domains and function access are granted explicitly (`SEC-080`, `SEC-102`, `FN-012`).
- **Limits come from the registry** (`LIM-001`); no size or count limit is hard-coded.

### Security

- Never log, trace, label, return or persist unencrypted: keys, tokens, DPoP proofs, secrets, or fields tagged `sensitive` (`SCH-012`, `SEC-092`).
- Secure defaults only. An unsafe setting is never a default and always produces a distinct warning.
- Security capabilities are never gated by edition (`GOV-032`).
- Full rules: [security-practices.md](docs/engineering/security-practices.md).

### Contracts and generated code

- Generated code is committed, never edited by hand, and regenerated only with `make gen` (`CI-003`).
- Every source file carries SPDX headers matching `REUSE.toml`; `make reuse-lint` passes.

### Tests

- Every `MUST` requirement set to `DONE` is verified by at least one automated test or CI check that names its ID (`QA-070`, `QA-071`); `make trace` enforces this.
- Tests are hermetic and deterministic, and Go tests pass with `-race`. A flaky test is quarantined with an issue link, never retried into passing.
- Never delete, skip or weaken a test to make a change pass. Coverage floors in `coverage.json` are never lowered to pass a change (`QA-001`).

### Dependencies

- Only dependencies on the allowlist in [dependencies.md](docs/engineering/dependencies.md) may be used. Anything else requires an accepted ADR first.

---

## 6. Workflow

- **The maintainer commits, pushes, merges and tags.** Agents prepare changes, run the checks, and draft commit messages and PR descriptions. An agent commits or pushes only when the maintainer explicitly asks in the current session.
- One branch per requirement slice, named `<type>/<short-kebab-description>` (e.g. `feat/delta-patcher`). Never work on `main`; it is protected.
- Commits and PR titles follow Conventional Commits. PRs are squash-merged, so the PR title becomes the commit on `main`.
- Stay in scope: no speculative features, no drive-by refactors, no edits to unrelated files. Note out-of-scope findings in the work log instead.
- Every task ends with the relevant `make` gates passing (`make check` for the whole repository). Details: [workflow.md](docs/engineering/workflow.md).

---

## 7. Stop and ask the maintainer before

- Changing anything in `schema/`, `proto/` or the bundle and manifest formats once their phase is tagged.
- Changing signing, key handling, DPoP, attestation, or any security default, control or test.
- Adding, removing or upgrading a dependency beyond a patch release.
- Changing a coverage floor, a performance budget, a limit default, or a CI gate.
- Changing the licence of any path, or moving code into or out of `ee/`.
- Deviating from, or reinterpreting, a requirement or any other document — including when the document looks wrong. Present it as described in §3.
- Resolving an ambiguity in the spec or a conflict between sources of truth.
- Deleting tests, data or history, or rewriting published commits.

When you stop, state the question, the options, and your recommendation with its trade-off.

---

## 8. Never

- Claim that something builds, passes or works without having run it in this session.
- Invent a library API. Verify signatures against the source or documentation of the pinned version.
- Leave `TODO`, `FIXME`, placeholders or commented-out code. A deferred item is tracked as an issue and referenced as `TODO(#<issue>): …`.
- Introduce global mutable state, import-time side effects, or panics/exceptions across package boundaries.
- Skip git hooks (`--no-verify`) or bypass CI.
- Commit secrets, credentials, certificates, keystores or `.env` files.

---

## 9. Reporting format

End every task with:

1. **Changed** — files and a one-line summary of each.
2. **Requirements** — IDs implemented or affected, with their new `Status`.
3. **Verified** — the exact commands run and their result. Anything not run is listed as not verified.
4. **Open** — questions, risks and follow-ups.
5. **Suggested commit message** — Conventional Commits format.

---

## 10. Environment notes

- `make help` lists every task. The Makefile is the single entry point; CI runs the same targets.
- `make setup` installs the pinned tools (built with the project's Go toolchain) and the git hooks. Flutter and Bun are installed separately at the versions pinned in the Makefile.
- Toolchain versions: Go from the `toolchain` line in `backend/go.mod`; Flutter and Bun in the Makefile and `.github/workflows/ci.yml`; changed together, in one PR ([ci.md](docs/engineering/ci.md)).
- The repository uses LF line endings only (`.gitattributes`). On Windows, prefer an editor over `sed -i`, which can write CRLF.
- The race detector needs cgo; if it is unavailable locally, `make go-test` runs without it and CI runs `make go-cover` with `-race`.

---

## 11. Repository map

| Path | Contents |
|---|---|
| `backend/` | Go module: `plux-server`, the `plux` CLI, compiler and server packages |
| `tools/` | Go module: `reqtrace` (spec lint, traceability), `covgate` (coverage floors), policy checks |
| `packages/` | Dart packages; `plux_flutter` is the runtime |
| `apps/` | Flutter host apps (from P3) |
| `studio/` | Bun workspace for Plux Studio |
| `schema/`, `proto/` | Contracts and conformance vectors (from P1, P2) |
| `deploy/`, `test/` | Deployment assets and cross-component suites |
| `ee/` | Enterprise edition (from P9) |
| `docs/requirements.md` | The specification |
| `docs/engineering/` | The engineering handbook |
| `docs/adr/` | Architecture Decision Records |
| `docs/WORKLOG.md` | Current focus and hand-off notes |
