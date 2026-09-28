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
/// never a copy. The mapping lasts until [release], or until this object is
/// garbage-collected; a view must not be used after either.
final class MappedFile implements Finalizable {
  MappedFile._(this.path, this._handle, this._bytes);

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
    final bytes = m.address == nullptr
        ? Uint8List(0)
        : m.address.asTypedList(m.length);
    final file = MappedFile._(path, handle, bytes);
    _finalizer.attach(
      file,
      handle.cast(),
      detach: file,
      externalSize: m.length,
    );
    return file;
  }

  static final _finalizer = NativeFinalizer(nativeReleaseAddress);

  /// The file's path.
  final String path;

  Pointer<NativeMapping>? _handle;
  final Uint8List _bytes;

  /// Whether [release] has been called.
  bool get isReleased => _handle == null;

  /// The file's bytes, a view of the mapping.
  Uint8List get bytes {
    if (_handle == null) throw StateError('$path has been released');
    return _bytes;
  }

  /// Unmaps the file. Every view obtained from [bytes] becomes invalid.
  void release() {
    final h = _handle;
    if (h == null) return;
    _handle = null;
    _finalizer.detach(this);
    nativeRelease(h);
  }
}
