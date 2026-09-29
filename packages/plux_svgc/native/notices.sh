#!/bin/sh
# SPDX-FileCopyrightText: 2026 Plux contributors
# SPDX-License-Identifier: Apache-2.0
#
# Writes the third-party notices of plux-svgc into <out-dir>: the Dart
# SDK's, whose runtime `dart compile exe` links in, and those of the
# packages it resolves, dev dependencies aside. The server image ships
# them with the binary (backend/Dockerfile); build.sh writes those of
# libpath_ops. Run it in packages/plux_svgc after `dart pub get`; it fails
# if a package has no licence file.
set -eu

out=${1:?usage: notices.sh <out-dir>}
mkdir -p "$out"
# DART_SDK, or the SDK of the dart on PATH (<sdk>/bin/dart).
sdk=${DART_SDK:-$(dirname "$(dirname "$(readlink -f "$(command -v dart)")")")}
cp "$sdk/LICENSE" "$out/dart-sdk.LICENSE"
list=$(mktemp)
trap 'rm -f "$list"' EXIT
dart pub deps --no-dev --style=list | sed -n 's/^- \([a-z0-9_]*\) \([0-9][^ ]*\)$/\1 \2/p' | sort -u > "$list"
test -s "$list" || { echo "notices.sh: no packages" >&2; exit 1; }
while read -r name version; do
	cp "${PUB_CACHE:-$HOME/.pub-cache}/hosted/pub.dev/$name-$version/LICENSE" "$out/$name-$version.LICENSE"
done < "$list"
