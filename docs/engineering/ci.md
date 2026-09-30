# CI, Makefile and Hooks

How the pipeline is built and why. The requirements are spec §29 (`CI-001`–`CI-009`) and §28.

---

## 1. Principles

- **One definition of every check.** CI jobs, git hooks and developers all run the same Makefile targets. A green `make check` locally means a green pipeline.
- **Fast feedback, complete coverage.** Toolchain jobs run only when their files change on a pull request, and always on `main`, on schedule and on manual runs. Repository-wide checks always run.
- **One required check.** The job **CI OK** depends on every other job and fails if any of them failed or was cancelled; skipped jobs count as passed. The `main` ruleset requires only CI OK, so path filtering never blocks a merge and adding a job never requires a settings change.
- **A failing command fails its step.** Every workflow sets `defaults: run: shell: bash`, which GitHub runs with `-eo pipefail`; without it a gate piped into the job summary (`make go-cover | tee …`) passes whatever `make` returned. A policy test (`tools/internal/policy`, `CI-001`) fails if a workflow lacks the default or overrides it.
- **Least privilege.** Workflows start with `permissions: {}`; each job requests only what it needs. Checkouts never persist credentials. Actions are pinned to full commit SHAs. `actionlint` and `zizmor` check every workflow.

## 2. Workflows

| Workflow | Trigger | Purpose |
|---|---|---|
| [ci.yml](../../.github/workflows/ci.yml) | Pull requests, pushes to `main`, merge queue, daily, manual | Every quality gate |
| [fuzz.yml](../../.github/workflows/fuzz.yml) | Nightly, manual (time per target as input) | `make go-fuzz`: every Go fuzz target for 5 minutes; failing inputs are kept as an artifact (`QA-004`, `CMP-052`) |
| [scorecard.yml](../../.github/workflows/scorecard.yml) | Pushes to `main`, weekly, ruleset changes | OpenSSF Scorecard; results in code scanning |
| [load.yml](../../.github/workflows/load.yml) | Nightly, manual (rate as input) | Starts the Compose stack with the load overlay, seeds it and runs `test/load/manifest.js` with k6; results in the job summary (`NFR-020`; the reference numbers are in [p2-backend.md](../benchmarks/p2-backend.md)) |
| [release.yml](../../.github/workflows/release.yml) | Component tags `<component>/v*` | Verify the signed tag and publish the release (`CI-008`). For `backend/v*` also: `make release-binaries` (reproducible archives of `plux` and `plux-server` for Linux, macOS and Windows on amd64 and arm64, `SHA256SUMS`, a Homebrew formula and a Scoop manifest), a keyless cosign signature of `SHA256SUMS`, SLSA Build Level 3 provenance for the archives, and both images through `image.yml` with their own provenance (`DEP-001`, `CLI-001`, `CI-004`) |
| [image.yml](../../.github/workflows/image.yml) | Called by `release.yml` | Builds one target of `backend/Dockerfile` for linux/amd64 and linux/arm64, pushes it to GHCR, signs it with cosign and attests a CycloneDX SBOM |
| CodeQL | GitHub default setup | Static analysis of Go, TypeScript and Actions |

## 3. CI jobs

