#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Plux contributors
# SPDX-License-Identifier: Apache-2.0
#
# The starter app's end-to-end flows on an Android emulator (QA-006), run
# by `make e2e-android` in CI. See test/e2e/README.md.
#
#   android.sh [api]   API level of the system image (default 35)
#
# Installs the emulator and a Google APIs x86_64 system image with the SDK's
# own tools, boots a headless emulator (hardware acceleration needs KVM),
# forwards the device's loopback port of the test server to this machine
# (adb reverse), and runs the Go driver with PLUX_E2E_DEVICE, which builds
# the app for the emulator and runs integration_test/app_test.dart there.
# Cloud development sessions do not start emulators (docs/WORKLOG.md,
# device testing); this runs on GitHub's runners.
set -euo pipefail

api=${1:-35}
root=$(git rev-parse --show-toplevel)
out=${E2E_OUT:-$root/build/e2e}
sdk=${ANDROID_HOME:-${ANDROID_SDK_ROOT:?set ANDROID_HOME to the Android SDK}}
tools=$sdk/cmdline-tools/latest/bin
image="system-images;android-$api;google_apis;x86_64"
avd=plux_e2e_$api
# The test server's address (backend/internal/server/starter_e2e_integration_test.go).
port=18094
mkdir -p "$out"

yes | "$tools/sdkmanager" --licenses >/dev/null || true
"$tools/sdkmanager" --install emulator platform-tools "$image" >"$out/sdkmanager.log"
echo no | "$tools/avdmanager" create avd --force --name "$avd" --package "$image" --device pixel_6 >/dev/null

adb=$sdk/platform-tools/adb
"$sdk/emulator/emulator" -avd "$avd" -no-window -no-audio -no-boot-anim -no-snapshot \
	-gpu swiftshader_indirect -memory 4096 -port 5554 >"$out/emulator-$api.log" 2>&1 &
emulator=$!
serial=emulator-5554
trap '"$adb" -s "$serial" emu kill >/dev/null 2>&1 || kill "$emulator" 2>/dev/null || true' EXIT

# Booted when the package manager answers and the boot animation stopped.
"$adb" -s "$serial" wait-for-device
for _ in $(seq 180); do
	if [ "$("$adb" -s "$serial" shell getprop sys.boot_completed 2>/dev/null | tr -d '\r')" = 1 ]; then
		break
	fi
	kill -0 "$emulator" 2>/dev/null || { cat "$out/emulator-$api.log"; exit 1; }
	sleep 2
done
[ "$("$adb" -s "$serial" shell getprop sys.boot_completed | tr -d '\r')" = 1 ] || {
	echo "✗ the emulator did not boot in 6 minutes"; exit 1
}
for s in window_animation_scale transition_animation_scale animator_duration_scale; do
	"$adb" -s "$serial" shell settings put global "$s" 0
done
"$adb" -s "$serial" reverse "tcp:$port" "tcp:$port"

PLUX_E2E_DEVICE=$serial make -C "$root" --no-print-directory e2e-starter 2>&1 | tee "$out/android-$api.log"
