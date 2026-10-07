// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/security/device_keys.dart';

// x = bytes 0..31, y = bytes 32..63.
final _x = Uint8List.fromList([for (var i = 0; i < 32; i++) i]);
final _y = Uint8List.fromList([for (var i = 32; i < 64; i++) i]);

const _xB64 = 'AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8';
const _yB64 = 'ICEiIyQlJicoKSorLC0uLzAxMjM0NTY3ODk6Ozw9Pj8';

// SEC-001, SEC-021: RFC 7638 thumbprint of the key above. Computed
// independently with Python:
//   c = '{"crv":"P-256","kty":"EC","x":"<x>","y":"<y>"}'
//   base64.urlsafe_b64encode(hashlib.sha256(c.encode()).digest()).rstrip(b'=')
const _thumbprint = '0r62zgBj277RicA3LnaBKQ5_9RCDIomrlbpWjO0QTG0';

const _channel = MethodChannel('dev.plux/runtime');

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  final messenger =
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;

  tearDown(() => messenger.setMockMethodCallHandler(_channel, null));

  group('EcPublicKey', () {
    test('SEC-021 jwk holds the public members in base64url', () {
      final jwk = EcPublicKey(_x, _y).jwk;
      expect(jwk, {'kty': 'EC', 'crv': 'P-256', 'x': _xB64, 'y': _yB64});
    });

    test('SEC-021 thumbprint matches an independently computed vector', () {
      expect(EcPublicKey(_x, _y).thumbprint, _thumbprint);
    });

    test('rejects coordinates that are not 32 bytes', () {
      expect(() => EcPublicKey(Uint8List(31), _y), throwsArgumentError);
      expect(() => EcPublicKey(_x, Uint8List(33)), throwsArgumentError);
      expect(
        () => EcPublicKey(Uint8List(0), Uint8List(0)),
        throwsArgumentError,
      );
    });

    test('copies the coordinates', () {
      final x = Uint8List.fromList(_x);
      final key = EcPublicKey(x, _y);
      x[0] = 99;
      expect(key.x[0], 0);
      expect(key.thumbprint, _thumbprint);
    });
  });

  group('KeyStorage', () {
    test('SEC-001 only software is not hardware', () {
      expect(KeyStorage.strongbox.hardware, isTrue);
      expect(KeyStorage.tee.hardware, isTrue);
      expect(KeyStorage.secureEnclave.hardware, isTrue);
      expect(KeyStorage.software.hardware, isFalse);
    });
  });

  group('key aliases', () {
    // sha256("a\u0000b") hex, first 16 characters, computed with Python:
    //   hashlib.sha256(b'a\x00b').hexdigest()[:16]
    test('are a prefix and 16 hex characters of the hash', () {
      expect(dpopKeyAlias('a', 'b'), 'dev.plux.dpop.59b271ae1bbcb1d3');
      expect(
        agreementKeyAlias('a', 'b'),
        'dev.plux.agreement.59b271ae1bbcb1d3',
      );
    });

    test('are deterministic and separate apps and environments', () {
      expect(
        dpopKeyAlias('com.example.app', 'production'),
        'dev.plux.dpop.e765e7a8ffa45d1a',
      );
      expect(
        dpopKeyAlias('com.example.app', 'production'),
        dpopKeyAlias('com.example.app', 'production'),
      );
      expect(
        dpopKeyAlias('com.example.app', 'staging'),
        isNot(dpopKeyAlias('com.example.app', 'production')),
      );
      // The NUL separator keeps ("ab","c") apart from ("a","bc").
      expect(dpopKeyAlias('ab', 'c'), isNot(dpopKeyAlias('a', 'bc')));
    });

    test('match the platform alias pattern', () {
      final alias = RegExp(r'^[a-z0-9._-]{1,64}$');
      expect(alias.hasMatch(dpopKeyAlias('Ünï/cödé app', 'Prod')), isTrue);
      expect(alias.hasMatch(agreementKeyAlias('x', 'y')), isTrue);
    });
  });

  group('PlatformDeviceKeys', () {
    const keys = PlatformDeviceKeys();
    late List<MethodCall> calls;

    void reply(Object? Function(MethodCall) handler) {
      calls = [];
      messenger.setMockMethodCallHandler(_channel, (call) async {
        calls.add(call);
        return handler(call);
      });
    }

    Map<String, Object?> keyReply({
      String storage = 'tee',
      List<Uint8List> chain = const [],
    }) => {'x': _x, 'y': _y, 'storage': storage, 'chain': chain};

    test('SEC-001 create sends the arguments and reads the key', () async {
      final challenge = Uint8List.fromList([1, 2, 3]);
      final cert = Uint8List.fromList([0x30, 0x82]);
      reply((_) => keyReply(storage: 'strongbox', chain: [cert]));

      final key = await keys.create(
        'dev.plux.dpop.abc',
        KeyPurpose.dpop,
        challenge: challenge,
      );

      expect(calls.single.method, 'keyCreate');
      expect(calls.single.arguments, {
        'alias': 'dev.plux.dpop.abc',
        'purpose': 'dpop',
        'challenge': challenge,
        'strongBox': true,
      });
      expect(key.alias, 'dev.plux.dpop.abc');
      expect(key.purpose, KeyPurpose.dpop);
      expect(key.storage, KeyStorage.strongbox);
      expect(key.publicKey.thumbprint, _thumbprint);
      expect(key.attestationChain, [cert]);
    });

    test('create passes a null challenge and strongBox false', () async {
      reply((_) => keyReply(storage: 'software'));
      final key = await keys.create(
        'a',
        KeyPurpose.agreement,
        strongBox: false,
      );
      expect(calls.single.arguments, {
        'alias': 'a',
        'purpose': 'agreement',
        'challenge': null,
        'strongBox': false,
      });
      expect(key.storage, KeyStorage.software);
      expect(key.storage.hardware, isFalse);
      expect(key.purpose, KeyPurpose.agreement);
      expect(key.attestationChain, isEmpty);
    });

    test('create accepts every storage name', () async {
      for (final s in KeyStorage.values) {
        reply((_) => keyReply(storage: s.wireName));
        expect((await keys.create('a', KeyPurpose.dpop)).storage, s);
      }
    });

    test('load returns the key', () async {
      reply((_) => keyReply(storage: 'secureEnclave'));
      final key = await keys.load('a', KeyPurpose.dpop);
      expect(calls.single.method, 'keyPublic');
      expect(calls.single.arguments, {'alias': 'a'});
      expect(key!.storage, KeyStorage.secureEnclave);
      expect(key.publicKey.jwk['x'], _xB64);
    });

    test('load returns null when there is no key', () async {
      reply((_) => null);
      expect(await keys.load('a', KeyPurpose.dpop), isNull);
    });

    test('SEC-021 sign returns the 64-byte signature', () async {
      final sig = Uint8List.fromList([for (var i = 0; i < 64; i++) 255 - i]);
      reply((_) => sig);
      final data = Uint8List.fromList([9, 8, 7]);
      expect(await keys.sign('a', data), sig);
      expect(calls.single.method, 'keySign');
      expect(calls.single.arguments, {'alias': 'a', 'data': data});
    });

    test('delete sends the alias', () async {
      reply((_) => null);
      await keys.delete('a');
      expect(calls.single.method, 'keyDelete');
      expect(calls.single.arguments, {'alias': 'a'});
    });

    group('platform errors', () {
      Future<PluxException> failWith(String code) async {
        reply((_) => throw PlatformException(code: code, message: 'Boom'));
        try {
          await keys.sign('a', Uint8List(1));
        } on PluxException catch (e) {
          return e;
        }
        fail('expected a PluxException');
      }

      test('PLUX_KEY_MISSING is attestationFailed', () async {
        final e = await failWith('PLUX_KEY_MISSING');
        expect(e.code, PluxErrorCode.attestationFailed);
        expect(e.details['platformCode'], 'PLUX_KEY_MISSING');
        expect(e.details['method'], 'keySign');
      });

      test('PLUX_KEY_UNSUPPORTED is keyNotHardwareBacked', () async {
        final e = await failWith('PLUX_KEY_UNSUPPORTED');
        expect(e.code, PluxErrorCode.keyNotHardwareBacked);
        expect(e.details['platformCode'], 'PLUX_KEY_UNSUPPORTED');
      });

      test('PLUX_PLATFORM is attestationFailed', () async {
        final e = await failWith('PLUX_PLATFORM');
        expect(e.code, PluxErrorCode.attestationFailed);
        expect(e.details['platformCode'], 'PLUX_PLATFORM');
      });

      test('an unknown code fails closed and is not echoed', () async {
        final e = await failWith('SOMETHING_ELSE');
        expect(e.code, PluxErrorCode.attestationFailed);
        expect(e.details.containsKey('platformCode'), isFalse);
        expect(e.message, isNot(contains('Boom')));
      });

      test('every method maps errors', () async {
        reply((_) => throw PlatformException(code: 'PLUX_KEY_MISSING'));
        for (final call in <Future<Object?> Function()>[
          () => keys.create('a', KeyPurpose.dpop),
          () => keys.load('a', KeyPurpose.dpop),
          () => keys.sign('a', Uint8List(1)),
          () => keys.delete('a'),
        ]) {
          await expectLater(call(), throwsA(isA<PluxException>()));
        }
      });

      test('a missing plugin is keyNotHardwareBacked', () async {
        // No handler installed: the binding answers with MissingPluginException.
        messenger.setMockMethodCallHandler(_channel, null);
        await expectLater(
          keys.sign('a', Uint8List(1)),
          throwsA(
            isA<PluxException>().having(
              (e) => e.code,
              'code',
              PluxErrorCode.keyNotHardwareBacked,
            ),
          ),
        );
      });
    });

    group('malformed replies fail closed', () {
      Future<void> expectMalformed(Future<Object?> Function() call) =>
          expectLater(
            call(),
            throwsA(
              isA<PluxException>().having(
                (e) => e.code,
                'code',
                PluxErrorCode.attestationFailed,
              ),
            ),
          );

      final badKeyReplies = <String, Object?>{
        'not a map': 'key',
        'missing x': {'y': _y, 'storage': 'tee', 'chain': <Object?>[]},
        'missing y': {'x': _x, 'storage': 'tee', 'chain': <Object?>[]},
        'short x': {
          'x': Uint8List(31),
          'y': _y,
          'storage': 'tee',
          'chain': <Object?>[],
        },
        'long y': {
          'x': _x,
          'y': Uint8List(33),
          'storage': 'tee',
          'chain': <Object?>[],
        },
        'x as list': {
          'x': List<int>.filled(32, 1),
          'y': _y,
          'storage': 'tee',
          'chain': <Object?>[],
        },
        'unknown storage': {
          'x': _x,
          'y': _y,
          'storage': 'cloud',
          'chain': <Object?>[],
        },
        'missing storage': {'x': _x, 'y': _y, 'chain': <Object?>[]},
        'missing chain': {'x': _x, 'y': _y, 'storage': 'tee'},
        'chain not a list': {'x': _x, 'y': _y, 'storage': 'tee', 'chain': 1},
        'chain with a string': {
          'x': _x,
          'y': _y,
          'storage': 'tee',
          'chain': ['cert'],
        },
        'chain with an empty certificate': {
          'x': _x,
          'y': _y,
          'storage': 'tee',
          'chain': [Uint8List(0)],
        },
      };

      for (final entry in badKeyReplies.entries) {
        test('create: ${entry.key}', () async {
          reply((_) => entry.value);
          await expectMalformed(() => keys.create('a', KeyPurpose.dpop));
        });
        test('load: ${entry.key}', () async {
          reply((_) => entry.value);
          await expectMalformed(() => keys.load('a', KeyPurpose.dpop));
        });
      }

      test('create: a null reply', () async {
        reply((_) => null);
        await expectMalformed(() => keys.create('a', KeyPurpose.dpop));
      });

      test('sign: null, wrong type and wrong length', () async {
        for (final bad in <Object?>[
          null,
          'sig',
          Uint8List(63),
          Uint8List(65),
          Uint8List(0),
        ]) {
          reply((_) => bad);
          await expectMalformed(() => keys.sign('a', Uint8List(1)));
        }
      });

      test('delete: a non-null reply', () async {
        reply((_) => true);
        await expectMalformed(() => keys.delete('a'));
      });
    });
  });
}
