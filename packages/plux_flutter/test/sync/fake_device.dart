// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Stand-ins for the platform's device keys and attestation, for tests of
/// the registration and token flow: no platform channel, deterministic
/// keys, and a record of what the flow asked of them.
library;

import 'dart:typed_data';

import 'package:plux_flutter/src/security/attestation.dart';
import 'package:plux_flutter/src/security/device_keys.dart';
import 'package:plux_flutter/src/security/dpop.dart';
import 'package:plux_flutter/src/sync/api_client.dart';

/// [DeviceKeys] in memory; every created key is new and distinct.
final class FakeDeviceKeys implements DeviceKeys {
  /// The keys by alias.
  final Map<String, DeviceKey> stored = {};

  /// The challenge of each `create`, in order.
  final List<Uint8List?> challenges = [];

  /// The aliases deleted, in order.
  final List<String> deleted = [];

  /// The aliases signed with, in order.
  final List<String> signed = [];

  /// The chain a key created with a challenge reports.
  List<Uint8List> chain = [];

  /// Where created keys live.
  KeyStorage storage = KeyStorage.tee;

  var _created = 0;

  /// The number of keys created.
  int get created => _created;

  @override
  Future<DeviceKey> create(
    String alias,
    KeyPurpose purpose, {
    Uint8List? challenge,
    bool strongBox = true,
  }) async {
    _created++;
    challenges.add(challenge);
    final key = DeviceKey(
      alias: alias,
      purpose: purpose,
      publicKey: EcPublicKey(
        Uint8List.fromList(List.filled(32, _created)),
        Uint8List.fromList(List.filled(32, _created + 100)),
      ),
      storage: storage,
    );
    stored[alias] = key;
    return DeviceKey(
      alias: alias,
      purpose: purpose,
      publicKey: key.publicKey,
      storage: storage,
      attestationChain: challenge == null ? const [] : chain,
    );
  }

  @override
  Future<DeviceKey?> load(String alias, KeyPurpose purpose) async =>
      stored[alias];

  @override
  Future<Uint8List> sign(String alias, Uint8List data) async {
    signed.add(alias);
    return Uint8List.fromList([for (var i = 0; i < 64; i++) i]);
  }

  @override
  Future<void> delete(String alias) async {
    deleted.add(alias);
    stored.remove(alias);
  }
}

/// [Attestation] with canned answers that records its calls.
final class FakeAttestation implements Attestation {
  /// Creates the fake; [evidence] is what `attest` returns.
  FakeAttestation({
    this.evidence = const DevelopmentEvidence('build-1'),
    this.assertionBytes,
    this.token,
  });

  /// What `attest` returns.
  AttestationEvidence evidence;

  /// What `assertion` returns.
  Uint8List? assertionBytes;

  /// What `integrityToken` returns.
  String? token;

  /// The arguments of each `attest`, in order.
  final List<({Uint8List challenge, String jkt, List<Uint8List> chain})>
  attested = [];

  /// The client data hash of each `assertion`, in order.
  final List<Uint8List> assertedHashes = [];

  /// The request hash of each `integrityToken`, in order.
  final List<String> integrityHashes = [];

  @override
  Future<AttestationEvidence> attest({
    required Uint8List challenge,
    required String jkt,
    required List<Uint8List> keyAttestationChain,
  }) async {
    attested.add((challenge: challenge, jkt: jkt, chain: keyAttestationChain));
    return evidence;
  }

  @override
  Future<Uint8List?> assertion(Uint8List clientDataHash) async {
    assertedHashes.add(clientDataHash);
    return assertionBytes;
  }

  @override
  Future<String?> integrityToken(String requestHash) async {
    integrityHashes.add(requestHash);
    return token;
  }
}

/// A device token [value] that expires far ahead, with proofs signed by a
/// [FakeDeviceKeys] key.
DeviceToken fakeToken(String value) => DeviceToken(
  value,
  DateTime.utc(2100),
  DpopProofs(
    keys: FakeDeviceKeys(),
    alias: 'alias',
    publicKey: EcPublicKey(Uint8List(32), Uint8List(32)),
  ),
);
