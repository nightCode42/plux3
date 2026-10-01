// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:math' as math;
import 'dart:typed_data';
import 'dart:ui' as ui;

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/assets/thumbhash.dart';

/// The ThumbHash encoder, from the same published algorithm, so that the
/// decoder is checked by round trips.
Uint8List encodeThumbHash(int w, int h, Uint8List rgba) {
  var avgR = 0.0, avgG = 0.0, avgB = 0.0, avgA = 0.0;
  for (var i = 0, j = 0; i < w * h; i++, j += 4) {
    final alpha = rgba[j + 3] / 255;
    avgR += alpha / 255 * rgba[j];
    avgG += alpha / 255 * rgba[j + 1];
    avgB += alpha / 255 * rgba[j + 2];
    avgA += alpha;
  }
  if (avgA > 0) {
    avgR /= avgA;
    avgG /= avgA;
    avgB /= avgA;
  }
  final hasAlpha = avgA < w * h;
  final lLimit = hasAlpha ? 5 : 7;
  final lx = math.max(1, (lLimit * w / math.max(w, h)).round());
  final ly = math.max(1, (lLimit * h / math.max(w, h)).round());
  final l = <double>[], p = <double>[], q = <double>[], a = <double>[];
  for (var i = 0, j = 0; i < w * h; i++, j += 4) {
    final alpha = rgba[j + 3] / 255;
    final r = avgR * (1 - alpha) + alpha / 255 * rgba[j];
    final g = avgG * (1 - alpha) + alpha / 255 * rgba[j + 1];
    final b = avgB * (1 - alpha) + alpha / 255 * rgba[j + 2];
    l.add((r + g + b) / 3);
    p.add((r + g) / 2 - b);
    q.add(r - g);
    a.add(alpha);
  }
  (double, List<double>, double) channel(List<double> c, int nx, int ny) {
    var dc = 0.0, scale = 0.0;
    final ac = <double>[];
    for (var cy = 0; cy < ny; cy++) {
      for (var cx = 0; cx * ny < nx * (ny - cy); cx++) {
        var f = 0.0;
        for (var y = 0; y < h; y++) {
          final fy = math.cos(math.pi / h * cy * (y + 0.5));
          for (var x = 0; x < w; x++) {
            f += c[x + y * w] * math.cos(math.pi / w * cx * (x + 0.5)) * fy;
          }
        }
        f /= w * h;
        if (cx > 0 || cy > 0) {
          ac.add(f);
          scale = math.max(scale, f.abs());
        } else {
          dc = f;
        }
      }
    }
    if (scale > 0) {
      for (var i = 0; i < ac.length; i++) {
        ac[i] = 0.5 + 0.5 / scale * ac[i];
      }
    }
    return (dc, ac, scale);
  }

  final (lDc, lAc, lScale) = channel(l, math.max(3, lx), math.max(3, ly));
  final (pDc, pAc, pScale) = channel(p, 3, 3);
  final (qDc, qAc, qScale) = channel(q, 3, 3);
  final (aDc, aAc, aScale) = hasAlpha
      ? channel(a, 5, 5)
      : (0.0, <double>[], 0.0);
  final isLandscape = w > h;
  final header24 =
      (63 * lDc).round() |
      ((31.5 + 31.5 * pDc).round() << 6) |
      ((31.5 + 31.5 * qDc).round() << 12) |
      ((31 * lScale).round() << 18) |
      ((hasAlpha ? 1 : 0) << 23);
  final header16 =
      (isLandscape ? ly : lx) |
      ((63 * pScale).round() << 3) |
      ((63 * qScale).round() << 9) |
      ((isLandscape ? 1 : 0) << 15);
  final hash = <int>[
    header24 & 255,
    (header24 >> 8) & 255,
    header24 >> 16,
    header16 & 255,
    header16 >> 8,
  ];
  final acStart = hasAlpha ? 6 : 5;
  var acIndex = 0;
  if (hasAlpha) hash.add((15 * aDc).round() | ((15 * aScale).round() << 4));
  for (final ac in hasAlpha ? [lAc, pAc, qAc, aAc] : [lAc, pAc, qAc]) {
    for (final f in ac) {
      final at = acStart + (acIndex >> 1);
      while (hash.length <= at) {
        hash.add(0);
      }
      hash[at] |= (15 * f).round() << ((acIndex & 1) << 2);
      acIndex++;
    }
  }
  return Uint8List.fromList(hash);
}

