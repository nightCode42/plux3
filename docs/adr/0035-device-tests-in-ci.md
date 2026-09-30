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
- **flutter test sometimes loses the app on iOS simulators.** The app starts, draws its first
  frame and logs "The Dart VM service is listening on …", but flutter test, which learns the
  address only from that one log line and starts reading the simulator's log as it launches the
  app, sometimes attaches after the line was delivered and waits until stopped (iOS 26.5 and
  18; the app's log shows the line in run 36756301098). It is a race in Flutter's tooling, not
  in the app.

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
- **iOS:** the newest iPhone on the newest runtime (`macos-latest`) and an iOS 18 simulator
  (`macos-15`). For the lost VM service line, the test entry point
  (`apps/starter/integration_test/app_test.dart`) repeats the line on iOS during its first 40
  seconds, so a late log reader finds the same address; the driver stops a run in which nothing
  follows flutter's "Waiting for VM Service port" within three minutes, and a failed iOS run
  prints the app's own lines about the service.

## Consequences

- **Positive:** every change runs the flows on two Android levels and two iOS versions with the
  platform key stores and HTTP clients; hangs end in minutes with the evidence in the log.
- **Negative:** API 24 and 25 are not exercised in CI; the iOS workaround depends on the text
  of the engine's line, which a Flutter upgrade could change (the job would then show the old
  hang, and the line is updated with the upgrade).
- **Follow-up:** report the log race to Flutter with run 36756301098's evidence and remove the
  workaround once fixed; decide on a device service for API 24 and real devices (`QA-006`).

## Options in detail

### Option 1

As decided. Runner minutes: about 15 per Android job and 10 per iOS job.

### Option 2

Tried in CI: emulator 37 with more time, cores and memory; emulator 34.1.19 by build ID
(`emulator-linux_x64-11525734.zip`); the Google APIs and plain images. All boot the kernel and
all panic in the same driver, so no configuration within reach runs API 24.

### Option 3

Covers API 24 and physical devices, including a low-end Android, within a free quota, but needs
a Google Cloud project and a service-account secret in the repository; a maintainer decision.
