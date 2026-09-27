# Work Log Archive

Hand-off notes moved out of [WORKLOG.md](WORKLOG.md), newest first. Notes are moved unchanged, never edited. Current state lives in the work log, not here.

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
