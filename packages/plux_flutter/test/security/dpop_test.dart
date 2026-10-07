// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/security/device_keys.dart';
import 'package:plux_flutter/src/security/dpop.dart';

final class _FakeKeys implements DeviceKeys {
  final List<({String alias, Uint8List data})> signed = [];

  @override
  Future<Uint8List> sign(String alias, Uint8List data) async {
    signed.add((alias: alias, data: data));
    return Uint8List.fromList([for (var i = 0; i < 64; i++) i]);
  }

  @override
  Future<DeviceKey> create(
    String alias,
    KeyPurpose purpose, {
    Uint8List? challenge,
    bool strongBox = true,
  }) => throw UnimplementedError();

  @override
  Future<DeviceKey?> load(String alias, KeyPurpose purpose) =>
      throw UnimplementedError();

  @override
  Future<void> delete(String alias) => throw UnimplementedError();
}

final _x = Uint8List.fromList([for (var i = 0; i < 32; i++) i]);
final _y = Uint8List.fromList([for (var i = 32; i < 64; i++) i]);

const _xB64 = 'AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8';
const _yB64 = 'ICEiIyQlJicoKSorLC0uLzAxMjM0NTY3ODk6Ozw9Pj8';

// The signature the fake returns: bytes 0..63, base64url without padding.
const _sigB64 =
    'AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8gISIjJCUmJygpKissLS4vMDEyMzQ1Njc4OTo7PD0-Pw';

// SEC-021: the RFC 9449 §4.2 example access token and its ath, which is
// also what Python gives:
//   base64.urlsafe_b64encode(hashlib.sha256(tok.encode()).digest())
const _rfcToken = 'Kz~8mXK1EalYznwH-LC-1fBAo.4Ljp~zsPE_NeO.gxU';
const _rfcAth = 'fUHyO2r2Z3DZ53EsNrWBb0xWXoaNy59IiKCAqksmQEo';

const _header =
    '{"typ":"dpop+jwt","alg":"ES256","jwk":{"kty":"EC","crv":"P-256",'
    '"x":"$_xB64","y":"$_yB64"}}';

String _decode(String part) =>
    utf8.decode(base64Url.decode(base64Url.normalize(part)));

