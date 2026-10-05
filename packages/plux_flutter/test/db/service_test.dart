// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_flutter/src/db/builtin_adapter.dart';
import 'package:plux_flutter/src/db/service.dart';
import 'package:plux_flutter/src/pxl/types.dart';
import 'package:plux_flutter/src/pxl/values.dart';
import 'package:plux_flutter/src/store/kv_store.dart';

import 'adapter_conformance.dart' show expectCode;

String _uuid(int n) =>
    '01a0c450-6c00-7014-8000-00000007${n.toString().padLeft(4, '0')}';

/// A release's declarations for the tests: collections by plugin.
final class _Declarations implements DbDeclarations {
  _Declarations(
    this.byPlugin, {
    this.sequence = 1,
    this.requireEncryption = false,
    this.limits = const {},
    this.dropped = const {},
  });

  final Map<String, List<DbCollectionSchema>> byPlugin;
  final Map<String, List<String>> dropped;

  @override
  final int sequence;

  @override
  final bool requireEncryption;

  @override
  final Map<String, int> limits;

  @override
  List<DbCollectionSchema> collectionsOf(String plugin) =>
      byPlugin[plugin] ?? const [];

  @override
  List<String> droppedOf(String plugin) => dropped[plugin] ?? const [];

  @override
  NamedType? Function(String) typesOf(String plugin) =>
      (_) => null;
}

DbCollectionSchema _notes(
  String plugin, {
  int version = 1,
  int n = 1,
  List<DbField>? fields,
  List<DbMigrationPlan> migrations = const [],
}) => DbCollectionSchema(
  name: '${dbNamespace(plugin)}/${_uuid(n)}',
  id: _uuid(n),
  key: 'notes',
  version: version,
  fields:
      fields ??
      [
        DbField('id', 'string'),
        DbField('text', 'string'),
        DbField('due', 'date?'),
        DbField('tags', 'list<string>'),
        DbField('rank', 'int'),
      ],
  primaryKey: const ['id'],
  indexes: const [
    ['rank'],
  ],
  migrations: migrations,
);

Map<String, Object?> _note(String id, {int rank = 0, String? text}) => {
  'id': id,
  'text': text ?? 'note $id',
  'due': null,
  'tags': <Object?>[],
  'rank': rank,
};

