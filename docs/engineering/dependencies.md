# Dependencies

Every third-party dependency is a long-term commitment: code we ship but did not write, which must be kept secure, licensed compatibly and up to date. Plux depends on a deliberately small set of widely used, actively maintained components, each chosen for a stated reason.

---

## 1. Policy

1. **Standard library first.** A dependency is added only when the standard library cannot do the job reasonably.
2. **Allowlist only.** Code uses only the dependencies listed in §2. Adding one requires an accepted ADR stating the need, the alternatives, and the maintenance, security and licence posture (`CI-007`).
3. **Compatible licences only.** Permissive licences (Apache-2.0, MIT, BSD, ISC, MPL-2.0 and similar) are accepted; copyleft licences are not, except where an ADR accepts them for a component that is itself copyleft. CI's dependency review enforces the list. Packages whose registry reports a licence the review cannot validate as SPDX — pub.dev reports licences in lower case, some packages report none, and Go's `golang.org/x` modules add the Go patent grant to BSD-3-Clause — are checked against their `LICENSE` file by hand and listed, with that licence, in `allow-dependencies-licenses` in `ci.yml`.
4. **Major versions are decisions.** Upgrading a major version or replacing a dependency also requires an ADR. Minor and patch updates arrive through Dependabot, after a cooldown, and are merged when CI passes.
5. **Versions live in manifests and lockfiles** (`go.mod`, `pubspec.lock`, `bun.lock`), all committed. Tool versions are pinned in the Makefile and CI, and changed together. A published Dart package declares caret ranges, so its users can take compatible releases and pub awards its points (`RT-001`); the lockfile still fixes what this repository builds and tests. The one exception is `flat_buffers`, pinned to the `flatc` version that generates its accessors ([0002](../adr/0002-flatbuffers-sectioned-bundles.md)); the policy check enforces both.
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
| `github.com/getkin/kin-openapi` | `plux import openapi` and `plux mock` (`DAT-002`, `TST-004`); CLI only | MIT | [0052](../adr/0052-plux-test-and-import-tools.md) | Approved (maintainer, 2026-10-04, P5 plan B2), not yet in use; checked in its ADR before use (P5 R9) |
| `github.com/vektah/gqlparser/v2` | `plux import graphql` (`DAT-002`); CLI only | MIT | [0052](../adr/0052-plux-test-and-import-tools.md) | Approved (maintainer, 2026-10-04, P5 plan B2), not yet in use; checked in its ADR before use (P5 R9) |
| `github.com/goccy/go-yaml` | Plux Test scenarios in YAML, with positions for diagnostics (`TST-001`); CLI only | MIT | [0052](../adr/0052-plux-test-and-import-tools.md) | Approved (maintainer, 2026-10-04, P5 plan B2), not yet in use; checked in its ADR before use (P5 R9) |
| `github.com/go-jose/go-jose/v4` | Access tokens and DPoP proofs (JWS), Play Integrity tokens (JWE) (`SEC-003`, `SEC-020`–`SEC-022`) | Apache-2.0 | [0012](../adr/0012-dpop-hardware-keys-and-attestation.md) | Approved (maintainer, 2026-10-07, P6 S0); v4.1.5 |
| `github.com/fxamacker/cbor/v2` | App Attest attestation objects (`SEC-004`) | MIT | [0012](../adr/0012-dpop-hardware-keys-and-attestation.md) | Not used: S2 chose the in-house decoder in `internal/cbor` (ADR-0012, Revision) |
| `github.com/miekg/pkcs11` | The PKCS#11 helper binary only, never `plux-server` (`SEC-120`) | BSD-3-Clause | [0060](../adr/0060-signing-backends-and-audit-checkpoints.md) | Approved (maintainer, 2026-10-07, P6 S0), not yet in use |

`tools/` stays standard-library only (§3). Build tools pinned in the Makefile: `sqlc` v1.31.1 generates the query code from the migrations ([0007](../adr/0007-postgresql-and-object-storage.md)). The image codecs are C libraries compiled to WebAssembly by `backend/internal/compiler/media/codecs/build.sh` from pinned commits with clang 18 and wasi-libc; `codecs.lock` pins the modules' hashes, and their notices travel with them in `THIRD_PARTY_NOTICES.txt` ([0027](../adr/0027-asset-pipeline.md)). WebAuthn verification and the CBOR and COSE decoding it needs are written in-house on the standard library (the CBOR decoder lives in `internal/cbor` and App Attest uses it too), so `go-webauthn/webauthn` is not a dependency ([0026](../adr/0026-identity-tenancy-and-access.md), Revision). HashiCorp Vault Transit is reached over its HTTP API with the standard library, so no Vault client is a dependency ([0004](../adr/0004-tuf-style-update-security.md)).

