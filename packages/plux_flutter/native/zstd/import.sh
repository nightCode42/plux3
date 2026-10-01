#!/bin/sh
# SPDX-FileCopyrightText: 2026 Plux contributors
# SPDX-License-Identifier: Apache-2.0
#
# Re-imports the decompression-only zstd sources vendored here (ADR-0030)
# from the pinned release, after checking that the tag is the pinned
# commit. Run from anywhere; needs git. Review the diff before committing.
set -eu

TAG=v1.5.7
COMMIT=f8745da6ff1ad1e7bab384bd1f9d742439278e99
REPO=${ZSTD_REPO:-https://github.com/facebook/zstd.git}

here=$(cd "$(dirname "$0")" && pwd)
src=$(mktemp -d)
trap 'rm -rf "$src"' EXIT

git -c advice.detachedHead=false clone --quiet --depth 1 --branch "$TAG" "$REPO" "$src"
if [ "$(git -C "$src" rev-parse HEAD)" != "$COMMIT" ]; then
	echo "zstd tag $TAG is not commit $COMMIT" >&2
	exit 1
fi

for dir in common decompress; do
	rm -rf "${here:?}/$dir"
	mkdir -p "$here/$dir"
done
for f in allocations.h bits.h bitstream.h compiler.h cpu.h debug.c debug.h \
	entropy_common.c error_private.c error_private.h fse.h fse_decompress.c \
	huf.h mem.h portability_macros.h xxhash.c xxhash.h zstd_common.c \
	zstd_deps.h zstd_internal.h zstd_trace.h; do
	cp "$src/lib/common/$f" "$here/common/$f"
done
for f in huf_decompress.c zstd_ddict.c zstd_ddict.h zstd_decompress.c \
	zstd_decompress_block.c zstd_decompress_block.h zstd_decompress_internal.h; do
	cp "$src/lib/decompress/$f" "$here/decompress/$f"
done
cp "$src/lib/zstd.h" "$src/lib/zstd_errors.h" "$here/"
cp "$src/LICENSE" "$here/LICENSE"
printf 'zstd %s\ncommit %s\nsource %s\n' "$TAG" "$COMMIT" "$REPO" > "$here/VERSION"
echo "imported zstd $TAG ($COMMIT) into $here"
