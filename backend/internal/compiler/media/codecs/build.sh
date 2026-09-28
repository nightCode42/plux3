#!/bin/sh
# SPDX-FileCopyrightText: 2026 Plux contributors
# SPDX-License-Identifier: Apache-2.0
#
# Builds the WebAssembly image codecs the asset pipeline runs on wazero
# (CMP-030, ADR-0027): webp.wasm from libwebp, avif.wasm from libavif and
# libaom. Sources are fetched at pinned commits and verified; the build is
# single-threaded and reproducible for the pinned toolchain, and
# codecs.lock records the SHA-256 of each module. `make wasm-codecs`
# runs it; `make wasm-codecs-check` rebuilds and compares.
#
# Toolchain (Ubuntu 24.04): clang-18, lld-18, libclang-rt-18-dev-wasm32,
# wasi-libc 0.0~git20230113.4362b18-3, cmake, ninja-build.
set -eu

LIBWEBP_URL=https://chromium.googlesource.com/webm/libwebp
LIBWEBP_COMMIT=a4d7a715337ded4451fec90ff8ce79728e04126c # v1.5.0
AOM_URL=https://aomedia.googlesource.com/aom
AOM_COMMIT=10aece4157eb79315da205f39e19bf6ab3ee30d0     # v3.12.1
LIBAVIF_URL=https://github.com/AOMediaCodec/libavif
LIBAVIF_COMMIT=1aadfad932c98c069a1204261b1856f81f3bc199 # v1.3.0

here=$(cd "$(dirname "$0")" && pwd)
out=${1:-$here}
work=${WORK:-$(mktemp -d)}
mkdir -p "$work"

fetch() { # name url commit
	if [ ! -d "$work/$1/.git" ]; then
		git init -q "$work/$1"
		git -C "$work/$1" fetch -q --depth 1 "$2" "$3" || git -C "$work/$1" fetch -q --depth 1 "${MIRROR_PREFIX:-}$2" "$3"
		git -C "$work/$1" checkout -q FETCH_HEAD
	fi
	test "$(git -C "$work/$1" rev-parse HEAD)" = "$3" || { echo "$1 is not at $3" >&2; exit 1; }
}
fetch libwebp "$LIBWEBP_URL" "$LIBWEBP_COMMIT"
fetch aom "$AOM_URL" "$AOM_COMMIT"
fetch libavif "$LIBAVIF_URL" "$LIBAVIF_COMMIT"
# libaom names its version from `git describe`, which depends on how the
# checkout was made (with or without tags, which Git, which mirror), not on
# the source. Without .git it reads the pinned CHANGELOG instead, so every
# build embeds the same version string. The commit was verified above.
rm -rf "$work/aom/.git"

sysroot="$work/sysroot"
mkdir -p "$sysroot/lib"
ln -sfn /usr/include/wasm32-wasi "$sysroot/include"
ln -sfn /usr/lib/wasm32-wasi "$sysroot/lib/wasm32-wasi"
cflags="--target=wasm32-wasi --sysroot=$sysroot -isystem $here/include -O2 -DNDEBUG -ffile-prefix-map=$work=/src -ffile-prefix-map=$here=/plux"
ldflags="-nostartfiles -mexec-model=reactor -Wl,--no-entry -Wl,--strip-all"

cat > "$work/wasi.cmake" <<CMAKE
set(CMAKE_SYSTEM_NAME WASI)
set(CMAKE_SYSTEM_PROCESSOR wasm32)
set(CMAKE_C_COMPILER clang)
set(CMAKE_CXX_COMPILER clang++)
set(CMAKE_C_COMPILER_TARGET wasm32-wasi)
set(CMAKE_CXX_COMPILER_TARGET wasm32-wasi)
set(CMAKE_SYSROOT $sysroot)
set(CMAKE_C_FLAGS_INIT "-isystem $here/include -ffile-prefix-map=$work=/src")
set(CMAKE_FIND_ROOT_PATH_MODE_PROGRAM NEVER)
set(CMAKE_TRY_COMPILE_TARGET_TYPE STATIC_LIBRARY)
CMAKE

# libwebp: the encoder and decoder, portable C only.
(
	cd "$work/libwebp"
	src=$(ls src/enc/*.c src/dec/*.c src/dsp/*.c src/utils/*.c sharpyuv/*.c | grep -v -E '_(sse2|sse41|neon|msa|mips32|mips_dsp_r2|avx2)\.c$' | sort)
	# shellcheck disable=SC2086
	clang $cflags -Isrc -I. $ldflags -o "$out/webp.wasm" $src "$here/plux_webp.c"
)

# libaom (encoder only, generic C, one thread), then libavif on it.
cmake -S "$work/aom" -B "$work/aom-build" -G Ninja -DCMAKE_TOOLCHAIN_FILE="$work/wasi.cmake" \
	-DCMAKE_BUILD_TYPE=Release -DAOM_TARGET_CPU=generic -DCONFIG_MULTITHREAD=0 -DCONFIG_AV1_DECODER=0 \
	-DCONFIG_RUNTIME_CPU_DETECT=0 -DCONFIG_WEBM_IO=0 -DENABLE_TESTS=0 -DENABLE_TOOLS=0 \
	-DENABLE_EXAMPLES=0 -DENABLE_DOCS=0 -DENABLE_TESTDATA=0 >/dev/null
ninja -C "$work/aom-build" aom >/dev/null
cmake -S "$work/libavif" -B "$work/avif-build" -G Ninja -DCMAKE_TOOLCHAIN_FILE="$work/wasi.cmake" \
	-DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF -DAVIF_CODEC_AOM=SYSTEM -DAVIF_CODEC_AOM_DECODE=OFF \
	-DAVIF_CODEC_AOM_ENCODE=ON -DAVIF_LIBYUV=OFF -DAVIF_LIBSHARPYUV=OFF -DAVIF_LIBXML2=OFF \
	-DAVIF_BUILD_APPS=OFF -DAVIF_BUILD_TESTS=OFF -DAVIF_ENABLE_WERROR=OFF \
	-DAOM_INCLUDE_DIR="$work/aom" -DAOM_LIBRARY="$work/aom-build/libaom.a" \
	-DCMAKE_C_FLAGS="-isystem $here/include -I$work/aom-build -ffile-prefix-map=$work=/src" >/dev/null
ninja -C "$work/avif-build" >/dev/null
# shellcheck disable=SC2086
clang $cflags -I"$work/libavif/include" $ldflags -o "$out/avif.wasm" "$here/plux_avif.c" \
	"$work/avif-build/libavif.a" "$work/aom-build/libaom.a" -lm

(cd "$out" && sha256sum webp.wasm avif.wasm)
