// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Platform attestation of the device's DPoP key (SEC-002–SEC-005,
/// ADR-0012 §Registration).
///
/// The evidence is bound to the server's registration challenge and to the
/// DPoP key's thumbprint (`jkt`): Play Integrity's request hash is
/// base64url(SHA-256(challenge ‖ jkt)) and App Attest's client data hash is
/// SHA-256(challenge ‖ jkt) ([bindingHash]). Android Key Attestation attests
/// the DPoP key itself, through the certificate chain the key was created
/// with. Development evidence is offered only by debug and profile builds
/// on emulators and simulators; production environments refuse it
/// (SEC-008).
library;

import 'dart:convert';
import 'dart:typed_data';

import 'package:crypto/crypto.dart';

/// SHA-256(challenge ‖ jkt): what the platform evidence is bound to.
Uint8List bindingHash(Uint8List challenge, String jkt) => Uint8List.fromList(
  sha256.convert([...challenge, ...utf8.encode(jkt)]).bytes,
);

/// The evidence a device offers for its DPoP key: exactly one form.
sealed class AttestationEvidence {
  const AttestationEvidence();
}

/// Android: the Key Attestation chain of the DPoP key (DER, leaf first)
/// and a Play Integrity token whose request hash is the binding hash.
final class AndroidEvidence extends AttestationEvidence {
  /// Creates Android evidence.
  const AndroidEvidence({
    required this.keyAttestationChain,
    required this.playIntegrityToken,
  });

  /// The DER certificates of the DPoP key's attestation, leaf first.
  final List<Uint8List> keyAttestationChain;

  /// The Play Integrity token; never logged (SEC-092).
  final String playIntegrityToken;

  @override
  String toString() => 'AndroidEvidence([redacted])';
}

/// iOS: an App Attest key and its attestation object for the binding hash.
final class IosEvidence extends AttestationEvidence {
  /// Creates iOS evidence.
  const IosEvidence({required this.keyId, required this.attestationObject});

  /// The App Attest key identifier (the raw 32 bytes).
  final Uint8List keyId;

  /// The CBOR attestation object App Attest returned.
  final Uint8List attestationObject;

  @override
  String toString() => 'IosEvidence([redacted])';
}

/// A development build on an emulator or simulator (SEC-008).
final class DevelopmentEvidence extends AttestationEvidence {
  /// Creates development evidence.
  const DevelopmentEvidence(this.buildId);

  /// Identifies the development build.
  final String buildId;
}

/// The platform's attestation services.
abstract interface class Attestation {
  /// Evidence for registration or re-attestation of the DPoP key with
  /// thumbprint [jkt], bound to [challenge]. [keyAttestationChain] is the
  /// chain the Android key was created with for this challenge (empty on
  /// iOS).
  Future<AttestationEvidence> attest({
    required Uint8List challenge,
    required String jkt,
    required List<Uint8List> keyAttestationChain,
  });

  /// An App Attest assertion over [clientDataHash] with the device's App
  /// Attest key, for a token refresh on iOS (SEC-025); null on other
  /// platforms or when the device registered with development evidence.
  Future<Uint8List?> assertion(Uint8List clientDataHash);

  /// A Play Integrity token whose request hash is [requestHash], for a
  /// re-attestation on Android (SEC-025); null on other platforms.
  Future<String?> integrityToken(String requestHash);
}
