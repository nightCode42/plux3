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
*Size (iOS)* job the IPA's.

## Budgets

Since 2026-10-02 (`RT-061`, `NFR-009`, [ADR-0036](../adr/0036-size-budgets-per-build.md),
Revision), each build is gated in CI on its own budget, and on no more than 20% (10% until 2026-10-05) over the
overhead committed in `test/size/baseline.json` (`QA-007`):

| Build | Budget |
|---|---:|
| Android App Bundle download, per ABI | 5 MiB (4 MiB until 2026-10-05) |
| Android APK, per ABI | 10 MiB |
| iOS IPA, arm64 | 5 MiB (3 MiB until 2026-10-05) |

From 2026-10-01 to 2026-10-02 the budgets were 3 MiB for the App Bundle download and the
IPA and 6.5 MiB for the APK (round 1); before, `RT-061` allowed 3 MiB to the arm64 APK and
the IPA.

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
- The budget is restated per build: at most 3 MiB for the App Bundle download and the iOS
  IPA, and 6.5 MiB for the APK file of each ABI (6 MiB was considered; armeabi-v7a exceeds
  it by 0.40 MiB). `RT-061` and `NFR-009` are reworded (specification 1.1.7,
  [ADR-0036](../adr/0036-size-budgets-per-build.md)), all seven builds are gated, and every
  one meets its budget — armeabi-v7a's APK with 0.10 MiB to spare.
- Size optimization is parked until a later round.

**Candidates for an optimization round**, inside the runtime only and none measured yet:
decoding widget properties from tables instead of per-widget code, fewer generated and
generic classes, no debug strings in release builds, and fewer small dependencies
(`source_span`). With every widget and Cronet kept, the expected saving is hundreds of
kilobytes, not megabytes.

## Round 2 — the P4 action engine and routing core (2026-10-02)

**State:** commit `e536be9`, P4 R2 (action engine core) and R3 (routing core) on top of
round 1. CI run 36964307810. No size work: the growth is the new code — the engine, the
nine action handlers, the router, the navigation delegate, page routes with their
transitions, shells and host events.

| Platform | Build | Blank | With `plux_flutter` | Added | Change |
|---|---|---:|---:|---:|---:|
| Android arm64-v8a | APK file | 14.35 MiB | 20.23 MiB | **6,158,495 B (5.87 MiB)** | +196,744 B |
| Android arm64-v8a | App Bundle download | 7.00 MiB | 9.51 MiB | **2,633,191 B (2.51 MiB)** | +68,912 B |
| Android armeabi-v7a | APK file | 11.68 MiB | 18.28 MiB | **6,926,631 B (6.61 MiB)** | +213,124 B |
| Android armeabi-v7a | App Bundle download | 6.25 MiB | 9.01 MiB | **2,900,533 B (2.77 MiB)** | +82,551 B |
| Android x86_64 | APK file | 15.72 MiB | 21.75 MiB | **6,321,793 B (6.03 MiB)** | +131,208 B |
| Android x86_64 | App Bundle download | 7.02 MiB | 9.55 MiB | **2,659,928 B (2.54 MiB)** | +69,936 B |
| iOS arm64 | IPA | 5.81 MiB | 8.23 MiB | **2,535,588 B (2.42 MiB)** | +75,157 B |

**Where the bytes go:** all of the change is Dart AOT code. On arm64 `libapp.so` adds
5,570,560 B to the APK (+196,608 B over round 1) and 2,280,260 B to the App Bundle
download (+68,776 B); the other files change by less than 200 B.

**Findings:**

1. **Every download grows by about 3%:** 69 to 83 KB on Android, 75 KB on iOS, and every
   one stays under 3 MiB.
2. **The armeabi-v7a APK crossed 6.5 MiB,** at 6.61 MiB: round 1 left it 0.10 MiB of
   room, and the job failed on that budget alone; every build stayed within 10% of its
   baseline.
3. **A candidate, not implemented:** a local x64 AOT build without flatc's generated
   `toString` methods on the bundle classes was about 105 KB smaller. It is not a CI figure,
   and the runtime still ships those methods.

**Decided by the maintainer:** the Android budgets rise to 4 MiB for the App Bundle download
and 10 MiB for the APK of each ABI; the IPA stays at 3 MiB (`RT-061`, `NFR-009`,
specification 1.2.1, [ADR-0036](../adr/0036-size-budgets-per-build.md) Revision). The
baseline is the round 2 state, so the 10% gate measures growth from here. Size optimization
stays parked.

## Round 3 — Phase 4 complete (2026-10-03)

**State:** commit `3572b40`, P4 R4 to R10 on top of round 2: guards, deep links and push
payloads, the auth delegate and user context, the native catalogue (native routes, slots
and custom actions), `PluxView` for components, host events and exposed state. CI run
37082540130. No size work. The router adapters, `plux_native_scan` and the add-to-app module
are packages of their own and not in the measured app.

| Platform | Build | Blank | With `plux_flutter` | Added | Change |
|---|---|---:|---:|---:|---:|
| Android arm64-v8a | APK file | 14.35 MiB | 20.29 MiB | **6,224,031 B (5.94 MiB)** | +65,536 B |
| Android arm64-v8a | App Bundle download | 7.00 MiB | 9.55 MiB | **2,672,665 B (2.55 MiB)** | +39,474 B |
| Android armeabi-v7a | APK file | 11.68 MiB | 18.42 MiB | **7,074,087 B (6.75 MiB)** | +147,456 B |
| Android armeabi-v7a | App Bundle download | 6.25 MiB | 9.06 MiB | **2,950,310 B (2.81 MiB)** | +49,777 B |
| Android x86_64 | APK file | 15.72 MiB | 21.81 MiB | **6,387,329 B (6.09 MiB)** | +65,536 B |
| Android x86_64 | App Bundle download | 7.02 MiB | 9.59 MiB | **2,696,484 B (2.57 MiB)** | +36,556 B |
| iOS arm64 | IPA | 5.81 MiB | 8.26 MiB | **2,568,062 B (2.45 MiB)** | +32,474 B |

**Where the bytes go:** all of the change is Dart AOT code. On arm64 `libapp.so` adds
5,636,096 B to the APK (+65,536 B over round 2) and 2,319,734 B to the App Bundle
download (+39,474 B); the other files are unchanged.

**Findings:**

1. **Every download grows by 1.3% to 1.7%:** 37 to 50 KB on Android, 32 KB on iOS, about
   half of what R2 and R3 added (69 to 83 KB and 75 KB).
2. **Every build is within its budget and within 10% of the round 2 baseline.** The
   largest are the armeabi-v7a APK at 6.75 MiB (budget 10 MiB, +2.1% over its baseline),
   the armeabi-v7a App Bundle download at 2.81 MiB (budget 4 MiB) and the IPA at 2.45 MiB
   (budget 3 MiB).
3. **The committed baseline was round 2's** until P4's close.

**Decided by the maintainer** (2026-10-03): the baseline moves to round 3, so the 10% gate
measures P5's growth from P4's state (`test/size/baseline.json`). Size optimization stays
parked.

## Adding a round

Copy the shape of the latest round: the date and commit, what changed since the previous
round, the table of the seven builds with the change against the previous round, where
the bytes go, findings and decisions. Take the figures from the *Size (Android)* and
*Size (iOS)* jobs of one CI run and name it. When an intended change moves the overhead,
`test/size/size.sh android -update` (or `ios`) rewrites the committed baseline in the same
pull request.
