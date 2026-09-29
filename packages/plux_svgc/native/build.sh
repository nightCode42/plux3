#!/bin/sh
# SPDX-FileCopyrightText: 2026 Plux contributors
# SPDX-License-Identifier: Apache-2.0
#
# Builds libpath_ops.so, the Skia path operations plux-svgc's optimisers
# call through FFI (CMP-031, ADR-0027 Revision), from source: the Flutter
# engine's path_ops wrapper at the pinned Flutter version and the Skia
# sources it needs at the revision that Flutter pins. The library links
# the C++ runtime statically and needs only glibc; built on Debian 12, it
# runs on the server image's distroless Debian 12 base. The server image
# build runs it (backend/Dockerfile); `sh build.sh <out-dir>` runs it
# anywhere with git, curl and a C++20 compiler.
set -eu

SKIA_URL=https://github.com/google/skia
SKIA_COMMIT=8df24be66531469e576a806749a0202ae26b8d08 # the revision Flutter 3.47.5 pins
FLUTTER_VERSION=3.47.5

out=${1:?usage: build.sh <out-dir>}
work=${WORK:-$(mktemp -d)}
CXX=${CXX:-g++}
mkdir -p "$out" "$work"

if [ ! -d "$work/skia/.git" ]; then
	git init -q "$work/skia"
	git -C "$work/skia" sparse-checkout set --no-cone /include /src/core /src/pathops /src/ports /src/base /src/utils
	git -C "$work/skia" fetch -q --depth 1 --filter=blob:none "$SKIA_URL" "$SKIA_COMMIT"
	git -C "$work/skia" checkout -q FETCH_HEAD
fi
test "$(git -C "$work/skia" rev-parse HEAD)" = "$SKIA_COMMIT" || { echo "skia is not at $SKIA_COMMIT" >&2; exit 1; }

wrapper=https://raw.githubusercontent.com/flutter/flutter/$FLUTTER_VERSION/engine/src/flutter/tools/path_ops
curl -sSfL -o "$work/path_ops.cc" "$wrapper/path_ops.cc"
curl -sSfL -o "$work/path_ops.h" "$wrapper/path_ops.h"
# The wrapper includes Skia as third_party/skia.
mkdir -p "$work/inc/third_party"
ln -sfn "$work/skia" "$work/inc/third_party/skia"

# The Skia sources path operations reach, besides src/pathops.
core="core/SkPath core/SkPathBuilder core/SkGeometry core/SkMatrix core/SkRect
core/SkPoint core/SkRRect core/SkArenaAlloc core/SkPathData core/SkPathIter
core/SkPathPriv core/SkMalloc core/SkTDArray core/SkContainers core/SkDebug
core/SkLog core/SkEdgeClipper core/SkIDChangeListener core/SkPathRaw
core/SkPathRawShapes core/SkBezierCurves core/SkCubics core/SkLineClipper
core/SkSafeMath core/SkSemaphore core/SkQuads core/SkFloatingPoint
ports/SkMemory_malloc ports/SkLog_stdio"

flags="-O2 -fPIC -std=c++20 -DNDEBUG -DSK_RELEASE -fvisibility=hidden
-fvisibility-inlines-hidden -ffunction-sections -fdata-sections -fno-exceptions
-fno-rtti -ffile-prefix-map=$work=. -I$work/skia -I$work/inc"
mkdir -p "$work/obj"
objs=""
for src in "$work/path_ops.cc" "$work"/skia/src/pathops/*.cpp $(for f in $core; do echo "$work/skia/src/$f.cpp"; done); do
	obj="$work/obj/$(basename "$src").o"
	# shellcheck disable=SC2086 # flags is a list of words
	$CXX $flags -w -c "$src" -o "$obj"
	objs="$objs $obj"
done
# shellcheck disable=SC2086 # objs is a list of words
$CXX -shared -static-libstdc++ -static-libgcc -Wl,--gc-sections -Wl,--exclude-libs,ALL \
	-Wl,--no-undefined -Wl,--build-id=none -Wl,-s -o "$out/libpath_ops.so" $objs -lm -lpthread
ls -l "$out/libpath_ops.so"
