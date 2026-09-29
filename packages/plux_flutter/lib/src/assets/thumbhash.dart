// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// ThumbHash placeholders (RT-014): a hash of a few dozen bytes decoded
/// into a small blurred image shown while the real one loads. The format
/// is Evan Wallace's ThumbHash: a DCT of the image in an L/P/Q (and
/// alpha) colour space, packed in 4-bit coefficients.
library;

import 'dart:math' as math;
import 'dart:typed_data';

/// A decoded placeholder: [width]×[height] pixels, RGBA, row by row.
typedef ThumbHashImage = ({int width, int height, Uint8List rgba});

/// Decodes a ThumbHash; throws [FormatException] for one too short to
/// hold its coefficients.
ThumbHashImage decodeThumbHash(Uint8List hash) {
  if (hash.length < 5) throw const FormatException('a ThumbHash is too short');
  final header24 = hash[0] | (hash[1] << 8) | (hash[2] << 16);
  final header16 = hash[3] | (hash[4] << 8);
  final lDc = (header24 & 63) / 63;
  final pDc = ((header24 >> 6) & 63) / 31.5 - 1;
  final qDc = ((header24 >> 12) & 63) / 31.5 - 1;
  final lScale = ((header24 >> 18) & 31) / 31;
  final hasAlpha = (header24 >> 23) != 0;
  final pScale = ((header16 >> 3) & 63) / 63;
  final qScale = ((header16 >> 9) & 63) / 63;
  final isLandscape = (header16 >> 15) != 0;
  final lx = math.max(3, isLandscape ? (hasAlpha ? 5 : 7) : header16 & 7);
  final ly = math.max(3, isLandscape ? header16 & 7 : (hasAlpha ? 5 : 7));
  if (hasAlpha && hash.length < 6) {
    throw const FormatException('a ThumbHash is too short');
  }
  final aDc = hasAlpha ? (hash[5] & 15) / 15 : 1.0;
  final aScale = hasAlpha ? (hash[5] >> 4) / 15 : 0.0;

  final acStart = hasAlpha ? 6 : 5;
  var acIndex = 0;
  List<double> channel(int nx, int ny, double scale) {
    final ac = <double>[];
    for (var cy = 0; cy < ny; cy++) {
      for (var cx = cy > 0 ? 0 : 1; cx * ny < nx * (ny - cy); cx++) {
        final at = acStart + (acIndex >> 1);
        if (at >= hash.length) {
          throw const FormatException('a ThumbHash is too short');
        }
        final nibble = (hash[at] >> ((acIndex & 1) << 2)) & 15;
        acIndex++;
        ac.add((nibble / 7.5 - 1) * scale);
      }
    }
    return ac;
  }

  // Saturation is boosted by 1.25 to make up for quantisation.
  final lAc = channel(lx, ly, lScale);
  final pAc = channel(3, 3, pScale * 1.25);
  final qAc = channel(3, 3, qScale * 1.25);
  final aAc = hasAlpha ? channel(5, 5, aScale) : const <double>[];

  final ratio = thumbHashAspectRatio(hash);
  final w = (ratio > 1 ? 32 : 32 * ratio).round();
  final h = (ratio > 1 ? 32 / ratio : 32).round();
  final rgba = Uint8List(w * h * 4);
  final fx = List<double>.filled(7, 0), fy = List<double>.filled(7, 0);
  int byte(double v) => (255 * v.clamp(0.0, 1.0)).floor();
  for (var y = 0, i = 0; y < h; y++) {
    for (var x = 0; x < w; x++, i += 4) {
      var l = lDc, p = pDc, q = qDc, a = aDc;
      for (var cx = 0, n = math.max(lx, hasAlpha ? 5 : 3); cx < n; cx++) {
        fx[cx] = math.cos(math.pi / w * (x + 0.5) * cx);
      }
      for (var cy = 0, n = math.max(ly, hasAlpha ? 5 : 3); cy < n; cy++) {
        fy[cy] = math.cos(math.pi / h * (y + 0.5) * cy);
      }
      for (var cy = 0, j = 0; cy < ly; cy++) {
        final fy2 = fy[cy] * 2;
        for (var cx = cy > 0 ? 0 : 1; cx * ly < lx * (ly - cy); cx++, j++) {
          l += lAc[j] * fx[cx] * fy2;
        }
      }
      for (var cy = 0, j = 0; cy < 3; cy++) {
        final fy2 = fy[cy] * 2;
        for (var cx = cy > 0 ? 0 : 1; cx < 3 - cy; cx++, j++) {
          final f = fx[cx] * fy2;
          p += pAc[j] * f;
          q += qAc[j] * f;
        }
      }
      if (hasAlpha) {
        for (var cy = 0, j = 0; cy < 5; cy++) {
          final fy2 = fy[cy] * 2;
          for (var cx = cy > 0 ? 0 : 1; cx < 5 - cy; cx++, j++) {
            a += aAc[j] * fx[cx] * fy2;
          }
        }
      }
      final b = l - 2 / 3 * p;
      final r = (3 * l - b + q) / 2;
      final g = r - q;
      rgba[i] = byte(r);
      rgba[i + 1] = byte(g);
      rgba[i + 2] = byte(b);
      rgba[i + 3] = byte(a);
    }
  }
  return (width: w, height: h, rgba: rgba);
}

/// The approximate width-to-height ratio a ThumbHash records.
double thumbHashAspectRatio(Uint8List hash) {
  final header = hash[3];
  final hasAlpha = (hash[2] & 0x80) != 0;
  final isLandscape = (hash[4] & 0x80) != 0;
  final lx = isLandscape ? (hasAlpha ? 5 : 7) : header & 7;
  final ly = isLandscape ? header & 7 : (hasAlpha ? 5 : 7);
  return ly == 0 ? 1 : lx / ly;
}

/// [image] as an uncompressed 32-bit BMP file, which every Flutter image
/// decoder reads, so a placeholder is an ordinary `MemoryImage`.
Uint8List bmpOf(ThumbHashImage image) {
  const header = 14 + 108;
  final (:width, :height, :rgba) = image;
  final out = ByteData(header + rgba.length);
  out
    ..setUint8(0, 0x42)
    ..setUint8(1, 0x4D)
    ..setUint32(2, out.lengthInBytes, Endian.little)
    ..setUint32(10, header, Endian.little)
    // BITMAPV4HEADER, top-down rows, BI_BITFIELDS with explicit masks.
    ..setUint32(14, 108, Endian.little)
    ..setInt32(18, width, Endian.little)
    ..setInt32(22, -height, Endian.little)
    ..setUint16(26, 1, Endian.little)
    ..setUint16(28, 32, Endian.little)
    ..setUint32(30, 3, Endian.little)
    ..setUint32(34, rgba.length, Endian.little)
    ..setUint32(54, 0x000000FF, Endian.little)
    ..setUint32(58, 0x0000FF00, Endian.little)
    ..setUint32(62, 0x00FF0000, Endian.little)
    ..setUint32(66, 0xFF000000, Endian.little)
    ..setUint32(70, 0x73524742, Endian.little); // 'sRGB'
  out.buffer.asUint8List(header).setAll(0, rgba);
  return out.buffer.asUint8List();
}
