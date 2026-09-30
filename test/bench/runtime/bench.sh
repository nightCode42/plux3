#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Plux contributors
# SPDX-License-Identifier: Apache-2.0
#
# The runtime benchmark (QA-007), run by `make bench-runtime` and
# `make bench-runtime-ab`. See test/bench/runtime/README.md.
#
#   bench.sh measure [runs]            this checkout, summarised
#   bench.sh compare <base-ref> [runs] this checkout against <base-ref>
#
# Both build the benchmark app in profile mode for Linux and run it under
# xvfb-run. compare builds the same app — this checkout's sources and
# embedded release — a second time against the runtime of <base-ref>,
# checked out into a temporary worktree, so the only difference between
# the two is the runtime; then tools/cmd/benchcmp runs them alternately
# and fails when this checkout is more than 10% slower. Results, logs and
# the stores are written to $BENCH_OUT (default build/bench-runtime).
set -euo pipefail

root=$(git rev-parse --show-toplevel)
app=$root/test/bench/runtime
out=${BENCH_OUT:-$root/build/bench-runtime}
work=$(mktemp -d)
cleanup() {
	[ -d "$work/base" ] && git -C "$root" worktree remove --force "$work/base" >/dev/null 2>&1 || true
	rm -rf "$work"
}
trap cleanup EXIT

# linux <app dir>: adds the Linux runner `flutter create` makes, which is
# not committed, unless the directory has one.
linux() {
	[ -d "$1/linux" ] && return
	(cd "$work" && flutter create --platforms=linux --project-name plux_bench_runtime \
		--org dev.plux --no-pub runner >/dev/null)
	cp -r "$work/runner/linux" "$1/linux"
	rm -rf "$work/runner"
}

# build <app dir>: prints the profile build's executable.
build() {
	linux "$1"
	(cd "$1" && flutter build linux --profile >&2)
	echo "$1/build/linux/x64/profile/bundle/plux_bench_runtime"
}

benchcmp() {
	(cd "$root/tools" && go build -o "$work/benchcmp" ./cmd/benchcmp)
	xvfb-run -a -s "-screen 0 1280x800x24" "$work/benchcmp" "$@"
}

mode=${1:-}
case "$mode" in
measure)
	head=$(build "$app")
	rm -rf "$out"
	benchcmp measure -app "$head" -runs "${2:-5}" -out "$out"
	;;
compare)
	base_ref=${2:?usage: bench.sh compare <base-ref> [runs]}
	git -C "$root" worktree add --detach "$work/base" "$base_ref" >/dev/null
	base_app=$work/base/test/bench/runtime
	rm -rf "$base_app"
	mkdir -p "$base_app"
	tar -C "$app" --exclude=./build --exclude=./linux --exclude=./.dart_tool -cf - . | tar -C "$base_app" -xf -
	# A base older than the benchmark does not list it in its workspace.
	if ! grep -q '^  - test/bench/runtime$' "$work/base/pubspec.yaml"; then
		printf '  - test/bench/runtime\n' >>"$work/base/pubspec.yaml"
	fi
	(cd "$work/base" && flutter pub get >/dev/null)
	base=$(build "$base_app")
	head=$(build "$app")
	rm -rf "$out"
	benchcmp run -base "$base" -head "$head" -runs "${3:-10}" -out "$out"
	;;
*)
	echo "usage: bench.sh measure [runs] | compare <base-ref> [runs]" >&2
	exit 2
	;;
esac
