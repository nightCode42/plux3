# Dependencies

Every third-party dependency is a long-term commitment: code we ship but did not write, which must be kept secure, licensed compatibly and up to date. Plux depends on a deliberately small set of widely used, actively maintained components, each chosen for a stated reason.

---

## 1. Policy

1. **Standard library first.** A dependency is added only when the standard library cannot do the job reasonably.
2. **Allowlist only.** Code uses only the dependencies listed in §2. Adding one requires an accepted ADR stating the need, the alternatives, and the maintenance, security and licence posture (`CI-007`).
3. **Compatible licences only.** Permissive licences (Apache-2.0, MIT, BSD, ISC, MPL-2.0 and similar) are accepted; copyleft licences are not, except where an ADR accepts them for a component that is itself copyleft. CI's dependency review enforces the list. Packages whose registry reports a licence the review cannot validate as SPDX — pub.dev reports licences in lower case, some packages report none, and Go's `golang.org/x` modules add the Go patent grant to BSD-3-Clause — are checked against their `LICENSE` file by hand and listed, with that licence, in `allow-dependencies-licenses` in `ci.yml`.
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
| `github.com/zalando/go-keyring` | The CLI's token in the OS credential store | MIT | [0028](../adr/0028-cli-credential-storage.md) | In use (`backend`, CLI only) |
| `github.com/godbus/dbus/v5` | Linux Secret Service, through go-keyring | BSD-2-Clause | [0028](../adr/0028-cli-credential-storage.md) | In use (`backend`, CLI only) |
| `github.com/danieljoos/wincred` | Windows Credential Manager, through go-keyring | MIT | [0028](../adr/0028-cli-credential-storage.md) | In use (`backend`, CLI only) |
| `github.com/santhosh-tekuri/jsonschema/v6` | Structural validation of documents (JSON Schema 2020-12) | Apache-2.0 | [0025](../adr/0025-document-schema-toolchain.md) | In use (`backend`) |
| `pgregory.net/rapid` | Property-based tests | MPL-2.0 | [0025](../adr/0025-document-schema-toolchain.md) | In use (`backend`, tests only) |
| `connectrpc.com/connect` | ConnectRPC handlers and clients for the API contract | Apache-2.0 | [0005](../adr/0005-connectrpc-and-protobuf.md) | In use (`backend`) |
| `google.golang.org/protobuf` | Generated API messages | BSD-3-Clause | [0005](../adr/0005-connectrpc-and-protobuf.md) | In use (`backend`) |
| `google.golang.org/genproto/googleapis/rpc` | `google.rpc.ErrorInfo` on API errors (`SRV-006`) | Apache-2.0 | [0005](../adr/0005-connectrpc-and-protobuf.md) | In use (`backend`) |
| `github.com/jackc/pgx/v5` | PostgreSQL driver and pool | MIT | [0007](../adr/0007-postgresql-and-object-storage.md) | In use (`backend`) |
| `github.com/riverqueue/river` | Durable jobs in PostgreSQL | MPL-2.0 | [0007](../adr/0007-postgresql-and-object-storage.md) | In use (`backend`) |
| `github.com/aws/aws-sdk-go-v2` (config, credentials, s3) | S3-compatible object storage | Apache-2.0 | [0007](../adr/0007-postgresql-and-object-storage.md) | In use (`backend`) |
| `go.opentelemetry.io/otel` (+ sdk, otlptracehttp) | Traces (`OBS-001`) | Apache-2.0 | [0006](../adr/0006-modular-monolith-with-roles.md) | In use (`backend`) |
| `github.com/prometheus/client_golang` | Metrics (`OBS-002`) | Apache-2.0 | [0006](../adr/0006-modular-monolith-with-roles.md) | In use (`backend`) |
| `sigs.k8s.io/yaml` | The server configuration file, decoded strictly as JSON (`SRV-008`) | Apache-2.0, BSD-3-Clause | [0007](../adr/0007-postgresql-and-object-storage.md) | In use (`backend`) |
| `golang.org/x/crypto` (argon2) | Argon2id password hashing (`SEC-100`) | BSD-3-Clause | [0026](../adr/0026-identity-tenancy-and-access.md) | In use (`backend`) |
| `github.com/tetratelabs/wazero` | Runs the WebAssembly image codecs, with no cgo (`CMP-030`) | Apache-2.0 | [0027](../adr/0027-asset-pipeline.md) | In use (`backend`) |
| libwebp v1.5.0, libavif v1.3.0, libaom v3.12.1 (C, built to WebAssembly) | WebP and AVIF encoding; the modules are committed and rebuilt by `make wasm-codecs` | BSD-3-Clause; BSD-2-Clause with the AOM Patent License 1.0 | [0027](../adr/0027-asset-pipeline.md) | In use (`backend`, embedded) |

