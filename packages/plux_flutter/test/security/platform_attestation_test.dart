// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:convert';

import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/security/attestation.dart';
import 'package:plux_flutter/src/security/platform_attestation.dart';

const _channel = MethodChannel('dev.plux/runtime');

final _challenge = Uint8List.fromList([for (var i = 0; i < 32; i++) i]);
const _jkt = '0r62zgBj277RicA3LnaBKQ5_9RCDIomrlbpWjO0QTG0';
final _chain = [
  Uint8List.fromList([1, 2, 3]),
  Uint8List.fromList([4, 5, 6]),
];
final _keyId = Uint8List.fromList([for (var i = 0; i < 32; i++) 200 - i]);
final _object = Uint8List.fromList([9, 8, 7, 6]);

String _b64url(List<int> bytes) => base64Url.encode(bytes).replaceAll('=', '');

PlatformAttestation _attestation(
  TargetPlatform platform, {
  bool release = false,
  int? cloudProjectNumber = 123456789,
}) => PlatformAttestation(
  appId: 'dev.example.app',
  environment: 'production',
  cloudProjectNumber: cloudProjectNumber,
  buildId: 'build-42',
  release: release,
  platform: platform,
);

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  final messenger =
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;

  late List<MethodCall> calls;
  late Map<String, String> secrets;

  /// Installs a platform whose method answers come from [replies]; a reply
  /// that is a [PlatformException] is thrown.
  void platform(Map<String, Object?> replies) {
    calls = [];
    secrets = {};
    messenger.setMockMethodCallHandler(_channel, (call) async {
      calls.add(call);
      final args = call.arguments as Map<Object?, Object?>;
      switch (call.method) {
        case 'secretWrite':
          secrets[args['name']! as String] = args['value']! as String;
          return null;
        case 'secretRead':
          return secrets[args['name']];
      }
      final reply = replies[call.method];
      if (reply is PlatformException) throw reply;
      return reply;
    });
  }

  tearDown(() => messenger.setMockMethodCallHandler(_channel, null));

  group('Android', () {
    test(
      'SEC-003 evidence holds the chain and a token bound to the hash',
      () async {
        platform({'attestationSupported': true, 'integrityToken': 'the-token'});
        final evidence = await _attestation(
          TargetPlatform.android,
        ).attest(challenge: _challenge, jkt: _jkt, keyAttestationChain: _chain);
        evidence as AndroidEvidence;
        expect(evidence.keyAttestationChain, _chain);
        expect(evidence.playIntegrityToken, 'the-token');
        final request = calls.singleWhere((c) => c.method == 'integrityToken');
        final hash = bindingHash(_challenge, _jkt);
        expect(request.arguments, {
          'requestHash': _b64url(hash),
          'cloudProjectNumber': 123456789,
        });
        expect(
          (request.arguments as Map)['requestHash'] as String,
          isNot(anyOf(contains('='), contains('+'), contains('/'))),
        );
      },
    );

    test('SEC-003 an empty key attestation chain is rejected', () async {
      platform({'attestationSupported': true, 'integrityToken': 'the-token'});
      await expectLater(
        _attestation(
          TargetPlatform.android,
          release: true,
        ).attest(challenge: _challenge, jkt: _jkt, keyAttestationChain: []),
        throwsA(
          isA<PluxException>().having(
            (e) => e.code,
            'code',
            PluxErrorCode.attestationUnavailable,
          ),
        ),
      );
      expect(calls.where((c) => c.method == 'integrityToken'), isEmpty);
    });

    test('SEC-025 integrityToken forwards the request hash', () async {
      platform({'integrityToken': 'fresh'});
      final token = await _attestation(TargetPlatform.android)
          .integrityToken('hash-value');
      expect(token, 'fresh');
      expect(calls.single.arguments, {
        'requestHash': 'hash-value',
        'cloudProjectNumber': 123456789,
      });
    });

    test('a missing cloud project number is refused', () async {
      platform({'integrityToken': 'fresh'});
      await expectLater(
        _attestation(
          TargetPlatform.android,
          cloudProjectNumber: null,
        ).integrityToken('hash-value'),
        throwsA(isA<PluxException>()),
      );
      expect(calls, isEmpty);
    });

    test('assertion is null', () async {
      platform({});
      expect(
        await _attestation(TargetPlatform.android)
            .assertion(Uint8List.fromList([1])),
        isNull,
      );
      expect(calls, isEmpty);
    });
  });

  group('iOS', () {
    test('SEC-004 evidence holds the key id and attestation object', () async {
      platform({
        'attestationSupported': true,
        'appAttestKey': _keyId,
        'appAttestAttest': _object,
      });
      final evidence = await _attestation(TargetPlatform.iOS)
          .attest(challenge: _challenge, jkt: _jkt, keyAttestationChain: []);
      evidence as IosEvidence;
      expect(evidence.keyId, _keyId);
      expect(evidence.attestationObject, _object);
      final attest = calls.singleWhere((c) => c.method == 'appAttestAttest');
      expect(attest.arguments, {
        'keyId': _keyId,
        'clientDataHash': bindingHash(_challenge, _jkt),
      });
    });

    test('SEC-025 the key id is persisted and assertion uses it', () async {
      platform({
        'attestationSupported': true,
        'appAttestKey': _keyId,
        'appAttestAttest': _object,
        'appAttestAssert': Uint8List.fromList([5, 5]),
      });
      final sut = _attestation(TargetPlatform.iOS);
      await sut.attest(
        challenge: _challenge,
        jkt: _jkt,
        keyAttestationChain: [],
      );
      final name = secrets.keys.single;
      expect(name, matches(RegExp(r'^plux\.appattest\.[0-9a-f]{16}$')));
      expect(name, isNot(contains('example')));
      expect(secrets[name], _b64url(_keyId));

      final hash = Uint8List.fromList([7, 7, 7]);
      final reply = await sut.assertion(hash);
      expect(reply, Uint8List.fromList([5, 5]));
      final assertion = calls.singleWhere((c) => c.method == 'appAttestAssert');
      expect(assertion.arguments, {'keyId': _keyId, 'clientDataHash': hash});
    });

    test('the secret name is stable per app and environment', () async {
      platform({
        'attestationSupported': true,
        'appAttestKey': _keyId,
        'appAttestAttest': _object,
      });
      await _attestation(TargetPlatform.iOS)
          .attest(challenge: _challenge, jkt: _jkt, keyAttestationChain: []);
      final first = secrets.keys.single;
      await PlatformAttestation(
        appId: 'dev.example.app',
        environment: 'staging',
        buildId: 'b',
        release: false,
        platform: TargetPlatform.iOS,
      ).attest(challenge: _challenge, jkt: _jkt, keyAttestationChain: []);
      expect(secrets.keys.toSet(), hasLength(2));
      expect(secrets.keys, contains(first));
    });

    test('assertion is null without a stored key id', () async {
      platform({
        'appAttestAssert': Uint8List.fromList([5, 5]),
      });
      expect(
        await _attestation(TargetPlatform.iOS).assertion(Uint8List(1)),
        isNull,
      );
      expect(calls.where((c) => c.method == 'appAttestAssert'), isEmpty);
    });

    test('integrityToken is null', () async {
      platform({});
      expect(
        await _attestation(TargetPlatform.iOS).integrityToken('h'),
        isNull,
      );
      expect(calls, isEmpty);
    });
  });

  group('development fallback (SEC-008)', () {
    test('a debug build falls back when attestation is unsupported', () async {
      platform({'attestationSupported': false});
      for (final p in [TargetPlatform.android, TargetPlatform.iOS]) {
        final evidence = await _attestation(p)
            .attest(challenge: _challenge, jkt: _jkt, keyAttestationChain: []);
        expect((evidence as DevelopmentEvidence).buildId, 'build-42');
      }
      expect(calls.map((c) => c.method).toSet(), {'attestationSupported'});
    });

    test('a debug build falls back on the UNSUPPORTED error', () async {
      platform({
        'attestationSupported': true,
        'integrityToken': PlatformException(code: 'UNSUPPORTED'),
      });
      final evidence = await _attestation(
        TargetPlatform.android,
      ).attest(challenge: _challenge, jkt: _jkt, keyAttestationChain: _chain);
      expect(evidence, isA<DevelopmentEvidence>());
    });

    test(
      'a debug build on another platform gets development evidence',
      () async {
        platform({});
        final evidence = await _attestation(TargetPlatform.linux)
            .attest(challenge: _challenge, jkt: _jkt, keyAttestationChain: []);
        expect(evidence, isA<DevelopmentEvidence>());
      },
    );

    test('a debug build on Android with no Play Integrity project gets '
        'development evidence [SEC-008]', () async {
      platform({'attestationSupported': true});
      final evidence = await _attestation(
        TargetPlatform.android,
        cloudProjectNumber: null,
      ).attest(challenge: _challenge, jkt: _jkt, keyAttestationChain: _chain);
      expect(evidence, isA<DevelopmentEvidence>());
      expect(calls.map((c) => c.method), isNot(contains('integrityToken')));
    });

    test('a debug build does not fall back on other failures', () async {
      platform({
        'attestationSupported': true,
        'integrityToken': PlatformException(code: 'PLUX_PLATFORM'),
      });
      await expectLater(
        _attestation(
          TargetPlatform.android,
        ).attest(challenge: _challenge, jkt: _jkt, keyAttestationChain: _chain),
        throwsA(isA<PluxException>()),
      );
    });

    test('a release build never falls back on UNSUPPORTED', () async {
      platform({
        'attestationSupported': false,
        'integrityToken': PlatformException(code: 'UNSUPPORTED'),
        'appAttestKey': PlatformException(code: 'UNSUPPORTED'),
      });
      for (final p in [TargetPlatform.android, TargetPlatform.iOS]) {
        await expectLater(
          _attestation(p, release: true).attest(
            challenge: _challenge,
            jkt: _jkt,
            keyAttestationChain: p == TargetPlatform.android ? _chain : [],
          ),
          throwsA(
            isA<PluxException>()
                .having(
                  (e) => e.code,
                  'code',
                  PluxErrorCode.attestationUnavailable,
                )
                .having(
                  (e) => e.details['platformCode'],
                  'platformCode',
                  'UNSUPPORTED',
                ),
          ),
        );
      }
      expect(calls.where((c) => c.method == 'attestationSupported'), isEmpty);
    });

    test('a release build on another platform throws', () async {
      platform({});
      await expectLater(
        _attestation(
          TargetPlatform.linux,
          release: true,
        ).attest(challenge: _challenge, jkt: _jkt, keyAttestationChain: []),
        throwsA(isA<PluxException>()),
      );
    });

    test('assertion is null after an UNSUPPORTED answer in debug', () async {
      platform({
        'attestationSupported': true,
        'appAttestKey': _keyId,
        'appAttestAttest': PlatformException(code: 'UNSUPPORTED'),
        'appAttestAssert': PlatformException(code: 'UNSUPPORTED'),
      });
      final sut = _attestation(TargetPlatform.iOS);
      final evidence = await sut.attest(
        challenge: _challenge,
        jkt: _jkt,
        keyAttestationChain: [],
      );
      expect(evidence, isA<DevelopmentEvidence>());
      expect(await sut.assertion(Uint8List(1)), isNull);
    });
  });

  group('secrecy (SEC-092)', () {
    test(
      'evidence and errors never print tokens, key ids or objects',
      () async {
        platform({
          'attestationSupported': true,
          'integrityToken': 'secret-token-value',
        });
        final evidence = await _attestation(
          TargetPlatform.android,
        ).attest(challenge: _challenge, jkt: _jkt, keyAttestationChain: _chain);
        expect(evidence.toString(), isNot(contains('secret-token-value')));
        expect(
          IosEvidence(keyId: _keyId, attestationObject: _object).toString(),
          isNot(contains('200')),
        );

        platform({
          'attestationSupported': true,
          'integrityToken': PlatformException(
            code: 'secret-token-value',
            message: 'secret-token-value',
            details: 'secret-token-value',
          ),
        });
        try {
          await _attestation(TargetPlatform.android).attest(
            challenge: _challenge,
            jkt: _jkt,
            keyAttestationChain: _chain,
          );
          fail('expected a PluxException');
        } on PluxException catch (e) {
          expect(e.toString(), isNot(contains('secret-token-value')));
          expect(
            e.details.values.join(),
            isNot(contains('secret-token-value')),
          );
          expect(e.details.containsKey('platformCode'), isFalse);
          expect(e.code, PluxErrorCode.attestationUnavailable);
        }
      },
    );
  });
}