| Job | Runs | Make target | Requirements |
|---|---|---|---|
| Detect changes | always | — | `CI-002` |
| Hygiene | always | `hygiene` | — |
| Secret scan | always | `secrets` (full history) | — |
| Specification and traceability | always | `spec-lint`, `trace` | `QA-070`, `QA-073` |
| Workflow lint | always | `workflows-lint` | — |
| Licensing (REUSE) | always | `reuse-lint` | — |
| Documentation links | always | lychee, offline | — |
| SBOM | always | CycloneDX via Syft | `CI-001` |
| Commit messages | pull requests | `scripts/check-commit-msg.sh` on title and commits | `CI-009` |
| Dependency review | pull requests | vulnerabilities and licences of new dependencies | `CI-007` |
| Go lint | Go changes | `go-fmt-check go-lint go-tidy-check go-gen-check` (regenerates everything `make gen` writes and fails on any difference), `registry-lock-check` (no permanent ID of the base commit changed or removed) | `CI-001`, `CI-003`, `BND-011` |
| API contract | `proto/` changes | `proto-check`: `buf lint`, `buf format --diff --exit-code` and `buf breaking` against the last `backend/v*` tag | `CI-001`, `SRV-000`, `SRV-002` |
| Go test | Go changes | `go-cover` (race detector, coverage floors) against a real PostgreSQL service, with `PLUX_TEST_DATABASE_URL` set so the integration tests run rather than skip; `go-budgets` (compiler timing budgets; `ubuntu-latest` is the reference runner) | `QA-001`, `QA-005`, `CMP-050`, `SCH-042` |
| Go determinism | Go changes, on Linux, macOS and Windows | `go-determinism` (the conformance vectors and projects compile to the committed goldens byte for byte) | `CMP-002` |
| Go build | Go changes | `go-build go-reproducible` | `CI-006` |
| Go vulnerabilities | Go changes | `go-vuln` | `CI-001` |
| Dart and Flutter | Dart changes | `dart-lock-check dart-fmt-check dart-analyze dart-cover`, `widgets-api-check` (the Flutter snapshot matches the pinned SDK) | `CI-001`, `CI-003`, `QA-001`, `WGT-003` |
| Starter app end-to-end | Go or Dart changes | `compat`: the starter app's flows under `flutter test` against a server built from source, and the compatibility matrix against released runtimes and servers | `QA-006`, `QA-010` |
| Runtime benchmark | Dart changes, on `ubuntu-24.04` | `bench-runtime-ab`: the runtime benchmark ([test/bench/runtime](../../test/bench/runtime/README.md)) in profile mode on Linux desktop under `xvfb`, this commit's runtime and the base's alternately, ten runs each; fails when a metric is more than 10% slower at 99% confidence. The base is the pull request's base, the merge queue's, the previous head of `main`, or the merge base with `main` on manual runs; a base that predates the benchmark is not compared with, and the report says so. Results and logs are an artifact | `QA-007` |
| Sync benchmark | Go or Dart changes | `bench-sync`: the runtime syncs the fifty-plugin benchmark app through the simulated slow network (spec §30.1) against a server built from source; fails when an update of three plugins takes over 3 s at p95 or a phase costs more than 10% over the bytes in `test/bench/runtime/sync-baseline.json` | `QA-007`, `NFR-007` |
| Size (Android), Size (iOS) | Dart changes; on `ubuntu-latest` and `macos-latest` | `size-android`, `size-ios`: the blank app of [test/size](../../test/size/README.md) built for release on arm64 with and without `plux_flutter`; fails when the runtime adds more than 3 MiB, or more than 10% over `test/size/baseline.json` | `RT-061`, `NFR-009`, `QA-007` |
| Studio | Studio changes | `studio-check` (frozen install, Biome, types, coverage) | `CI-001`, `QA-001` |
| Compose stack | Go or `deploy/` changes | `compose-up` (builds the server image and starts the whole stack), waits for `/readyz`, then `compose-test`: the Go integration and end-to-end tests against the stack's PostgreSQL, SeaweedFS and Valkey; then `image-check` on amd64 (as the arm64 job) | `DEP-002`, `QA-005` |
| Server image (arm64) | Go changes, on `ubuntu-24.04-arm` | `image-check`: builds the server image natively, so the arm64 half of the release image (Skia path operations and `plux-svgc`, built per platform) is built and tested before a release; the image's own `plux-svgc` must reproduce `packages/plux_svgc/test/icon.pathops.vec`, the golden made on amd64, byte for byte, and the image must carry its third-party notices under `/usr/share/doc` | `CMP-031`, `CMP-002` |
| Image codecs reproduce | codec changes (`backend/internal/compiler/media/codecs/**`), daily and manual runs | `wasm-codecs-check`: rebuilds `webp.wasm` and `avif.wasm` from their pinned sources with the pinned Ubuntu 24.04 toolchain and compares them with `codecs.lock`, so a committed binary cannot differ from its source; it takes minutes, so it does not run on every push | `CMP-030` |
| CI OK | always | — | `CI-009` |

The traceability report and coverage tables are written to each job's summary; the report and the SBOM are uploaded as artifacts.

