# Work Log

Current focus, hand-off notes and open decisions. This file lets any contributor — or an AI agent starting a fresh session — pick up the work exactly where it stopped.

Per-requirement implementation status is **not** tracked here; it lives only in the `Status` column of [requirements.md](requirements.md) (spec §4.3).

---

## Current focus

| Item | Value |
|---|---|
| Phase | P1 — Schema and compiler |
| Active branch | `claude/inspiring-turing-1n40om` (local `feat/p1-schema-compiler`, started from `main` at #3) |
| Active work | P1 milestones M1–M9 below |
| Requirement IDs | All P1 requirements (spec §5.1, Appendix K.2: 72 `MUST`, 1 `SHOULD`) |

## Next up

P1 is delivered as nine milestones, each a commit set with `make check` green. Design decisions are in ADR-0002, ADR-0009, ADR-0010, ADR-0018 and ADR-0025.

1. **M1 — Decisions** ✓: ADRs above, dependency allowlist, spec 1.1.2 clarifications.
2. **M2 — Foundations** ✓: `plxerr` and the generated error catalogue; RFC 8785 canonicalisation (`schema/jcs`); UUIDv7 (`schema/uuid7`); the limits registry `schema/limits.json`; generated-code exclusion in `covgate`; `make gen` and a repository-wide `gen-check`.
3. **M3 — Document model** ✓: JSON Schemas under `schema/json/`, `tools/cmd/schemagen` (Go, Dart, TypeScript types), structural validation, project loader for the Git layout, migrations, template instantiation.
4. **M4 — Registries**: widget descriptors for the P3 set of Appendix C, value types and enums, permanent-ID lock, `packages/plux_widget_api` Flutter snapshot, generated coverage table; action descriptors (Appendix D).
5. **M5 — PXL in Go**: parser, checker, standard library, decimal and money, bytecode, evaluator, folding, read sets, conformance vectors, property and fuzz tests.
6. **M6 — PXL in Dart**: bytecode VM passing every conformance vector.
7. **M7 — Bundle format**: FlatBuffers IDL, `flatc` pin, generated Go and Dart code, container writer and reader, verifier, zstd transport.
8. **M8 — Compiler**: pipeline stages, reference graph, budgets, optimisation, lowering, encoding, source maps, golden and determinism tests, fuzzing, benchmarks.
9. **M9 — Delivery**: `plux validate` and `plux build` offline, CI determinism matrix and fuzz schedule, reference documentation, benchmarks, threat-model delta, statuses.

Carried over from P0 (maintainer): set **CI OK** as the only required status check of the `main` ruleset and confirm the repository security settings (docs/engineering/ci.md §7); then `CI-009` can be `DONE` and P0 closed.

### P1 scope decisions (maintainer, 2026-09-26)

- Requirements whose text also covers later phases keep their IDs; P1 implements its part and they stay `WIP` until the rest lands: `QA-002` (delta round-trip P2, activation crash P3), `QA-003` (DPoP vectors P6, TypeScript runner P11), `QA-004` (manifest P2, DPoP and attestation P6, Dart verifier P3), `PXL-006` (CLDR `format.*`, `t`, `plural`, calendars P8; `isPhone`, `format.phone`, `matches` P5), `PXL-007` (Studio language service P11), `SEC-054` (runtime and WASM clauses P3/P7), `SCH-012` (runtime redaction P3–P5), `BND-002` (manifest P2), `BND-008` and `BND-018` (Dart runtime P3), `LIM-001` (server, runtime and Studio readers P2–P11).
- Structural validation uses `santhosh-tekuri/jsonschema/v6`; descriptors cover the P3 set of Appendix C; the structural `Switch` is renamed `Match`; section-directory entries are 72 bytes.

## Open decisions

| Decision | Owner | Notes |
|---|---|---|
| Legal review of ADR-0022 and the CLA | Maintainer | Before accepting external contributions and before the first public release. |
| Limits registry values not stated in the spec | Maintainer | `schema/limits.json` takes every default the spec states; the hard maximums, and the keys `document.fileSize`, `document.jsonDepth`, `pxl.expressionLength`, `pxl.nestingDepth`, `pxl.stringLength`, `pxl.collectionSize`, `bundle.verifierDepth` and `bundle.verifierTables`, are proposals needed by P1 code. Review before P2 (`LIM-001`). |
| CLA signing automation | Maintainer | e.g. CLA Assistant with signatures stored in a separate branch; needed before external pull requests are merged (`GOV-034`). |

## Hand-off notes

Newest first. Keep the latest ten; move older notes to [worklog-archive.md](worklog-archive.md) unchanged.

- 2026-09-26 — **P1 M3: document model.** JSON Schemas for eleven document kinds in `schema/json/` (profile: closed objects, `x-` extensions, named enums, `x-plux-type` names, one single-or-list union, raw prop values); `schemagen` generates Go (`backend/internal/schema/model_gen.go`), Dart (`document.g.dart`), TypeScript (`document.gen.ts`), `docs/reference/document-schema.md` and the embedded schema copies. `backend/internal/schema`: validator on jsonschema/v6 mapping errors to `PLX-1xxx` with exact JSON Pointers (best branch of unions; works around a jsonschema v6.0.3 bug that corrupts `propertyNames` locations), strict loader for the Git layout, migrator with golden harness, template instantiation. Conformance project `schema/testdata/documents/loan-calculator` (Appendix A, corrected in spec 1.1.2). Dart round-trip test caught that optional raw values must keep explicit `null` (`JsonValue`). Verified: `go-cover` 93.8%, `dart-analyze`, `dart-cover`, `studio-check`, `spec-lint`, `trace`, `reuse-lint`, lint clean. Open for M5: Appendix E.2 needs an `event` root for handler payloads (used by the example).
- 2026-09-26 — **P1 M2: foundations.** `plxerr` (codes, diagnostics, errors; `docs/reference/errors.md` and `schema/errors.json` generated and pinned by a test); `schema/jcs` (RFC 8785, strict I-JSON parser; fuzzing found that integer literals must round-trip through canonicalisation, now enforced); `schema/uuid7`; the limits registry `schema/limits.json` with `tools/cmd/schemagen` generating Go, Dart, TypeScript and `docs/reference/limits.md`; `covgate` excludes generated files; `make gen` runs schemagen, `go-gen-check` covers every generated path; new Studio package `@plux/schema`. Verified: `make go-cover` (93.7%), `dart-analyze`, `dart-cover`, `studio-check`, `spec-lint`, `trace`, `workflows-lint`, `reuse-lint`; fuzz 45 s clean. Not run here: `go-vuln` (network).
- 2026-09-26 — **P1 M1: decisions.** ADR-0002 (bundle container and sections), ADR-0009 (PXL), ADR-0010 (layered widgets and descriptors), ADR-0018 (error model), ADR-0025 (schema toolchain) accepted; dependency allowlist extended; spec 1.1.2 clarifications. Verified: `make spec-lint`, link and REUSE checks.
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
