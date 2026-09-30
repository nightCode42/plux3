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
| iOS simulator, the newest runtime of the runner's Xcode | `make e2e-ios` | CI job *Device end-to-end (iOS)* |

On a device the driver sets `PLUX_E2E_DEVICE`, and the flows run in the app
built for that device: the platform's key store (Android Keystore, iOS
Keychain) holds the device credential and the platform's HTTP client (Cronet,
`URLSession`) syncs — the parts `flutter test` on the host replaces. After the
flows the driver checks that the device's `session_start` and `sync_result`
events reached the server.

- [`android.sh`](android.sh) installs the emulator and a Google APIs x86_64
  system image with the SDK's own `sdkmanager`, boots it headless, and
  forwards the device's port 18094 to the server on the runner
  (`adb reverse`). API 24 is the oldest Android the runtime supports
  (`RT-002`), but current emulators no longer boot its system image, so CI
  runs 26 and 35. The runner needs KVM, which the job enables.
- [`ios.sh`](ios.sh) creates and boots an iPhone simulator on the newest iOS
  runtime installed; the simulator shares the runner's network.

Both need `PLUX_TEST_DATABASE_URL` (see
[testing.md](../../docs/engineering/testing.md)). They use no third-party
emulator action and no paid device service; cloud development sessions do not
start emulators (maintainer decision, P3). Reference-device numbers and a
real-device farm are recorded in the work log's open decisions.
