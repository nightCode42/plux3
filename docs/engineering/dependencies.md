# Dependencies

Every third-party dependency is a long-term commitment: code we ship but did not write, which must be kept secure, licensed compatibly and up to date. Plux depends on a deliberately small set of widely used, actively maintained components, each chosen for a stated reason.

---

## 1. Policy

1. **Standard library first.** A dependency is added only when the standard library cannot do the job reasonably.
2. **Allowlist only.** Code uses only the dependencies listed in §2. Adding one requires an accepted ADR stating the need, the alternatives, and the maintenance, security and licence posture (`CI-007`).
3. **Compatible licences only.** Permissive licences (Apache-2.0, MIT, BSD, ISC, MPL-2.0 and similar) are accepted; copyleft licences are not, except where an ADR accepts them for a component that is itself copyleft. CI's dependency review enforces the list. Packages whose registry reports a licence the review cannot validate as SPDX are checked by hand and listed with their licence in `allow-dependencies-licenses` in `ci.yml`.
4. **Major versions are decisions.** Upgrading a major version or replacing a dependency also requires an ADR. Minor and patch updates arrive through Dependabot, after a cooldown, and are merged when CI passes.
5. **Versions live in manifests and lockfiles** (`go.mod`, `pubspec.lock`, `bun.lock`), all committed. Tool versions are pinned in the Makefile and CI, and changed together.
6. **Every dependency is scanned**: govulncheck and dependency review on every pull request, Dependabot alerts continuously, an SBOM on every build.

## 2. Allowlist

### Go (`backend/`, `tools/`)

| Module | Used for | Licence | ADR | Status |
|---|---|---|---|---|
| Standard library | Everything in `tools/`; most of `backend/` | BSD-3-Clause | — | In use |
| `github.com/google/flatbuffers` | Bundle section builders and accessors | Apache-2.0 | [0002](../adr/0002-flatbuffers-sectioned-bundles.md) | In use (`backend`) |
| `github.com/klauspost/compress` | zstd transport compression of bundles | BSD-3-Clause, Apache-2.0 | [0002](../adr/0002-flatbuffers-sectioned-bundles.md) | In use (`backend`) |
| `github.com/santhosh-tekuri/jsonschema/v6` | Structural validation of documents (JSON Schema 2020-12) | Apache-2.0 | [0025](../adr/0025-document-schema-toolchain.md) | In use (`backend`) |
| `pgregory.net/rapid` | Property-based tests | MPL-2.0 | [0025](../adr/0025-document-schema-toolchain.md) | In use (`backend`, tests only) |

`tools/` stays standard-library only (§3). Planned for P2, each with its ADR: ConnectRPC and Protocol Buffers (ADR-0005), `pgx` and `sqlc` (ADR-0007), River job queue (ADR-0007).

### Dart (`packages/`, `apps/`)

| Package | Used for | Licence | Status |
|---|---|---|---|
| `flutter` SDK | Framework | BSD-3-Clause | In use |
| `flutter_test` (SDK) | Tests | BSD-3-Clause | In use (dev) |
| `flutter_lints` | Lint rule set | BSD-3-Clause | In use (dev) |
| `flat_buffers` | Bundle section accessors in `plux_flutter` ([ADR-0002](../adr/0002-flatbuffers-sectioned-bundles.md)) | Apache-2.0 | In use |
| `analyzer` | Flutter constructor extraction in the development-only `plux_widget_api` tool ([ADR-0010](../adr/0010-layered-widget-model.md)); never a dependency of a shipped package | BSD-3-Clause | In use (tool) |

Planned for P3: `flutter_riverpod` (ADR-0008).

### TypeScript (`studio/`)

| Package | Used for | Status |
|---|---|---|
| `@biomejs/biome` | Formatting and linting | In use (dev) |
| `typescript` | Type-checking | In use (dev) |
| `@types/bun` | Bun type definitions | In use (dev) |

Planned for P11 (ADR-0014): React, TanStack Router and Query, shadcn/ui on Radix, Tailwind CSS, Monaco, Connect-ES.

### Build and CI tools

| Tool | Pinned in | Used for |
|---|---|---|
| Go toolchain | `toolchain` in `go.mod` | Build |
| Flutter | Makefile, CI | Build and test |
| Bun | `studio/package.json`, Makefile, CI | Studio runtime and tests |
| golangci-lint, govulncheck, gitleaks, actionlint | Makefile (built with the project toolchain), CI | Lint, vulnerabilities, secrets, workflows |
| pre-commit, zizmor, reuse, git-cliff | Makefile, CI | Hooks, workflow security, licensing, release notes |
| `flatc` (FlatBuffers compiler) | Makefile (`FLATC_VERSION`, tag commit), built from source; cached in CI | Bundle code generation ([ADR-0002](../adr/0002-flatbuffers-sectioned-bundles.md)) |
| GitHub Actions | Full commit SHAs in `.github/workflows/` | CI |

## 3. Decisions made in P0

### Repository tooling: Go standard library only

**Chosen:** `tools/` uses only the standard library.
**Why:** The tooling runs in every CI job and must stay trivially auditable; its needs (Markdown and JSON parsing, file walking, regular expressions) are covered by the standard library.
**Rejected:** a YAML parser for Dependabot checks — the repository's Dependabot file uses a small, controlled subset that a line parser handles, and the policy test guards the format.

### Studio formatting and linting: Biome

**Chosen:** Biome for formatting and linting.
**Why:** One fast tool instead of ESLint plus Prettier plus plugins, with a single configuration file; `CI-001` names it as acceptable.
**Rejected:** ESLint with Prettier — more dependencies and configuration for the same result.
