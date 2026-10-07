// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Hardware-backed device keys (SEC-001, ADR-0012 §Keys).
///
/// Private keys are generated and kept by the platform — Android Keystore
/// (StrongBox or TEE), iOS Secure Enclave — are non-exportable, and never
/// cross the platform channel. Dart sees the public key, where the key
/// really lives, and signatures. A key held in software is reported as such
/// so the registration flow can cap the device's assurance (SEC-001).
library;

import 'dart:convert';

import 'package:crypto/crypto.dart';
import 'package:flutter/services.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';

const _channel = MethodChannel('dev.plux/runtime');

/// What a device key is used for.
enum KeyPurpose {
  /// Signs DPoP proofs (SEC-021).
  dpop,

  /// ECDH key agreement for confidential bundles.
  agreement,
}

/// Where a key really lives, as reported by the platform.
enum KeyStorage {
  /// A discrete secure element (Android StrongBox).
  strongbox('strongbox'),

  /// The Android trusted execution environment.
  tee('tee'),

  /// The iOS Secure Enclave.
  secureEnclave('secureEnclave'),

  /// A key outside secure hardware, such as an emulator or simulator key.
  software('software');

  const KeyStorage(this.wireName);

  /// The name the platform channel uses.
  final String wireName;

  /// Whether the key is inside secure hardware (SEC-001).
  bool get hardware => this != software;

  static KeyStorage? _parse(Object? name) {
    for (final s in values) {
      if (s.wireName == name) return s;
    }
    return null;
  }
}

String _b64url(List<int> bytes) => base64Url.encode(bytes).replaceAll('=', '');

/// A P-256 public key as its affine coordinates.
final class EcPublicKey {
  /// Creates the key from the 32-byte big-endian [x] and [y] coordinates.
  ///
  /// Throws [ArgumentError] when either coordinate is not 32 bytes. The
  /// bytes are copied, so later changes to the arguments do not alter the
  /// key.
  EcPublicKey(Uint8List x, Uint8List y)
    : x = _coordinate(x, 'x'),
      y = _coordinate(y, 'y');

  /// The x coordinate, 32 bytes.
  final Uint8List x;

  /// The y coordinate, 32 bytes.
  final Uint8List y;

  static Uint8List _coordinate(Uint8List v, String name) {
    if (v.length != 32) {
      throw ArgumentError.value(
        v.length,
        name,
        'a P-256 coordinate is 32 bytes',
      );
    }
    return Uint8List.fromList(v);
  }

  /// The key as a JSON Web Key (RFC 7517), public members only.
  Map<String, String> get jwk => {
    'kty': 'EC',
    'crv': 'P-256',
    'x': _b64url(x),
    'y': _b64url(y),
  };

  /// The JWK SHA-256 thumbprint (RFC 7638): base64url, unpadded, of the
  /// digest of the required members in lexicographic order without
  /// whitespace.
  String get thumbprint {
    final canonical =
        '{"crv":"P-256","kty":"EC","x":"${_b64url(x)}","y":"${_b64url(y)}"}';
    return _b64url(sha256.convert(utf8.encode(canonical)).bytes);
  }
}

/// A key held by the platform.
final class DeviceKey {
  /// Creates the description of a platform key.
  const DeviceKey({
    required this.alias,
    required this.purpose,
    required this.publicKey,
    required this.storage,
    this.attestationChain = const [],
  });

  /// The alias the platform knows the key by.
  final String alias;

  /// What the key is for.
  final KeyPurpose purpose;

  /// The public key.
  final EcPublicKey publicKey;

  /// Where the key really lives.
  final KeyStorage storage;

  /// The DER certificates of the Android Key Attestation, leaf first; empty
  /// when no challenge was given or the platform has none.
  final List<Uint8List> attestationChain;
}

/// The device's keys, held by the platform (SEC-001).
abstract interface class DeviceKeys {
  /// Creates a P-256 key under [alias], replacing any key with that alias.
  ///
  /// [challenge] is the Android Key Attestation challenge; iOS ignores it.
  /// With [strongBox] Android tries StrongBox first.
  Future<DeviceKey> create(
    String alias,
    KeyPurpose purpose, {
    Uint8List? challenge,
    bool strongBox = true,
  });

  /// The key under [alias], or null when there is none. Its attestation
  /// chain is empty.
  Future<DeviceKey?> load(String alias, KeyPurpose purpose);

  /// Signs [data] with the `dpop` key under [alias]: ES256, 64 bytes of
  /// `r || s` over SHA-256 of [data].
  Future<Uint8List> sign(String alias, Uint8List data);

