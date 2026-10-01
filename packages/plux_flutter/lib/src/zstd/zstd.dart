// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// One-shot zstd decoding through the vendored decoder (ADR-0030): into a
/// buffer of a size known before the call, with an optional raw-content
/// dictionary for section patches (ADR-0003). Callers check [size]
/// against the limits before calling, so no frame can make the runtime
/// allocate more than the limits allow.
library;

import 'dart:typed_data';

import 'package:plux_flutter/src/native/plux_native.dart';

/// A frame that cannot be decoded to the expected bytes.
final class ZstdException implements Exception {
  /// Creates the failure.
  const ZstdException(this.message);

  /// What is wrong.
  final String message;

  @override
  String toString() => 'zstd: $message';
}

/// The content size [frame] declares, or null when it declares none.
/// Throws [ZstdException] when [frame] does not start with a zstd frame
/// header.
int? zstdContentSize(Uint8List frame) {
  final n = nativeZstdContentSize(frame);
  if (n == -1) return null;
  if (n < 0) throw const ZstdException('not a zstd frame');
  return n;
}

/// Decodes exactly one zstd [frame] of at most [capacity] bytes, with
/// [dictionary] as a raw-content dictionary, and returns what it decoded.
/// Throws [ZstdException] when the frame is invalid, fails its checksum,
/// declares a content size above [capacity], or decodes to more.
Uint8List zstdDecompress(
  Uint8List frame, {
  required int capacity,
  Uint8List? dictionary,
}) {
  if (capacity < 0) throw ArgumentError.value(capacity, 'capacity');
  final declared = zstdContentSize(frame);
  if (declared != null && declared > capacity) {
    throw ZstdException('the frame declares $declared bytes, over $capacity');
  }
  final out = Uint8List(declared ?? capacity);
  final dict = dictionary ?? Uint8List(0);
  final n = nativeZstdDecompress(out, frame, dict);
  if (n < 0) throw ZstdException('the frame is invalid (error $n)');
  return n == out.length ? out : Uint8List.sublistView(out, 0, n);
}
