// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// A self-signed P-256 certificate for `localhost`, generated for each test
/// run, so that no private key is committed. It builds the DER of the
/// certificate by hand, and so does not share the code under test.
library;

import 'dart:convert';
import 'dart:io';
import 'dart:math';
import 'dart:typed_data';

import 'package:crypto/crypto.dart';
import 'package:plux_flutter/src/security/device_keys.dart';
import 'package:plux_flutter/src/security/software_keys.dart';

/// A certificate, its key, and the pin of its public key.
final class TestCertificate {
  TestCertificate._(this.der, this.keyPem, this.spki);

  /// Generates a certificate valid for `localhost` for a day.
  static Future<TestCertificate> generate({DateTime? now}) async {
    final random = Random.secure();
    final d = Uint8List.fromList([
      for (var i = 0; i < 32; i++) random.nextInt(256),
    ])..[0] &= 0x7F;
    final scalar = d.fold(BigInt.zero, (a, b) => (a << 8) | BigInt.from(b));
    final keys = SoftwareDeviceKeys.withScalar('server', scalar);
    final key = (await keys.load('server', KeyPurpose.dpop))!.publicKey;
    final x = key.x;
    final y = key.y;
    final spki = _seq([
      _seq([
        _oid([1, 2, 840, 10045, 2, 1]),
        _oid([1, 2, 840, 10045, 3, 1, 7]),
      ]),
      _tlv(0x03, [0, 4, ...x, ...y]),
    ]);
    final signatureAlgorithm = _seq([
      _oid([1, 2, 840, 10045, 4, 3, 2]),
    ]);
    final name = _seq([
      _tlv(
        0x31,
        _seq([
          _oid([2, 5, 4, 3]),
          _tlv(0x0C, utf8.encode('localhost')),
        ]),
      ),
    ]);
    final start = (now ?? DateTime.now().toUtc()).subtract(
      const Duration(hours: 1),
    );
    final tbs = _seq([
      _tlv(0xA0, _tlv(0x02, [2])),
      _tlv(0x02, [1, 2, 3, 4, 5, 6, 7, 8]),
      signatureAlgorithm,
      name,
      _seq([_utcTime(start), _utcTime(start.add(const Duration(days: 1)))]),
      name,
      spki,
      _tlv(
        0xA3,
        _seq([
          // basicConstraints: CA, critical.
          _seq([
            _oid([2, 5, 29, 19]),
            _tlv(0x01, [0xFF]),
            _tlv(
              0x04,
              _seq([
                _tlv(0x01, [0xFF]),
              ]),
            ),
          ]),
          // subjectAltName: DNS:localhost.
          _seq([
            _oid([2, 5, 29, 17]),
            _tlv(0x04, _seq([_tlv(0x82, utf8.encode('localhost'))])),
          ]),
        ]),
      ),
    ]);
    final raw = await keys.sign('server', tbs);
    final der = _seq([
      tbs,
      signatureAlgorithm,
      _tlv(0x03, [
        0,
        ..._seq([_integer(raw.sublist(0, 32)), _integer(raw.sublist(32))]),
      ]),
    ]);
    // PKCS #8: ECPrivateKey (RFC 5915) inside a PrivateKeyInfo.
    final pkcs8 = _seq([
      _tlv(0x02, [0]),
      _seq([
        _oid([1, 2, 840, 10045, 2, 1]),
        _oid([1, 2, 840, 10045, 3, 1, 7]),
      ]),
      _tlv(
        0x04,
        _seq([
          _tlv(0x02, [1]),
          _tlv(0x04, d),
          _tlv(0xA1, _tlv(0x03, [0, 4, ...x, ...y])),
        ]),
      ),
    ]);
    return TestCertificate._(der, _pem('PRIVATE KEY', pkcs8), spki);
  }

  /// The certificate, DER.
  final Uint8List der;

  /// The private key, PKCS #8 PEM.
  final String keyPem;

  /// The SubjectPublicKeyInfo the generator wrote, DER.
  final Uint8List spki;

  /// The pin of the key (RFC 7469), from the SPKI bytes written above and
  /// not from the certificate.
  String get pin => base64.encode(sha256.convert(spki).bytes);

  /// The certificate, PEM.
  String get pem => _pem('CERTIFICATE', der);

  /// A server context holding the certificate and its key.
  SecurityContext serverContext() => SecurityContext(withTrustedRoots: false)
    ..useCertificateChainBytes(utf8.encode(pem))
    ..usePrivateKeyBytes(utf8.encode(keyPem));

  /// A client context that trusts only this certificate.
  SecurityContext clientContext() =>
      SecurityContext(withTrustedRoots: false)
        ..setTrustedCertificatesBytes(utf8.encode(pem));
}

/// A pin that matches no key: the hash of [seed].
String unrelatedPin(int seed) =>
    base64.encode(sha256.convert([seed, 7, 7, 7]).bytes);

Uint8List _integer(List<int> bigEndian) {
  var v = bigEndian.skipWhile((b) => b == 0).toList();
  if (v.isEmpty || v.first & 0x80 != 0) v = [0, ...v];
  return _tlv(0x02, v);
}

String _pem(String label, List<int> der) {
  final b64 = base64.encode(der);
  final lines = [
    for (var i = 0; i < b64.length; i += 64)
      b64.substring(i, i + 64 > b64.length ? b64.length : i + 64),
  ];
  return '-----BEGIN $label-----\n${lines.join('\n')}\n-----END $label-----\n';
}

Uint8List _tlv(int tag, List<int> content) {
  final n = content.length;
  final length = n < 0x80
      ? [n]
      : n < 0x100
      ? [0x81, n]
      : [0x82, n >> 8, n & 0xFF];
  return Uint8List.fromList([tag, ...length, ...content]);
}

Uint8List _seq(List<List<int>> parts) =>
    _tlv(0x30, [for (final p in parts) ...p]);

Uint8List _oid(List<int> arcs) {
  final out = <int>[arcs[0] * 40 + arcs[1]];
  for (final a in arcs.skip(2)) {
    final groups = <int>[a & 0x7F];
    for (var v = a >> 7; v > 0; v >>= 7) {
      groups.insert(0, (v & 0x7F) | 0x80);
    }
    out.addAll(groups);
  }
  return _tlv(0x06, out);
}

Uint8List _utcTime(DateTime t) {
  String two(int v) => v.toString().padLeft(2, '0');
  final u = t.toUtc();
  return _tlv(
    0x17,
    ascii.encode(
      '${two(u.year % 100)}${two(u.month)}${two(u.day)}'
      '${two(u.hour)}${two(u.minute)}${two(u.second)}Z',
    ),
  );
}
