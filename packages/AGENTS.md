# packages — Agent Notes

Dart and Flutter packages, managed as one pub workspace from the repository root (`pubspec.yaml`, one `pubspec.lock`). `plux_flutter` is the runtime (spec §12); optional capabilities ship as separate packages (`RT-060`), each created in the phase that brings its first feature. `plux_devtools` is the debug overlay (`RT-060`); it uses only `plux_flutter`'s public API (`Plux.diagnostics`) and renders nothing in release builds. `plux_widget_api` is a development-only tool that snapshots the pinned Flutter SDK for the widget coverage table (`WGT-003`, ADR-0010); no shipped package may depend on it. Read [dart-standards.md](../docs/engineering/dart-standards.md) and the root [AGENTS.md](../AGENTS.md) before editing.

The runtime's design is recorded in ADRs; read the one for the area you change before editing it:

| Area | Package and path | Read first |
|---|---|---|
| State engine, providers | `plux_flutter/lib/src/state/` | [ADR-0008](../docs/adr/0008-riverpod-runtime-state-engine.md) |
| Sync, the release store | `plux_flutter/lib/src/sync/`, `store/`, `delta/` | [ADR-0021](../docs/adr/0021-sync-all-plugins-at-start.md), [ADR-0037](../docs/adr/0037-up-to-date-check-installed-digest.md) |
| Verification | `plux_flutter/lib/src/verify/`, `bundle/` | [ADR-0029](../docs/adr/0029-on-device-verification.md) |
| Native code (mmap, zstd) | `plux_flutter/native/`, `hook/`, `lib/src/mmap/`, `zstd/` | [ADR-0030](../docs/adr/0030-native-code-in-plux-flutter.md) |
| Rendering, node builders | `plux_flutter/lib/src/render/` | [ADR-0031](../docs/adr/0031-rendering-model.md) |
| Theming | `plux_flutter/lib/src/render/theme.dart` | [ADR-0032](../docs/adr/0032-design-tokens-to-material-and-cupertino.md) |
| Telemetry | `plux_flutter/lib/src/telemetry/` | [ADR-0034](../docs/adr/0034-runtime-telemetry.md) |
| Action engine (P4 R2) | `plux_flutter/lib/src/actions/` | [ADR-0039](../docs/adr/0039-action-engine-core.md) |
| Navigation, guards, deep links, push (P4 R3, R4) | `plux_flutter/lib/src/navigation/` | [ADR-0040](../docs/adr/0040-navigation-delegate-and-router-adapters.md) |
| Router adapters (P4 R5) | `plux_go_router`, `plux_auto_route` | [ADR-0040](../docs/adr/0040-navigation-delegate-and-router-adapters.md), [dependencies.md](../docs/engineering/dependencies.md) |
| Native routes, slots, custom actions (P4 R6) | `plux_flutter/lib/src/native_catalogue/`, `plux_native_scan` | [ADR-0041](../docs/adr/0041-native-catalogue-and-host-builds.md) |
| Mixed screens, `PluxView`, `Plux.events`, exposed state (P4 R7) | `plux_flutter/lib/src/core/` | [ADR-0023](../docs/adr/0023-mixed-screens-slots-and-plux-view.md) |
| Action engine completion, triggers, flows, traces (P5) | `plux_flutter/lib/src/actions/` | [ADR-0045](../docs/adr/0045-action-engine-completion.md) |
| State writes, computed entries, persistence (P5) | `plux_flutter/lib/src/state/` | [ADR-0046](../docs/adr/0046-state-engine.md) |
| Forms, validators, PXL `regex` and `phone` (P5) | `plux_flutter/lib/src/state/`, `pxl/` | [ADR-0047](../docs/adr/0047-forms-validators-regex-and-phone.md) |
| Data sources, outbox, transfers (P5) | `plux_flutter/lib/src/data/` | [ADR-0048](../docs/adr/0048-data-layer.md) |
| Local persistence, `plux_db_drift` (P5) | `plux_flutter/lib/src/db/`, `plux_db_drift` | [ADR-0049](../docs/adr/0049-local-persistence.md) |
| Animation, `plux_lottie`, `plux_rive` (P5) | `plux_flutter/lib/src/anim/`, `plux_lottie`, `plux_rive` | [ADR-0050](../docs/adr/0050-animation-engine.md) |
| Device actions, capabilities, `plux_media`, `plux_scanner`, `plux_location` (P5) | `plux_flutter/lib/src/actions/`, the device packages | [ADR-0051](../docs/adr/0051-device-actions-packages-and-capabilities.md) |

