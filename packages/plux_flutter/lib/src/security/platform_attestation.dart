// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// [Attestation] over the `dev.plux/runtime` platform channel (SEC-002,
/// SEC-003, SEC-008, ADR-0012 §Registration).
///
/// Android answers with a Play Integrity standard-request token whose
/// request hash is the binding hash; the Key Attestation chain comes from
/// the DPoP key's creation. iOS generates an App Attest key, keeps its
/// identifier in the platform's secret store for later assertions (SEC-025)
/// and attests it over the binding hash. Only debug and profile builds fall
/// back to development evidence, and only where the platform has no
/// attestation (SEC-008); a release build never does.
library;

import 'dart:convert';

import 'package:crypto/crypto.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/platform/runtime_channel.dart';
import 'package:plux_flutter/src/security/attestation.dart';

const _channel = RuntimeChannel();

/// The platform's answer when it has no attestation service.
const _unsupported = 'UNSUPPORTED';

/// The error codes of the platform side's contract.
const _platformCodes = {_unsupported, 'PLUX_PLATFORM'};

String _b64url(List<int> bytes) => base64Url.encode(bytes).replaceAll('=', '');

/// Platform attestation through the Android Play Integrity API and iOS App
/// Attest.
///
/// Platform failures become [PluxException]s with
/// [PluxErrorCode.attestationUnavailable] (PLX-6009). Messages and details
/// carry the platform's error code only, never evidence, key identifiers or
/// tokens (SEC-092).
final class PlatformAttestation implements Attestation {
  /// Creates the attestation for [appId] in [environment].
  ///
  /// [cloudProjectNumber] is the Google Cloud project Play Integrity runs
  /// under; [buildId] identifies the build in development evidence. [release]
  /// defaults to [kReleaseMode] and [platform] to [defaultTargetPlatform];
  /// tests inject both.
  PlatformAttestation({
    required this.appId,
    required this.environment,
    required this.buildId,
    this.cloudProjectNumber,
    bool? release,
    TargetPlatform? platform,
  }) : _release = release ?? kReleaseMode,
       _platform = platform ?? defaultTargetPlatform;

  /// The app's identifier.
  final String appId;

  /// The environment the device registers in.
  final String environment;

  /// The Google Cloud project number for Play Integrity; null where unset.
  final int? cloudProjectNumber;

  /// Identifies the build in development evidence.
  final String buildId;

  final bool _release;
  final TargetPlatform _platform;

  /// The secret that keeps the App Attest key identifier: a fixed prefix and
  /// the first 16 hex characters of SHA-256 over the app id, a NUL and the
  /// environment, so the name never carries the app's name.
  String get _keyIdName {
    final digest = sha256.convert(utf8.encode('$appId\u0000$environment'));
    final hex = digest.bytes
        .take(8)
        .map((b) => b.toRadixString(16).padLeft(2, '0'))
        .join();
    return 'plux.appattest.$hex';
  }

  @override
  Future<AttestationEvidence> attest({
    required Uint8List challenge,
    required String jkt,
    required List<Uint8List> keyAttestationChain,
  }) async {
    if (!_release) {
      try {
        // A debug or profile build with no attestation service, or on
        // Android with no Play Integrity project configured, offers
        // development evidence; the server accepts it only where the
        // development provider is enabled (SEC-008).
        if (!await _supported() ||
            (_platform == TargetPlatform.android &&
                cloudProjectNumber == null)) {
          return DevelopmentEvidence(buildId);
        }
        return await _attest(challenge, jkt, keyAttestationChain);
      } on PluxException catch (e) {
        if (e.details['platformCode'] == _unsupported) {
          return DevelopmentEvidence(buildId);
        }
        rethrow;
      }
    }
    return _attest(challenge, jkt, keyAttestationChain);
  }

