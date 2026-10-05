# 0036. Size budgets per build: what a device downloads, and the APK file

- **Status:** Accepted
- **Date:** 2026-10-01, revised 2026-10-02 and [2026-10-05](#revision-2026-10-05)
- **Requirements:** `RT-061`, `NFR-009`, `QA-007`

## Context and problem

`RT-061` allowed the core package to add at most 3 MiB to a release APK for arm64 and to a
thinned iOS IPA. The first measurement ([size journey](../benchmarks/size.md), round 1)
found the iOS IPA at +2.35 MiB but the arm64 APK at +5.69 MiB, and the two figures do not
measure the same thing: Android stores the Dart AOT code (`libapp.so`) uncompressed in an
APK so it can be mapped without extraction, while the IPA figure is compressed. Compressed,
the same code is 2.2 MB instead of 5.4 MB. Apps on Google Play ship as App Bundles, from
which Play delivers each device only its ABI's native code, compressed; APKs remain how apps
are sideloaded and distributed outside Play.

| Build (added by `plux_flutter`) | arm64-v8a | armeabi-v7a | x86_64 |
|---|---:|---:|---:|
| App Bundle download | 2.45 MiB | 2.69 MiB | 2.47 MiB |
| APK file | 5.69 MiB | 6.40 MiB | 5.90 MiB |

The maintainer ruled out reducing the size by dropping built-in widget builders, by making
hosts register them, or by making Cronet optional: none may add work for developers, and
Play Services Cronet is the Android HTTP client (`SYN-010`).

## Decision drivers

- Measure what users download, the same way on both platforms.
- Keep a hard ceiling on the APK too, which sideloaded and enterprise apps install.
- No reduction that costs developers work or drops Cronet (maintainer).
- Gates catch regressions on every build the runtime ships to (`QA-007`).

## Considered options

1. Keep ≤ 3 MiB on the arm64 APK and shrink the runtime until it fits.
2. ≤ 3 MiB on the App Bundle download per ABI and the IPA, plus a separate APK budget.
3. ≤ 3 MiB on the App Bundle download only.

## Decision

Chosen option: **2**, set by the maintainer:

- **Android App Bundle download, per ABI (arm64-v8a, armeabi-v7a, x86_64): ≤ 3 MiB** — the
  base module with that ABI's native libraries, each file compressed at the highest level
  (`tools/cmd/sizegate`; language and density splits are not applied, so the figure is an
  upper bound of Play's).
- **Android APK, per ABI: ≤ 6.5 MiB** — the file `flutter build apk --split-per-abi` writes.
- **iOS IPA, arm64, thinned: ≤ 3 MiB** — unchanged.

Every one of the seven builds is gated in CI, each also at no more than 10% over its
committed overhead in `test/size/baseline.json` (`QA-007`). `RT-061` and `NFR-009` are
reworded accordingly (specification 1.1.7).

## Consequences

- **Positive:** the Android and iOS budgets measure the same thing, a compressed download;
  the APK keeps a ceiling; all three Android ABIs are gated, not only arm64.
- **Negative:** the armeabi-v7a APK is 0.10 MiB under its 6.5 MiB budget, so growth there
  meets the budget before the 10% baseline gate; the App Bundle figure is an approximation of
  Play's, computed without bundletool.
- **Follow-up:** size optimization inside the runtime, recorded as later rounds of the
  [size journey](../benchmarks/size.md).

## Options in detail

### Option 1

Meets the original wording, but the uncompressed Dart code makes 3 MiB out of reach without
dropping widgets or Cronet, which the maintainer ruled out; the round 1 candidates inside the
runtime are expected to save hundreds of kilobytes, not 2.7 MB.

### Option 2

As decided.

### Option 3

Measures only what Play users download and leaves APK distribution without a ceiling.

## Revision (2026-10-02)

The P4 action engine and routing core (R2, R3) added 131 to 213 KB to each APK: CI run
36964307810 measured the armeabi-v7a APK at +6.61 MiB, over its 6.5 MiB budget, while every
download stayed under 3 MiB ([size journey](../benchmarks/size.md), round 2). The maintainer
raised the Android budgets rather than trim the runtime:

- **Android App Bundle download, per ABI: ≤ 4 MiB** (was 3 MiB).
- **Android APK, per ABI: ≤ 10 MiB** (was 6.5 MiB).
- **iOS IPA, arm64, thinned: ≤ 3 MiB** — unchanged.

The 10% gate over `test/size/baseline.json` (`QA-007`) still catches each regression, so
growth stays visible and reviewed even with room under the budgets. `RT-061` and `NFR-009`
are reworded accordingly (specification 1.2.1).

## Revision (2026-10-05)

Phase 5's first batch (action engine completion, state engine, forms, data layer I) took the
runtime's overhead 8 to 11% over the committed baseline in CI run 37262413120: the IPA at
2.71 MiB (budget 3 MiB, baseline 2.45 MiB), the App Bundle downloads at 2.79 to 3.09 MiB,
the APKs at 6.50 to 7.42 MiB. The maintainer decided (P5 plan §2.1, B7) to give the phase
room now and to reduce size aggressively later, in the phase's performance and size
milestone (R11):

- **App Bundle download, per ABI: ≤ 5 MiB**, replacing 4 MiB.
- **iOS IPA, arm64, thinned: ≤ 5 MiB**, replacing 3 MiB.
- **APK, per ABI: ≤ 10 MiB** — unchanged.
- **The size regression gate fails beyond 20%** over `test/size/baseline.json`, replacing
  10%; the other benchmarks of `QA-007` keep 10%.

The baseline is reset to that run's measurements. Known levers for R11: the bundles' limits
table (every bundle carries all limits; plan B8), and flatc's generated `toString`s, about
105 KB of AOT code (size journey, round 2). `RT-061`, `NFR-009` and `QA-007` are reworded
accordingly (specification 1.3.1).
