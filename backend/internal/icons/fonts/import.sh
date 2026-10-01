#!/bin/sh
# SPDX-FileCopyrightText: 2026 Plux contributors
# SPDX-License-Identifier: Apache-2.0
#
# Imports the icon fonts the server subsets for each release (THM-005,
# ADR-0032 § Icons) and the name tables the compiler checks icon names
# against:
#
#   MaterialSymbolsOutlined.ttf, material.codepoints
#       Material Symbols Outlined, the variable font (FILL, GRAD, opsz,
#       wght) and its name list, verbatim from google/material-design-icons.
#   CupertinoIcons.ttf
#       The font of the cupertino_icons package, verbatim.
#   cupertino.codepoints
#       The names Flutter's CupertinoIcons class gives that font's glyphs.
#   mirrored.txt
#       The icons Flutter mirrors in right-to-left text (matchTextDirection),
#       by set: Flutter's Material icon names carry over to Material
#       Symbols, which uses the same names.
#
# Sources are pinned and verified by icons.lock. Run it only to move a pin:
# `go generate ./internal/icons` then regenerates the name tables.
set -eu

MATERIAL_COMMIT=bd8cb85bd4bad964fe6918f79665bb40c3a8efef
CUPERTINO_ICONS_VERSION=1.0.9
FLUTTER_VERSION=3.47.5

here=$(cd "$(dirname "$0")" && pwd)
work=${WORK:-$(mktemp -d)}
mkdir -p "$work"
raw=https://raw.githubusercontent.com

variable="$raw/google/material-design-icons/$MATERIAL_COMMIT/variablefont/MaterialSymbolsOutlined%5BFILL,GRAD,opsz,wght%5D"
curl -sSfL -o "$here/MaterialSymbolsOutlined.ttf" "$variable.ttf"
curl -sSfL -o "$here/material.codepoints" "$variable.codepoints"
curl -sSfL -o "$here/LICENSE-material-design-icons" "$raw/google/material-design-icons/$MATERIAL_COMMIT/LICENSE"

curl -sSfL -o "$work/cupertino_icons.tar.gz" "https://pub.dev/api/archives/cupertino_icons-$CUPERTINO_ICONS_VERSION.tar.gz"
mkdir -p "$work/cupertino_icons"
tar -xzf "$work/cupertino_icons.tar.gz" -C "$work/cupertino_icons"
cp "$work/cupertino_icons/assets/CupertinoIcons.ttf" "$here/CupertinoIcons.ttf"
cp "$work/cupertino_icons/LICENSE" "$here/LICENSE-cupertino-icons"

flutter="$raw/flutter/flutter/$FLUTTER_VERSION/packages/flutter/lib/src"
curl -sSfL -o "$work/cupertino.dart" "$flutter/cupertino/icons.dart"
curl -sSfL -o "$work/material.dart" "$flutter/material/icons.dart"

# Each `static const IconData <name> = IconData(0x<code>, …);` declaration,
# as "<name> <code> <mirrored>".
declarations() {
	awk '
		/static const IconData [a-z0-9_]+ = IconData\(/ { name = $4; code = ""; mirror = 0; open = 1 }
		open && match($0, /0x[0-9a-fA-F]+/) && code == "" { code = tolower(substr($0, RSTART + 2, RLENGTH - 2)) }
		open && /matchTextDirection: true/ { mirror = 1 }
		open && /\);/ { print name, code, mirror; open = 0 }
	' "$1"
}
declarations "$work/cupertino.dart" | awk '{ print $1, $2 }' | LC_ALL=C sort > "$here/cupertino.codepoints"
{
	declarations "$work/cupertino.dart" | awk '$3 == 1 { print "cupertino", $1 }'
	declarations "$work/material.dart" | awk '$3 == 1 { print "material", $1 }'
} | LC_ALL=C sort -u > "$here/mirrored.txt"

cd "$here"
sha256sum -c icons.lock