  /// Deletes the key under [alias]; absent keys are not an error.
  Future<void> delete(String alias);
}

/// The alias of the DPoP key of one app in one environment: a fixed prefix
/// and the first 16 hex characters of SHA-256 over the app id, a NUL and the
/// environment, so aliases never carry the app's name.
String dpopKeyAlias(String appId, String environment) =>
    _alias('dpop', appId, environment);

/// The alias of the agreement key of one app in one environment; see
/// [dpopKeyAlias].
String agreementKeyAlias(String appId, String environment) =>
    _alias('agreement', appId, environment);

String _alias(String kind, String appId, String environment) {
  final digest = sha256.convert(utf8.encode('$appId\u0000$environment'));
  final hex = digest.bytes
      .take(8)
      .map((b) => b.toRadixString(16).padLeft(2, '0'))
      .join();
  return 'dev.plux.$kind.$hex';
}

/// The error codes of the platform side's contract.
const _platformCodes = {
  'PLUX_KEY_MISSING',
  'PLUX_KEY_UNSUPPORTED',
  'PLUX_PLATFORM',
};

/// [DeviceKeys] over the `dev.plux/runtime` platform channel.
///
/// Platform failures become [PluxException]s with
/// [PluxErrorCode.deviceKeyUnavailable] (PLX-6017): a missing or invalidated
/// key, a key the platform cannot create, any other platform failure and any
/// malformed reply fail closed, and the runtime registers again. Messages carry
/// the platform's error code only, never key material.
final class PlatformDeviceKeys implements DeviceKeys {
  /// Creates the platform-backed keys.
  const PlatformDeviceKeys();

  @override
  Future<DeviceKey> create(
    String alias,
    KeyPurpose purpose, {
    Uint8List? challenge,
    bool strongBox = true,
  }) async {
    final reply = await _call('keyCreate', {
      'alias': alias,
      'purpose': purpose.name,
      'challenge': challenge,
      'strongBox': strongBox,
    });
    return _key(reply, alias, purpose);
  }

  @override
  Future<DeviceKey?> load(String alias, KeyPurpose purpose) async {
    final reply = await _call('keyPublic', {'alias': alias});
    if (reply == null) return null;
    return _key(reply, alias, purpose);
  }

  @override
  Future<Uint8List> sign(String alias, Uint8List data) async {
    final reply = await _call('keySign', {'alias': alias, 'data': data});
    if (reply is! Uint8List || reply.length != 64) {
      throw _malformed('keySign');
    }
    return reply;
  }

  @override
  Future<void> delete(String alias) async {
    final reply = await _call('keyDelete', {'alias': alias});
    if (reply != null) throw _malformed('keyDelete');
  }

  Future<Object?> _call(String method, Map<String, Object?> args) async {
    try {
      return await _channel.invokeMethod<Object?>(method, args);
    } on PlatformException catch (e) {
      throw PluxException(
        PluxErrorCode.deviceKeyUnavailable,
        switch (e.code) {
          'PLUX_KEY_MISSING' => 'the platform has no key for the alias',
          'PLUX_KEY_UNSUPPORTED' => 'the platform cannot create this key',
          'PLUX_PLATFORM' => 'the platform key operation failed',
          _ => 'the platform key operation failed with an unknown code',
        },
        // An unknown code is not echoed: only the contract's codes are.
        details: {
          if (_platformCodes.contains(e.code)) 'platformCode': e.code,
          'method': method,
        },
      );
    } on MissingPluginException {
      throw PluxException(
        PluxErrorCode.deviceKeyUnavailable,
        'the platform has no device key support',
        details: {'method': method},
      );
    }
  }

  PluxException _malformed(String method) => PluxException(
    PluxErrorCode.deviceKeyUnavailable,
    'the platform answered $method with a malformed reply',
    details: {'method': method},
  );

  DeviceKey _key(Object? reply, String alias, KeyPurpose purpose) {
    if (reply is! Map) throw _malformed('key');
    final x = reply['x'];
    final y = reply['y'];
    final storage = KeyStorage._parse(reply['storage']);
    final chain = reply['chain'];
    if (x is! Uint8List ||
        y is! Uint8List ||
        x.length != 32 ||
        y.length != 32 ||
        storage == null ||
        chain is! List ||
        chain.any((c) => c is! Uint8List || c.isEmpty)) {
      throw _malformed('key');
    }
    return DeviceKey(
      alias: alias,
      purpose: purpose,
      publicKey: EcPublicKey(x, y),
      storage: storage,
      attestationChain: List.unmodifiable(chain.cast<Uint8List>()),
    );
  }
}
