// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// DPoP proofs (SEC-021, RFC 9449): every device request carries a JWS
/// signed by the device's hardware key, binding the request's method and
/// URL — and the access token, when there is one — to that key.
library;

import 'dart:convert';
import 'dart:math';
import 'dart:typed_data';

import 'package:crypto/crypto.dart';
import 'package:plux_flutter/src/security/device_keys.dart';

String _b64url(List<int> bytes) => base64Url.encode(bytes).replaceAll('=', '');

String _secureJti() {
  final random = Random.secure();
  return _b64url(List<int>.generate(16, (_) => random.nextInt(256)));
}

/// Builds DPoP proofs with one device key (SEC-021, RFC 9449 §4).
///
/// The JSON of header and payload is written with a fixed member order, so
/// the same inputs give the same signing input.
final class DpopProofs {
  /// Creates the builder for the key [alias] whose public half is
  /// [publicKey].
  ///
  /// [now] and [newJti] default to the system clock and 16 random bytes from
  /// a secure generator; tests inject their own.
  DpopProofs({
    required this.keys,
    required this.alias,
    required this.publicKey,
    DateTime Function()? now,
    String Function()? newJti,
  }) : _now = now ?? DateTime.now,
       _newJti = newJti ?? _secureJti;

  /// The keys that sign.
  final DeviceKeys keys;

  /// The alias of the signing key.
  final String alias;

  /// The public half of the signing key, sent in every proof's header.
  final EcPublicKey publicKey;

  final DateTime Function() _now;
  final String Function() _newJti;

  /// A compact JWS proof for a request with HTTP [method] to [uri].
  ///
  /// With an [accessToken] the proof carries `ath`, the base64url SHA-256
  /// of the token; with a server-issued [nonce] it carries `nonce`.
  Future<String> proof({
    required String method,
    required Uri uri,
    String? accessToken,
    String? nonce,
  }) async {
    final header = jsonEncode({
      'typ': 'dpop+jwt',
      'alg': 'ES256',
      'jwk': publicKey.jwk,
    });
    final payload = jsonEncode({
      'htm': method,
      'htu': dpopHtu(uri),
      'iat': _now().millisecondsSinceEpoch ~/ 1000,
      'jti': _newJti(),
      'ath': ?(accessToken == null
          ? null
          : _b64url(sha256.convert(ascii.encode(accessToken)).bytes)),
      'nonce': ?nonce,
    });
    final input =
        '${_b64url(utf8.encode(header))}.${_b64url(utf8.encode(payload))}';
    final signature = await keys.sign(
      alias,
      Uint8List.fromList(ascii.encode(input)),
    );
    return '$input.${_b64url(signature)}';
  }
}

/// The `htu` claim for [uri] (RFC 9449 §4.2): scheme, host, port and path
/// without query and fragment, scheme and host lower case, the scheme's
/// default port left out, and an empty path written as `/`.
String dpopHtu(Uri uri) {
  final scheme = uri.scheme.toLowerCase();
  final port = uri.hasPort && uri.port != _defaultPort(scheme)
      ? ':${uri.port}'
      : '';
  final host = uri.host.toLowerCase();
  final shownHost = host.contains(':') ? '[$host]' : host;
  final path = uri.path.isEmpty ? '/' : uri.path;
  return '$scheme://$shownHost$port$path';
}

int? _defaultPort(String scheme) => switch (scheme) {
  'https' => 443,
  'http' => 80,
  _ => null,
};
