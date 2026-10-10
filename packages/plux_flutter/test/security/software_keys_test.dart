// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:convert';
import 'dart:typed_data';

import 'package:crypto/crypto.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/security/attestation.dart';
import 'package:plux_flutter/src/security/device_keys.dart';
import 'package:plux_flutter/src/security/dpop.dart';
import 'package:plux_flutter/src/security/software_keys.dart';

BigInt _hex(String h) => BigInt.parse(h, radix: 16);

BigInt _int(List<int> bytes) =>
    bytes.fold(BigInt.zero, (a, b) => (a << 8) | BigInt.from(b));

final _p = _hex(
  'ffffffff00000001000000000000000000000000ffffffffffffffffffffffff',
);
final _b = _hex(
  '5ac635d8aa3a93e7b3ebbd55769886bc651d06b0cc53b0f63bce3c3e27d2604b',
);
final _n = _hex(
  'ffffffff00000000ffffffffffffffffbce6faada7179e84f3b9cac2fc632551',
);
final _g = (
  _hex('6b17d1f2e12c4247f8bce6e563a440f277037d812deb33a0f4a13945d898c296'),
  _hex('4fe342e2fe1a7f9b8ee7eb4a7c0f9e162bce33576b315ececbb6406837bf51f5'),
);

typedef _Pt = (BigInt, BigInt);

// The verifier's own affine arithmetic, written separately from the code
// under test; the published vectors below tie both to the standard.
_Pt? _add(_Pt? a, _Pt? b) {
  if (a == null) return b;
  if (b == null) return a;
  final BigInt m;
  if (a.$1 == b.$1) {
    if ((a.$2 + b.$2) % _p == BigInt.zero) return null;
    m =
        BigInt.from(3) *
        (a.$1 * a.$1 - BigInt.one) *
        (BigInt.two * a.$2).modInverse(_p) %
        _p;
  } else {
    m = (b.$2 - a.$2) * (b.$1 - a.$1).modInverse(_p) % _p;
  }
  final x = (m * m - a.$1 - b.$1) % _p;
  return (x, (m * (a.$1 - x) - a.$2) % _p);
}

_Pt? _mul(BigInt k, _Pt p) {
  _Pt? acc;
  for (var i = k.bitLength - 1; i >= 0; i--) {
    acc = _add(acc, acc);
    if ((k >> i).isOdd) acc = _add(acc, p);
  }
  return acc;
}

bool _onCurve(_Pt q) =>
    (q.$2 * q.$2 - (q.$1 * q.$1 * q.$1 - BigInt.from(3) * q.$1 + _b)) % _p ==
    BigInt.zero;

/// ECDSA verification (FIPS 186-4 §6.4) of the 64-byte `r || s` [sig] over
/// SHA-256 of [data].
bool _verify(_Pt q, List<int> data, List<int> sig) {
  if (sig.length != 64) return false;
  final r = _int(sig.sublist(0, 32));
  final s = _int(sig.sublist(32));
  if (r < BigInt.one || r >= _n || s < BigInt.one || s >= _n) return false;
  final z = _int(sha256.convert(data).bytes);
  final w = s.modInverse(_n);
  final x = _add(_mul(z * w % _n, _g), _mul(r * w % _n, q));
  return x != null && x.$1 % _n == r;
}

_Pt _point(EcPublicKey k) => (_int(k.x), _int(k.y));

List<int> _unb64(String s) => base64Url.decode(base64Url.normalize(s));

Uint8List _be32(BigInt v) => Uint8List.fromList([
  for (var i = 31; i >= 0; i--) ((v >> (8 * i)) & BigInt.from(0xff)).toInt(),
]);

// RFC 6979 A.2.5: P-256 with SHA-256.
final _rfcX = _hex(
  'c9afa9d845ba75166b5c215767b1d6934e50c3db36e89b127b8a622b120f6721',
);
final _rfcUx = _hex(
  '60fed4ba255a9d31c961eb74c6356d68c049b8923b61fa6ce669622e60f29fb6',
);
final _rfcUy = _hex(
  '7903fe1008b8bc99a41ae9e95628bc64f2f1b20c2d7e9f5177a3c294d4462299',
);

