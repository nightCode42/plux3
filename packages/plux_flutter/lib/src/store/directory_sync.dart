// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The native `fsync` of a directory the release store needs after each
/// rename (ADR-0021); Dart's own file API cannot open a directory.
library;

import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:plux_flutter/src/native/plux_native.dart';

/// Makes the entries of the directory at [path] durable; throws
/// [FileSystemException] when the operating system refuses.
void syncDirectory(String path) {
  final name = Uint8List.fromList([...utf8.encode(path), 0]);
  final errno = nativeFsyncDirectory(name);
  if (errno != 0) {
    throw FileSystemException(
      'cannot sync the directory',
      path,
      OSError('', errno),
    );
  }
}
