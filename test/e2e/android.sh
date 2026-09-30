#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Plux contributors
# SPDX-License-Identifier: Apache-2.0
#
# The starter app's end-to-end flows on an Android emulator (QA-006), run
# by `make e2e-android` in CI. See test/e2e/README.md.
#
#   android.sh [api]   API level of the system image (default 35)
#
# E2E_VARIANT=emulator boots an image current emulators do not (API 24
# never finished booting, even given 20 minutes, more cores and memory: CI
# runs 36719678834, 36729487188, 36740007227):
#   emulator  an older emulator build by ID (E2E_EMULATOR_BUILD, default
#             34.1.19), checked against E2E_EMULATOR_SHA256 when set
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
variant=${E2E_VARIANT:-}
emulator_bin=$sdk/emulator/emulator
flags=(-memory 4096 -no-metrics)
boot_seconds=600
avd=plux_e2e_$api
# The test server's address (backend/internal/server/starter_e2e_integration_test.go).
port=18094
mkdir -p "$out"

yes | "$tools/sdkmanager" --licenses >/dev/null || true
# A download that arrives broken ("Error on ZipFile unknown archive", CI
# run 36743012409) is fetched once more before the job gives up.
"$tools/sdkmanager" --install emulator platform-tools "$image" >"$out/sdkmanager.log" ||
	"$tools/sdkmanager" --install emulator platform-tools "$image" >>"$out/sdkmanager.log"
# One AVD home for avdmanager and the emulator, whatever the runner sets.
export ANDROID_AVD_HOME=${ANDROID_AVD_HOME:-$HOME/.android/avd}
mkdir -p "$ANDROID_AVD_HOME"
case "$variant" in
"") ;;
emulator)
	# Google's repository lists only current emulator builds (35 and
	# later), but older ones stay downloadable by build ID. The default is
	# 34.1.19; its SHA-256 is printed so it can be pinned in
	# E2E_EMULATOR_SHA256 once it has booted API 24, and checked when set.
	build=${E2E_EMULATOR_BUILD:-11525734}
	curl -fsS "https://dl.google.com/android/repository/emulator-linux_x64-$build.zip" -o "$out/emulator.zip"
	sum=$(sha256sum "$out/emulator.zip" | cut -d' ' -f1)
	if [ -n "${E2E_EMULATOR_SHA256:-}" ] && [ "$sum" != "$E2E_EMULATOR_SHA256" ]; then
		echo "✗ emulator build $build: SHA-256 $sum, expected $E2E_EMULATOR_SHA256"; exit 1
	fi
	rm -rf "$out/emulator" && unzip -q "$out/emulator.zip" -d "$out"
	echo "Emulator build $build: $(grep -h '^Pkg.Revision' "$out/emulator/source.properties" 2>/dev/null), SHA-256 $sum"
	emulator_bin=$out/emulator/emulator
	flags=(-memory 4096) # 34.x has no -no-metrics
	# It finds the SDK's system images through the environment.
	export ANDROID_SDK_ROOT=$sdk ANDROID_HOME=$sdk
	;;
*) echo "✗ unknown E2E_VARIANT $variant"; exit 2 ;;
esac
echo no | "$tools/avdmanager" create avd --force --name "$avd" --package "$image" --device pixel_6 >/dev/null
[ -r /dev/kvm ] && [ -w /dev/kvm ] || echo "⚠ /dev/kvm is not usable: the emulator runs without acceleration"

adb=$sdk/platform-tools/adb
log=$out/emulator-$api${variant:+-$variant}.log
"$emulator_bin" -avd "$avd" -no-window -no-audio -no-boot-anim -no-snapshot \
	-gpu swiftshader_indirect -port 5554 "${flags[@]}" >"$log" 2>&1 &
emulator=$!
serial=emulator-5554
trap '"$adb" -s "$serial" emu kill >/dev/null 2>&1 || kill "$emulator" 2>/dev/null || true' EXIT

# fail <message>: the emulator's log, then the message.
fail() {
	echo "--- $log (last 80 lines)"; tail -n 80 "$log" || true
	echo "✗ $1"; exit 1
}

# Booted when sys.boot_completed is 1; never waits on a dead emulator or
# longer than boot_seconds of wall time (adb wait-for-device would wait
# forever).
booted=false
deadline=$((SECONDS + boot_seconds))
while [ "$SECONDS" -lt "$deadline" ]; do
	kill -0 "$emulator" 2>/dev/null || fail "the emulator exited while booting"
	if [ "$(timeout 10 "$adb" -s "$serial" shell getprop sys.boot_completed 2>/dev/null | tr -d '\r')" = 1 ]; then
		booted=true; break
	fi
	sleep 2
done
$booted || fail "the emulator did not boot in $((boot_seconds / 60)) minutes"
for s in window_animation_scale transition_animation_scale animator_duration_scale; do
	"$adb" -s "$serial" shell settings put global "$s" 0
done
"$adb" -s "$serial" reverse "tcp:$port" "tcp:$port"

PLUX_E2E_DEVICE=$serial make -C "$root" --no-print-directory e2e-starter 2>&1 | tee "$out/android-$api${variant:+-$variant}.log"