`tools/` stays standard-library only (§3). Build tools pinned in the Makefile: `sqlc` v1.31.1 generates the query code from the migrations ([0007](../adr/0007-postgresql-and-object-storage.md)). The image codecs are C libraries compiled to WebAssembly by `backend/internal/compiler/media/codecs/build.sh` from pinned commits with clang 18 and wasi-libc; `codecs.lock` pins the modules' hashes, and their notices travel with them in `THIRD_PARTY_NOTICES.txt` ([0027](../adr/0027-asset-pipeline.md)). WebAuthn verification and the CBOR and COSE decoding it needs are written in-house on the standard library, so `go-webauthn/webauthn` is not a dependency ([0026](../adr/0026-identity-tenancy-and-access.md), Revision). HashiCorp Vault Transit is reached over its HTTP API with the standard library, so no Vault client is a dependency ([0004](../adr/0004-tuf-style-update-security.md)).

Valkey is reached with a small RESP client in `backend/internal/cache`, so no Redis client is a dependency; section deltas use the zstd raw-dictionary support of `klauspost/compress`, so no patching library is one either ([ADR-0003](../adr/0003-section-level-deltas.md)).

### Dart (`packages/`, `apps/`)

| Package | Used for | Licence | Status |
|---|---|---|---|
| `flutter` SDK | Framework | BSD-3-Clause | In use |
| `flutter_test` (SDK) | Tests | BSD-3-Clause | In use (dev) |
| `flutter_lints` | Lint rule set | BSD-3-Clause | In use (dev) |
| `test` | Tests of the pure-Dart `plux_widget_api` tool | BSD-3-Clause | In use (dev) |
| `flat_buffers` | Bundle section accessors in `plux_flutter` ([ADR-0002](../adr/0002-flatbuffers-sectioned-bundles.md)) | Apache-2.0 | In use |
| `analyzer` | Flutter constructor extraction in the development-only `plux_widget_api` tool ([ADR-0010](../adr/0010-layered-widget-model.md)); never a dependency of a shipped package | BSD-3-Clause | In use (tool) |
| `flutter_riverpod` 3.4.3 (with `riverpod`) | The runtime's state engine (`RT-003`, [ADR-0008](../adr/0008-riverpod-runtime-state-engine.md)); no code generation | MIT | In use (P3) |
| `cryptography` 2.9.0 | Ed25519 verification of manifests and baseline bundles, pure-Dart implementation only ([ADR-0029](../adr/0029-on-device-verification.md)) | Apache-2.0 | In use (P3) |
| `crypto` 3.0.7 | SHA-256 of bundles, sections and assets ([ADR-0029](../adr/0029-on-device-verification.md)) | BSD-3-Clause | In use (P3) |
| `http` 1.6.0 | HTTP client interface for sync and telemetry ([ADR-0021](../adr/0021-sync-all-plugins-at-start.md)) | BSD-3-Clause | In use (P3) |
| `cronet_http` 1.9.0 | HTTP/2 client on Android (Cronet), behind `http` (`SYN-010`) | BSD-3-Clause | In use (P3) |
| `cupertino_http` 3.1.0 | HTTP/2 client on iOS (`URLSession`), behind `http` (`SYN-010`) | BSD-3-Clause | In use (P3) |
| `vector_graphics` 1.2.3 | Renders SVG assets compiled at publish time (`CMP-031`, [ADR-0027](../adr/0027-asset-pipeline.md) Revision) | BSD-3-Clause | In use (P3) |
| `hooks` 2.2.0, `code_assets` 1.2.1, `native_toolchain_c` 0.19.3 | The build hook that compiles `plux_native` (mmap and zstd) for every target; build time only, never imported by `lib/` ([ADR-0030](../adr/0030-native-code-in-plux-flutter.md)) | BSD-3-Clause | In use (P3, build) |
| `vector_graphics_compiler` 1.3.0 | The SVG encoder inside the server-side `plux-svgc` helper; never a dependency of a shipped package ([ADR-0027](../adr/0027-asset-pipeline.md) Revision) | BSD-3-Clause | In use (P3, server tool) |
| `integration_test` (SDK) | End-to-end tests of the example host app on emulators and simulators (`QA-006`) | BSD-3-Clause | In use (P3, dev) |