`plux_native_scan` (P4) is a development-only tool like `plux_widget_api`: it runs in host projects through `dart run`, and no shipped package may depend on it. `plux_go_router` and `plux_auto_route` (P4) are optional adapters; their router is a dependency of the adapter alone, never of `plux_flutter`, and they use only `plux_flutter`'s public API. Their tests run the shared delegate suite of `plux_flutter/test/support/delegate_suite.dart`, which the runtime's own tests run against the default delegate, so every delegate passes the same navigation cases; the adapters reach it by a relative import, test code only.

## Invariants

- Every package declares `resolution: workspace` and is listed under `workspace:` in the root `pubspec.yaml`.
- The public API is semantically versioned and fully documented; `public_member_api_docs` is enforced (`RT-001`). Only `lib/plux_flutter.dart` exports public API; everything under `lib/src/` is private to the package.
- `PluxRuntimeInfo.version` equals the `version` in `pubspec.yaml`; a test enforces it, and the release workflow checks the tag against it (`CI-008`).
- Nothing on the UI isolate performs network I/O, decompression, patching or hashing of more than 64 KiB (layering rule L-6). The one bounded exception is the first-use check of a section of at most 64 KiB (ADR-0029).
- Nothing is loaded before it is verified (`SEC-052`); failures are contained by error boundaries and never crash the host app (`RT-020`).
- Native code lives only in `plux_flutter/native/` and is built from source by `hook/build.dart` (ADR-0030); no prebuilt binary is committed. Vendored third-party C (zstd) is changed only by re-importing a pinned release with its script.
- Platform channels are used only for platform services — key storage, background scheduling — never for bundle data.
- No secret is persisted in the clear: the device secret is encrypted under a platform-held key (ADR-0029); access tokens stay in memory.
- The core package stays within its size budget (`RT-061`); heavy features belong in optional packages.
- **Package placement** ([ADR-0051](../docs/adr/0051-device-actions-packages-and-capabilities.md)): a capability ships in an optional package (`RT-060`) when it (1) adds a third-party or native dependency, (2) needs a platform permission, or (3) adds a measurable size cost most apps would not use; otherwise it belongs in the core. The core's budgets (`RT-061`: ≤ 5 MiB per App Bundle download and ≤ 10 MiB per APK, per ABI; ≤ 5 MiB for the thinned IPA) are measured at every milestone, and an exception is recorded in an ADR (`url_launcher` for `openUrl` is one, in ADR-0051). An optional package uses only `plux_flutter`'s public API and registers with the runtime through `PluxConfig`.
- `lib/src/schema/*.g.dart`, the verifier's layout tables and the generated widget builders are written by `make gen` from `schema/`; never edit them. Widget, prop, enum and action IDs come only from the generated registry (`BND-011`).
- Events run their action graphs on the engine (ADR-0039). An action or option the runtime does not run fails its step with `PLX-4010`, or is reported once in debug builds; it is never partially implemented.

## Stop and ask before

- Adding a dependency to `plux_flutter` (it ships inside every host app), or a router dependency anywhere but its adapter package.
- Changing the public API of `plux_flutter` in a way that is not additive.
- Changing the release store's on-disk layout or the pointer protocol (ADR-0021): installed devices depend on it.
- Changing the verification order or what is verified (ADR-0029).

## Required checks

`make dart-check` — lockfile committed, formatting, `flutter analyze --fatal-infos`, tests with coverage floors. Tests run on the Linux host (the build hook compiles the native library for it); device and simulator runs happen only in CI.
