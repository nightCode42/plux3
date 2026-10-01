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
xcrun simctl bootstatus "$udid" -b >/dev/null

status=0
# A healthy run, Xcode build included, takes about ten minutes; a hang
# waiting for the Dart VM service (runs 36719678834, 36729487188,
# 36735134286, 36743012409) is stopped after 15.
PLUX_E2E_DEVICE_TIMEOUT=15m PLUX_E2E_VERBOSE=1 PLUX_E2E_DEVICE=$udid make -C "$root" --no-print-directory e2e-starter 2>&1 | tee "$out/ios.log" || status=$?
if [ "$status" -ne 0 ]; then
	# The app's own log since before its launch: whether the Dart VM
	# service started (flutter test waits for its address in this log), and
	# what became of the app, since the log of a hung run ends within a
	# second of the launch: a crash report, the system's word on the
	# process, and whether it still runs.
	xcrun simctl spawn "$udid" log show --last 45m --style compact \
		--predicate 'process == "Runner"' >"$out/ios-app.log" 2>&1 || true
	echo "--- the app's lines about the Dart VM service (none: it never reported one)"
	grep -i 'vm service\|observatory\|dartvm\|plux-e2e' "$out/ios-app.log" || echo "(none)"
	echo "--- is the app running?"
	xcrun simctl spawn "$udid" launchctl list 2>&1 | grep -i 'dev.plux' || echo "(no: the app is gone)"
	echo "--- crash reports"
	reports=$(find "$HOME/Library/Logs/DiagnosticReports" "$HOME/Library/Developer/CoreSimulator/Devices/$udid/data/Library/Logs" \
		\( -name 'Runner*.ips' -o -name 'Runner*.crash' \) 2>/dev/null || true)
	[ -n "$reports" ] || echo "(none)"
	for r in $reports; do
		echo "$r"
		cp "$r" "$out/" || true
		# An .ips file is a JSON header line and a JSON report.
		tail -n +2 "$r" | jq -r '. as $r | "\(.exception // {} | tostring)\n\(.termination // {} | tostring)\n\(.asi // {} | tostring)",
			([.threads[.faultingThread // 0].frames[:25][] | "  \(.symbol // "?") in \($r.usedImages[.imageIndex].name // "?")"] | join("\n"))' 2>/dev/null \
			|| head -n 60 "$r"
	done
	echo "--- what the system logged about the app's process"
	log show --last 45m --style compact --predicate 'eventMessage CONTAINS "dev.plux.pluxStarter" AND NOT process == "Runner"' 2>&1 \
		| grep -i 'terminat\|crash\|exit\|jetsam\|kill\|remov\|invalid\|suspend' | tail -n 40 || true
	echo "--- the app's simulator log (last 60 lines of 45 minutes)"
	tail -n 60 "$out/ios-app.log"
fi
exit "$status"