  Future<AttestationEvidence> _attest(
    Uint8List challenge,
    String jkt,
    List<Uint8List> chain,
  ) async {
    final hash = bindingHash(challenge, jkt);
    switch (_platform) {
      case TargetPlatform.android:
        if (chain.isEmpty) {
          throw const PluxException(
            PluxErrorCode.attestationUnavailable,
            'the device key has no key attestation chain',
          );
        }
        final token = await integrityToken(_b64url(hash));
        if (token == null) throw _malformed('integrityToken');
        return AndroidEvidence(
          keyAttestationChain: List.unmodifiable(chain),
          playIntegrityToken: token,
        );
      case TargetPlatform.iOS:
        final keyId = await _call('appAttestKey', const {});
        if (keyId is! Uint8List || keyId.isEmpty) {
          throw _malformed('appAttestKey');
        }
        await _call('secretWrite', {
          'name': _keyIdName,
          'value': _b64url(keyId),
        });
        final object = await _call('appAttestAttest', {
          'keyId': keyId,
          'clientDataHash': hash,
        });
        if (object is! Uint8List || object.isEmpty) {
          throw _malformed('appAttestAttest');
        }
        return IosEvidence(keyId: keyId, attestationObject: object);
      case TargetPlatform.fuchsia ||
          TargetPlatform.linux ||
          TargetPlatform.macOS ||
          TargetPlatform.windows:
        throw const PluxException(
          PluxErrorCode.attestationUnavailable,
          'this platform has no attestation service',
        );
    }
  }

  @override
  Future<Uint8List?> assertion(Uint8List clientDataHash) async {
    if (_platform != TargetPlatform.iOS) return null;
    try {
      final stored = await _call('secretRead', {'name': _keyIdName});
      if (stored == null) return null;
      final Uint8List keyId;
      try {
        keyId = base64Url.decode(base64Url.normalize(stored as String));
      } on Object {
        throw _malformed('secretRead');
      }
      final reply = await _call('appAttestAssert', {
        'keyId': keyId,
        'clientDataHash': clientDataHash,
      });
      if (reply is! Uint8List || reply.isEmpty) {
        throw _malformed('appAttestAssert');
      }
      return reply;
    } on PluxException catch (e) {
      if (!_release && e.details['platformCode'] == _unsupported) return null;
      rethrow;
    }
  }

  @override
  Future<String?> integrityToken(String requestHash) async {
    if (_platform != TargetPlatform.android) return null;
    if (cloudProjectNumber == null) {
      throw const PluxException(
        PluxErrorCode.attestationUnavailable,
        'no Google Cloud project number is configured for Play Integrity',
      );
    }
    final reply = await _call('integrityToken', {
      'requestHash': requestHash,
      'cloudProjectNumber': cloudProjectNumber,
    });
    if (reply is! String || reply.isEmpty) throw _malformed('integrityToken');
    return reply;
  }

  /// Whether the platform has an attestation service on this device.
  Future<bool> _supported() async {
    if (_platform != TargetPlatform.android &&
        _platform != TargetPlatform.iOS) {
      return false;
    }
    final reply = await _call('attestationSupported', {
      'cloudProjectNumber': cloudProjectNumber,
    });
    if (reply is! bool) throw _malformed('attestationSupported');
    return reply;
  }

  Future<Object?> _call(String method, Map<String, Object?> args) async {
    try {
      return await _channel.invokeMethod<Object?>(method, args);
    } on PlatformException catch (e) {
      throw PluxException(
        PluxErrorCode.attestationUnavailable,
        e.code == _unsupported
            ? 'the platform has no attestation service'
            : 'the platform attestation failed',
        // An unknown code is not echoed: only the contract's codes are.
        details: {
          if (_platformCodes.contains(e.code)) 'platformCode': e.code,
          'method': method,
        },
      );
    } on MissingPluginException {
      throw PluxException(
        PluxErrorCode.attestationUnavailable,
        'the platform has no attestation support',
        details: {'method': method},
      );
    }
  }

  PluxException _malformed(String method) => PluxException(
    PluxErrorCode.attestationUnavailable,
    'the platform answered $method with a malformed reply',
    details: {'method': method},
  );
}
