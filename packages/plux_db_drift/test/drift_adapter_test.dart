// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:io';
import 'dart:isolate';

import 'package:drift/native.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_db_drift/plux_db_drift.dart';
import 'package:plux_flutter/plux_flutter.dart';
import 'package:sqlite3/sqlite3.dart' as sqlite;

// The shared suite lives with the interface it checks.
import '../../plux_flutter/test/db/adapter_conformance.dart';

final class _Secrets implements SecretStore {
  final Map<String, String> values = {};

  @override
  Future<String?> read(String name) async => values[name];

  @override
  Future<void> write(String name, String value) async => values[name] = value;

  @override
  Future<void> delete(String name) async => values.remove(name);
}

bool _cipherInThisSqlite() {
  final db = sqlite.sqlite3.openInMemory();
  try {
    return db.select('pragma cipher_version').isNotEmpty ||
        db.select('pragma cipher').isNotEmpty;
  } finally {
    db.close();
  }
}

/// Verifies: DB-001, DB-002, DB-003, DB-004, DB-005, DB-006, DB-007,
/// DB-008, DB-009.
void main() {
  late Directory dir;
  late _Secrets secrets;
  final cipher = _cipherInThisSqlite();

  setUp(() {
    dir = Directory.systemTemp.createTempSync('plux-drift-test');
    secrets = _Secrets();
  });
  tearDown(() => dir.deleteSync(recursive: true));

  PluxDriftAdapter make({List<PluxException>? problems}) => PluxDriftAdapter(
    path: '${dir.path}/plux.db',
    secrets: secrets,
    onProblem: problems?.add,
  );

  // The suite creates adapters over one fresh directory per test: the same
  // path serves the restart tests.
  runAdapterConformance(
    'PluxDriftAdapter (file, background isolate)',
    create: () async => make(),
    reopen: () async => make(),
    collections: true,
    encrypted: cipher,
  );

  group('on the file system', () {
    test('without an encrypting SQLite, `strict` and `maximum` refuse the '
        'database and create neither the file nor its key [DB-002]', () async {
      if (cipher) return;
      final db = make();
      await expectCode(
        db.open(requireEncryption: true),
        PluxErrorCode.dbEncryptionRequired,
      );
      expect(File('${dir.path}/plux.db').existsSync(), isFalse);
      expect(secrets.values, isEmpty);
      // A profile that does not require it still works.
      await db.open(requireEncryption: false);
      expect(db.encrypted, isFalse);
      await db.close();
    });

    test('an open unencrypted database is refused when encryption becomes '
        'required [DB-002]', () async {
      if (cipher) return;
      final db = make();
      await db.open(requireEncryption: false);
      await expectCode(
        db.open(requireEncryption: true),
        PluxErrorCode.dbEncryptionRequired,
      );
      await db.close();
    });

    test('with an encrypting SQLite the database is encrypted under the '
        'installation key and unreadable without it [DB-002]', () async {
      if (!cipher) {
        markTestSkipped('this SQLite has no cipher (select sqlcipher)');
        return;
      }
      final db = make();
      await db.open(requireEncryption: true);
      expect(db.encrypted, isTrue);
      await db.kvSet('plugin.a', 'k', 'secret-value');
      await db.close();
      expect(secrets.values.keys, ['db-drift']);
      final bytes = File('${dir.path}/plux.db').readAsBytesSync();
      expect(String.fromCharCodes(bytes).contains('secret-value'), isFalse);
      expect(String.fromCharCodes(bytes).contains('SQLite format'), isFalse);
      // Another installation's key discards the database and reports it.
      secrets.values['db-drift'] = '${'A' * 43}=';
      final problems = <PluxException>[];
      final other = make(problems: problems);
      await other.open(requireEncryption: true);
      expect(await other.kvGet('plugin.a', 'k'), isNull);
      expect(problems.single.code, PluxErrorCode.dbStoreFailed);
      await other.close();
    });

    test('the plain database is a SQLite file the key is never written to '
        '[DB-002]', () async {
      if (cipher) return;
      final db = make();
      await db.open(requireEncryption: false);
      await db.kvSet('plugin.a', 'k', 'v');
      await db.close();
      expect(secrets.values, isEmpty);
    });

    test('wiping everything removes the files and the key [DB-008]', () async {
      final db = make();
      await db.open(requireEncryption: false);
      await db.kvSet('plugin.a', 'k', 'v');
      await db.wipe();
      expect(File('${dir.path}/plux.db').existsSync(), isFalse);
      expect(File('${dir.path}/plux.db-wal').existsSync(), isFalse);
      expect(secrets.values, isEmpty);
    });

    test('wiping a namespace of a closed database opens, clears and keeps '
        'the others [DB-008]', () async {
      final db = make();
      await db.open(requireEncryption: false);
      await db.kvSet('plugin.a', 'k', 1);
      await db.kvSet('plugin.b', 'k', 2);
      await db.close();
      final again = make();
      await again.wipe(namespace: 'plugin.a');
      await again.open(requireEncryption: false);
      expect(await again.kvGet('plugin.a', 'k'), isNull);
      expect(await again.kvGet('plugin.b', 'k'), 2);
      await again.close();
    });

    test(
      'SQL runs off the isolate that opened the database [DB-007]',
      () async {
        final db = make();
        await db.open(requireEncryption: false);
        // The executor is Drift's background isolate: a statement completes
        // while this isolate keeps running its event loop.
        var ticks = 0;
        final timer = Stream<void>.periodic(const Duration(milliseconds: 1))
            .listen((_) => ticks++);
        for (var i = 0; i < 50; i++) {
          await db.kvSet('plugin.a', 'k$i', i);
        }
        await timer.cancel();
        expect(ticks, greaterThan(0));
        expect(Isolate.current.debugName, isNot('Drift isolate worker'));
        await db.close();
      },
    );

    test('a corrupt, undecryptable file is discarded and reported when a '
        'key is in use', () async {
      if (!cipher) return;
      File('${dir.path}/plux.db').writeAsBytesSync(List.filled(4096, 7));
      final problems = <PluxException>[];
      final db = make(problems: problems);
      await db.open(requireEncryption: true);
      expect(problems, hasLength(1));
      await db.kvSet('plugin.a', 'k', 1);
      await db.close();
    });

    test('a file that is not a database is a typed failure without a '
        'cipher, and is kept', () async {
      if (cipher) return;
      File('${dir.path}/plux.db').writeAsBytesSync(List.filled(4096, 7));
      final db = make();
      await expectCode(
        db.open(requireEncryption: false),
        PluxErrorCode.dbStoreFailed,
      );
      expect(File('${dir.path}/plux.db').existsSync(), isTrue);
    });
  });

  group('with a custom executor', () {
    test('an in-memory executor passes the key pragma it is given', () async {
      String? seen;
      final db = PluxDriftAdapter.custom(
        opener: (keyPragma) {
          seen = keyPragma;
          return NativeDatabase.memory();
        },
        probeCipher: () async => true,
        secrets: secrets,
      );
      await db.open(requireEncryption: false);
      // The database answers no cipher pragma: the key was offered, but
      // the adapter does not claim encryption it cannot see.
      expect(seen, startsWith('pragma key = "x\''));
      expect(seen!.length, 'pragma key = "x\''.length + 64 + 2);
      expect(db.encrypted, cipher);
      await db.close();
    });

    test('a database that does not encrypt is refused where it is required '
        'even if the library claims to', () async {
      if (cipher) return;
      final db = PluxDriftAdapter.custom(
        opener: (_) => NativeDatabase.memory(),
        probeCipher: () async => true,
        secrets: secrets,
      );
      await expectCode(
        db.open(requireEncryption: true),
        PluxErrorCode.dbEncryptionRequired,
      );
    });
  });
}
