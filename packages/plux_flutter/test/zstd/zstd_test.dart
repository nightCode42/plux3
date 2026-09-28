// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:convert';
import 'dart:io';
import 'dart:math';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/zstd/zstd.dart';

/// Every `whole` payload of the delta vectors: a zstd frame written by the
/// server's encoder, with the section size it decodes to.
List<(Uint8List, int)> _frames() {
  final json = jsonDecode(
    File('../../schema/testdata/delta/vectors.json').readAsStringSync(),
  ) as Map<String, Object?>;
  final out = <(Uint8List, int)>[];
  for (final v
      in (json['vectors']! as List<Object?>).cast<Map<String, Object?>>()) {
    if (v['error'] != null) continue;
    final d = base64.decode(v['delta']! as String);
    final view = ByteData.sublistView(d);
    var at = 80;
    for (var i = view.getUint32(8, Endian.little); i > 0; i--) {
      final size = view.getUint64(at + 20, Endian.little);
      final n = view.getUint32(at + 28, Endian.little);
      if (d[at + 18] == 1) {
        out.add((Uint8List.sublistView(d, at + 32, at + 32 + n), size));
      }
      at += 32 + n;
    }
  }
  return out;
}

void main() {
  final frames = _frames();

  test('decodes the frames the server encodes [BND-007]', () {
    expect(frames, isNotEmpty);
    for (final (frame, size) in frames) {
      expect(zstdDecompress(frame, capacity: size).length, size);
    }
  });

  test('refuses a frame larger than the capacity and a non-frame', () {
    final (frame, size) = frames.firstWhere((f) => f.$2 > 1);
    expect(
      () => zstdDecompress(frame, capacity: size - 1),
      throwsA(isA<ZstdException>()),
    );
    expect(
      () => zstdDecompress(Uint8List.fromList([1, 2, 3, 4, 5]), capacity: 10),
      throwsA(isA<ZstdException>()),
    );
    expect(
      () => zstdDecompress(Uint8List(0), capacity: 10),
      throwsA(isA<ZstdException>()),
    );
    expect(() => zstdContentSize(Uint8List(8)), throwsA(isA<ZstdException>()));
    expect(
      () => zstdDecompress(frame, capacity: -1),
      throwsA(isA<ArgumentError>()),
    );
    expect(const ZstdException('x').toString(), 'zstd: x');
  });

  test('never crashes or overruns on damaged frames [QA-004]', () {
    final rng = Random(3044);
    for (var i = 0; i < 3000; i++) {
      final (frame, size) = frames[rng.nextInt(frames.length)];
      final damaged = Uint8List.fromList(frame);
      for (var k = rng.nextInt(4) + 1; k > 0; k--) {
        damaged[rng.nextInt(damaged.length)] = rng.nextInt(256);
      }
      final cut = rng.nextBool()
          ? damaged
          : Uint8List.sublistView(damaged, 0, rng.nextInt(damaged.length + 1));
      try {
        final out = zstdDecompress(cut, capacity: size);
        expect(out.length, lessThanOrEqualTo(size));
      } on ZstdException {
        // The expected outcome for most damage.
      }
    }
  });
}
