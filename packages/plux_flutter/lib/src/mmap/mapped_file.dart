// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Read-only memory maps of files in the release store, exposed as
/// zero-copy byte views (RT-010, ADR-0030).
library;

import 'dart:convert';
import 'dart:ffi';
import 'dart:io';
import 'dart:typed_data';

import 'package:plux_flutter/src/native/plux_native.dart';

/// A file mapped read-only into memory. [bytes] is a view of the mapping,
/// never a copy. The view owns the mapping: the file is unmapped by the
/// view's finalizer once no object can reach the bytes, so a view kept by a
/// decoded object stays valid however long that object lives (RT-010).
final class MappedFile {
  MappedFile._(this.path, this._bytes);

  /// Maps the file at [path]; throws [FileSystemException] when it cannot.
  factory MappedFile.open(String path) {
    final name = Uint8List.fromList([...utf8.encode(path), 0]);
    final handle = nativeMap(name);
    if (handle == nullptr) {
      throw FileSystemException('out of memory while mapping', path);
    }
    final m = handle.ref;
    if (m.error != 0) {
      nativeRelease(handle);
      throw FileSystemException(
        'cannot map the file',
        path,
        OSError('', m.error),
      );
    }
    if (m.address == nullptr) {
      // An empty file maps nothing: there is no memory to keep.
      nativeRelease(handle);
      return MappedFile._(path, Uint8List(0));
    }
    return MappedFile._(
      path,
      m.address.asTypedList(
        m.length,
        finalizer: nativeReleaseAddress.cast(),
        token: handle.cast(),
      ),
    );
  }

  /// The file's path.
  final String path;

  final Uint8List _bytes;
  bool _released = false;

  /// Whether [release] has been called.
  bool get isReleased => _released;

  /// The file's bytes, a view of the mapping.
  Uint8List get bytes {
    if (_released) throw StateError('$path has been released');
    return _bytes;
  }

  /// Gives the file up: [bytes] throws from now on. The mapping itself
  /// ends when the last view obtained earlier becomes unreachable, so
  /// objects still holding one never read unmapped memory.
  void release() {
    _released = true;
  }
}
