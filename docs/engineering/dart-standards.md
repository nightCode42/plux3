# Dart and Flutter Standards

How Dart and Flutter code in Plux (`packages/`, `apps/`) is written. The baseline is [Effective Dart](https://dart.dev/effective-dart) and the [Flutter style guide](https://github.com/flutter/flutter/blob/main/docs/contributing/Style-guide-for-Flutter-repo.md); the analyser configuration ([analysis_options.yaml](../../analysis_options.yaml)) enforces most of this document.

---

## 1. Workspace

- All Dart packages belong to one pub workspace declared in the root `pubspec.yaml`; members declare `resolution: workspace`, and one `pubspec.lock` is committed.
- The Flutter version is pinned in the Makefile and CI; the SDK constraint in each pubspec is `^3.13.0` or later.
- Optional capabilities are separate packages (`RT-060`) so host apps pay only for what they use.

## 2. Analysis

- `strict-casts`, `strict-inference` and `strict-raw-types` are on; `flutter analyze --fatal-infos --fatal-warnings` must pass.
- Every public member is documented (`public_member_api_docs`, `RT-001`).
- Futures are never dropped silently (`unawaited_futures`, `discarded_futures`); subscriptions and sinks are closed.
- Imports use `package:` URIs; directives are ordered.

## 3. API design

- The public API of `plux_flutter` is small, semantically versioned and changes additively (`RT-001`).
- Public types are `final class` or `abstract final class` unless designed for extension.
- Constructors validate their inputs; invalid states are unrepresentable.
- Use the spec's vocabulary exactly (`PluxView`, `NativeSlot`, `AppRelease`, `SyncEvent`).

## 4. State, isolates and performance

- Riverpod is the state engine (`RT-003`, ADR-0008). Widgets subscribe only to the state paths they read, with `select` (`RT-012`).
- Network I/O, decompression, patching and hashing of more than 64 KiB run off the UI isolate (layering rule L-6).
- Build methods are pure and cheap: no I/O, no allocation of large objects, no work proportional to data size.
- Lists are built lazily; images are decoded at their laid-out size (`RT-011`, `RT-014`).
- Performance claims are measured in profile mode on the reference devices (spec §30), never assumed.

## 5. Fault isolation

- Every page and component instance is wrapped in an error boundary; failures render a fallback and are reported, never thrown into the host app (`RT-020`).
- Exceptions thrown are `Error` or `Exception` subclasses with a clear message (`only_throw_errors`).

## 6. Documentation

- Every library starts with the SPDX header and a `///` doc comment stating its responsibility.
- Doc comments explain what and why, name requirement IDs where behaviour is spec-driven, and include examples for non-obvious APIs.

## 7. Formatting

`dart format` with the default settings (`make dart-fmt`); CI rejects unformatted code.
