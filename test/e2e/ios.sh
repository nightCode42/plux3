#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Plux contributors
# SPDX-License-Identifier: Apache-2.0
#
# The starter app's end-to-end flows on an iOS simulator (QA-006), and the
# Swift add-to-app host's (HST-033), run by `make e2e-ios` in CI. See
# test/e2e/README.md.
#
# Creates and boots an iPhone simulator on the newest iOS runtime Xcode
# has, and runs the Go driver with PLUX_E2E_DEVICE and PLUX_E2E_XCTEST:
# it builds the app for the simulator with integration_test/app_test.dart
# as its entry point and runs it under XCTest (`xcodebuild test`,
# ios/RunnerTests), which reports the flows' results without the Dart VM
# service connection flutter test needs (ADR-0035). The add-to-app host
# gets the module's pods with CocoaPods and runs its XCUITests. The
# simulator shares this machine's network, so the test server is on its
# loopback address. Needs Xcode, CocoaPods and jq.
set -euo pipefail

root=$(git rev-parse --show-toplevel)
out=${E2E_OUT:-$root/build/e2e}
mkdir -p "$out"
# GitHub's macOS images have CocoaPods; elsewhere Homebrew installs it.
command -v pod >/dev/null || brew install --quiet cocoapods

# The newest available iOS runtime (of major version E2E_IOS_MAJOR when
# set, e.g. 18), and the newest iPhone it supports (the iPhone family also
# lists iPod touch models, which newer runtimes refuse).
runtimes=$(xcrun simctl list runtimes --json)
runtime=$(jq -r --arg m "${E2E_IOS_MAJOR:-}" '[.runtimes[] | select(.isAvailable and .platform == "iOS")
	| select($m == "" or (.version | startswith($m + ".")))] | last | .identifier' <<<"$runtimes")
[ -n "$runtime" ] && [ "$runtime" != null ] || {
	echo "✗ no available iOS ${E2E_IOS_MAJOR:-} simulator runtime; installed:"
	jq -r '.runtimes[] | select(.platform == "iOS") | "  \(.version) \(.isAvailable)"' <<<"$runtimes"
	exit 1
}
type=$(jq -r --arg r "$runtime" '[.runtimes[] | select(.identifier == $r) | .supportedDeviceTypes[]
	| select(.name | test("^iPhone [0-9]+$"))] | sort_by(.name | ltrimstr("iPhone ") | tonumber)
	| last | .identifier' <<<"$runtimes")
[ -n "$type" ] && [ "$type" != null ] || { echo "✗ $runtime supports no iPhone"; exit 1; }
echo "Simulator: $type on $runtime"
udid=$(xcrun simctl create plux-e2e "$type" "$runtime")
trap 'xcrun simctl shutdown "$udid" >/dev/null 2>&1 || true; xcrun simctl delete "$udid" >/dev/null 2>&1 || true' EXIT
xcrun simctl boot "$udid"
# While the simulator boots, a few minutes on a cold runner: the server's
# tests, and the apps of E2E_SHARD's flows (mk/device.mk) built as the flows
# build them, so the flows' own builds, with their defines, reuse the native
# part (ADR-0043, Revision).
# Only the first flow's app: building a second one here took longer than
# the boot it hides (run 37633642167).
shard=${E2E_SHARD:-all}
case "${shard%%+*}" in
starter) apps=starter ;;
bank) apps=plux_bank ;;
express) apps=plux_express ;;
*) apps= ;;
esac
prebuild_log=$out/prebuild-ios.log
(
	cd "$root/backend" && go test -count=1 -run '^$' ./internal/server
	for app in $apps; do
		(cd "$root/apps/$app" && flutter build ios --simulator --debug --target=integration_test/app_test.dart)
	done
) >"$prebuild_log" 2>&1 &
prebuild=$!
xcrun simctl bootstatus "$udid" -b >/dev/null
if ! wait "$prebuild"; then
	echo "--- $prebuild_log (last 80 lines)"; tail -n 80 "$prebuild_log" || true
	echo "✗ building the server's tests or the app failed"; exit 1
fi

status=0
xcresult=$out/ios.xcresult
rm -rf "$xcresult"
PLUX_E2E_DEVICE_TIMEOUT=30m PLUX_E2E_XCTEST=1 PLUX_E2E_XCRESULT=$xcresult PLUX_E2E_DEVICE=$udid \
	make -C "$root" --no-print-directory e2e-starter 2>&1 | tee "$out/ios.log" || status=$?
if [ "$status" -ne 0 ]; then
	# Why the flows failed, from the test results, and the apps' own logs.
	# The starter's flows, the generated project's launch test and each
	# add-to-app flow write a result bundle.
	for bundle in "$xcresult" "${xcresult%.xcresult}"-*.xcresult; do
		[ -d "$bundle" ] || continue
		echo "--- test results: $(basename "$bundle")"
		xcrun xcresulttool get test-results tests --path "$bundle" 2>&1 \
			| jq -r '.. | objects | select(.result? == "Failed") | "\(.name): \(.details // "" | tostring)"' 2>/dev/null \
			|| echo "(no result bundle)"
		xcrun xcresulttool get test-results tests --path "$bundle" 2>&1 | grep -i -A3 'failure\|message' | head -n 60 || true
	done
	echo "--- the apps' simulator log (last 100 lines of 45 minutes)"
	xcrun simctl spawn "$udid" log show --last 45m --style compact \
		--predicate 'process == "Runner" OR process == "HostApp"' >"$out/ios-app.log" 2>&1 || true
	grep -v 'Metal\|RunningBoard' "$out/ios-app.log" | tail -n 100
fi
exit "$status"
