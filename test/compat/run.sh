#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Plux contributors
# SPDX-License-Identifier: Apache-2.0
#
# The compatibility matrix (QA-010), run by `make compat`: the starter
# app's end-to-end flows (backend/internal/server/starter_e2e_integration_test.go)
# for
#   - today's runtime against today's server and compiler;
#   - each of the last three released runtimes (tags plux_flutter/v*)
#     against today's server and the bundles today's compiler writes;
#   - today's runtime against each of the last three released servers
#     (tags backend/v*), whose compilers write the bundles.
# A released side is checked out from its tag into a temporary worktree.
# Needs PLUX_TEST_DATABASE_URL, Go and Flutter. See test/compat/README.md.
set -euo pipefail

root=$(git rev-parse --show-toplevel)
flutter=$(command -v flutter)
work=$(mktemp -d)
cleanup() {
	for wt in "$work"/*/; do
		[ -d "$wt" ] && git -C "$root" worktree remove --force "$wt" >/dev/null 2>&1 || true
	done
	rm -rf "$work"
}
trap cleanup EXIT

# e2e <backend dir> <starter dir> <label>
e2e() {
	echo "── $3"
	(cd "$1" && PLUX_E2E_FLUTTER="$flutter" PLUX_E2E_STARTER_DIR="$2" \
		go test -count=1 -run TestStarterAppAgainstTheServer ./internal/server)
}

# The last three releases of a component, newest first.
releases() {
	git -C "$root" tag --list "$1/v*" --sort=-version:refname | head -n 3
}

# checkout <tag>: a worktree of the tag; prints its directory.
checkout() {
	local dir="$work/${1//\//-}"
	git -C "$root" worktree add --detach --quiet "$dir" "$1"
	echo "$dir"
}

e2e "$root/backend" "$root/apps/starter" "runtime HEAD, server HEAD"
runs=1
for tag in $(releases plux_flutter); do
	dir=$(checkout "$tag")
	e2e "$root/backend" "$dir/apps/starter" "runtime $tag, server HEAD"
	runs=$((runs + 1))
done
for tag in $(releases backend); do
	dir=$(checkout "$tag")
	e2e "$dir/backend" "$root/apps/starter" "runtime HEAD, server $tag"
	runs=$((runs + 1))
done
echo "✓ compatibility matrix: all $runs runs passed"
if [ "$runs" -lt 7 ]; then
	echo "  (fewer than three releases of plux_flutter or backend are tagged: the matrix grows with each release)"
fi