/// A [w]×[h] image of one colour.
Uint8List solid(int w, int h, int r, int g, int b, int a) {
  final out = Uint8List(w * h * 4);
  for (var i = 0; i < out.length; i += 4) {
    out
      ..[i] = r
      ..[i + 1] = g
      ..[i + 2] = b
      ..[i + 3] = a;
  }
  return out;
}

void main() {
  test('a solid image round-trips to its colour, opaque [RT-014]', () {
    final hash = encodeThumbHash(40, 40, solid(40, 40, 200, 60, 30, 255));
    final img = decodeThumbHash(hash);
    expect((img.width, img.height), (32, 32));
    for (final i in [0, img.rgba.length ~/ 2]) {
      expect(img.rgba[i], closeTo(200, 12));
      expect(img.rgba[i + 1], closeTo(60, 12));
      expect(img.rgba[i + 2], closeTo(30, 12));
      expect(img.rgba[i + 3], 255);
    }
  });

  test('alpha, gradients and the aspect ratio survive', () {
    final w = 60, h = 30;
    final rgba = Uint8List(w * h * 4);
    for (var y = 0; y < h; y++) {
      for (var x = 0; x < w; x++) {
        final i = (y * w + x) * 4;
        rgba
          ..[i] = x * 255 ~/ w
          ..[i + 1] = 100
          ..[i + 2] = 255 - x * 255 ~/ w
          ..[i + 3] = x < w ~/ 2 ? 255 : 0;
      }
    }
    final hash = encodeThumbHash(w, h, rgba);
    expect(thumbHashAspectRatio(hash), closeTo(2, 0.5));
    final img = decodeThumbHash(hash);
    expect(img.width, 32);
    expect(img.height, lessThan(20));
    int alphaAt(int x) => img.rgba[(img.height ~/ 2 * img.width + x) * 4 + 3];
    expect(alphaAt(2), greaterThan(200));
    expect(alphaAt(img.width - 3), lessThan(60));
    int redAt(int x) => img.rgba[(img.height ~/ 2 * img.width + x) * 4];
    expect(redAt(2), lessThan(redAt(img.width ~/ 2 - 2)));
    final tall = decodeThumbHash(
      encodeThumbHash(20, 60, solid(20, 60, 9, 9, 9, 255)),
    );
    expect(tall.height, 32);
    expect(tall.width, lessThan(16));
  });

  test('a hash too short for its coefficients is refused', () {
    final hash = encodeThumbHash(40, 40, solid(40, 40, 1, 2, 3, 255));
    expect(
      () => decodeThumbHash(Uint8List.sublistView(hash, 0, 8)),
      throwsFormatException,
    );
    expect(() => decodeThumbHash(Uint8List(3)), throwsFormatException);
    final alpha = encodeThumbHash(10, 10, solid(10, 10, 1, 2, 3, 128));
    expect(
      () => decodeThumbHash(Uint8List.sublistView(alpha, 0, 5)),
      throwsFormatException,
    );
  });

  test('the placeholder is a BMP every decoder reads', () async {
    final img = decodeThumbHash(
      encodeThumbHash(40, 40, solid(40, 40, 200, 60, 30, 255)),
    );
    final codec = await ui.instantiateImageCodec(bmpOf(img));
    final frame = await codec.getNextFrame();
    expect((frame.image.width, frame.image.height), (32, 32));
    final pixels = (await frame.image.toByteData())!;
    expect(pixels.getUint8(0), closeTo(200, 12));
    expect(pixels.getUint8(3), 255);
  });
}