void main() {
  test('create returns an on-curve software key without a chain', () async {
    final keys = SoftwareDeviceKeys();
    final key = await keys.create('a', KeyPurpose.dpop);
    expect(key.storage, KeyStorage.software);
    expect(key.storage.hardware, isFalse);
    expect(key.attestationChain, isEmpty);
    expect(key.alias, 'a');
    expect(_onCurve(_point(key.publicKey)), isTrue);
  });

  test(
    'load returns the created key; create replaces it; delete removes it',
    () async {
      final keys = SoftwareDeviceKeys();
      expect(await keys.load('a', KeyPurpose.dpop), isNull);
      final first = await keys.create('a', KeyPurpose.dpop);
      final loaded = await keys.load('a', KeyPurpose.dpop);
      expect(loaded!.publicKey.thumbprint, first.publicKey.thumbprint);
      final second = await keys.create('a', KeyPurpose.dpop);
      expect(second.publicKey.thumbprint, isNot(first.publicKey.thumbprint));
      await keys.delete('a');
      await keys.delete('a');
      expect(await keys.load('a', KeyPurpose.dpop), isNull);
      await expectLater(
        keys.sign('a', Uint8List(1)),
        throwsA(isA<StateError>()),
      );
    },
  );

  test('a signature is 64 bytes and verifies; tampering does not', () async {
    final keys = SoftwareDeviceKeys();
    final key = await keys.create('a', KeyPurpose.dpop);
    final data = Uint8List.fromList(utf8.encode('hello plux'));
    final sig = await keys.sign('a', data);
    expect(sig, hasLength(64));
    expect(_verify(_point(key.publicKey), data, sig), isTrue);
    expect(_verify(_point(key.publicKey), utf8.encode('other'), sig), isFalse);
    final other = await SoftwareDeviceKeys().create('a', KeyPurpose.dpop);
    expect(_verify(_point(other.publicKey), data, sig), isFalse);
    final again = await keys.sign('a', data);
    expect(again, isNot(sig), reason: 'k is fresh for every signature');
    expect(_verify(_point(key.publicKey), data, again), isTrue);
  });

  test(
    'the RFC 6979 A.2.5 private key derives the published public key',
    () async {
      final keys = SoftwareDeviceKeys.withScalar('rfc', _rfcX);
      final key = (await keys.load('rfc', KeyPurpose.dpop))!;
      expect(_int(key.publicKey.x), _rfcUx);
      expect(_int(key.publicKey.y), _rfcUy);
    },
  );

  test('the RFC 6979 A.2.5 "sample" signature verifies with that key', () {
    final r = _hex(
      'efd48b2aacb6a8fd1140dd9cd45e81d69d2c877b56aaf991c34d0ea84eaf3716',
    );
    final s = _hex(
      'f7cb1c942d657c41d436c7a1b6e29f65f3e900dbb9aff4064dc4ab2f843acda8',
    );
    final sig = [..._be32(r), ..._be32(s)];
    final q = (_rfcUx, _rfcUy);
    expect(_verify(q, utf8.encode('sample'), sig), isTrue);
    expect(_verify(q, utf8.encode('test'), sig), isFalse);
  });

  test('signing with the RFC key verifies under the published key', () async {
    final keys = SoftwareDeviceKeys.withScalar('rfc', _rfcX);
    final data = Uint8List.fromList(utf8.encode('sample'));
    expect(
      _verify((_rfcUx, _rfcUy), data, await keys.sign('rfc', data)),
      isTrue,
    );
  });

  test('withScalar rejects scalars outside [1, n-1]', () {
    expect(
      () => SoftwareDeviceKeys.withScalar('a', BigInt.zero),
      throwsArgumentError,
    );
    expect(() => SoftwareDeviceKeys.withScalar('a', _n), throwsArgumentError);
  });

  test(
    'a DPoP proof made with these keys has a valid ES256 signature',
    () async {
      final keys = SoftwareDeviceKeys();
      final key = await keys.create('dpop', KeyPurpose.dpop);
      final proofs = DpopProofs(
        keys: keys,
        alias: 'dpop',
        publicKey: key.publicKey,
      );
      final jws = await proofs.proof(
        method: 'POST',
        uri: Uri.parse('https://plux.example/v1/sync'),
        accessToken: 'token',
      );
      final parts = jws.split('.');
      expect(parts, hasLength(3));
      final header =
          jsonDecode(utf8.decode(_unb64(parts[0]))) as Map<String, Object?>;
      expect(header['alg'], 'ES256');
      expect(header['jwk'], key.publicKey.jwk);
      expect(
        _verify(
          _point(key.publicKey),
          ascii.encode('${parts[0]}.${parts[1]}'),
          _unb64(parts[2]),
        ),
        isTrue,
      );
    },
  );

  test('development attestation offers development evidence only', () async {
    const attestation = DevelopmentAttestation('e2e');
    final evidence = await attestation.attest(
      challenge: Uint8List(32),
      jkt: 'jkt',
      keyAttestationChain: const [],
    );
    expect(evidence, isA<DevelopmentEvidence>());
    expect((evidence as DevelopmentEvidence).buildId, 'e2e');
    expect(await attestation.assertion(Uint8List(32)), isNull);
    expect(await attestation.integrityToken('hash'), isNull);
  });
}
