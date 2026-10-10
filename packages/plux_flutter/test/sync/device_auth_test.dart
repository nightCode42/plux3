// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:convert';
import 'dart:io' show gzip;
import 'dart:typed_data';

import 'package:crypto/crypto.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:plux_flutter/src/security/attestation.dart';
import 'package:plux_flutter/src/security/device_keys.dart';
import 'package:plux_flutter/src/sync/api_client.dart';
import 'package:plux_flutter/src/sync/device_auth.dart';
import 'package:plux_flutter/src/sync/sync_engine.dart';

import 'fake_device.dart';

const _challengePath = '/plux.v1.DeviceService/CreateRegistrationChallenge';
const _registerPath = '/plux.v1.DeviceService/RegisterAttestedDevice';
const _reattestPath = '/plux.v1.DeviceService/ReattestDevice';
const _refreshPath = '/plux.v1.TokenService/RefreshDeviceToken';
const _manifestPath = '/plux.v1.ManifestService/GetManifest';
const _reportPath = '/plux.v1.DeviceService/ReportInstalled';

final _challenge = Uint8List.fromList([for (var i = 1; i <= 32; i++) i]);

String _b64url(List<int> bytes) => base64Url.encode(bytes).replaceAll('=', '');

/// One request the client made.
final class _Seen {
  _Seen(this.path, this.headers, this.body);
  final String path;
  final Map<String, String> headers;
  final Map<String, Object?> body;

  String? get proof => headers['dpop'];

  Map<String, Object?> get claims => jsonDecode(
    utf8.decode(base64Url.decode(base64Url.normalize(proof!.split('.')[1]))),
  ) as Map<String, Object?>;
}

http.Response _json(
  Object body, {
  int status = 200,
  Map<String, String> headers = const {},
}) => http.Response(jsonEncode(body), status, headers: headers);

http.Response _error(int status, String code, String message) =>
    _json({'code': code, 'message': message}, status: status);

/// A scripted server: answers per path from a queue, then by default.
final class _Rig {
  _Rig({this.platform = 'android'}) {
    client = MockClient((req) async {
      final path = req.url.path;
      seen.add(
        _Seen(
          path,
          req.headers,
          jsonDecode(
            utf8.decode(
              req.headers['content-encoding'] == 'gzip'
                  ? gzip.decode(req.bodyBytes)
                  : req.bodyBytes,
            ),
          ) as Map<String, Object?>,
        ),
      );
      final queued = script[path];
      if (queued != null && queued.isNotEmpty) return queued.removeAt(0)();
      return _default(path);
    });
    api = PluxApiClient(client, Uri.parse('https://plux.example/'));
  }

  final String platform;
  late final http.Client client;
  late final PluxApiClient api;
  final List<_Seen> seen = [];
  final Map<String, List<http.Response Function()>> script = {};
  final keys = FakeDeviceKeys();
  final attestation = FakeAttestation();
  final credentials = MemoryCredentialStore();
  var now = DateTime.utc(2026, 9, 28, 12);
  var devices = 0;
  var lifetime = const Duration(minutes: 15);
  Object? assuranceLevel;

  DeviceAuth get auth => _auth ??= DeviceAuth(
    api: api,
    keys: keys,
    attestation: attestation,
    credentials: credentials,
    appId: 'app_1',
    environment: 'production',
    device: DeviceInfo(
      platform: platform,
      osVersion: '14',
      runtimeVersion: '0.1.0',
      hostBuild: '7',
    ),
    clock: () => now,
  );
  DeviceAuth? _auth;

  http.Response _default(String path) => switch (path) {
    _challengePath => _json({
      'challenge': base64.encode(_challenge),
      'expiresAt': '2100-01-01T00:00:00Z',
    }),
    _registerPath => _json({
      'device': {'id': 'dev-${++devices}'},
    }),
    _reattestPath => _json(<String, Object?>{}),
    _refreshPath => _json({
      'accessToken': 'tok-$devices',
      'expiresAt': now.add(lifetime).toIso8601String(),
      'tokenType': 'DPoP',
      'assuranceLevel': ?assuranceLevel,
    }),
    _ => _json(<String, Object?>{}),
  };

  List<String> get paths => [for (final s in seen) s.path];
  List<_Seen> at(String path) => [
    for (final s in seen)
      if (s.path == path) s,
  ];
}

String _alias() => dpopKeyAlias('app_1', 'production');

