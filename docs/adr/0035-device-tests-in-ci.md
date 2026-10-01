# 0035. Device end-to-end tests in CI: emulators and simulators on GitHub's free runners

- **Status:** Accepted
- **Date:** 2026-09-30
- **Requirements:** `QA-006`, `RT-002`

## Context and problem

`QA-006` asks for the example host app's end-to-end flows on Android emulators and iOS
simulators in CI, and `RT-002` names the supported platforms: Android 7.0 (API 24) and later,
iOS 15 and later. The P3 plan rules out paid device services and emulators in cloud
development sessions: device runs happen only on GitHub's free runners (work log, P3 scope
decisions). The flows already run under `flutter test` on the host; on a device they must
also exercise what the host run replaces: the Android Keystore and iOS Keychain, Cronet and
`URLSession`, the native library of ADR-0030.

Building this in CI (runs 36714710195 to 36756301098) found two limits that decide what the
jobs can cover.

- **API 24 does not boot on the emulator.** Its Google APIs image never reached
  `sys.boot_completed` on emulator 37 (with 10 and 20 minutes, four cores and 6 GiB), nor on
  emulator 34.1.19; the plain Android image did not either. With `-show-kernel` the guest
  kernel (3.10) reports `kernel BUG at drivers/platform/goldfish/goldfish_pipe_v2.c:854` in
  `goldfish_dma_mmap` when the first app maps graphics memory, then `Kernel panic - not
  syncing: Fatal exception` (run 36756301098). API 26 and 35 boot and pass.
- **flutter test loses the app on iOS simulators, often.** The app starts, draws its first
  frame and logs "The Dart VM service is listening on …" once. flutter test learns the address
  only from that line, through a `log stream` it starts alongside `simctl launch`
  (`flutter_tools` `IOSSimulator.startApp`); a live stream has no history, and on a busy runner
  it attaches after the line, so the tool waits for it until stopped. The app cannot help:
  under flutter test its test code is driven by the tool and starts only once the tool
  connects. Runs 36756301098 to 36816150804 show it on iOS 26.5 and 18 alike, in more than
  half the runs: the app process alive, no crash report, no Dart code run, no request to the
  server. It is a race in Flutter's tooling, not in the app.

## Decision drivers

- Verify on real platform stacks what the host run cannot, on every change.
- No paid services, no third-party emulator actions (dependency policy), deterministic jobs.
- A failure is root-caused, never retried into passing (AGENTS.md §5); a hang must not cost
  the job's whole timeout.

## Considered options

1. Emulator and simulator jobs driven by the SDK's own tools and `simctl`, with the API 24
   run left to devices outside CI.
2. The same, plus an API 24 job on an older emulator or system image.
3. Firebase Test Lab (physical and virtual devices, including API 24) from a nightly workflow.

## Decision

Chosen option: **1**, because it covers every stack the host run replaces on each change, with
no new dependency, and option 2 is not possible: API 24's kernel panics on every emulator build
and image we can use. Firebase Test Lab (option 3) stays the way to add real and older devices,
put to the maintainer with `QA-006`'s device farm.

- **Android:** API 26 (the oldest level that boots) and 35, Google APIs x86_64 images, so Play
  Services Cronet is what syncs; `test/e2e/android.sh` installs them with `sdkmanager` (a
  broken download is fetched once more), boots headless with KVM, bounds the boot at ten
  minutes and prints the emulator's log when it fails.
- **API 24** is covered by the runtime's `minSdk 24` and the Android builds of every change,
  and on devices by the maintainer's manual runs until a device service is chosen; `RT-002`
  records it.
- **iOS:** one job, the newest iPhone on the newest runtime (`macos-latest`). It does not use
  flutter test: the driver builds the app with the flows as its entry point (`flutter build ios
  --simulator --debug --target=integration_test/app_test.dart`) and runs it under XCTest
  (`xcodebuild test`), where `ios/RunnerTests` waits for integration_test's results through
  `FLTIntegrationTestRunner` — Flutter's documented route for running integration tests on iOS
  without a host connection. Nothing reads the simulator's log, so the race cannot occur; a
  failed run prints the app's log.

## Consequences

- **Positive:** every change runs the flows on two Android levels and one iOS version with the
  platform key stores and HTTP clients; the iOS run has no timing dependency on the tool.
- **Negative:** API 24 and 25 and iOS versions older than the runner's newest are not exercised
  in CI; the iOS run reports one XCTest result for all the flows, with the Dart failure's
  message, rather than flutter test's per-test output.
- **Follow-up:** report the log race to Flutter with the evidence of runs 36809265180 and
  36816150804; decide on a device service for API 24, older iOS and real devices (`QA-006`).

## Options in detail

### Option 1

As decided. Runner minutes: about 15 per Android job and 10 for the iOS job.

### Option 2

Tried in CI: emulator 37 with more time, cores and memory; emulator 34.1.19 by build ID
(`emulator-linux_x64-11525734.zip`); the Google APIs and plain images. All boot the kernel and
all panic in the same driver, so no configuration within reach runs API 24.

### Option 3

Covers API 24 and physical devices, including a low-end Android, within a free quota, but needs
a Google Cloud project and a service-account secret in the repository; a maintainer decision.
