// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Verifies: SEC-182.

import 'dart:convert';
import 'dart:typed_data';

import 'package:crypto/crypto.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/security/security_config.dart';
import 'package:plux_flutter/src/store/kv_store.dart';
import 'package:plux_flutter/src/verify/jcs.dart';
import 'package:plux_flutter/src/verify/manifest.dart';

final class _Secrets implements SecretStore {
  final Map<String, String> values = {};
  bool failing = false;

  @override
  Future<String?> read(String name) async {
    if (failing) throw StateError('keystore');
    return values[name];
  }

  @override
  Future<void> write(String name, String value) async {
    if (failing) throw StateError('keystore');
    values[name] = value;
  }

  @override
  Future<void> delete(String name) async => values.remove(name);
}

Object? _j(String s) => jsonDecode(s);

/// A manifest whose signed part pins [version] and [hash]; only the parser
/// reads it here, the signature is the verifier's business.
SecurityConfigRef _ref(int version, String hash) {
  final doc = {
    'type': 'manifest',
    'specVersion': 1,
    'role': 'targets',
    'app': 'a',
    'environment': 'e',
    'channel': 'c',
    'releaseSequence': 1,
    'issuedAt': '2026-09-28T00:00:00Z',
    'expires': '2100-01-01T00:00:00Z',
    'appBundle': {
      'hash': 'sha256:${'0' * 64}',
      'size': 1,
      'requiredFeatures': <String>[],
      'minRuntime': '0.1.0',
    },
    'plugins': <Object?>[],
    'control': {'killSwitches': <String>[], 'message': ''},
    'config': {'version': version, 'sha256': hash},
  };
  return parseManifest(Uint8List.fromList(utf8.encode(jsonEncode(doc))))
      .config!;
}

// The device document of the examples, in the canonical form RFC 8785
// gives it: members sorted by name, no white space.
const _strictJcs =
    '{"overrides":{"allowDirectDataSources":false,'
    '"inactivityLockTimeout":120},"profile":"strict"}';

String _hexOf(String text) => sha256.convert(utf8.encode(text)).toString();