void main() {
  group('registration', () {
    test(
      'Android registers with the key attestation chain and a Play Integrity '
      'token, then exchanges a DPoP proof for a token [SEC-002] [SEC-021]',
      () async {
        final rig = _Rig()
          ..keys.chain = [
            Uint8List.fromList([1, 2, 3]),
            Uint8List.fromList([4, 5]),
          ]
          ..attestation.evidence = AndroidEvidence(
            keyAttestationChain: [
              Uint8List.fromList([1, 2, 3]),
              Uint8List.fromList([4, 5]),
            ],
            playIntegrityToken: 'integrity-token',
          );
        final token = await rig.auth.token();
        expect(token.value, 'tok-1');
        expect(rig.paths, [_challengePath, _registerPath, _refreshPath]);

        final key = rig.keys.stored[_alias()]!;
        final jkt = key.publicKey.thumbprint;
        expect(rig.keys.challenges, [_challenge]);
        final attested = rig.attestation.attested.single;
        expect(attested.challenge, _challenge);
        expect(attested.jkt, jkt);
        expect(attested.chain, rig.keys.chain);

        final challenge = rig.seen[0];
        expect(challenge.body, {'appId': 'app_1', 'environment': 'production'});
        expect(challenge.headers['content-type'], 'application/json');
        expect(challenge.headers['connect-protocol-version'], '1');
        expect(challenge.headers, isNot(contains('authorization')));
        expect(challenge.headers, isNot(contains('dpop')));

        final register = rig.seen[1];
        expect(register.body, {
          'appId': 'app_1',
          'environment': 'production',
          'platform': 'android',
          'osVersion': '14',
          'runtimeVersion': '0.1.0',
          'hostBuild': '7',
          'challenge': base64.encode(_challenge),
          'dpopPublicKeyJwk': base64.encode(
            utf8.encode(jsonEncode(key.publicKey.jwk)),
          ),
          'keyStorage': 'KEY_STORAGE_TEE',
          'evidence': {
            'android': {
              'keyAttestationChain': [
                base64.encode([1, 2, 3]),
                base64.encode([4, 5]),
              ],
              'playIntegrityToken': 'integrity-token',
            },
          },
        });
        expect(register.headers, isNot(contains('authorization')));
        expect(register.headers, isNot(contains('dpop')));

        final refresh = rig.seen[2];
        expect(refresh.body, {'deviceId': 'dev-1'});
        expect(refresh.headers, isNot(contains('authorization')));
        expect(refresh.claims, containsPair('htm', 'POST'));
        expect(
          refresh.claims,
          containsPair(
            'htu',
            'https://plux.example/plux.v1.TokenService/RefreshDeviceToken',
          ),
        );
        expect(refresh.claims, isNot(contains('ath')));
        expect(refresh.claims, isNot(contains('nonce')));
        expect(refresh.proof!.split('.'), hasLength(3));

        final stored = await rig.credentials.read();
        expect((stored!.deviceId, stored.jkt), ('dev-1', jkt));
      },
    );

    test('iOS registers with an App Attest object and binds every refresh to '
        'its proof with an assertion [SEC-002] [SEC-025]', () async {
      final rig = _Rig(platform: 'ios')
        ..keys.storage = KeyStorage.secureEnclave
        ..attestation.assertionBytes = Uint8List.fromList([9, 8, 7])
        ..attestation.evidence = IosEvidence(
          keyId: Uint8List.fromList(List.filled(32, 5)),
          attestationObject: Uint8List.fromList([6, 6, 6]),
        );
      await rig.auth.token();
      final register = rig.at(_registerPath).single.body;
      expect(register['platform'], 'ios');
      expect(register['keyStorage'], 'KEY_STORAGE_SECURE_ENCLAVE');
      expect(register['evidence'], {
        'ios': {
          'appAttestKeyId': base64.encode(List.filled(32, 5)),
          'attestationObject': base64.encode([6, 6, 6]),
        },
      });
      final refresh = rig.at(_refreshPath).single;
      expect(refresh.body, {
        'deviceId': 'dev-1',
        'appAttestAssertion': base64.encode([9, 8, 7]),
      });
      expect(
        rig.attestation.assertedHashes.single,
        sha256.convert(utf8.encode(refresh.proof!)).bytes,
        reason: 'the assertion covers the SHA-256 of that request\'s proof',
      );
    });

    test('a null assertion adds nothing to the refresh [SEC-025]', () async {
      final rig = _Rig(platform: 'ios');
      await rig.auth.token();
      expect(rig.at(_refreshPath).single.body, {'deviceId': 'dev-1'});
      expect(rig.attestation.assertedHashes, hasLength(1));
    });

    test('development evidence registers a software key, whatever the key '
        'storage, since it proves nothing about the key [SEC-008]', () async {
      final rig = _Rig()..keys.storage = KeyStorage.secureEnclave;
      await rig.auth.token();
      final register = rig.at(_registerPath).single.body;
      expect(register['keyStorage'], 'KEY_STORAGE_SOFTWARE');
      expect(register['evidence'], {
        'development': {'buildId': 'build-1'},
      });
    });

    test('every key storage has its wire name [SEC-001]', () async {
      for (final (storage, name) in [
        (KeyStorage.strongbox, 'KEY_STORAGE_STRONGBOX'),
        (KeyStorage.tee, 'KEY_STORAGE_TEE'),
        (KeyStorage.secureEnclave, 'KEY_STORAGE_SECURE_ENCLAVE'),
        (KeyStorage.software, 'KEY_STORAGE_SOFTWARE'),
      ]) {
        final rig = _Rig()..keys.storage = storage;
        rig.attestation.evidence = AndroidEvidence(
          keyAttestationChain: [Uint8List(1)],
          playIntegrityToken: 'token',
        );
        await rig.auth.token();
        expect(rig.at(_registerPath).single.body['keyStorage'], name);
      }
    });

    test('a stored registration whose key is gone, or is another key, or '
        'predates DPoP is registered again [SEC-001]', () async {
      // A credential and no key in the platform.
      var rig = _Rig();
      await rig.credentials.write(const DeviceCredential('old', 'jkt-x'));
      await rig.auth.token();
      expect(rig.at(_registerPath), hasLength(1));
      expect((await rig.credentials.read())!.deviceId, 'dev-1');

      // A key with another thumbprint than the credential's.
      rig = _Rig();
      await rig.keys.create(_alias(), KeyPurpose.dpop);
      await rig.credentials.write(const DeviceCredential('old', 'jkt-x'));
      await rig.auth.token();
      expect(rig.at(_registerPath), hasLength(1));
      expect(rig.keys.created, 2);

      // The matching key and credential register nothing.
      rig = _Rig();
      final key = await rig.keys.create(_alias(), KeyPurpose.dpop);
      await rig.credentials.write(
        DeviceCredential('known', key.publicKey.thumbprint),
      );
      await rig.auth.token();
      expect(rig.paths, [_refreshPath]);
      expect(rig.seen.single.body, {'deviceId': 'known'});
    });
  });

  group('tokens', () {
    test(
      'a token is refreshed thirty seconds before it expires [SEC-021]',
      () async {
        final rig = _Rig();
        final first = await rig.auth.token();
        expect(identical(await rig.auth.token(), first), isTrue);
        rig.now = rig.now.add(const Duration(minutes: 14, seconds: 29));
        expect(identical(await rig.auth.token(), first), isTrue);
        expect(rig.at(_refreshPath), hasLength(1));
        rig.now = rig.now.add(const Duration(seconds: 1));
        final second = await rig.auth.token();
        expect(identical(second, first), isFalse);
        expect(rig.at(_refreshPath), hasLength(2));
        expect(rig.at(_registerPath), hasLength(1));
      },
    );

    test('concurrent callers share one exchange', () async {
      final rig = _Rig();
      final tokens = await Future.wait([rig.auth.token(), rig.auth.token()]);
      expect(identical(tokens[0], tokens[1]), isTrue);
      expect(rig.at(_refreshPath), hasLength(1));
    });

    test('a forgotten token is exchanged again', () async {
      final rig = _Rig();
      await rig.auth.token();
      rig.auth.forgetToken();
      await rig.auth.token();
      expect(rig.at(_refreshPath), hasLength(2));
    });

    test('a manifest request carries Authorization: DPoP and a proof bound to '
        'the token with ath [SEC-021]', () async {
      final rig = _Rig();
      final token = await rig.auth.token();
      await rig.api.reportInstalled(token, 'dev-1', 3);
      final r = rig.at(_reportPath).single;
      expect(r.headers['authorization'], 'DPoP tok-1');
      expect(r.claims, containsPair('htm', 'POST'));
      expect(
        r.claims,
        containsPair(
          'htu',
          'https://plux.example/plux.v1.DeviceService/ReportInstalled',
        ),
      );
      expect(
        r.claims,
        containsPair(
          'ath',
          _b64url(sha256.convert(utf8.encode('tok-1')).bytes),
        ),
      );
      rig.script[_manifestPath] = [
        () => _json({'etag': '"e"', 'notModified': true}),
      ];
      await rig.api.manifest(
        token: token,
        appId: 'app_1',
        environment: 'production',
        channel: 'production',
        installedSequence: 0,
        installed: const {},
        ifNoneMatch: '',
      );
      final m = rig.at(_manifestPath).single;
      expect(m.headers['authorization'], 'DPoP tok-1');
      expect(m.claims['ath'], r.claims['ath']);
      expect(m.claims['htu'], endsWith('GetManifest'));
      expect(
        rig.seen.expand((s) => s.headers.values),
        isNot(contains(startsWith('Bearer'))),
      );
      await rig.api.ingestEvents(
        token,
        appId: 'app_1',
        environment: 'production',
        events: const [],
      );
      expect(
        rig.at('/plux.v1.TelemetryService/IngestEvents').single.headers,
        containsPair('authorization', 'DPoP tok-1'),
      );
    });
  });

  group('nonces', () {
    http.Response needNonce(String nonce) => _json(
      {'code': 'unauthenticated', 'message': 'PLX-6012: nonce required'},
      status: 401,
      headers: {
        'www-authenticate': 'DPoP error="use_dpop_nonce"',
        'dpop-nonce': nonce,
      },
    );

    test('a request refused for its nonce is repeated once with the new one, '
        'and later proofs carry the latest nonce [SEC-021]', () async {
      final rig = _Rig();
      final token = await rig.auth.token();
      rig.script[_reportPath] = [
        () => needNonce('n1'),
        () => _json(<String, Object?>{}, headers: {'dpop-nonce': 'n2'}),
      ];
      await rig.api.reportInstalled(token, 'dev-1', 3);
      final tries = rig.at(_reportPath);
      expect(tries, hasLength(2));
      expect(tries[0].claims, isNot(contains('nonce')));
      expect(tries[1].claims['nonce'], 'n1');
      expect(tries[0].proof, isNot(tries[1].proof));
      await rig.api.reportInstalled(token, 'dev-1', 4);
      expect(rig.at(_reportPath)[2].claims['nonce'], 'n2');
    });

    test('the retry happens once, not twice [SEC-021]', () async {
      final rig = _Rig();
      final token = await rig.auth.token();
      rig.script[_reportPath] = [() => needNonce('n1'), () => needNonce('n2')];
      await expectLater(
        rig.api.reportInstalled(token, 'dev-1', 3),
        throwsA(isA<ApiError>().having((e) => e.status, 'status', 401)),
      );
      expect(rig.at(_reportPath), hasLength(2));
    });

    test('a 401 without use_dpop_nonce is not repeated', () async {
      final rig = _Rig();
      final token = await rig.auth.token();
      rig.script[_reportPath] = [
        () => _json(
          {'code': 'unauthenticated', 'message': 'no'},
          status: 401,
          headers: {'www-authenticate': 'DPoP error="invalid_token"'},
        ),
      ];
      await expectLater(
        rig.api.reportInstalled(token, 'dev-1', 3),
        throwsA(isA<ApiError>()),
      );
      expect(rig.at(_reportPath), hasLength(1));
    });

    test(
      'the nonce of an error response reaches the next proof, and a refresh '
      'asked for a nonce is repeated with its assertion recomputed [SEC-021]',
      () async {
        final rig = _Rig(platform: 'ios')
          ..attestation.assertionBytes = Uint8List.fromList([1]);
        rig.script[_refreshPath] = [() => needNonce('n7')];
        await rig.auth.token();
        final tries = rig.at(_refreshPath);
        expect(tries, hasLength(2));
        expect(tries[1].claims['nonce'], 'n7');
        expect(rig.attestation.assertedHashes, hasLength(2));
        expect(
          rig.attestation.assertedHashes.last,
          sha256.convert(utf8.encode(tries[1].proof!)).bytes,
        );
      },
    );
  });

  group('server demands', () {
    test('PLX-6007 on Android: a challenge, a Play Integrity token bound to '
        'the key and challenge, then a new token [SEC-025]', () async {
      final rig = _Rig()..attestation.token = 'integrity-2';
      rig.script[_refreshPath] = [
        () => _error(403, 'permission_denied', 'PLX-6007: re-attest'),
      ];
      final token = await rig.auth.token();
      expect(token.value, 'tok-1');
      expect(rig.paths, [
        _challengePath,
        _registerPath,
        _refreshPath,
        _challengePath,
        _reattestPath,
        _refreshPath,
      ]);
      final jkt = rig.keys.stored[_alias()]!.publicKey.thumbprint;
      expect(rig.attestation.integrityHashes, [
        _b64url(bindingHash(_challenge, jkt)),
      ]);
      final re = rig.at(_reattestPath).single;
      expect(re.body, {
        'deviceId': 'dev-1',
        'challenge': base64.encode(_challenge),
        'evidence': {
          'android': {
            'keyAttestationChain': <Object?>[],
            'playIntegrityToken': 'integrity-2',
          },
        },
      });
      expect(re.headers, isNot(contains('authorization')));
      expect(re.claims['htu'], endsWith('ReattestDevice'));
      expect(re.proof, isNotNull);
      expect(rig.keys.created, 1, reason: 'the same key is attested again');
      expect(rig.keys.deleted, isEmpty);
    });

    test('PLX-6007 on iOS re-attests with the registration evidence and no '
        'key attestation chain [SEC-025]', () async {
      final rig = _Rig(platform: 'ios')
        ..attestation.evidence = IosEvidence(
          keyId: Uint8List.fromList(List.filled(32, 2)),
          attestationObject: Uint8List.fromList([3]),
        );
      rig.script[_refreshPath] = [
        () => _error(403, 'permission_denied', 'PLX-6007: re-attest'),
      ];
      await rig.auth.token();
      expect(rig.attestation.integrityHashes, isEmpty);
      expect(rig.attestation.attested, hasLength(2));
      expect(rig.attestation.attested.last.chain, isEmpty);
      expect(
        rig.at(_reattestPath).single.body['evidence'],
        containsPair('ios', isA<Map<String, Object?>>()),
      );
    });

    test('PLX-6007 on an Android emulator falls back to development evidence '
        '[SEC-008]', () async {
      final rig = _Rig();
      rig.script[_refreshPath] = [
        () => _error(403, 'permission_denied', 'PLX-6007: re-attest'),
      ];
      await rig.auth.token();
      expect(rig.attestation.integrityHashes, hasLength(1));
      expect(rig.at(_reattestPath).single.body['evidence'], {
        'development': {'buildId': 'build-1'},
      });
    });

    test('re-attestation is asked for once per exchange', () async {
      final rig = _Rig();
      rig.script[_refreshPath] = [
        () => _error(403, 'permission_denied', 'PLX-6007: re-attest'),
        () => _error(403, 'permission_denied', 'PLX-6007: re-attest'),
      ];
      await expectLater(
        rig.auth.token(),
        throwsA(isA<ApiError>().having((e) => e.plxCode, 'plx', 6007)),
      );
      expect(rig.at(_reattestPath), hasLength(1));
    });

    for (final (name, response) in [
      (
        'PLX-6006',
        () => _error(403, 'permission_denied', 'PLX-6006: device revoked'),
      ),
      ('not_found', () => _error(404, 'not_found', 'no such device')),
      ('unauthenticated', () => _error(401, 'unauthenticated', 'unknown')),
    ]) {
      test('$name deletes the key and the registration and registers again, '
          'once per sync [SEC-025]', () async {
        final rig = _Rig();
        await rig.auth.token();
        rig.auth.forgetToken();
        rig.script[_refreshPath] = [response];
        final token = await rig.auth.token();
        expect(token.value, 'tok-2');
        expect(rig.at(_registerPath), hasLength(2));
        expect(rig.keys.deleted, [_alias()]);
        expect(rig.keys.created, 2);
        expect((await rig.credentials.read())!.deviceId, 'dev-2');

        // Refused again within the same sync: no second registration.
        rig.auth.forgetToken();
        rig.script[_refreshPath] = [response];
        await expectLater(rig.auth.token(), throwsA(isA<ApiError>()));
        expect(rig.at(_registerPath), hasLength(2));

        // A new sync may register again.
        rig.auth.beginSync();
        rig.script[_refreshPath] = [response];
        await rig.auth.token();
        expect(rig.at(_registerPath), hasLength(3));
      });
    }

    test('other failures are not recovered from', () async {
      final rig = _Rig();
      rig.script[_refreshPath] = [
        () => _error(503, 'unavailable', 'PLX-3050: busy'),
      ];
      await expectLater(
        rig.auth.token(),
        throwsA(isA<ApiError>().having((e) => e.retryable, 'retryable', true)),
      );
      expect(rig.at(_registerPath), hasLength(1));
      expect(rig.keys.deleted, isEmpty);
    });

    test('the Plux code is read from the start of the message', () {
      expect(const ApiError(403, 'x', 'PLX-6007: again').plxCode, 6007);
      expect(const ApiError(403, 'x', 'see PLX-6007').plxCode, isNull);
      expect(const ApiError(403, 'x', 'PLX-60').plxCode, isNull);
      expect(const ApiError(403, 'x', '').plxCode, isNull);
    });
  });

  group('assurance', () {
    test('is AL0 until a token arrives, then the level the server named '
        '[SEC-007]', () async {
      final rig = _Rig()..assuranceLevel = 'AL2';
      expect(rig.auth.assurance, 0);
      final token = await rig.auth.token();
      expect(token.assurance, 2);
      expect(rig.auth.assurance, 2);
    });

    for (final level in <Object?>[null, '', 'AL4', 'high', 2]) {
      test(
        'a response naming $level means AL0, never more [SEC-007]',
        () async {
          final rig = _Rig()..assuranceLevel = level;
          expect((await rig.auth.token()).assurance, 0);
          expect(rig.auth.assurance, 0);
        },
      );
    }

    test('PLX-6002 lowers the level to AL0 at once [SEC-007]', () async {
      final rig = _Rig()..assuranceLevel = 'AL3';
      await rig.auth.token();
      expect(rig.auth.assurance, 3);
      rig.auth.forgetToken();
      rig.script[_refreshPath] = [
        () => _error(403, 'permission_denied', 'PLX-6002: not enough'),
      ];
      await expectLater(rig.auth.token(), throwsA(isA<ApiError>()));
      expect(rig.auth.assurance, 0);
    });

    test(
      'a revoked device the server no longer registers is AL0 [SEC-007]',
      () async {
        final rig = _Rig()..assuranceLevel = 'AL1';
        await rig.auth.token();
        rig.auth.forgetToken();
        rig.script[_refreshPath] = [
          () => _error(403, 'permission_denied', 'PLX-6006: device revoked'),
          () => _error(403, 'permission_denied', 'PLX-6006: device revoked'),
        ];
        await expectLater(rig.auth.token(), throwsA(isA<ApiError>()));
        expect(rig.auth.assurance, 0);
      },
    );

    test(
      'lowerAssurance drops the level until the next token [SEC-007]',
      () async {
        final rig = _Rig()..assuranceLevel = 'AL2';
        await rig.auth.token();
        rig.auth.lowerAssurance();
        expect(rig.auth.assurance, 0);
        rig.auth.forgetToken();
        await rig.auth.token();
        expect(rig.auth.assurance, 2);
      },
    );
  });

  group('secrets', () {
    test('no token, proof or evidence reaches an error or a description '
        '[SEC-092]', () async {
      final rig = _Rig()
        ..attestation.evidence = const AndroidEvidence(
          keyAttestationChain: [],
          playIntegrityToken: 'integrity-secret',
        );
      final token = await rig.auth.token();
      rig.script[_reportPath] = [
        () => _error(500, 'internal', 'PLX-9999: broke'),
      ];
      Object? caught;
      try {
        await rig.api.reportInstalled(token, 'dev-1', 3);
      } on ApiError catch (e) {
        caught = e;
      }
      final proof = rig.at(_reportPath).single.proof!;
      final shown = [
        caught.toString(),
        (caught! as ApiError).toException('report').toString(),
        token.toString(),
        (await rig.credentials.read()).toString(),
        rig.attestation.evidence.toString(),
      ].join('\n');
      for (final secret in ['tok-1', proof, 'integrity-secret']) {
        expect(shown, isNot(contains(secret)));
      }
      expect(token.toString(), 'DeviceToken([redacted])');
    });

    test('a transport failure does not echo the request headers', () async {
      final rig = _Rig();
      final token = await rig.auth.token();
      final api = PluxApiClient(
        MockClient((_) async => throw const FormatException('down')),
        Uri.parse('https://plux.example/'),
      );
      try {
        await api.reportInstalled(token, 'dev-1', 3);
        fail('no error');
      } on ApiError catch (e) {
        expect(e.status, 0);
        expect(e.toString(), isNot(contains('tok-1')));
      }
    });
  });
}
