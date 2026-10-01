# End-to-end tests on emulators and simulators

The starter app's flows (`QA-006`) — first launch syncs and renders, full
screen, consent and theme switches, manual sync, a relaunch from the cache —
live with the app, in
[`apps/starter/integration_test/starter_flows.dart`](../../apps/starter/integration_test/starter_flows.dart).
They run in three places, always against a Plux server built from source that
the Go driver (`TestStarterAppAgainstTheServer`) starts, publishes the
`starter` fixture to and promotes:

| Where | Command | Runs |
|---|---|---|
| This machine, under `flutter test` | `make e2e-starter` | every change, in the *Starter app end-to-end* job |
| Android emulator, API 26 and 35 | `make e2e-android ANDROID_API=<level>` | CI job *Device end-to-end (Android)* |
| iOS simulator: the newest iPhone and runtime | `make e2e-ios` (`E2E_IOS_MAJOR=18` for another) | CI job *Device end-to-end (iOS)* |

On a device the driver sets `PLUX_E2E_DEVICE`, and the flows run in the app
built for that device: the platform's key store (Android Keystore, iOS
Keychain) holds the device credential and the platform's HTTP client (Cronet,
`URLSession`) syncs — the parts `flutter test` on the host replaces. After the
flows the driver checks that the device's `session_start` and `sync_result`
events reached the server.

- [`android.sh`](android.sh) installs the emulator and a Google APIs x86_64
  system image with the SDK's own `sdkmanager`, boots it headless, and
  forwards the device's port 18094 to the server on the runner
  (`adb reverse`). The runner needs KVM, which the job enables. API 24, the
  oldest Android the runtime supports (`RT-002`), does not run here: its
  kernel panics in the emulator's graphics pipe driver on every emulator
  build and image tried ([ADR-0035](../../docs/adr/0035-device-tests-in-ci.md));
  `minSdk 24` and the maintainer's device runs cover it.
- [`ios.sh`](ios.sh) creates and boots the newest iPhone on the newest iOS
  runtime installed, or on the newest of a major version (`E2E_IOS_MAJOR`);
  the simulator shares the runner's network. It runs the flows under XCTest,
  not flutter test: the app is built with them as its entry point and
  `xcodebuild test` runs it, `ios/RunnerTests` reporting integration_test's
  results. flutter test finds an iOS simulator app only through one line of
  the simulator's log, which it often misses on CI runners
  ([ADR-0035](../../docs/adr/0035-device-tests-in-ci.md)).

While the emulator or simulator boots, both scripts build the Go driver and
the starter app for the device, so the build the flows start recompiles only
the Dart code with their defines ([ADR-0043](../../docs/adr/0043-affected-only-ci.md));
the CI job restores Gradle's caches, which only runs on `main` save.

Both need `PLUX_TEST_DATABASE_URL` (see
[testing.md](../../docs/engineering/testing.md)). They use no third-party
emulator action and no paid device service; cloud development sessions do not
start emulators (maintainer decision, P3). Reference-device numbers and a
real-device farm are recorded in the work log's open decisions.