void main() {
  group('RFC 7396 merge patch', () {
    // Appendix A of RFC 7396: original, patch, result.
    const cases = <(String, String, String)>[
      ('{"a":"b"}', '{"a":"c"}', '{"a":"c"}'),
      ('{"a":"b"}', '{"b":"c"}', '{"a":"b","b":"c"}'),
      ('{"a":"b"}', '{"a":null}', '{}'),
      ('{"a":"b","b":"c"}', '{"a":null}', '{"b":"c"}'),
      ('{"a":["b"]}', '{"a":"c"}', '{"a":"c"}'),
      ('{"a":"c"}', '{"a":["b"]}', '{"a":["b"]}'),
      ('{"a":{"b":"c"}}', '{"a":{"b":"d","c":null}}', '{"a":{"b":"d"}}'),
      ('{"a":[{"b":"c"}]}', '{"a":[1]}', '{"a":[1]}'),
      ('["a","b"]', '["c","d"]', '["c","d"]'),
      ('{"a":"b"}', '["c"]', '["c"]'),
      ('{"a":"foo"}', 'null', 'null'),
      ('{"a":"foo"}', '"bar"', '"bar"'),
      ('{"e":null}', '{"a":1}', '{"e":null,"a":1}'),
      ('[1,2]', '{"a":"b","c":null}', '{"a":"b"}'),
      ('{}', '{"a":{"bb":{"ccc":null}}}', '{"a":{"bb":{}}}'),
    ];
    for (final (i, (original, patch, result)) in cases.indexed) {
      test('Appendix A case ${i + 1}: $original + $patch [SEC-182]', () {
        final target = _j(original);
        final p = _j(patch);
        expect(applyMergePatch(target, p), _j(result));
        // Neither argument is modified.
        expect(target, _j(original));
        expect(p, _j(patch));
      });
    }

    test('a null target becomes an object for an object patch [SEC-182]', () {
      expect(applyMergePatch(null, _j('{"a":1,"b":null}')), {'a': 1});
    });
  });

  group('hash', () {
    test('is SHA-256 of the canonical JSON, agreeing with a hand-written form [SEC-182]', () {
      final d =
          _j(
                '{"profile":"strict","overrides":{"inactivityLockTimeout":120,'
                '"allowDirectDataSources":false}}',
              )!
              as Map<String, Object?>;
      expect(canonicalJson(d), _strictJcs);
      expect(configHash(d), sha256.convert(utf8.encode(_strictJcs)).bytes);
      expect(
        hexEncode(configHash(d)),
        '29d4ae407efbfc6b5d8e02a8b0d6e77ced5cc8c5b3f22a98eccee806b6b98e21',
      );
    });

    test('version 0 is the empty document [SEC-182]', () {
      expect(
        hexEncode(configHash(const {})),
        '44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a',
      );
    });
  });

  group('effective settings', () {
    test('no document is the standard defaults [SEC-182]', () {
      final s = SecuritySettings.fromDocument(const {});
      expect(s, SecuritySettings.builtIn);
      expect(s.profile, SecurityProfile.standard);
      for (final setting in SecuritySetting.values) {
        expect(s[setting], setting.standard);
      }
    });

    test(
      'the profile sets the defaults and the overrides sit on top [SEC-182]',
      () {
        final s = SecuritySettings.fromDocument({
          'profile': 'maximum',
          'overrides': {
            'inactivityLockTimeout': 60,
            'minAssuranceForSync': 'AL1',
          },
        }, version: 7);
        expect(s.version, 7);
        expect(s.number(SecuritySetting.inactivityLockTimeout), 60);
        expect(s.choice(SecuritySetting.minAssuranceForSync), 'AL1');
        expect(s.flag(SecuritySetting.screenshotBlockingDefault), isTrue);
        expect(s.choice(SecuritySetting.raspRootHookingResponse), 'block');
      },
    );

    test(
      'an unknown key or a value of the wrong type is ignored [SEC-182]',
      () {
        final s = SecuritySettings.fromDocument({
          'profile': 'strict',
          'overrides': {
            'tokenLifetime': 5,
            'inactivityLock': 'yes',
            'inactivityLockTimeout': true,
          },
        });
        expect(s.flag(SecuritySetting.inactivityLock), isTrue);
        expect(s.number(SecuritySetting.inactivityLockTimeout), 300);
      },
    );

    test('settings with the same values are equal [SEC-182]', () {
      final a = SecuritySettings.fromDocument({
        'overrides': {'inactivityLock': true},
      });
      final b = SecuritySettings.fromDocument({
        'overrides': {'inactivityLock': true},
      });
      expect(a, b);
      expect(a.hashCode, b.hashCode);
      expect(a, isNot(SecuritySettings.builtIn));
    });
  });

  group('applyConfigUpdate', () {
    final target = {
      'profile': 'strict',
      'overrides': {'inactivityLockTimeout': 120},
    };
    final ref = _ref(3, _hexOf(canonicalJson(target)));
    Uint8List patch(Object? p) =>
        Uint8List.fromList(utf8.encode(jsonEncode(p)));

    test('a patch from version 0 builds the document [SEC-182]', () {
      final r = applyConfigUpdate(
        ref: ref,
        current: null,
        sentVersion: 0,
        patch: patch(target),
        fullRequired: false,
      );
      expect(r, isA<ConfigUpdated>());
      final c = (r as ConfigUpdated).config;
      expect(c.version, 3);
      expect(c.document, target);
    });

    test('an incremental patch changes the held document [SEC-182]', () {
      final held = StoredSecurityConfig(2, {
        'profile': 'strict',
        'overrides': {'inactivityLockTimeout': 200},
      }, Uint8List(32));
      final r = applyConfigUpdate(
        ref: ref,
        current: held,
        sentVersion: 2,
        patch: patch({
          'overrides': {'inactivityLockTimeout': 120},
        }),
        fullRequired: false,
      );
      expect((r as ConfigUpdated).config.document, target);
    });

    test('the pinned version already held changes nothing [SEC-182]', () {
      final r = applyConfigUpdate(
        ref: ref,
        current: StoredSecurityConfig(3, target, ref.sha256),
        sentVersion: 3,
        patch: null,
        fullRequired: false,
      );
      expect(r, isA<ConfigCurrent>());
    });

    test('version 0 with the empty document changes nothing [SEC-182]', () {
      final r = applyConfigUpdate(
        ref: _ref(0, _hexOf('{}')),
        current: null,
        sentVersion: 0,
        patch: null,
        fullRequired: false,
      );
      expect(r, isA<ConfigCurrent>());
    });

    test(
      'a full-required answer patches from the empty document [SEC-182]',
      () {
        final held = StoredSecurityConfig(2, {
          'overrides': {'inactivityLock': true},
        }, Uint8List(32));
        final r = applyConfigUpdate(
          ref: ref,
          current: held,
          sentVersion: 2,
          patch: patch(target),
          fullRequired: true,
        );
        expect((r as ConfigUpdated).config.document, target);
      },
    );

    test(
      'a result that does not hash to the signed hash is rejected [SEC-182]',
      () {
        final r = applyConfigUpdate(
          ref: ref,
          current: null,
          sentVersion: 0,
          patch: patch({'profile': 'maximum'}),
          fullRequired: false,
        );
        expect(r, isA<ConfigRejected>());
        expect(
          (r as ConfigRejected).error.code,
          PluxErrorCode.securityConfigHashMismatch,
        );
      },
    );

    test('a missing, malformed or non-object result is rejected [SEC-182]', () {
      for (final bytes in [
        null,
        Uint8List.fromList(utf8.encode('{"a"')),
        patch(['x']),
        patch('x'),
      ]) {
        final r = applyConfigUpdate(
          ref: ref,
          current: null,
          sentVersion: 0,
          patch: bytes,
          fullRequired: false,
        );
        expect(r, isA<ConfigRejected>(), reason: '$bytes');
      }
    });

    test('a patch over the limit is out of bounds [SEC-182]', () {
      final r = applyConfigUpdate(
        ref: ref,
        current: StoredSecurityConfig(2, const {}, configHash(const {})),
        sentVersion: 2,
        patch: Uint8List(20000),
        fullRequired: false,
      );
      expect(
        (r as ConfigRejected).error.code,
        PluxErrorCode.securityConfigOutOfBounds,
      );
    });
  });

  group('SecurityConfigStore', () {
    final doc = {
      'profile': 'strict',
      'overrides': {'inactivityLockTimeout': 120},
    };
    late _Secrets secrets;
    late SecurityConfigStore store;
    setUp(() {
      secrets = _Secrets();
      store = SecurityConfigStore(secrets, 'App.One', 'Production');
    });

    test('keeps one secret per app and environment [SEC-182]', () async {
      expect(store.name, 'security-config.app.one.production');
      await store.write(StoredSecurityConfig(4, doc, configHash(doc)));
      final back = await store.read();
      expect(back!.version, 4);
      expect(back.document, doc);
      expect(secrets.values.keys, ['security-config.app.one.production']);
    });

    test('nothing stored reads as none [SEC-182]', () async {
      expect(await store.read(), isNull);
    });

    test(
      'a document that no longer hashes to its hash is discarded [SEC-182]',
      () async {
        await store.write(StoredSecurityConfig(4, doc, configHash(doc)));
        final tampered = jsonDecode(secrets.values[store.name]!) as Map;
        (tampered['document'] as Map)['profile'] = 'standard';
        secrets.values[store.name] = jsonEncode(tampered);
        expect(await store.read(), isNull);
        expect(secrets.values, isEmpty);
      },
    );

    test('an unreadable value is discarded [SEC-182]', () async {
      secrets.values[store.name] = 'not json';
      expect(await store.read(), isNull);
      expect(secrets.values, isEmpty);
    });

    test(
      'failing secure storage reads as none and fails a write [SEC-182]',
      () async {
        secrets.failing = true;
        expect(await store.read(), isNull);
        await expectLater(
          store.write(StoredSecurityConfig(4, doc, configHash(doc))),
          throwsA(isA<PluxException>()),
        );
      },
    );
  });

  group('manifest pin', () {
    test('a manifest pins the version and the hash [SEC-182]', () {
      final r = _ref(5, _hexOf('{}'));
      expect(r.version, 5);
      expect(hexEncode(r.sha256), _hexOf('{}'));
    });
  });
}
