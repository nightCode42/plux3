#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Plux contributors
# SPDX-License-Identifier: Apache-2.0
#
# The starter app's end-to-end flows on an Android emulator (QA-006), and
# the Kotlin add-to-app host's (HST-033), run by `make e2e-android` in CI.
# See test/e2e/README.md.
#
#   android.sh [api]   API level of the system image (default 35; 26 or
#                      later: the API 24 kernel panics on the emulator,
#                      ADR-0035)
#
# Installs the emulator and a Google APIs x86_64 system image with the SDK's
# own tools, boots a headless emulator (hardware acceleration needs KVM),
# forwards the device's loopback ports of the test server and the reference
# backend to this machine (adb reverse), and runs the Go driver with PLUX_E2E_DEVICE, which builds
# the app for the emulator and runs integration_test/app_test.dart there.
# While the emulator boots, it builds the Go driver, the app's Android
# project and the add-to-app host, so the builds the flows start recompile
# only the Dart code with their defines (ADR-0043).
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
# The reference apps' servers (Plux Bank 18097, Plux Express 18098) and their
# reference backend (18096; test/refapi), which the device reaches over
# reverse forwards of their own
# (backend/internal/server/reference_e2e_integration_test.go).
refapi_port=18096
reference_ports="18097 18098"
mkdir -p "$out"

yes | "$tools/sdkmanager" --licenses >/dev/null || true
# A download that arrives broken ("Error on ZipFile unknown archive", CI
# run 36743012409) is fetched once more before the job gives up.
"$tools/sdkmanager" --install emulator platform-tools "$image" >"$out/sdkmanager.log" ||
	"$tools/sdkmanager" --install emulator platform-tools "$image" >>"$out/sdkmanager.log"
# One AVD home for avdmanager and the emulator, whatever the runner sets.
export ANDROID_AVD_HOME=${ANDROID_AVD_HOME:-$HOME/.android/avd}
mkdir -p "$ANDROID_AVD_HOME"
echo no | "$tools/avdmanager" create avd --force --name "$avd" --package "$image" --device pixel_6 >/dev/null
[ -r /dev/kvm ] && [ -w /dev/kvm ] || echo "⚠ /dev/kvm is not usable: the emulator runs without acceleration"

adb=$sdk/platform-tools/adb
log=$out/emulator-$api.log
# The adb server, and its key in ~/.android, exist before the emulator
# starts, so the emulator's registration with the server cannot race the
# server's first start. Suspected in CI run 36966274717, where the
# emulator reported its boot complete and adb never answered for it; fail
# now prints the devices adb sees.
"$adb" start-server >/dev/null
"$sdk/emulator/emulator" -avd "$avd" -no-window -no-audio -no-boot-anim -no-snapshot \
	-gpu swiftshader_indirect -port 5554 -memory 4096 -no-metrics >"$log" 2>&1 &
emulator=$!
serial=emulator-5554
trap '"$adb" -s "$serial" emu kill >/dev/null 2>&1 || kill "$emulator" 2>/dev/null || true' EXIT

prebuild_log=$out/prebuild-$api.log
# Only what E2E_SHARD's flows build (mk/device.mk).
shard=${E2E_SHARD:-all}
(
	cd "$root/backend" && go test -count=1 -run '^$' ./internal/server
	if [ "$shard" = all ] || [ "$shard" = starter ]; then
		cd "$root/apps/starter" && flutter build apk --debug
	fi
	if [ "$shard" = all ] || [ "$shard" = hosts ]; then
		# The add-to-app host with the Gradle wrapper flutter pub get writes
		# into the module (TestAddToAppAgainstTheServer builds it again, with
		# the baseline it pulls).
		cd "$root/apps/add_to_app/plux_module" && flutter pub get
		cd "$root/apps/add_to_app/android_host" && ../plux_module/.android/gradlew --no-daemon --console=plain \
			-Ptarget-platform=android-x64 :app:assembleDebug :app:assembleDebugAndroidTest
	fi
) >"$prebuild_log" 2>&1 &
prebuild=$!

# fail <message>: the emulator's log, the devices adb sees, then the
# message.
fail() {
	echo "--- $log (last 80 lines)"; tail -n 80 "$log" || true
	echo "--- adb devices"; timeout 10 "$adb" devices -l || true
	echo "✗ $1"; exit 1
}

# Booted when sys.boot_completed is 1; never waits on a dead emulator or
# longer than ten minutes of wall time (adb wait-for-device would wait
# forever). The emulator can report its boot complete while adb's shell
# never answers on a stale connection (CI runs 36966274717, 37630248033:
# listed as a device, no answer for ten minutes); 30 seconds after the
# emulator's own report, adb reconnects to it every 30 seconds, and every
# second time restarts its server instead.
booted=false
deadline=$((SECONDS + 600))
answer=
reconnect_at=
reconnects=0
while [ "$SECONDS" -lt "$deadline" ]; do
	kill -0 "$emulator" 2>/dev/null || fail "the emulator exited while booting"
	answer=$(timeout 10 "$adb" -s "$serial" shell getprop sys.boot_completed 2>&1 | tr -d '\r') || true
	if [ "$answer" = 1 ]; then
		booted=true; break
	fi
	if grep -q 'Boot completed' "$log"; then
		if [ -z "$reconnect_at" ]; then
			reconnect_at=$((SECONDS + 30))
		elif [ "$SECONDS" -ge "$reconnect_at" ]; then
			reconnects=$((reconnects + 1))
			echo "adb has no answer from the booted emulator (last: ${answer:-nothing}); reconnecting ($reconnects)"
			if [ $((reconnects % 2)) -eq 1 ]; then
				timeout 10 "$adb" reconnect >/dev/null 2>&1 || true
			else
				timeout 10 "$adb" kill-server >/dev/null 2>&1 || true
				timeout 10 "$adb" start-server >/dev/null 2>&1 || true
			fi
			reconnect_at=$((SECONDS + 30))
		fi
	fi
	sleep 2
done
$booted || fail "the emulator did not boot in 10 minutes (adb's last answer: ${answer:-nothing})"
if ! wait "$prebuild"; then
	echo "--- $prebuild_log (last 80 lines)"; tail -n 80 "$prebuild_log" || true
	echo "✗ building the driver or the app failed"; exit 1
fi
for s in window_animation_scale transition_animation_scale animator_duration_scale; do
	"$adb" -s "$serial" shell settings put global "$s" 0
done
"$adb" -s "$serial" reverse "tcp:$port" "tcp:$port"
for p in $refapi_port $reference_ports; do
	"$adb" -s "$serial" reverse "tcp:$p" "tcp:$p"
done

PLUX_E2E_REFAPI_ADDR=127.0.0.1:$refapi_port PLUX_E2E_DEVICE=$serial make -C "$root" --no-print-directory e2e-starter 2>&1 | tee "$out/android-$api-$shard.log"
