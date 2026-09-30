#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Plux contributors
# SPDX-License-Identifier: Apache-2.0
#
# The starter app's end-to-end flows on an Android emulator (QA-006), run
# by `make e2e-android` in CI. See test/e2e/README.md.
#
#   android.sh [api]   API level of the system image (default 35)
#
# E2E_VARIANT tries a way to boot an image current emulators do not (API 24
# never finished booting in CI runs 36719678834 and 36729487188):
#   slow      more time (20 minutes), cores and memory; no camera or Vulkan
#   emulator  the newest emulator build before 35 from Google's repository,
#             checked against the SHA-1 it lists; the build is printed so it
#             can be pinned
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
flags=(-memory 4096)
boot_seconds=600
avd=plux_e2e_$api
# The test server's address (backend/internal/server/starter_e2e_integration_test.go).
port=18094
mkdir -p "$out"

yes | "$tools/sdkmanager" --licenses >/dev/null || true
"$tools/sdkmanager" --install emulator platform-tools "$image" >"$out/sdkmanager.log"
# One AVD home for avdmanager and the emulator, whatever the runner sets.
export ANDROID_AVD_HOME=${ANDROID_AVD_HOME:-$HOME/.android/avd}
mkdir -p "$ANDROID_AVD_HOME"
case "$variant" in
"") ;;
slow)
	flags=(-memory 6144 -cores 4 -camera-back none -camera-front none -feature -Vulkan)
	boot_seconds=1200
	;;
emulator)
	# Google's repository lists the emulator packages with their archives
	# and SHA-1s (older builds only in the older list); take the newest
	# whose major version is below 35.
	for n in 1 3; do
		curl -fsS "https://dl.google.com/android/repository/repository2-$n.xml" -o "$out/repository2-$n.xml" || true
	done
	read -r version url sha1 < <(python3 - "$out"/repository2-*.xml <<'PY'
import re, sys
xml = "".join(open(f).read() for f in sys.argv[1:])
best = None
for pkg in re.findall(r'<remotePackage path="emulator">(.*?)</remotePackage>', xml, re.S):
    rev = re.search(r"<major>(\d+)</major>\s*<minor>(\d+)</minor>\s*<micro>(\d+)</micro>", pkg)
    if not rev or int(rev.group(1)) >= 35:
        continue
    for arc in re.findall(r"<archive>(.*?)</archive>", pkg, re.S):
        if "<host-os>linux</host-os>" not in arc:
            continue
        url = re.search(r"<url>(.*?)</url>", arc).group(1)
        sha1 = re.search(r'<checksum(?: type="sha1")?>(.*?)</checksum>', arc).group(1)
        version = tuple(int(x) for x in rev.groups())
        if best is None or version > best[0]:
            best = (version, url, sha1)
if best is None:
    sys.exit("no emulator build before 35 is listed")
print(".".join(map(str, best[0])), best[1], best[2])
PY
)
	echo "Emulator build: $version ($url, sha1 $sha1)"
	curl -fsS "https://dl.google.com/android/repository/$url" -o "$out/emulator.zip"
	echo "$sha1  $out/emulator.zip" | sha1sum -c -
	rm -rf "$out/emulator" && unzip -q "$out/emulator.zip" -d "$out"
	emulator_bin=$out/emulator/emulator
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
	-gpu swiftshader_indirect -port 5554 -no-metrics "${flags[@]}" >"$log" 2>&1 &
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