/// Verifies: DB-001, DB-004, DB-005, DB-006, DB-007, DB-008, DB-009,
/// LIM-004.
void main() {
  late MemoryDatabaseAdapter adapter;
  late _Declarations release;
  late List<PluxException> reports;
  late PluxDatabase db;

  PluxDatabase make({_Declarations? Function()? declarations}) => PluxDatabase(
    adapter: adapter,
    report: reports.add,
    declarations: declarations ?? () => release,
  );

  setUp(() {
    adapter = MemoryDatabaseAdapter(encrypted: true);
    reports = [];
    release = _Declarations({
      '': [_notes('', n: 9)],
      'alpha': [_notes('alpha', n: 1)],
      'beta': [_notes('beta', n: 2)],
    });
    db = make();
  });

  group('namespaces [DB-004]', () {
    test('a plugin reads and writes its private collection and the app\'s '
        'shared one', () async {
      final key = await db.insert('alpha', 'notes', _note('1'));
      expect(key, '1');
      final rows = await db.query('alpha', 'notes');
      expect(rows, hasLength(1));
    });

    test('plugin-private collections are namespaced: plugins cannot read '
        'each other\'s data', () async {
      await db.insert('alpha', 'notes', _note('1', text: 'alpha secret'));
      await db.insert('beta', 'notes', _note('1', text: 'beta text'));
      final beta = await db.query('beta', 'notes');
      expect((beta.single! as Map)['text'], 'beta text');
      final alpha = await db.query('alpha', 'notes');
      expect((alpha.single! as Map)['text'], 'alpha secret');
      // The physical collections differ by namespace and ID.
      expect(
        (await adapter.query(
          release.byPlugin['alpha']!.single.name,
          const DbQuery(),
        )).single['text'],
        'alpha secret',
      );
    });

    test('a collection only another plugin declares is unknown to the '
        'plugin', () async {
      release = _Declarations({
        'alpha': [_notes('alpha')],
        'beta': const [],
      });
      db = make();
      await db.insert('alpha', 'notes', _note('1'));
      await expectCode(
        db.query('beta', 'notes'),
        PluxErrorCode.dbCollectionUnknown,
      );
      await expectCode(
        db.insert('beta', 'notes', _note('1')),
        PluxErrorCode.dbCollectionUnknown,
      );
    });

    test('app-shared collections are visible to every plugin; the app\'s '
        'own graphs cannot see plugin collections', () async {
      release = _Declarations({
        '': [_notes('', n: 9)],
        'alpha': const [],
        'beta': const [],
      });
      db = make();
      await db.insert('alpha', 'notes', _note('1', text: 'from alpha'));
      final seenByBeta = await db.query('beta', 'notes');
      expect((seenByBeta.single! as Map)['text'], 'from alpha');

      release = _Declarations({
        '': const [],
        'alpha': [_notes('alpha')],
      });
      db = make();
      await expectCode(
        db.query('', 'notes'),
        PluxErrorCode.dbCollectionUnknown,
      );
    });

    test('key-value stores are per plugin [DB-009]', () async {
      await db.kvSet('alpha', 'theme', 'dark');
      await db.kvSet('beta', 'theme', 'light');
      await db.kvSet('', 'theme', 'app');
      expect(await db.kvGet('alpha', 'theme'), 'dark');
      expect(await db.kvGet('beta', 'theme'), 'light');
      expect(await db.kvGet('', 'theme'), 'app');
      expect(await db.kvGet('alpha', 'missing'), isNull);
    });
  });

  group('records', () {
    test('convert PXL values to stored JSON and back [DB-006]', () async {
      await db.insert('alpha', 'notes', {
        ..._note('1'),
        'due': parseDate('2026-03-04'),
        'tags': ['a', 'b'],
      });
      final row =
          (await db.query('alpha', 'notes')).single! as Map<String, Object?>;
      expect(row['due'], parseDate('2026-03-04'));
      expect(row['tags'], ['a', 'b']);
      final stored = (await adapter.get(
        release.byPlugin['alpha']!.single.name,
        '1',
      ))!;
      expect(stored['due'], '2026-03-04');
    });

    test('check values against their declared types', () async {
      await expectCode(
        db.insert('alpha', 'notes', {..._note('1'), 'rank': 'high'}),
        PluxErrorCode.dbRecordInvalid,
      );
      await expectCode(
        db.insert('alpha', 'notes', {
          ..._note('1'),
          'tags': [1],
        }),
        PluxErrorCode.dbRecordInvalid,
      );
      await expectCode(
        db.insert('alpha', 'notes', {..._note('1'), 'unknown': 1}),
        PluxErrorCode.dbRecordInvalid,
      );
    });

    test('insert reports a taken key, update a missing one', () async {
      await db.insert('alpha', 'notes', _note('1'));
      await expectCode(
        db.insert('alpha', 'notes', _note('1')),
        PluxErrorCode.dbKeyConflict,
      );
      await expectCode(
        db.update('alpha', 'notes', '2', {'text': 'x'}),
        PluxErrorCode.dbRecordNotFound,
      );
      await db.update('alpha', 'notes', '1', {'text': 'changed'});
      final row = (await db.query('alpha', 'notes')).single! as Map;
      expect(row['text'], 'changed');
    });

    test(
      'upsert fills the key field from the key and rejects a mismatch',
      () async {
        await db.upsert('alpha', 'notes', '5', {
          'text': 't',
          'due': null,
          'tags': <Object?>[],
          'rank': 1,
        });
        await db.upsert('alpha', 'notes', '5', _note('5', text: 'again'));
        expect(await db.query('alpha', 'notes'), hasLength(1));
        await expectCode(
          db.upsert('alpha', 'notes', '5', _note('6')),
          PluxErrorCode.dbRecordInvalid,
        );
      },
    );

    test('delete is idempotent', () async {
      await db.insert('alpha', 'notes', _note('1'));
      expect(await db.delete('alpha', 'notes', '1'), isTrue);
      expect(await db.delete('alpha', 'notes', '1'), isFalse);
    });
  });

  group('queries [DB-006]', () {
    setUp(() async {
      for (var i = 1; i <= 6; i++) {
        await db.insert('alpha', 'notes', _note('$i', rank: i % 3));
      }
    });

    List<String> ids(List<Object?> rows) => [
      for (final r in rows) (r! as Map)['id']! as String,
    ];

    test('sort, limit and offset', () async {
      expect(ids(await db.query('alpha', 'notes', orderBy: 'rank', limit: 3)), [
        '3',
        '6',
        '1',
      ]);
      expect(
        ids(
          await db.query(
            'alpha',
            'notes',
            orderBy: 'rank',
            descending: true,
            limit: 2,
            offset: 1,
          ),
        ),
        ['5', '1'],
      );
    });

    test('a typed filter runs in the adapter', () async {
      expect(
        ids(
          await db.query(
            'alpha',
            'notes',
            filter: const DbCompare('rank', DbOp.eq, 0),
          ),
        ),
        ['3', '6'],
      );
    });

    test(
      'a condition over the record filters before the page is cut',
      () async {
        bool odd(Object? r) => int.parse((r! as Map)['id']! as String).isOdd;
        expect(
          ids(await db.query('alpha', 'notes', where: odd, orderBy: 'rank')),
          ['3', '1', '5'],
        );
        expect(
          ids(
            await db.query(
              'alpha',
              'notes',
              where: odd,
              orderBy: 'rank',
              limit: 1,
              offset: 1,
            ),
          ),
          ['1'],
        );
      },
    );

    test('a condition that fails is a typed query error', () async {
      await expectCode(
        db.query('alpha', 'notes', where: (_) => throw StateError('boom')),
        PluxErrorCode.dbQueryInvalid,
      );
    });

    test(
      'a negative limit and an unknown sort field are query errors',
      () async {
        await expectCode(
          db.query('alpha', 'notes', limit: -1),
          PluxErrorCode.dbQueryInvalid,
        );
        await expectCode(
          db.query('alpha', 'notes', orderBy: 'nope'),
          PluxErrorCode.dbQueryInvalid,
        );
      },
    );

    test('never return more than db.queryRows [LIM-004]', () async {
      release = _Declarations(release.byPlugin, limits: {'db.queryRows': 4});
      db = make();
      expect(await db.query('alpha', 'notes', limit: 100), hasLength(4));
    });
  });

  group('limits [LIM-004]', () {
    test(
      'a record over db.recordBytes is refused with a typed error',
      () async {
        release = _Declarations(
          release.byPlugin,
          limits: {'db.recordBytes': 200},
        );
        db = make();
        await db.insert('alpha', 'notes', _note('1'));
        await expectCode(
          db.insert('alpha', 'notes', _note('2', text: 'x' * 500)),
          PluxErrorCode.dbLimitExceeded,
        );
        await expectCode(
          db.update('alpha', 'notes', '1', {'text': 'x' * 500}),
          PluxErrorCode.dbLimitExceeded,
        );
        expect(await db.query('alpha', 'notes'), hasLength(1));
      },
    );

    test(
      'a collection over db.collectionRecords refuses new records',
      () async {
        release = _Declarations(
          release.byPlugin,
          limits: {'db.collectionRecords': 2},
        );
        db = make();
        await db.insert('alpha', 'notes', _note('1'));
        await db.insert('alpha', 'notes', _note('2'));
        await expectCode(
          db.insert('alpha', 'notes', _note('3')),
          PluxErrorCode.dbLimitExceeded,
        );
        // Replacing and deleting still work.
        await db.upsert('alpha', 'notes', '2', _note('2', text: 'again'));
        await db.delete('alpha', 'notes', '1');
        await db.insert('alpha', 'notes', _note('3'));
      },
    );

    test('a key-value store over db.kvBytes refuses the write', () async {
      release = _Declarations(release.byPlugin, limits: {'db.kvBytes': 64});
      db = make();
      await db.kvSet('alpha', 'a', 'short');
      await expectCode(
        db.kvSet('alpha', 'b', 'x' * 100),
        PluxErrorCode.dbLimitExceeded,
      );
      expect(await db.kvGet('alpha', 'b'), isNull);
      // Another plugin has its own budget.
      await db.kvSet('beta', 'a', 'x' * 40);
    });

    test('kvSet refuses null and keeps the type of a key [DB-009]', () async {
      await expectCode(
        db.kvSet('alpha', 'k', null),
        PluxErrorCode.dbRecordInvalid,
      );
      await db.kvSet('alpha', 'k', 1);
      await expectCode(
        db.kvSet('alpha', 'k', 'one'),
        PluxErrorCode.dbValueTypeMismatch,
      );
    });
  });

  group('migrations at activation [DB-005]', () {
    test('a new release migrates a namespace before its first use', () async {
      await db.insert('alpha', 'notes', _note('1', text: 'kept'));
      release = _Declarations({
        ...release.byPlugin,
        'alpha': [
          _notes(
            'alpha',
            version: 2,
            fields: [
              DbField('id', 'string'),
              DbField('body', 'string'),
              DbField('due', 'date?'),
              DbField('tags', 'list<string>'),
              DbField('rank', 'int'),
              DbField('pinned', 'bool?'),
            ],
            migrations: const [
              DbMigrationPlan(from: 1, rename: {'body': 'text'}),
            ],
          ),
        ],
      }, sequence: 2);
      final row = (await db.query('alpha', 'notes')).single! as Map;
      expect(row['body'], 'kept');
      expect(row['pinned'], isNull);
      expect(reports, isEmpty);
    });

    test('a failed migration is reported once, leaves the data at the '
        'previous version and refuses actions on the collection with '
        'PLX-5200', () async {
      await db.insert('alpha', 'notes', _note('1', text: 'kept'));
      final broken = _Declarations({
        ...release.byPlugin,
        'alpha': [
          _notes(
            'alpha',
            version: 2,
            fields: [
              ...release.byPlugin['alpha']!.single.fields,
              DbField('required', 'string'),
            ],
          ),
        ],
      }, sequence: 2);
      release = broken;
      await expectCode(
        db.query('alpha', 'notes'),
        PluxErrorCode.dbMigrationFailed,
      );
      await expectCode(
        db.insert('alpha', 'notes', _note('2')),
        PluxErrorCode.dbMigrationFailed,
      );
      expect(reports.map((e) => e.code), [PluxErrorCode.dbMigrationFailed]);
      // Other namespaces are unaffected.
      await db.insert('beta', 'notes', _note('1'));
      // The data is intact at the previous version.
      expect((await adapter.get(_notes('alpha').name, '1'))!['text'], 'kept');
      // A fixed release migrates and the collection is usable again.
      release = _Declarations({
        ...release.byPlugin,
        'alpha': [
          _notes(
            'alpha',
            version: 2,
            fields: [
              ...broken.byPlugin['alpha']!.single.fields.where(
                (f) => f.name != 'required',
              ),
              DbField('extra', 'string?'),
            ],
          ),
        ],
      }, sequence: 3);
      expect(await db.query('alpha', 'notes'), hasLength(1));
    });

    test('dropped collections are deleted with their records', () async {
      await db.insert('beta', 'notes', _note('1'));
      release = _Declarations(
        {...release.byPlugin, 'beta': const []},
        sequence: 2,
        dropped: {
          'beta': [release.byPlugin['beta']!.single.name],
        },
      );
      // The plugin now sees the app's shared collection of that key, and
      // its own is gone with its records.
      expect(await db.query('beta', 'notes'), isEmpty);
      await expectCode(
        adapter.query(_notes('beta', n: 2).name, const DbQuery()),
        PluxErrorCode.dbCollectionUnknown,
      );
    });
  });

  group('profiles and adapters', () {
    test('a profile that requires encryption refuses an unencrypted '
        'adapter without opening it [DB-002]', () async {
      adapter = MemoryDatabaseAdapter();
      release = _Declarations(release.byPlugin, requireEncryption: true);
      db = make();
      await expectCode(
        db.query('alpha', 'notes'),
        PluxErrorCode.dbEncryptionRequired,
      );
      await expectCode(
        db.kvSet('alpha', 'k', 1),
        PluxErrorCode.dbEncryptionRequired,
      );
      // The same adapter serves a profile that does not require it.
      release = _Declarations(release.byPlugin);
      await db.insert('alpha', 'notes', _note('1'));
    });

    test('the built-in store serves key-value entries and reports '
        'PLX-5201 for collections [DB-001, DB-003]', () async {
      final dir = Directory.systemTemp.createTempSync('plux-db-svc');
      addTearDown(() => dir.deleteSync(recursive: true));
      final builtIn = BuiltInDatabaseAdapter(
        store: PlainFileStore(path: '${dir.path}/kv.json', maxBytes: 1 << 20),
        encrypted: false,
      );
      db = PluxDatabase(
        adapter: builtIn,
        report: reports.add,
        declarations: () => release,
      );
      await db.kvSet('alpha', 'k', 'v');
      expect(await db.kvGet('alpha', 'k'), 'v');
      await expectCode(db.query('alpha', 'notes'), PluxErrorCode.dbUnavailable);
      // A plugin that declares nothing still works.
      release = _Declarations({'gamma': const []});
      await db.kvSet('gamma', 'k', 1);
    });

    test('without a release only key-value entries work', () async {
      db = make(declarations: () => null);
      await db.kvSet('alpha', 'k', 1);
      expect(await db.kvGet('alpha', 'k'), 1);
      await expectCode(db.query('alpha', 'notes'), PluxErrorCode.dbUnavailable);
    });
  });

  group('wipe [DB-008]', () {
    setUp(() async {
      await db.insert('alpha', 'notes', _note('1'));
      await db.insert('beta', 'notes', _note('1'));
      await db.insert('', 'notes', _note('1'));
      await db.kvSet('alpha', 'k', 1);
      await db.kvSet('beta', 'k', 2);
    });

    test(
      'per plugin clears its collections and key-value entries only',
      () async {
        await db.wipe(plugin: 'alpha');
        expect(await db.query('alpha', 'notes'), isEmpty);
        expect(await db.kvGet('alpha', 'k'), isNull);
        expect(await db.query('beta', 'notes'), hasLength(1));
        expect(await db.kvGet('beta', 'k'), 2);
        // The app's shared collection is not the plugin's to lose.
        expect(await db.query('', 'notes'), hasLength(1));
        await db.insert('alpha', 'notes', _note('1'));
      },
    );

    test('everything clears every collection and key-value entry, and the '
        'database is usable again', () async {
      await db.wipe();
      expect(await db.query('alpha', 'notes'), isEmpty);
      expect(await db.query('beta', 'notes'), isEmpty);
      expect(await db.query('', 'notes'), isEmpty);
      expect(await db.kvGet('alpha', 'k'), isNull);
      expect(await db.kvGet('beta', 'k'), isNull);
      await db.insert('alpha', 'notes', _note('2'));
      expect(await db.query('alpha', 'notes'), hasLength(1));
    });
  });

  group('watch [DB-006]', () {
    test('delivers new objects only for rows that changed', () async {
      for (var i = 1; i <= 3; i++) {
        await db.insert('alpha', 'notes', _note('$i', rank: i));
      }
      final results = <DbRows>[];
      final sub = db
          .watch('alpha', 'notes', orderBy: 'rank')
          .listen(results.add);
      addTearDown(sub.cancel);
      await _until(() => results.isNotEmpty);
      final first = results.last;
      expect(first.rows, hasLength(3));
      expect(first.changed, {'1', '2', '3'});

      await db.update('alpha', 'notes', '2', {'text': 'edited'});
      await _until(() => results.length == 2);
      final second = results.last;
      expect(second.changed, {'2'});
      expect(second.removed, isEmpty);
      expect(identical(second.rows[0], first.rows[0]), isTrue);
      expect(identical(second.rows[1], first.rows[1]), isFalse);
      expect(identical(second.rows[2], first.rows[2]), isTrue);
      expect((second.rows[1]! as Map)['text'], 'edited');

      await db.delete('alpha', 'notes', '1');
      await db.insert('alpha', 'notes', _note('4', rank: 4));
      await _until(() => results.last.changed.contains('4'));
      final last = results.last;
      expect(
        last.removed.length + results.expand((r) => r.removed).length,
        greaterThan(0),
      );
      expect(last.rows.map((r) => (r! as Map)['id']), ['2', '3', '4']);
    });

    test('a watch over 1,000 rows delivers a single-row change as a result '
        'where only that row is new [DB-007]', () async {
      for (var i = 0; i < 1000; i++) {
        await db.insert(
          'alpha',
          'notes',
          _note('r${i.toString().padLeft(4, '0')}', rank: i),
        );
      }
      final results = <DbRows>[];
      final sub = db
          .watch('alpha', 'notes', orderBy: 'rank', limit: 1000)
          .listen(results.add);
      addTearDown(sub.cancel);
      await _until(() => results.isNotEmpty);
      expect(results.last.rows, hasLength(1000));
      final watch = Stopwatch()..start();
      await db.update('alpha', 'notes', 'r0500', {'text': 'changed'});
      await _until(() => results.length == 2);
      watch.stop();
      expect(results.last.changed, {'r0500'});
      expect(
        results.last.rows.where(
          (r) => !identical(r, results.first.rows[(r! as Map)['rank']! as int]),
        ),
        hasLength(1),
      );
      expect(watch.elapsedMilliseconds, lessThan(1000));
    });
  });
}

/// Waits until [test] holds, yielding to the event loop.
Future<void> _until(bool Function() test) async {
  final deadline = DateTime.now().add(const Duration(seconds: 10));
  while (!test()) {
    if (DateTime.now().isAfter(deadline)) fail('timed out waiting');
    await Future<void>.delayed(Duration.zero);
  }
}
