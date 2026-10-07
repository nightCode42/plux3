// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Software device keys and development attestation, for tests and host
/// tools only.
///
/// Under `flutter test` on a host there is no platform side, so the runtime's
/// hardware keys ([PlatformDeviceKeys]) and attestation cannot run. These
/// stand-ins let a host-side test register with a real server and sign DPoP
/// proofs. The runtime never selects them: a test injects them through
/// `RuntimeOverrides`. Their keys are held in memory, are not hardware-backed
/// and are reported as [KeyStorage.software], and the evidence is development
/// evidence, so a server whose assurance policy is `strict` or `maximum`
/// refuses them (SEC-001, SEC-008).
library;

import 'dart:math';
import 'dart:typed_data';

import 'package:crypto/crypto.dart';
import 'package:flutter/foundation.dart' show visibleForTesting;
import 'package:plux_flutter/src/security/attestation.dart';
import 'package:plux_flutter/src/security/device_keys.dart';

// The NIST P-256 domain parameters (FIPS 186-4 D.1.2.3): the curve
// y^2 = x^3 - 3x + b over the prime field p, with base point g of prime
// order n. The point arithmetic needs a = -3 and p, not b.
final _p = BigInt.parse(
  'ffffffff00000001000000000000000000000000ffffffffffffffffffffffff',
  radix: 16,
);
final _n = BigInt.parse(
  'ffffffff00000000ffffffffffffffffbce6faada7179e84f3b9cac2fc632551',
  radix: 16,
);
final _g = _Point(
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
final class _Point {
  const _Point(this.x, this.y);

  final BigInt x;
  final BigInt y;
}

_Point? _add(_Point? a, _Point? b) {
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
  return _Point(x, (slope * (a.x - x) - a.y) % _p);
}

/// [k] times [point] by double-and-add. It is not constant time: the keys
/// here are throwaway test keys, and nothing about them is secret from the
/// process that holds them.
_Point? _multiply(BigInt k, _Point point) {
  _Point? result;
  _Point? addend = point;
  for (var i = 0; i < k.bitLength; i++) {
    if ((k >> i).isOdd) result = _add(result, addend);
    addend = _add(addend, addend);
  }
  return result;
}

Uint8List _bytes32(BigInt v) {
  final out = Uint8List(32);
  for (var i = 31; i >= 0; i--) {
    out[i] = (v & BigInt.from(0xff)).toInt();
    v >>= 8;
  }
  return out;
}

BigInt _int(List<int> bytes) =>
    bytes.fold(BigInt.zero, (acc, b) => (acc << 8) | BigInt.from(b));

/// An in-memory P-256 key store implemented in pure Dart.
///
/// For tests and host tools only; the runtime never chooses it. The keys are
/// not hardware-backed ([KeyStorage.software]), so a server whose assurance
/// policy is `strict` or `maximum` refuses a device registered with them. The
/// arithmetic is not constant time and the private scalars sit in ordinary
/// memory; that is acceptable for a key that protects nothing.
final class SoftwareDeviceKeys implements DeviceKeys {
  /// Creates an empty store; keys are made with [create].
  SoftwareDeviceKeys() : _random = Random.secure();

  /// Creates a store holding one key with the private scalar [d] under
  /// [alias], so a test can check the arithmetic against published vectors.
  /// [d] must lie in [1, n-1].
  @visibleForTesting
  SoftwareDeviceKeys.withScalar(String alias, BigInt d)
    : _random = Random.secure() {
    if (d < BigInt.one || d >= _n) {
      throw ArgumentError.value(d, 'd', 'a P-256 scalar lies in [1, n-1]');
    }
    _scalars[alias] = d;
  }

  final Random _random;
  final _scalars = <String, BigInt>{};

  @override
  Future<DeviceKey> create(
    String alias,
    KeyPurpose purpose, {
    Uint8List? challenge,
    bool strongBox = true,
  }) async {
    _scalars[alias] = _randomScalar();
    return _describe(alias, purpose)!;
  }

  @override
  Future<DeviceKey?> load(String alias, KeyPurpose purpose) async =>
      _describe(alias, purpose);

  @override
  Future<Uint8List> sign(String alias, Uint8List data) async {
    final d = _scalars[alias];
    if (d == null) throw StateError('no software key under "$alias"');
    final z = _int(sha256.convert(data).bytes);
    while (true) {
      final k = _randomScalar();
      final r = _multiply(k, _g)!.x % _n;
      final s = (k.modInverse(_n) * (z + r * d)) % _n;
      if (r != BigInt.zero && s != BigInt.zero) {
        return Uint8List.fromList([..._bytes32(r), ..._bytes32(s)]);
      }
    }
  }

  @override
  Future<void> delete(String alias) async {
    _scalars.remove(alias);
  }

  /// A scalar drawn uniformly from [1, n-1] by rejection.
  BigInt _randomScalar() {
    while (true) {
      final k = _int([for (var i = 0; i < 32; i++) _random.nextInt(256)]);
      if (k >= BigInt.one && k < _n) return k;
    }
  }

  DeviceKey? _describe(String alias, KeyPurpose purpose) {
    final d = _scalars[alias];
    if (d == null) return null;
    final q = _multiply(d, _g)!;
    return DeviceKey(
      alias: alias,
      purpose: purpose,
      publicKey: EcPublicKey(_bytes32(q.x), _bytes32(q.y)),
      storage: KeyStorage.software,
    );
  }
}

/// Attestation for tests and host tools: development evidence only, which
/// production environments refuse (SEC-008).
final class DevelopmentAttestation implements Attestation {
  /// Creates the attestation that names the build [buildId].
  const DevelopmentAttestation(this.buildId);

  /// Identifies the development build in the evidence.
  final String buildId;

  @override
  Future<AttestationEvidence> attest({
    required Uint8List challenge,
    required String jkt,
    required List<Uint8List> keyAttestationChain,
  }) async => DevelopmentEvidence(buildId);

  @override
  Future<Uint8List?> assertion(Uint8List clientDataHash) async => null;

  @override
  Future<String?> integrityToken(String requestHash) async => null;
}
