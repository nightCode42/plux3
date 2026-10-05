// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/store/kv_store.dart';

import '../support/harness.dart';

/// The built-in encrypted store (plan p5 D5, D6).
void main() {
  late Directory dir;
  late MemorySecretStore secrets;

  setUp(() {
    dir = Directory.systemTemp.createTempSync('plux_kv');
    secrets = MemorySecretStore();
  });
  tearDown(() => dir.deleteSync(recursive: true));

  EncryptedFileStore store({String label = 'plux-state/persisted/a'}) =>
      EncryptedFileStore(
        path: '${dir.path}/s.pxk',
        secrets: secrets,
        keyName: 'state-persisted.a',
        label: label,
        maxBytes: 1024,
      );

  test('round-trips entries encrypted, with a key made on first save and kept in secure storage [STA-003] [SCH-012]', () async {
    final s = store();
    expect(await s.load(), isEmpty);
    expect(secrets.values, isEmpty, reason: 'no key until something is saved');
    await s.save({'a': 1, 'secret': 's3cr3t-value'});
    expect(base64Decode(secrets.values['state-persisted.a']!), hasLength(32));
    final raw = File('${dir.path}/s.pxk').readAsBytesSync();
    expect(latin1.decode(raw).contains('s3cr3t-value'), isFalse);
    expect(await store().load(), {'a': 1, 'secret': 's3cr3t-value'});
  });

  test('a tampered file fails authentication, is removed and reported as corrupt [STA-003]', () async {
    await store().save({'a': 1});
    final f = File('${dir.path}/s.pxk');
    final bytes = f.readAsBytesSync();
    bytes[bytes.length - 20] ^= 1;
    f.writeAsBytesSync(bytes);
    await expectLater(
      store().load(),
      throwsA(
        isA<StoreException>().having(
          (e) => e.failure,
          'failure',
          StoreFailure.corrupt,
        ),
      ),
    );
    expect(f.existsSync(), isFalse);
  });

  test(
    'a file is bound to its purpose and its installation key [STA-003]',
    () async {
      await store().save({'a': 1});
      await expectLater(
        store(label: 'plux-state/secure/a').load(),
        throwsA(isA<StoreException>()),
      );
      await store().save({'a': 1});
      secrets.values.clear();
      await expectLater(store().load(), throwsA(isA<StoreException>()));
    },
  );

  test('entries over the limit are refused; wipe removes the file and the key [LIM-001] [HST-001]', () async {
    final s = store();
    await expectLater(
      s.save({'big': 'x' * 2000}),
      throwsA(
        isA<StoreException>().having(
          (e) => e.failure,
          'failure',
          StoreFailure.tooLarge,
        ),
      ),
    );
    await s.save({'a': 1});
    await s.wipe();
    expect(File('${dir.path}/s.pxk').existsSync(), isFalse);
    expect(secrets.values, isEmpty);
  });

  test(
    'a secure storage that fails makes the store unavailable [STA-003]',
    () async {
      final s = EncryptedFileStore(
        path: '${dir.path}/s.pxk',
        secrets: _FailingSecrets(),
        keyName: 'k',
        label: 'l',
        maxBytes: 1024,
      );
      await expectLater(
        s.save({'a': 1}),
        throwsA(
          isA<StoreException>().having(
            (e) => e.failure,
            'failure',
            StoreFailure.unavailable,
          ),
        ),
      );
    },
  );
}

final class _FailingSecrets implements SecretStore {
  @override
  Future<String?> read(String name) => Future.error(StateError('no keystore'));

  @override
  Future<void> write(String name, String value) =>
      Future.error(StateError('no keystore'));

  @override
  Future<void> delete(String name) => Future.error(StateError('no keystore'));
}
