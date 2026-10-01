// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Bindings to `plux_native`, built by `hook/build.dart` (ADR-0030). Only
/// `mmap/` and `zstd/` call these; they check every bound before a call.
///
/// The externals are private and called only from the wrappers in this
/// library: `.address` arguments need a trampoline that the compiler
/// generates with the call site, and an incremental compile (as
/// `flutter test` runs between test files) does not regenerate one in a
/// cached library for a call site in another.
library;

import 'dart:ffi';
import 'dart:typed_data';

/// A mapped file, as `plux_map` returns it.
final class NativeMapping extends Struct {
  /// The first mapped byte, or null for an empty file or an error.
  external Pointer<Uint8> address;

  /// The number of mapped bytes.
  @Uint64()
  external int length;

  /// The `errno` of a failure, else zero.
  @Int32()
  external int error;
}

@Native<Pointer<NativeMapping> Function(Pointer<Uint8>)>(
  symbol: 'plux_map',
  isLeaf: true,
)
external Pointer<NativeMapping> _map(Pointer<Uint8> path);

@Native<Void Function(Pointer<NativeMapping>)>(
  symbol: 'plux_release',
  isLeaf: true,
)
external void _release(Pointer<NativeMapping> mapping);

@Native<Int64 Function(Pointer<Uint8>, Uint64)>(
  symbol: 'plux_zstd_content_size',
  isLeaf: true,
)
external int _zstdContentSize(Pointer<Uint8> src, int length);

@Native<
  Int64 Function(
    Pointer<Uint8>,
    Uint64,
    Pointer<Uint8>,
    Uint64,
    Pointer<Uint8>,
    Uint64,
  )
>(symbol: 'plux_zstd_decompress', isLeaf: true)
external int _zstdDecompress(
  Pointer<Uint8> dst,
  int capacity,
  Pointer<Uint8> src,
  int length,
  Pointer<Uint8> dict,
  int dictLength,
);

@Native<Int32 Function(Pointer<Uint8>)>(symbol: 'plux_fsync_dir', isLeaf: true)
external int _fsyncDir(Pointer<Uint8> path);

/// `fsync` of the directory whose NUL-terminated UTF-8 name is [path];
/// returns 0 or the `errno` value.
int nativeFsyncDirectory(Uint8List path) => _fsyncDir(path.address);

/// Maps the file whose NUL-terminated UTF-8 name is [path] read-only;
/// null when out of memory.
Pointer<NativeMapping> nativeMap(Uint8List path) => _map(path.address);

/// Unmaps a mapping and frees its handle.
void nativeRelease(Pointer<NativeMapping> mapping) => _release(mapping);

/// The address of the release function, for a `NativeFinalizer`.
final Pointer<NativeFinalizerFunction> nativeReleaseAddress =
    Native.addressOf<NativeFunction<Void Function(Pointer<NativeMapping>)>>(
      _release,
    ).cast();

/// The content size a zstd frame declares; -1 when it declares none, -2
/// when it is not a zstd frame.
int nativeZstdContentSize(Uint8List src) =>
    _zstdContentSize(src.address, src.length);

/// Decodes one zstd frame [src] into [dst] with an optional raw-content
/// dictionary [dict] (empty for none); returns the bytes written or a
/// negative error.
int nativeZstdDecompress(Uint8List dst, Uint8List src, Uint8List dict) =>
    _zstdDecompress(
      dst.address,
      dst.length,
      src.address,
      src.length,
      dict.address,
      dict.length,
    );
