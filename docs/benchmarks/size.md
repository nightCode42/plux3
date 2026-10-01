# Size Journey

What `plux_flutter` adds to a host app's download (`RT-061`, `NFR-009`), round by round.
Each round records the date, commit and CI run, what changed, the measurements, where the
bytes go, and what was decided. Rounds are appended and never rewritten, so the document
reads as the history of the runtime's size; the newest round is the current state.

## Method

The same blank Flutter app is built for release without and with `plux_flutter`
([test/size](../../test/size/README.md)); `tools/cmd/sizegate` measures both and reports
the difference. The plux app calls `Plux.initialize` and shows a `PluxView`, so everything
a host app ships — sync, verification, the renderer and every widget builder — is built
in. Flutter 3.47.5, CI's `ubuntu-latest` (Android) and `macos-latest` (iOS).

| Build | How it is measured |
|---|---|
| Android APK, per ABI | `flutter build apk --release --split-per-abi`; the APK file as it is. Its Dart AOT code (`libapp.so`) is stored uncompressed, so the file is larger than what a device downloads. |
| Android App Bundle download, per ABI | `flutter build appbundle --release`; the base module's files with only that ABI's native libraries, each compressed at the highest level into one ZIP archive — what Play delivers to a device of that ABI. Language and screen-density splits are not applied, so the figure is an upper bound. |
| iOS IPA, arm64 | `flutter build ios --release --no-codesign`; the `.app` archived as an IPA (`Payload/`, highest compression) — what App Store thinning delivers to one device. |

The *Size (Android)* job writes the six Android figures side by side
(`android-compare.md` in its summary and artifact) and a per-file breakdown of each; the
*Size (iOS)* job the IPA's. The gate is `RT-061`'s: at most 3 MiB, and no more than 10%
over the overhead committed in `test/size/baseline.json` (`QA-007`).

## Round 1 — the first measurement (2026-09-30 to 2026-10-01)

**State:** the P3 runtime as of commit `b026070`, before any size work. CI runs
36700053538 (first iOS figure), 36809265180 and 36816150804 (App Bundle and APK per ABI).

| Platform | Build | Blank | With `plux_flutter` | Added |
|---|---|---:|---:|---:|
| Android arm64-v8a | APK file | 14.35 MiB | 20.04 MiB | **5,961,751 B (5.69 MiB)** |
| Android arm64-v8a | App Bundle download | 7.00 MiB | 9.44 MiB | **2,564,279 B (2.45 MiB)** |
| Android armeabi-v7a | APK file | 11.68 MiB | 18.08 MiB | **6,713,507 B (6.40 MiB)** |
| Android armeabi-v7a | App Bundle download | 6.25 MiB | 8.93 MiB | **2,817,982 B (2.69 MiB)** |
| Android x86_64 | APK file | 15.72 MiB | 21.62 MiB | **6,190,585 B (5.90 MiB)** |
| Android x86_64 | App Bundle download | 7.02 MiB | 9.49 MiB | **2,589,992 B (2.47 MiB)** |
| iOS arm64 | IPA | 5.81 MiB | 8.16 MiB | **2,460,431 B (2.35 MiB)** |

**Where the bytes go (arm64):**

| File | Added to the APK | Added to the App Bundle download |
|---|---:|---:|
| `libapp.so` — Dart AOT code | 5,373,952 B | 2,211,484 B |
| `classes.dex` — mostly Play Services Cronet | 201,557 B | 209,721 B |
| Resources (`resources.arsc`, `resources.pb`) | 131,712 B | 48,082 B |
| `libdartjni.so` — JNI bridge for Cronet | 131,248 B | 26,296 B |
| `libplux_native.so` — the runtime's native code | 70,224 B | 41,650 B |
| Protocol Buffers `.proto` files (Cronet) | about 22 KB | about 22 KB |

Flutter's code-size analysis of the plux app (`--analyze-size`, arm64) attributes 883 KB
of Dart AOT symbols to `package:plux_flutter` itself; the rest of the growth is code it
keeps alive in the framework (`package:flutter` totals 4 MB in the plux app) and its
dependencies (`riverpod` 95 KB, `jni` 87 KB, `cronet_http` 59 KB, `source_span` 42 KB,
`cryptography` 28 KB). The analysis is the *size-android* artifact of each run.

**Findings:**

1. **The APK file is not the download.** Android stores `libapp.so` uncompressed in the
   APK so it can be mapped without extraction; compressed it is 2.2 MB instead of 5.4 MB.
   Play compresses what it delivers, and the iOS figure is compressed too, so the App Bundle
   download is the figure comparable with iOS. Every App Bundle download is under 3 MiB;
   every APK file is over.
2. **The 32-bit APK is the largest.** armeabi-v7a adds 6.40 MiB as an APK file, against
   5.69 MiB for arm64-v8a.
3. **iOS meets the budget** with 0.65 MiB to spare.
4. **Building at all needed a fix.** Flutter 3.47.5's Android Gradle plugin 9.1 refuses
   Play Services Cronet's `cronet-api` and `cronet-shared`, which share the namespace
   `org.chromium.net`; `android.uniquePackageNames=false` in the host's
   `gradle.properties` builds it (the size apps, the starter app and the host guide set it).

**Decided by the maintainer:**

- Every widget builder stays built in, and the host registers nothing: no reduction that
  adds work for developers.
- Cronet stays: Play Services Cronet is the Android HTTP client (`SYN-010`).
- The budget is restated per build: at most 3 MiB for the App Bundle download, and a
  separate budget for the APK file (6 MiB proposed; armeabi-v7a exceeds it by 0.40 MiB).
  This changes the wording of `RT-061`, a `MUST`, so it lands with an ADR and the gate
  targets; until then *Size (Android)* gates the arm64 APK at 3 MiB and stays red.
- Size optimization is parked until a later round.

**Candidates for an optimization round**, inside the runtime only and none measured yet:
decoding widget properties from tables instead of per-widget code, fewer generated and
generic classes, no debug strings in release builds, and fewer small dependencies
(`source_span`). With every widget and Cronet kept, the expected saving is hundreds of
kilobytes, not megabytes.

## Adding a round

Copy the shape of the latest round: the date and commit, what changed since the previous
round, the table of the seven builds with the change against the previous round, where
the bytes go, findings and decisions. Take the figures from the *Size (Android)* and
*Size (iOS)* jobs of one CI run and name it. When an intended change moves the overhead,
`test/size/size.sh android -update` (or `ios`) rewrites the committed baseline in the same
pull request.