Transitive packages these bring, all published by the Dart and Flutter teams or the Riverpod author and all BSD-3-Clause or MIT: `state_notifier`, `listen`, `uuid`, `fixnum` (Riverpod); `http_parser`, `http_profile`, `web`, `web_socket` (HTTP); `jni`, `jni_flutter`, `jni_util`, `package_config`, `plugin_platform_interface` (Cronet); `objective_c`, `ffi` (`URLSession`, `cryptography`); `vector_graphics_codec`; and, at build time only, `logging`, `pub_semver`, `record_use`, `yaml`, `glob`, `file`. `flutter_riverpod` declares `flutter_test` as a dependency; nothing in `plux_flutter/lib` imports it, so release builds do not contain it.

### Native code built from source

| Source | Used for | Licence | Record |
|---|---|---|---|
| zstd v1.5.7 (`lib/common`, `lib/decompress`), vendored in `packages/plux_flutter/native/zstd/` | Delta patching and transport decompression on the device | BSD-3-Clause | [ADR-0030](../adr/0030-native-code-in-plux-flutter.md) |
| Skia path operations at the revision the pinned Flutter uses, with the Flutter engine's `path_ops` wrapper, built for `plux-svgc` | Mask, clip and overdraw optimisation of SVGs at publish time | BSD-3-Clause | [ADR-0027](../adr/0027-asset-pipeline.md) Revision |

### Documentation site (`site/`)

| Package | Used for | Licence | Record |
|---|---|---|---|
| `astro` 7.3.5, `@astrojs/starlight` 0.42.4 | The documentation site (`DX-002`) | MIT | [ADR-0033](../adr/0033-documentation-site.md) |

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
| `buf`, `protoc-gen-go`, `protoc-gen-connect-go`, `protoc-gen-connect-openapi` | Makefile (`BUF_VERSION` and the plugin versions), built with the project toolchain | API contract lint, breaking-change detection and code generation ([ADR-0005](../adr/0005-connectrpc-and-protobuf.md)) |
| `flatc` (FlatBuffers compiler) | Makefile (`FLATC_VERSION`, tag commit), built from source; cached in CI | Bundle code generation ([ADR-0002](../adr/0002-flatbuffers-sectioned-bundles.md)) |
| GitHub Actions | Full commit SHAs in `.github/workflows/` | CI |
| cosign (sigstore/cosign-installer), Syft (anchore/sbom-action), Docker Buildx and QEMU | Action SHAs in `release.yml` and `image.yml` | Keyless signatures, CycloneDX SBOMs and multi-arch images (`DEP-001`, `CI-004`) |
| SLSA GitHub generators (`generator_generic_slsa3`, `generator_container_slsa3`) v2.1.0 | Tag in `release.yml` — the generators must be referenced by tag to be verifiable | SLSA level 3 provenance (`CI-004`) |
| k6 (grafana/setup-k6-action) | Action SHA in `load.yml` | The manifest load test (`NFR-020`) |

### Container images

Pinned by tag and digest in `backend/Dockerfile` and `deploy/compose/compose.yaml`; Dependabot (`docker`, `docker-compose`) proposes new tags and digests weekly, and the policy check fails if a Dockerfile or Compose file has no entry (`CI-007`).

| Image | Used for |
|---|---|
| `golang` (bookworm) | Builder stage of the server and CLI images |
| `gcr.io/distroless/static-debian12:nonroot` | Runtime base of the server and CLI images (`SEC-108`) |
| `postgres` 16 | Compose stack database |
| `chrislusf/seaweedfs` | Compose stack S3 store ([ADR-0007](../adr/0007-postgresql-and-object-storage.md), Revision — MinIO no longer publishes images) |
| `valkey/valkey` | Compose stack cache |
| `otel/opentelemetry-collector-contrib`, `prom/prometheus`, `grafana/grafana` | Compose stack telemetry |
| `keycloak/keycloak`, `ollama/ollama` | Optional Compose profiles |

## 3. Decisions made in P0

### Repository tooling: Go standard library only

**Chosen:** `tools/` uses only the standard library.
**Why:** The tooling runs in every CI job and must stay trivially auditable; its needs (Markdown and JSON parsing, file walking, regular expressions) are covered by the standard library.
**Rejected:** a YAML parser for Dependabot checks — the repository's Dependabot file uses a small, controlled subset that a line parser handles, and the policy test guards the format.

### Studio formatting and linting: Biome

**Chosen:** Biome for formatting and linting.
**Why:** One fast tool instead of ESLint plus Prettier plus plugins, with a single configuration file; `CI-001` names it as acceptable.
**Rejected:** ESLint with Prettier — more dependencies and configuration for the same result.
