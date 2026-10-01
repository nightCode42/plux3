// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Builds `plux_native` — memory maps and the vendored zstd decoder — from
/// source for every target the Flutter tool asks for (ADR-0030).
library;

import 'package:code_assets/code_assets.dart';
import 'package:hooks/hooks.dart';
import 'package:native_toolchain_c/native_toolchain_c.dart';

/// The zstd sources the decoder needs (native/zstd/VERSION).
const _zstd = [
  'native/zstd/common/debug.c',
  'native/zstd/common/entropy_common.c',
  'native/zstd/common/error_private.c',
  'native/zstd/common/fse_decompress.c',
  'native/zstd/common/xxhash.c',
  'native/zstd/common/zstd_common.c',
  'native/zstd/decompress/huf_decompress.c',
  'native/zstd/decompress/zstd_ddict.c',
  'native/zstd/decompress/zstd_decompress.c',
  'native/zstd/decompress/zstd_decompress_block.c',
];

void main(List<String> args) async {
  await build(args, (input, output) async {
    if (!input.config.buildCodeAssets) return;
    final os = input.config.code.targetOS;
    final apple = os == OS.iOS || os == OS.macOS;
    await CBuilder.library(
      name: 'plux_native',
      assetName: 'src/native/plux_native.dart',
      sources: ['native/plux_native.c', ..._zstd],
      includes: ['native'],
      defines: {
        // Only zstd's public API of this library is Plux's own three
        // functions; zstd's symbols stay hidden.
        'ZSTDLIB_VISIBLE': '',
        'ZSTDERRORLIB_VISIBLE': '',
        'ZSTD_LEGACY_SUPPORT': '0',
        'ZSTD_DISABLE_ASM': '1',
        'ZSTD_TRACE': '0',
      },
      flags: [
        '-fvisibility=hidden',
        '-ffunction-sections',
        '-fdata-sections',
        if (apple) '-Wl,-dead_strip' else '-Wl,--gc-sections',
        // Google Play requires 16 KiB page alignment for Android 15+.
        if (os == OS.android) '-Wl,-z,max-page-size=16384',
      ],
      optimizationLevel: OptimizationLevel.o2,
    ).run(input: input, output: output);
  });
}
