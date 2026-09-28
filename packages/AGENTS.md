# packages — Agent Notes

Dart and Flutter packages, managed as one pub workspace from the repository root (`pubspec.yaml`, one `pubspec.lock`). `plux_flutter` is the runtime (spec §12); optional capabilities ship as separate packages (`RT-060`), each created in the phase that brings its first feature. `plux_widget_api` is a development-only tool that snapshots the pinned Flutter SDK for the widget coverage table (`WGT-003`, ADR-0010); no shipped package may depend on it. Read [dart-standards.md](../docs/engineering/dart-standards.md) and the root [AGENTS.md](../AGENTS.md) before editing.

The runtime's design is recorded in ADR-0008 (Riverpod), ADR-0021 (sync and the release store), ADR-0029 (verification), ADR-0030 (native code), ADR-0031 (rendering) and ADR-0032 (theming). Read the one for the area you change.

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
- `lib/src/schema/*.g.dart`, the verifier's layout tables and the generated widget builders are written by `make gen` from `schema/`; never edit them. Widget, prop, enum and action IDs come only from the generated registry (`BND-011`).
- P3 renders; it does not act or navigate (ADR-0031): event handlers are explicit no-ops that report `PLX-4010` in debug builds, never partial implementations.

## Stop and ask before

- Adding a dependency to `plux_flutter` (it ships inside every host app).
- Changing the public API of `plux_flutter` in a way that is not additive.
- Changing the release store's on-disk layout or the pointer protocol (ADR-0021): installed devices depend on it.
- Changing the verification order or what is verified (ADR-0029).

## Required checks

`make dart-check` — lockfile committed, formatting, `flutter analyze --fatal-infos`, tests with coverage floors. Tests run on the Linux host (the build hook compiles the native library for it); device and simulator runs happen only in CI.