void main() {
  late _FakeKeys keys;
  late DpopProofs dpop;
  var jti = 0;

  setUp(() {
    keys = _FakeKeys();
    jti = 0;
    dpop = DpopProofs(
      keys: keys,
      alias: 'dev.plux.dpop.abc',
      publicKey: EcPublicKey(_x, _y),
      now: () =>
          DateTime.fromMillisecondsSinceEpoch(1700000000999, isUtc: true),
      newJti: () => 'jti-${++jti}',
    );
  });

  Future<List<String>> parts(
    String method,
    String uri, {
    String? accessToken,
    String? nonce,
  }) async {
    final p = await dpop.proof(
      method: method,
      uri: Uri.parse(uri),
      accessToken: accessToken,
      nonce: nonce,
    );
    return p.split('.');
  }

  Future<String> payload(
    String uri, {
    String? accessToken,
    String? nonce,
  }) async => _decode(
    (await parts('POST', uri, accessToken: accessToken, nonce: nonce))[1],
  );

  group('DpopProofs.proof', () {
    test('SEC-021 header and payload are exact', () async {
      final p = await parts('POST', 'https://api.example.com/v1/sync');
      expect(p, hasLength(3));
      expect(_decode(p[0]), _header);
      expect(
        _decode(p[1]),
        '{"htm":"POST","htu":"https://api.example.com/v1/sync",'
        '"iat":1700000000,"jti":"jti-1"}',
      );
      expect(p[2], _sigB64);
      expect(p.join('.'), isNot(contains('=')));
    });

    test('SEC-021 signs the ASCII of header.payload with the alias', () async {
      final p = await parts('GET', 'https://api.example.com/a');
      final call = keys.signed.single;
      expect(call.alias, 'dev.plux.dpop.abc');
      expect(ascii.decode(call.data), '${p[0]}.${p[1]}');
    });

    test('iat is seconds from the injected clock', () async {
      expect(
        await payload('https://a.example/x'),
        contains('"iat":1700000000'),
      );
      var now = DateTime.fromMillisecondsSinceEpoch(5000, isUtc: true);
      final later = DpopProofs(
        keys: keys,
        alias: 'a',
        publicKey: EcPublicKey(_x, _y),
        now: () => now,
        newJti: () => 'j',
      );
      String iat(String proof) =>
          (jsonDecode(_decode(proof.split('.')[1])) as Map)['iat'].toString();
      expect(
        iat(
          await later.proof(
            method: 'GET',
            uri: Uri.parse('https://a.example/'),
          ),
        ),
        '5',
      );
      now = now.add(const Duration(seconds: 61, milliseconds: 400));
      expect(
        iat(
          await later.proof(
            method: 'GET',
            uri: Uri.parse('https://a.example/'),
          ),
        ),
        '66',
      );
    });

    test('ath is the SHA-256 of the access token', () async {
      expect(
        await payload('https://a.example/x', accessToken: _rfcToken),
        '{"htm":"POST","htu":"https://a.example/x","iat":1700000000,'
        '"jti":"jti-1","ath":"$_rfcAth"}',
      );
      expect(
        await payload('https://a.example/x', accessToken: _rfcToken),
        contains('"ath":"$_rfcAth"'),
      );
    });

    test('nonce is present only when given, after ath', () async {
      expect(
        await payload('https://a.example/x'),
        isNot(anyOf(contains('nonce'), contains('ath'))),
      );
      expect(
        await payload('https://a.example/x', nonce: 'n-1'),
        '{"htm":"POST","htu":"https://a.example/x","iat":1700000000,'
        '"jti":"jti-2","nonce":"n-1"}',
      );
      expect(
        await payload(
          'https://a.example/x',
          accessToken: _rfcToken,
          nonce: 'n-2',
        ),
        '{"htm":"POST","htu":"https://a.example/x","iat":1700000000,'
        '"jti":"jti-3","ath":"$_rfcAth","nonce":"n-2"}',
      );
    });

    test('a new jti for every proof', () async {
      final a = await parts('GET', 'https://a.example/');
      final b = await parts('GET', 'https://a.example/');
      expect(_decode(a[1]), contains('jti-1'));
      expect(_decode(b[1]), contains('jti-2'));
    });

    test('the same inputs give the same proof', () async {
      String again() => 'fixed';
      final d = DpopProofs(
        keys: keys,
        alias: 'a',
        publicKey: EcPublicKey(_x, _y),
        now: () => DateTime.fromMillisecondsSinceEpoch(0, isUtc: true),
        newJti: again,
      );
      final uri = Uri.parse('https://a.example/p');
      expect(
        await d.proof(method: 'GET', uri: uri, nonce: 'n'),
        await d.proof(method: 'GET', uri: uri, nonce: 'n'),
      );
    });

    test('the default jti is 16 random bytes, base64url, unique', () async {
      final d = DpopProofs(
        keys: keys,
        alias: 'a',
        publicKey: EcPublicKey(_x, _y),
      );
      final ids = <String>{};
      for (var i = 0; i < 20; i++) {
        final p = await d.proof(
          method: 'GET',
          uri: Uri.parse('https://a.example/'),
        );
        final claims = jsonDecode(_decode(p.split('.')[1])) as Map;
        final id = claims['jti']! as String;
        expect(id, matches(RegExp(r'^[A-Za-z0-9_-]{22}$')));
        expect(base64Url.decode(base64Url.normalize(id)), hasLength(16));
        ids.add(id);
      }
      expect(ids, hasLength(20));
    });

    test('the default clock is the current time', () async {
      final d = DpopProofs(
        keys: keys,
        alias: 'a',
        publicKey: EcPublicKey(_x, _y),
      );
      final before = DateTime.now().millisecondsSinceEpoch ~/ 1000;
      final p = await d.proof(
        method: 'GET',
        uri: Uri.parse('https://a.example/'),
      );
      final after = DateTime.now().millisecondsSinceEpoch ~/ 1000;
      final iat = (jsonDecode(_decode(p.split('.')[1])) as Map)['iat']! as int;
      expect(iat, inInclusiveRange(before, after));
    });
  });

  group('htu (RFC 9449 §4.2)', () {
    const cases = <String, String>{
      'https://api.example.com/v1/sync': 'https://api.example.com/v1/sync',
      'https://api.example.com/v1/sync?a=1&b=2':
          'https://api.example.com/v1/sync',
      'https://api.example.com/v1/sync#frag': 'https://api.example.com/v1/sync',
      'https://api.example.com/v1/sync?a=1#frag':
          'https://api.example.com/v1/sync',
      'HTTPS://API.Example.COM/V1/Sync': 'https://api.example.com/V1/Sync',
      'https://api.example.com': 'https://api.example.com/',
      'https://api.example.com?x=1': 'https://api.example.com/',
      'https://api.example.com:443/a': 'https://api.example.com/a',
      'http://api.example.com:80/a': 'http://api.example.com/a',
      'https://api.example.com:8443/a': 'https://api.example.com:8443/a',
      'http://localhost:8080/a/b/': 'http://localhost:8080/a/b/',
      'https://[::1]:8443/a': 'https://[::1]:8443/a',
      'https://user:pw@api.example.com/a': 'https://api.example.com/a',
      'https://api.example.com/a%20b': 'https://api.example.com/a%20b',
    };
    for (final c in cases.entries) {
      test('${c.key} -> ${c.value}', () async {
        expect(dpopHtu(Uri.parse(c.key)), c.value);
        expect(await payload(c.key), contains('"htu":"${c.value}"'));
      });
    }
  });
}
