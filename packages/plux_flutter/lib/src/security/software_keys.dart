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
import 'package:plux_flutter/src/security/p256.dart';

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
    if (d < BigInt.one || d >= p256Order) {
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
    final z = p256Int(sha256.convert(data).bytes);
    while (true) {
      final k = _randomScalar();
      final r = p256Multiply(k, p256Base)!.x % p256Order;
      final s = (k.modInverse(p256Order) * (z + r * d)) % p256Order;
      if (r != BigInt.zero && s != BigInt.zero) {
        return Uint8List.fromList([...p256Bytes32(r), ...p256Bytes32(s)]);
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
      final k = p256Int([for (var i = 0; i < 32; i++) _random.nextInt(256)]);
      if (k >= BigInt.one && k < p256Order) return k;
    }
  }

  DeviceKey? _describe(String alias, KeyPurpose purpose) {
    final d = _scalars[alias];
    if (d == null) return null;
    final q = p256Multiply(d, p256Base)!;
    return DeviceKey(
      alias: alias,
      purpose: purpose,
      publicKey: EcPublicKey(p256Bytes32(q.x), p256Bytes32(q.y)),
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
