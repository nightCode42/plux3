#!/bin/sh
# SPDX-FileCopyrightText: 2026 Plux contributors
# SPDX-License-Identifier: Apache-2.0
#
# Writes the third-party notices of Go binaries into <out-dir>: the Go
# distribution's licence, the licence and notice files of every module
# the binaries link, and those of the third-party files the server embeds
# (the WebAssembly image codecs and the icon fonts). The release archives
# and the images ship them next to the binaries (DEP-001). GOOS and GOARCH
# select the platform, as for `go build`; it fails if a module has no
# licence file, so nothing ships without its notice.
#
#   sh scripts/release/go-notices.sh <out-dir> <main-package>...
#
# Packages are relative to backend/, where the script runs `go list`.
set -eu

out=${1:?usage: go-notices.sh <out-dir> <main-package>...}
shift
root=$(cd "$(dirname "$0")/../.." && pwd)
mkdir -p "$out"
out=$(cd "$out" && pwd)
GO=${GO:-go}

cp "$($GO env GOROOT)/LICENSE" "$out/go.LICENSE"
list=$(mktemp)
trap 'rm -f "$list"' EXIT
(cd "$root/backend" && $GO list -deps -f '{{with .Module}}{{if not .Main}}{{.Path}}@{{.Version}} {{.Dir}}{{end}}{{end}}' "$@") | sort -u > "$list"
test -s "$list" || { echo "go-notices.sh: no modules" >&2; exit 1; }
while read -r mod dir; do
	name=$(printf '%s' "$mod" | tr '/' '_')
	found=
	for f in "$dir"/LICENSE* "$dir"/LICENCE* "$dir"/License* "$dir"/Licence* "$dir"/license* "$dir"/licence* "$dir"/COPYING* "$dir"/NOTICE*; do
		[ -f "$f" ] || continue
		cp "$f" "$out/$name.$(basename "$f")"
		found=1
	done
	[ -n "$found" ] || { echo "go-notices.sh: $mod has no licence file" >&2; exit 1; }
done < "$list"

# Third-party files embedded with go:embed, by the package embedding them.
deps=$(cd "$root/backend" && $GO list -deps "$@")
embeds="internal/compiler/media:backend/internal/compiler/media/codecs/THIRD_PARTY_NOTICES.txt
internal/icons/fonts:backend/internal/icons/fonts/LICENSE-material-design-icons
internal/icons/fonts:backend/internal/icons/fonts/LICENSE-cupertino-icons"
for e in $embeds; do
	pkg=github.com/nightCode42/plux3/backend/${e%%:*}
	file=${e#*:}
	if printf '%s\n' "$deps" | grep -qx "$pkg"; then
		cp "$root/$file" "$out/$(basename "$file")"
	fi
done