## 4. Generated code

`make gen` is the only way generated code changes (`CI-003`): `buf` writes the Go messages, the ConnectRPC handlers and clients and the OpenAPI 3.1 description from `proto/plux/v1/` (`make proto`), `flatc` writes the Go and Dart accessors of the bundle sections from `schema/fbs/` and their binary schemas, `tools/cmd/schemagen` writes the Go, Dart and TypeScript code and reference documents derived from `schema/` (including the bundle verifier's layout tables, from those binary schemas), and `go generate` writes the rest (for example the error catalogue). Every generated file carries the marker `Code generated … DO NOT EDIT.` (flatc's Dart output: `automatically generated by the FlatBuffers compiler, do not modify`). `make gen` needs the pinned `flatc`; `make install-flatc` builds it from source at the commit of its release tag into `~/.cache/plux/` (it needs git, CMake and a C++ compiler: `sudo apt-get install -y git cmake g++` on Debian, Ubuntu and WSL, `brew install cmake` on macOS), and the Go lint job caches that build. The Go lint job regenerates everything and fails on any difference. Generated files are excluded from formatting checks (`dart format`, Biome) and coverage floors, and are checked instead by regeneration, compilation and the tests that use them.

One input of `make gen` needs Flutter and is therefore refreshed separately: `schema/widgets/flutter-api.json`, the snapshot of the pinned Flutter SDK that the widget coverage table is computed from. `make widgets-api` rewrites it (it refuses to run on any other Flutter version) and the Dart job's `widgets-api-check` fails when it is stale, so a Flutter upgrade runs `make widgets-api gen` in the same pull request (`WGT-003`, [ADR-0010](../adr/0010-layered-widget-model.md)).

## 5. Tool versions

| Tool | Where it is pinned |
|---|---|
| Go | `toolchain` line in `backend/go.mod` and `tools/go.mod`; `go.work` |
| Flutter, Bun, golangci-lint, govulncheck, gitleaks, actionlint, pre-commit, zizmor, reuse | Makefile header; `env:` of `ci.yml` (Flutter, Bun, pre-commit, zizmor, reuse) |
| buf, protoc-gen-go, protoc-gen-connect-go, protoc-gen-connect-openapi | Makefile header; installed by `make install-buf` and built with the project toolchain ([ADR-0005](../adr/0005-connectrpc-and-protobuf.md)) |
| flatc | Makefile header (`FLATC_VERSION` and the tag's commit `FLATC_COMMIT`, checked before building); must match the Go `github.com/google/flatbuffers` and Dart `flat_buffers` versions ([ADR-0002](../adr/0002-flatbuffers-sectioned-bundles.md)) |
| git-cliff | `release.yml` |
| SLSA generators | Tag `v2.1.0` in `release.yml`: the generators verify their own ref and refuse a commit SHA, so this is the one exception to SHA pinning |
| Container images | Tag and digest in `backend/Dockerfile` and `deploy/compose/compose.yaml` |
| GitHub Actions | Full commit SHA with the tag in a comment; updated by Dependabot |

Versions in the Makefile and in workflows are changed **together, in one pull request**. Go-based tools are built with the project toolchain (`make install-*`), because a tool built with an older Go cannot analyse code that needs a newer one.

## 6. Adding a component

1. Add its gates as Makefile targets and include them in `check`.
2. Add a path filter and a job in `ci.yml`, and add the job to the `needs` of CI OK.
3. Add its manifest directory to `.github/dependabot.yml` — the policy test fails otherwise (`CI-007`).
4. Declare its licence in `REUSE.toml`.
5. Add its coverage floors to `coverage.json` if they differ from the defaults.

## 7. Repository settings

These live in GitHub settings, not in the repository, and are recorded here so they can be audited:

- Ruleset on `main`: pull request required; required status check **CI OK**; linear history; block force pushes and deletions; Code Owners review; squash merge only (`CI-009`).
- Dependabot alerts and security updates, secret scanning with push protection, private vulnerability reporting, CodeQL default setup.
- Actions: read-only default token; Actions cannot approve pull requests; approval required for first-time contributors' workflows.