Valkey is reached with a small RESP client in `backend/internal/cache`, so no Redis client is a dependency; section deltas use the zstd raw-dictionary support of `klauspost/compress`, so no patching library is one either ([ADR-0003](../adr/0003-section-level-deltas.md)).

### Dart (`packages/`, `apps/`)

| Package | Used for | Licence | Status |
|---|---|---|---|
| `flutter` SDK | Framework | BSD-3-Clause | In use |
| `flutter_test` (SDK) | Tests | BSD-3-Clause | In use (dev) |
| `flutter_lints` | Lint rule set | BSD-3-Clause | In use (dev) |
| `test` | Tests of the pure-Dart `plux_widget_api` tool | BSD-3-Clause | In use (dev) |
| `flat_buffers` | Bundle section accessors in `plux_flutter` ([ADR-0002](../adr/0002-flatbuffers-sectioned-bundles.md)) | Apache-2.0 | In use |
| `analyzer` | Flutter constructor extraction in the development-only `plux_widget_api` tool ([ADR-0010](../adr/0010-layered-widget-model.md)), and the host scanner `plux_native_scan` behind `plux native scan` (`CLI-006`, [ADR-0041](../adr/0041-native-catalogue-and-host-builds.md); maintainer, 2026-10-01); never a dependency of a shipped package | BSD-3-Clause | In use (`plux_widget_api`; `plux_native_scan`, P4 R6) |
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
| `go_router` (publisher flutter.dev; 18.0.2 when approved) | The `plux_go_router` adapter only: Plux pages as `GoRoute`s, shells as `StatefulShellRoute`, discovery of the host's named routes (`NAV-006`, `HST-031`, [ADR-0040](../adr/0040-navigation-delegate-and-router-adapters.md)); never a dependency of `plux_flutter` | BSD-3-Clause | In use (`plux_go_router`, P4 R5; approved by plan D3) |
| `auto_route` (publisher codeness.ly; 11.2.0 when approved) | The `plux_auto_route` adapter only: the delegate on `StackRouter` and discovery from the router's routes, which the host's generated code declares (`NAV-006`, [ADR-0040](../adr/0040-navigation-delegate-and-router-adapters.md)); never a dependency of `plux_flutter` | MIT | In use (`plux_auto_route`, P4 R5; approved by plan D3) |
| `url_launcher` (publisher flutter.dev) | `openUrl` in `plux_flutter`, the one core exception to the package placement rule ([ADR-0051](../adr/0051-device-actions-packages-and-capabilities.md)) | BSD-3-Clause | Approved (maintainer, 2026-10-04, P5 plan B2), not yet in use; checked in its ADR before use (P5 R8) |
| `share_plus`, `permission_handler` | `share` and `requestPermission`, only if R8's size measurement chooses them over the runtime's own platform code ([ADR-0051](../adr/0051-device-actions-packages-and-capabilities.md)) | BSD-3-Clause; MIT | Approved (maintainer, 2026-10-04, P5 plan B2), not yet in use; checked in its ADR before use (P5 R8, if chosen) |
| `image_picker` (publisher flutter.dev), `file_picker` | `pickImage`, `capturePhoto`, `pickFile` in `plux_media` only ([ADR-0051](../adr/0051-device-actions-packages-and-capabilities.md)) | BSD-3-Clause (the Android implementation's `LICENSE` is checked for code under another licence); MIT | Approved (maintainer, 2026-10-04, P5 plan B2), not yet in use; checked in its ADR before use (P5 R8) |
| `mobile_scanner` | `scanCode` in `plux_scanner` only ([ADR-0051](../adr/0051-device-actions-packages-and-capabilities.md)) | BSD-3-Clause; on Android it uses Google's ML Kit, under Google's ML Kit terms, which the maintainer accepts or refuses before use | Approved (maintainer, 2026-10-04, P5 plan B2), not yet in use; checked in its ADR before use (P5 R8) |
| `com.google.android.play:integrity` (Android library of `plux_flutter`) | Play Integrity standard requests (`SEC-003`) | Play terms (not open source; accepted by the maintainer, B18) | Approved (maintainer, 2026-10-07, P6 S0), not yet in use; [ADR-0012](../adr/0012-dpop-hardware-keys-and-attestation.md) |
| `androidx.biometric` (Android library of `plux_flutter`) | SCA key unlock, `biometricAuth`, inactivity lock (`SEC-027`, `SEC-094`) | Apache-2.0 | Approved (maintainer, 2026-10-07, P6 S0), not yet in use; [ADR-0056](../adr/0056-secure-gateway-and-sca.md) |
| `com.google.android.gms:play-services-mlkit-barcode-scanning` (Android library of `plux_scanner`) | Unbundled ML Kit barcode model (B21) | Play terms | Approved (maintainer, 2026-10-07, P6 S0), not yet in use; [ADR-0051](../adr/0051-device-actions-packages-and-capabilities.md) Revision |
| `geolocator` | `getLocation` in `plux_location` only ([ADR-0051](../adr/0051-device-actions-packages-and-capabilities.md)) | MIT | Approved (maintainer, 2026-10-04, P5 plan B2), not yet in use; checked in its ADR before use (P5 R8) |
| `drift`, `sqlite3` (publisher simonbinder.eu) | Collections in `plux_db_drift` only, through Drift's runtime API with no code generation ([ADR-0049](../adr/0049-local-persistence.md)) | MIT | Approved (maintainer, 2026-10-04, P5 plan B2), not yet in use; checked in its ADR before use (P5 R6) |
| `sqlcipher_flutter_libs`, or the SQLCipher build option of the pinned `sqlite3` | SQLCipher for `plux_db_drift` (`DB-002`, [ADR-0049](../adr/0049-local-persistence.md)) | MIT; SQLCipher Community Edition under a BSD-style licence | Approved (maintainer, 2026-10-04, P5 plan B2), not yet in use; checked in its ADR before use (P5 R6) |
| `lottie` | `LottieView` in `plux_lottie` only ([ADR-0050](../adr/0050-animation-engine.md)) | MIT | Approved (maintainer, 2026-10-04, P5 plan B2), not yet in use; checked in its ADR before use (P5 R7) |
| `rive` (Rive's official runtime) | `RiveView` in `plux_rive` only; how its native library is supplied is checked first ([ADR-0050](../adr/0050-animation-engine.md)) | MIT | Approved (maintainer, 2026-10-04, P5 plan B2), not yet in use; checked in its ADR before use (P5 R7) |

Transitive packages these bring, all published by the Dart and Flutter teams or the Riverpod author and all BSD-3-Clause or MIT: `state_notifier`, `listen`, `uuid`, `fixnum` (Riverpod); `http_parser`, `http_profile`, `web`, `web_socket` (HTTP); `jni`, `jni_flutter`, `jni_util`, `package_config`, `plugin_platform_interface` (Cronet); `objective_c`, `ffi` (`URLSession`, `cryptography`); `vector_graphics_codec`; and, at build time only, `logging`, `pub_semver`, `record_use`, `yaml`, `glob`, `file`. `flutter_riverpod` declares `flutter_test` as a dependency; nothing in `plux_flutter/lib` imports it, so release builds do not contain it.

Each router adapter declares its router with a caret range bounded below the next major release ([ADR-0040](../adr/0040-navigation-delegate-and-router-adapters.md)); the lockfile fixes the version (`go_router` 18.0.2, `auto_route` 11.2.0 at R5). Their transitive packages — `collection`, `intl`, `logging`, `meta`, `path` and `web` (publisher dart.dev), `material_ui` and `cupertino_ui` (flutter.dev), and the SDK's `flutter_web_plugins` and `flutter_localizations` — are published by the Dart and Flutter teams; `intl` and `flutter_localizations` come through `material_ui` and `cupertino_ui`. `auto_route_generator` and `build_runner` are not dependencies: the adapter reads the host's generated routes, and its tests declare routes by hand.

The P5 rows marked approved were accepted by the maintainer with the P5 plan (B2) subject to their ADRs: each ADR records the version, maintenance, size and transitive packages, with their licences, when its milestone adds the package, and a licence CI's dependency review cannot validate is put to the maintainer before `ci.yml` changes. Except `url_launcher` (and `share_plus` and `permission_handler`, if R8 chooses them), each is a dependency of its optional package only, never of `plux_flutter` (`RT-060`).

`vector_graphics_compiler` brings, into `plux-svgc` only: `args` and `path_parsing` (Dart and Flutter teams, BSD-3-Clause), and `xml` with `petitparser` (Lukas Renggli, MIT), which it pins to audited versions; the maintainer approved these transitive dependencies on 2026-09-29. `plux_svgc` is outside the pub workspace with its own `pubspec.lock`, because the server image builds it with the Dart SDK alone.

### Native code built from source

| Source | Used for | Licence | Record |
|---|---|---|---|
| zstd v1.5.7 (`lib/common`, `lib/decompress`), vendored in `packages/plux_flutter/native/zstd/` | Delta patching and transport decompression on the device | BSD-3-Clause | [ADR-0030](../adr/0030-native-code-in-plux-flutter.md) |
| Skia path operations at the revision the pinned Flutter uses, with the Flutter engine's `path_ops` wrapper, built for `plux-svgc` | Mask, clip and overdraw optimisation of SVGs at publish time | BSD-3-Clause | [ADR-0027](../adr/0027-asset-pipeline.md) Revision |
| ZXing-C++ (version fixed in S10), vendored in `plux_scanner` | Offline barcode fallback where Play services is missing (B21) | Apache-2.0 | [ADR-0051](../adr/0051-device-actions-packages-and-capabilities.md) Revision; approved (maintainer, 2026-10-07, P6 S0) |
| Platform AES-GCM (CryptoKit, Conscrypt), or a vendored audited AES-GCM if measurement requires it | Encrypted stores and confidential bundles (`SEC-053`, `SEC-073`) | system / to be recorded | [ADR-0058](../adr/0058-encryption-at-rest-native-aes-gcm.md); approved (P6 S0), chosen by measurement in S7 |

### Vendored data

| Source | Used for | Licence | Record |
|---|---|---|---|
| libphonenumber's phone number metadata, a pinned release named by version and SHA-256 | Per-region validation tables for `pxl.phone.v1`, generated for Go and Dart and committed as generated code; the metadata file itself is not committed (approved by the maintainer, 2026-10-04, P5 plan B2; not yet in use) | Apache-2.0 | [ADR-0047](../adr/0047-forms-validators-regex-and-phone.md) |

### Add-to-app hosts (`apps/add_to_app/`)

The native Android and iOS hosts embed the Plux module with the Flutter SDK's own add-to-app tooling (`HST-033`, plan p4 §5.10). The Android host's `:flutter` project brings the Flutter embedding and the AndroidX libraries it declares, as any Flutter app has them; among them `androidx.fragment`, whose `FragmentActivity` holds the `FlutterFragment`. Beyond those, the hosts use only their UI tests' libraries (maintainer, 2026-10-02, plan p4 A33):

| Dependency | Used for | Licence | Status |
|---|---|---|---|
| `androidx.test:runner` 1.6.2, `androidx.test.ext:junit` 1.2.1 | The Kotlin host's instrumentation tests: `AndroidJUnitRunner` and the JUnit 4 runner class; `androidTest` only, never in the app | Apache-2.0 | In use (P4 R10, test) |
| `androidx.test.uiautomator:uiautomator` 2.3.0 | The Kotlin host's UI tests, which find Plux pages and native views by their accessibility labels; `androidTest` only | Apache-2.0 | In use (P4 R10, test) |
| `junit:junit` 4.13.2, with `org.hamcrest:hamcrest-core` 1.3 | Brought by the two above, whose test API JUnit 4 is; `androidTest` only, never distributed | EPL-1.0; BSD-3-Clause | In use (P4 R10, test). EPL-1.0, a weak copyleft licence, is accepted for `androidTest` only by [ADR-0044](../adr/0044-junit-4-for-the-android-host-tests.md) |

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
| SoftHSM2 | CI image (P6, S5) | PKCS#11 tests of the signing helper (BSD-2-Clause; ADR-0060; approved 2026-10-07, not yet in use) |
| Bun | `studio/package.json`, Makefile, CI | Studio runtime and tests |
| golangci-lint, govulncheck, gitleaks, actionlint | Makefile (built with the project toolchain), CI | Lint, vulnerabilities, secrets, workflows |
| pre-commit, zizmor, reuse, git-cliff | Makefile, CI | Hooks, workflow security, licensing, release notes |
| `buf`, `protoc-gen-go`, `protoc-gen-connect-go`, `protoc-gen-connect-openapi` | Makefile (`BUF_VERSION` and the plugin versions), built with the project toolchain | API contract lint, breaking-change detection and code generation ([ADR-0005](../adr/0005-connectrpc-and-protobuf.md)) |
| `flatc` (FlatBuffers compiler) | Makefile (`FLATC_VERSION`, tag commit), built from source; cached in CI | Bundle code generation ([ADR-0002](../adr/0002-flatbuffers-sectioned-bundles.md)) |
| GitHub Actions | Full commit SHAs in `.github/workflows/` | CI |
| cosign (sigstore/cosign-installer), Syft (anchore/sbom-action), Docker Buildx and QEMU | Action SHAs in `release.yml` and `image.yml` | Keyless signatures, CycloneDX SBOMs and multi-arch images (`DEP-001`, `CI-004`) |
| SLSA GitHub generators (`generator_generic_slsa3`, `generator_container_slsa3`) v2.1.0 | Tag in `release.yml` — the generators must be referenced by tag to be verifiable | SLSA level 3 provenance (`CI-004`) |
| k6 (grafana/setup-k6-action) | Action SHA in `load.yml` | The manifest load test (`NFR-020`) |
| CocoaPods | GitHub's macOS images; Homebrew where it is missing (`test/e2e/ios.sh`) | Installs the add-to-app module's pods into the Swift host, as Flutter's add-to-app guide describes (`HST-033`, plan p4 A33) |

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
