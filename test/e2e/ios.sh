#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Plux contributors
# SPDX-License-Identifier: Apache-2.0
#
# The starter app's end-to-end flows on an iOS simulator (QA-006), run by
# `make e2e-ios` in CI. See test/e2e/README.md.
#
# Creates and boots an iPhone simulator on the newest iOS runtime Xcode
# has, and runs the Go driver with PLUX_E2E_DEVICE, which builds the app
# for the simulator and runs integration_test/app_test.dart there. The
# simulator shares this machine's network, so the test server is on its
# loopback address. Needs Xcode and jq.
set -euo pipefail

root=$(git rev-parse --show-toplevel)
out=${E2E_OUT:-$root/build/e2e}
mkdir -p "$out"

# The newest available iOS runtime, and the newest iPhone it supports (the
# iPhone family also lists iPod touch models, which newer runtimes refuse).
runtimes=$(xcrun simctl list runtimes --json)
runtime=$(jq -r '[.runtimes[] | select(.isAvailable and .platform == "iOS")] | last | .identifier' <<<"$runtimes")
[ -n "$runtime" ] && [ "$runtime" != null ] || { echo "✗ Xcode has no iOS simulator runtime"; exit 1; }
type=$(jq -r --arg r "$runtime" '[.runtimes[] | select(.identifier == $r) | .supportedDeviceTypes[]
	| select(.name | test("^iPhone [0-9]+$"))] | sort_by(.name | ltrimstr("iPhone ") | tonumber)
	| last | .identifier' <<<"$runtimes")
[ -n "$type" ] && [ "$type" != null ] || { echo "✗ $runtime supports no iPhone"; exit 1; }
echo "Simulator: $type on $runtime"
udid=$(xcrun simctl create plux-e2e "$type" "$runtime")
trap 'xcrun simctl shutdown "$udid" >/dev/null 2>&1 || true; xcrun simctl delete "$udid" >/dev/null 2>&1 || true' EXIT
xcrun simctl boot "$udid"
xcrun simctl bootstatus "$udid" -b >/dev/null

status=0
PLUX_E2E_VERBOSE=1 PLUX_E2E_DEVICE=$udid make -C "$root" --no-print-directory e2e-starter 2>&1 | tee "$out/ios.log" || status=$?
if [ "$status" -ne 0 ]; then
	# The app's own log: whether the Dart VM service started (flutter test
	# waits for its address in this log), and any crash.
	echo "--- the app's simulator log (last 10 minutes)"
	xcrun simctl spawn "$udid" log show --last 10m --style compact \
		--predicate 'process == "Runner"' 2>&1 | tail -n 150 | tee "$out/ios-app.log"
fi
exit "$status"
