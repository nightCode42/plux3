// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// NIST P-256 arithmetic in pure Dart, and ECDSA verification with it.
///
/// `package:cryptography` implements ECDSA only through platform plug-ins
/// and throws `UnimplementedError` in pure Dart, and the update metadata
/// must be verified on the sync isolate, where no plug-in runs (SEC-050,
/// SEC-122). Verification handles public values only, so the arithmetic
/// need not be constant time. The same arithmetic backs the software device
/// keys of tests and host tools (`software_keys.dart`), which are not
/// secret either.
library;

import 'dart:typed_data';

import 'package:crypto/crypto.dart';

// The NIST P-256 domain parameters (FIPS 186-4 D.1.2.3): the curve
// y^2 = x^3 - 3x + b over the prime field p, with base point g of prime
// order n.
final _p = BigInt.parse(
  'ffffffff00000001000000000000000000000000ffffffffffffffffffffffff',
  radix: 16,
);
final _b = BigInt.parse(
  '5ac635d8aa3a93e7b3ebbd55769886bc651d06b0cc53b0f63bce3c3e27d2604b',
  radix: 16,
);

/// The order of the base point.
final BigInt p256Order = BigInt.parse(
  'ffffffff00000000ffffffffffffffffbce6faada7179e84f3b9cac2fc632551',
  radix: 16,
);

/// The base point.
final P256Point p256Base = P256Point(
  BigInt.parse(
    '6b17d1f2e12c4247f8bce6e563a440f277037d812deb33a0f4a13945d898c296',
    radix: 16,
  ),
  BigInt.parse(
    '4fe342e2fe1a7f9b8ee7eb4a7c0f9e162bce33576b315ececbb6406837bf51f5',
    radix: 16,
  ),
);

/// A point of the curve in affine coordinates; the point at infinity is
/// represented by null wherever a point is optional.
final class P256Point {
  /// Creates the point (x, y).
  const P256Point(this.x, this.y);

  /// The x coordinate.
  final BigInt x;

  /// The y coordinate.
  final BigInt y;
}

/// The sum of two points; null is the point at infinity.
P256Point? p256Add(P256Point? a, P256Point? b) {
  if (a == null) return b;
  if (b == null) return a;
  final BigInt slope;
  if (a.x == b.x) {
    if ((a.y + b.y) % _p == BigInt.zero) return null;
    slope =
        (BigInt.from(3) *
            (a.x * a.x - BigInt.one) *
            (BigInt.two * a.y).modInverse(_p)) %
        _p;
  } else {
    slope = ((b.y - a.y) * (b.x - a.x).modInverse(_p)) % _p;
  }
  final x = (slope * slope - a.x - b.x) % _p;
  return P256Point(x, (slope * (a.x - x) - a.y) % _p);
}

/// [k] times [point] by double-and-add. It is not constant time.
P256Point? p256Multiply(BigInt k, P256Point point) {
  P256Point? result;
  P256Point? addend = point;
  for (var i = 0; i < k.bitLength; i++) {
    if ((k >> i).isOdd) result = p256Add(result, addend);
    addend = p256Add(addend, addend);
  }
  return result;
}

/// [v] as 32 big-endian bytes.
Uint8List p256Bytes32(BigInt v) {
  final out = Uint8List(32);
  for (var i = 31; i >= 0; i--) {
    out[i] = (v & BigInt.from(0xff)).toInt();
    v >>= 8;
  }
  return out;
}

/// The big-endian integer of [bytes].
BigInt p256Int(List<int> bytes) =>
    bytes.fold(BigInt.zero, (acc, b) => (acc << 8) | BigInt.from(b));

/// Whether [publicKey], 65 bytes `04 || x || y`, is a point on the curve.
bool isP256PublicKey(List<int> publicKey) => _point(publicKey) != null;

P256Point? _point(List<int> publicKey) {
  if (publicKey.length != 65 || publicKey[0] != 4) return null;
  final x = p256Int(publicKey.sublist(1, 33));
  final y = p256Int(publicKey.sublist(33));
  if (x >= _p || y >= _p) return null;
  final rhs = (x * x * x - BigInt.from(3) * x + _b) % _p;
  return (y * y) % _p == rhs ? P256Point(x, y) : null;
}

/// Whether [signature], the 64 bytes `r || s` of RFC 7518 §3.4, is a valid
/// ECDSA signature of SHA-256([message]) under [publicKey], the 65-byte
/// uncompressed point. Any malformed input is simply not valid.
bool p256Verify(List<int> publicKey, List<int> message, List<int> signature) {
  final q = _point(publicKey);
  if (q == null || signature.length != 64) return false;
  final r = p256Int(signature.sublist(0, 32));
  final s = p256Int(signature.sublist(32));
  final n = p256Order;
  if (r < BigInt.one || r >= n || s < BigInt.one || s >= n) return false;
  final z = p256Int(sha256.convert(message).bytes);
  final w = s.modInverse(n);
  final point = p256Add(
    p256Multiply((z * w) % n, p256Base),
    p256Multiply((r * w) % n, q),
  );
  return point != null && point.x % n == r;
}
