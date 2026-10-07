#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Plux contributors
# SPDX-License-Identifier: Apache-2.0
#
# apt-install.sh [apt-get install options and packages]: installs packages
# on a CI runner without hanging on a stalled mirror. Every request times
# out after 30 seconds and is retried by apt; the whole update and install
# get three attempts, each bounded to ten minutes, 15 and 45 seconds apart.
# A plain apt-get hung for 27 minutes in CI run 37652814767.
set -euo pipefail

apt=(sudo apt-get -o Acquire::Retries=3 -o Acquire::http::Timeout=30 -o Acquire::https::Timeout=30)
for wait in 0 15 45; do
	sleep "$wait"
	if timeout 600 "${apt[@]}" update && timeout 600 "${apt[@]}" install -y --no-install-recommends "$@"; then
		exit 0
	fi
	echo "apt-get failed or timed out; trying again" >&2
done
echo "✗ apt-get install did not succeed in three attempts" >&2
exit 1
