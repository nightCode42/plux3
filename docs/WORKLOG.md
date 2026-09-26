# Work Log

Current focus, hand-off notes and open decisions. This file lets any contributor — or an AI agent starting a fresh session — pick up the work exactly where it stopped.

Per-requirement implementation status is **not** tracked here; it lives only in the `Status` column of [requirements.md](requirements.md) (spec §4.3).

---

## Current focus

| Item | Value |
|---|---|
| Phase | P0 — Foundations |
| Active branch | `claude/inspiring-turing-1n40om` |
| Active work | P0 foundations complete, awaiting the maintainer's review and merge |
| Requirement IDs | `CI-001`–`CI-003`, `CI-006`–`CI-009`, `QA-001`, `QA-070`–`QA-073`, `SEC-192`, `GOV-032`, `GOV-034` |

## Next up

In order. Each item is one branch and one pull request.

1. **Repository settings** (maintainer) — set **CI OK** as the only required status check of the `main` ruleset, replacing the three checks required so far; confirm Dependabot alerts, secret scanning with push protection, private vulnerability reporting and CodeQL default setup are on (docs/engineering/ci.md §6). Then `CI-009` can be set to `DONE`.
2. **Phase 0 exit** — once CI OK is green on `main`: close P0 in this log.
3. **P1: ADR-0002, ADR-0009, ADR-0010, ADR-0018** — bundle format, PXL, layered widgets, error model, written before their code.
4. **P1: schema** — JSON Schema 2020-12 for documents (`SCH-001`–`SCH-006`) and generated Go, Dart and TypeScript types.

## Open decisions

| Decision | Owner | Notes |
|---|---|---|
| Legal review of ADR-0022 and the CLA | Maintainer | Before accepting external contributions and before the first public release. |
| CLA signing automation | Maintainer | e.g. CLA Assistant with signatures stored in a separate branch; needed before external pull requests are merged (`GOV-034`). |

## Hand-off notes

Newest first. Keep the latest ten; move older notes to [worklog-archive.md](worklog-archive.md) unchanged.

- 2026-09-26 — **Context-loss hardening.** SessionStart hook (`scripts/session-start.sh`) installs pinned toolchains in web sessions; work-log rotation to `worklog-archive.md`; `tools/AGENTS.md`; handbook links the spec glossary; issue templates ask for requirement IDs, phase and acceptance criteria.
- 2026-09-26 — **P0 foundations built.**
  - Monorepo: Go modules `backend` (`plux`, `plux-server`, `internal/buildinfo`) and `tools` joined by `go.work`; Dart pub workspace with `plux_flutter`; Bun workspace `studio` with `@plux/brand`; placeholder directories with phase READMEs for `apps`, `schema`, `proto`, `deploy`, `test`, `ee`, `docs/*`.
  - Tooling in `tools/` (standard library only): `reqtrace lint` (spec consistency), `reqtrace report` (traceability, strict in CI), `covgate` (coverage floors from `coverage.json`), policy tests for `SECURITY.md` and Dependabot coverage. The linter found and fixed two spec defects: an area pattern mismatch (tool) and `RT-051`'s withdrawal wording (spec).
  - Makefile as the single entry point; tools are built with the project toolchain (a tool built with an older Go cannot analyse Go 1.27 code).
  - CI redesigned: path-filtered Go, Dart and Studio jobs; always-on hygiene, secrets, spec and traceability, workflow lint (actionlint, zizmor), REUSE, link check, SBOM, commit and PR-title check, dependency review; one **CI OK** gate. Scorecard and release workflows added. All actions pinned by SHA.
  - Licensing: REUSE 3.3 compliant; Apache-2.0 by default, AGPL-3.0-only for `backend/cmd/plux-server` and `studio/` (ADR-0022).
  - Documentation: `AGENTS.md`, `CLAUDE.md`, component `AGENTS.md` files, the engineering handbook, ADR-0001 and ADR-0022, CLA draft, README.
  - Verified with `make check` on a fresh clone: every gate passes except `go-vuln`, which could not reach `vuln.go.dev` from the build sandbox and runs in CI. Links were checked with a local script; lychee runs in CI. zizmor ran offline; its online audits run in CI. Coverage: Go 89.5%, Dart 100%, Studio 100%. Reproducible build verified.
  - The fresh-clone run caught two `.gitignore` patterns (`coverage/`, `coverage.*`) that silently excluded `tools/internal/coverage/` and `coverage.json` from the commit; both patterns now name output locations only.
- 2026-09-26 — Specification 1.1.0 and repository configuration merged (#1).
