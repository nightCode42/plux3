// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_flutter/src/db/builtin_adapter.dart';
import 'package:plux_flutter/src/store/kv_store.dart';

import 'adapter_conformance.dart';

/// Verifies: DB-001, DB-002, DB-003, DB-004, DB-005, DB-006, DB-008, DB-009.
void main() {
  runAdapterConformance(
    'MemoryDatabaseAdapter',
    create: () async => MemoryDatabaseAdapter(encrypted: true),
    collections: true,
    encrypted: true,
  );

  runAdapterConformance(
    'MemoryDatabaseAdapter (not encrypted)',
    create: () async => MemoryDatabaseAdapter(),
    collections: true,
    encrypted: false,
  );

  group('BuiltInDatabaseAdapter', () {
    late Directory dir;
    late _MemorySecrets secrets;

    setUp(() {
      dir = Directory.systemTemp.createTempSync('plux-db-test');
      secrets = _MemorySecrets();
    });
    tearDown(() => dir.deleteSync(recursive: true));

    BuiltInDatabaseAdapter make({bool encrypted = true}) =>
        BuiltInDatabaseAdapter(
          store: encrypted
              ? EncryptedFileStore(
                  path: '${dir.path}/kv.pxk',
                  secrets: secrets,
                  keyName: 'kv',
                  label: 'kv',
                  maxBytes: 1 << 20,
                )
              : PlainFileStore(path: '${dir.path}/kv.json', maxBytes: 1 << 20),
          encrypted: encrypted,
        );

    // An adapter made inside a widget test's fake-async zone, as a page
    // build makes the runtime's, closes from the real zone teardown runs
    // in: its queue must not wait for a microtask of the fake zone.
    testWidgets('closes from another zone than the one that made it', (
      tester,
    ) async {
      final db = make();
      addTearDown(() async {
        final closed = await tester.runAsync(
          () => db
              .close()
              .then((_) => true)
              .timeout(const Duration(seconds: 5), onTimeout: () => false),
        );
        expect(closed, isTrue);
      });
    });

    runAdapterConformance(
      'BuiltInDatabaseAdapter (encrypted file)',
      create: () async => make(),
      reopen: () async => make(),
      collections: false,
      encrypted: true,
    );

    runAdapterConformance(
      'BuiltInDatabaseAdapter (plain file)',
      create: () async => make(encrypted: false),
      reopen: () async => make(encrypted: false),
      collections: false,
      encrypted: false,
    );

    test(
      'seals the key-value file: no value appears in clear [SEC-092]',
      () async {
        final db = make();
        await db.open(requireEncryption: true);
        await db.kvSet('plugin.a', 'token', 'super-secret-value');
        await db.close();
        final bytes = File('${dir.path}/kv.pxk').readAsBytesSync();
        expect(
          String.fromCharCodes(bytes).contains('super-secret-value'),
          isFalse,
        );
      },
    );

    test('a corrupt file is discarded and reported, and the store starts '
        'empty', () async {
      final first = make();
      await first.open(requireEncryption: false);
      await first.kvSet('plugin.a', 'k', 1);
      await first.close();
      final file = File('${dir.path}/kv.pxk');
      final bytes = file.readAsBytesSync();
      bytes[bytes.length ~/ 2] ^= 0xff;
      file.writeAsBytesSync(bytes);
      final reported = <PluxException>[];
      final db = BuiltInDatabaseAdapter(
        store: EncryptedFileStore(
          path: file.path,
          secrets: secrets,
          keyName: 'kv',
          label: 'kv',
          maxBytes: 1 << 20,
        ),
        encrypted: true,
        report: reported.add,
      );
      await db.open(requireEncryption: true);
      expect(await db.kvGet('plugin.a', 'k'), isNull);
      expect(reported.single.code, PluxErrorCode.dbStoreFailed);
      await db.kvSet('plugin.a', 'k', 2);
      expect(await db.kvGet('plugin.a', 'k'), 2);
    });

    test(
      'a store over its size is a typed limit error and changes nothing',
      () async {
        final db = BuiltInDatabaseAdapter(
          store: PlainFileStore(path: '${dir.path}/small.json', maxBytes: 64),
          encrypted: false,
        );
        await db.open(requireEncryption: false);
        await db.kvSet('plugin.a', 'k', 'x');
        await expectCode(
          db.kvSet('plugin.a', 'big', 'y' * 200),
          PluxErrorCode.dbLimitExceeded,
        );
        expect(await db.kvGet('plugin.a', 'big'), isNull);
        expect(await db.kvGet('plugin.a', 'k'), 'x');
      },
    );

    test('wiping everything removes the file and the key [DB-008]', () async {
      final db = make();
      await db.open(requireEncryption: true);
      await db.kvSet('plugin.a', 'k', 1);
      expect(File('${dir.path}/kv.pxk').existsSync(), isTrue);
      expect(secrets.values, isNotEmpty);
      await db.wipe();
      expect(File('${dir.path}/kv.pxk').existsSync(), isFalse);
      expect(secrets.values, isEmpty);
    });
  });
}

final class _MemorySecrets implements SecretStore {
  final Map<String, String> values = {};

  @override
  Future<String?> read(String name) async => values[name];

  @override
  Future<void> write(String name, String value) async => values[name] = value;

  @override
  Future<void> delete(String name) async => values.remove(name);
}
