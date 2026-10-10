// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The device's standing with the server (SEC-002, SEC-021, SEC-025,
/// ADR-0012): registration with platform evidence for a hardware-held DPoP
/// key, access tokens obtained by proof of that key, re-attestation when
/// the server asks for it, and registering again when it no longer knows
/// the device.
library;

import 'dart:convert';
import 'dart:typed_data';

import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/security/attestation.dart';
import 'package:plux_flutter/src/security/device_keys.dart';
import 'package:plux_flutter/src/security/dpop.dart';
import 'package:plux_flutter/src/sync/api_client.dart';
import 'package:plux_flutter/src/sync/sync_engine.dart';

/// Registers the device and keeps a device token for it.
///
/// The token is refreshed thirty seconds before it expires. A server that
/// no longer knows the device (`PLX-6006`, `not_found`, `unauthenticated`)
/// makes the device register again, once per sync ([beginSync]); one that
/// asks for re-attestation (`PLX-6007`) gets fresh evidence for the same
/// key.
final class DeviceAuth {
  /// Creates the authority for one app and environment.
  DeviceAuth({
    required this.api,
    required this.keys,
    required this.attestation,
    required this.credentials,
    required this.appId,
    required this.environment,
    required this.device,
    DateTime Function()? clock,
  }) : _clock = clock ?? DateTime.now;

  /// The API client.
  final PluxApiClient api;

  /// The hardware-held keys.
  final DeviceKeys keys;

  /// The platform's attestation services.
  final Attestation attestation;

  /// Where the registration is kept.
  final CredentialStore credentials;

  /// The app.
  final String appId;

  /// The environment key.
  final String environment;

  /// What the device reports about itself at registration.
  final DeviceInfo device;

  final DateTime Function() _clock;

  /// How long before its expiry a token counts as expired.
  static const refreshMargin = Duration(seconds: 30);

  DeviceToken? _token;
  Future<DeviceToken>? _pending;
  var _reregistered = false;
  var _assurance = 0;

  /// The assurance level (0 to 3) of the latest token, `AL0` until one
  /// arrived, and again after the server refused the device with `PLX-6002`
  /// or `PLX-6006` (SEC-007).
  int get assurance => _assurance;

  /// Lowers the level to `AL0` at once, after the server refused the device
  /// for its assurance or revoked it; the next token restores what the
  /// server says.
  void lowerAssurance() => _assurance = 0;

  String get _alias => dpopKeyAlias(appId, environment);

  /// Starts a sync: registering again after a refusal is allowed once more.
  void beginSync() => _reregistered = false;

  /// Forgets the held token, after the server refused it.
  void forgetToken() => _token = null;

  /// A token that is valid for at least [refreshMargin], registering the
  /// device on first use. Concurrent callers share one exchange.
  Future<DeviceToken> token() {
    final t = _token;
    if (t != null && t.expiresAt.subtract(refreshMargin).isAfter(_clock())) {
      return Future.value(t);
    }
    return _pending ??= _obtain().whenComplete(() => _pending = null);
  }

  Future<DeviceToken> _obtain() async {
    try {
      final t = await _obtainToken();
      _assurance = t.assurance;
      return t;
    } on ApiError catch (e) {
      if (e.plxCode == PluxErrorCode.assuranceInsufficient.code ||
          e.plxCode == PluxErrorCode.deviceRevoked.code) {
        lowerAssurance();
      }
      rethrow;
    }
  }

  Future<DeviceToken> _obtainToken() async {
    var session = await _session() ?? await _register();
    try {
      return _token = await _refresh(session);
    } on ApiError catch (e) {
      if (e.plxCode == PluxErrorCode.reattestationRequired.code) {
        await _reattest(session);
        return _token = await _refresh(session);
      }
      if (!_unknownDevice(e) || _reregistered) rethrow;
      _reregistered = true;
      await _forget();
      session = await _register();
      return _token = await _refresh(session);
    }
  }

  bool _unknownDevice(ApiError e) =>
      e.plxCode == PluxErrorCode.deviceRevoked.code ||
      e.code == 'not_found' ||
      e.code == 'unauthenticated';

  /// The stored registration, when its key is still in the platform.
  Future<_Session?> _session() async {
    final c = await credentials.read();
    if (c == null) return null;
    final DeviceKey? key;
    try {
      key = await keys.load(_alias, KeyPurpose.dpop);
    } on PluxException catch (e) {
      if (e.code != PluxErrorCode.deviceKeyUnavailable) rethrow;
      return null;
    }
    if (key == null || key.publicKey.thumbprint != c.jkt) return null;
    return _Session(c.deviceId, key, _proofs(key));
  }

  DpopProofs _proofs(DeviceKey key) => DpopProofs(
    keys: keys,
    alias: _alias,
    publicKey: key.publicKey,
    now: _clock,
  );

  Future<void> _forget() async {
    await credentials.clear();
    await keys.delete(_alias);
  }

  /// Creates a new DPoP key for a challenge and registers it with the
  /// platform's evidence.
  Future<_Session> _register() async {
    final challenge = await api.registrationChallenge(
      appId: appId,
      environment: environment,
    );
    await credentials.clear();
    final key = await keys.create(
      _alias,
      KeyPurpose.dpop,
      challenge: challenge,
    );
    final evidence = await attestation.attest(
      challenge: challenge,
      jkt: key.publicKey.thumbprint,
      keyAttestationChain: key.attestationChain,
    );
    final id = await api.registerAttestedDevice(
      appId: appId,
      environment: environment,
      device: device,
      challenge: challenge,
      key: key,
      evidence: evidence,
    );
    await credentials.write(DeviceCredential(id, key.publicKey.thumbprint));
    return _Session(id, key, _proofs(key));
  }

  Future<DeviceToken> _refresh(_Session s) => api.refreshToken(
    deviceId: s.deviceId,
    proofs: s.proofs,
    now: _clock(),
    assertion: attestation.assertion,
  );

  /// Offers new evidence for the registered key (SEC-025): Android a Play
  /// Integrity token bound to the challenge and the key, the other
  /// platforms the evidence of registration without a key attestation.
  Future<void> _reattest(_Session s) async {
    final jkt = s.key.publicKey.thumbprint;
    final challenge = await api.registrationChallenge(
      appId: appId,
      environment: environment,
    );
    final binding = base64Url
        .encode(bindingHash(challenge, jkt))
        .replaceAll('=', '');
    final token = device.platform == 'android'
        ? await attestation.integrityToken(binding)
        : null;
    final AttestationEvidence evidence = token != null
        ? AndroidEvidence(
            keyAttestationChain: <Uint8List>[],
            playIntegrityToken: token,
          )
        : await attestation.attest(
            challenge: challenge,
            jkt: jkt,
            keyAttestationChain: const [],
          );
    await api.reattestDevice(
      deviceId: s.deviceId,
      challenge: challenge,
      evidence: evidence,
      proofs: s.proofs,
    );
  }
}

final class _Session {
  const _Session(this.deviceId, this.key, this.proofs);
  final String deviceId;
  final DeviceKey key;
  final DpopProofs proofs;
}
