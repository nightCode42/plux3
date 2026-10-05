// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The conformance suite every `PluxDatabaseAdapter` must pass (DB-001,
/// DB-003): the built-in store runs the key-value group, the in-memory
/// reference and `plux_db_drift` run both. A host's own adapter can run it
/// the same way.
library;

import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/plux_flutter.dart';

/// A UUID-shaped collection ID.
String _id(int n) =>
    '01a0c450-6c00-7014-8000-00000006${n.toString().padLeft(4, '0')}';

/// The tasks collection of namespace [ns] at [version].
DbCollectionSchema tasks(
  String ns, {
  int version = 1,
  List<DbField>? fields,
  List<List<String>> indexes = const [
    ['done'],
  ],
  List<DbMigrationPlan> migrations = const [],
  int id = 1,
}) => DbCollectionSchema(
  name: '$ns/${_id(id)}',
  id: _id(id),
  key: 'tasks',
  version: version,
  fields:
      fields ??
      [
        DbField('id', 'string'),
        DbField('title', 'string'),
        DbField('done', 'bool'),
        DbField('priority', 'int?'),
        DbField('score', 'double?'),
        DbField('tags', 'list<string>?'),
      ],
  primaryKey: const ['id'],
  indexes: indexes,
  migrations: migrations,
);

Map<String, Object?> task(
  String id, {
  String? title,
  bool done = false,
  int? priority,
  double? score,
  List<String>? tags,
}) => {
  'id': id,
  'title': title ?? 'Task $id',
  'done': done,
  'priority': priority,
  'score': score,
  'tags': tags,
};

Future<void> expectCode(Future<Object?> f, PluxErrorCode code) async {
  try {
    await f;
  } on PluxException catch (e) {
    expect(e.code, code, reason: e.message);
    return;
  }
  fail('expected ${code.id}, but the call succeeded');
}

/// Runs the conformance suite for the adapter [create] makes.
///
/// [reopen] makes a second adapter over the same storage, to check that
/// data survives; null for adapters that keep nothing. [collections] says
/// whether the adapter stores collections; [encrypted] whether it
/// encrypts at rest.
void runAdapterConformance(
  String name, {
  required Future<PluxDatabaseAdapter> Function() create,
  Future<PluxDatabaseAdapter> Function()? reopen,
  required bool collections,
  required bool encrypted,
}) {
  group('$name conformance', () {
    late PluxDatabaseAdapter db;

    setUp(() async {
      db = await create();
      await db.open(requireEncryption: false);
    });
    tearDown(() => db.close());

    test('reports what it stores [DB-001]', () {
      expect(db.supportsCollections, collections);
      expect(db.encrypted, encrypted);
    });

    test('refuses an unencrypted database where encryption is required '
        '[DB-002]', () async {
      final other = await create();
      if (encrypted) {
        await other.open(requireEncryption: true);
        await other.close();
      } else {
        await expectCode(
          other.open(requireEncryption: true),
          PluxErrorCode.dbEncryptionRequired,
        );
      }
    });

    group('key-value store', () {
      test('stores typed values by key [DB-009]', () async {
        await db.kvSet('plugin.a', 'name', 'Ada');
        await db.kvSet('plugin.a', 'count', 3);
        await db.kvSet('plugin.a', 'ratio', 0.5);
        await db.kvSet('plugin.a', 'on', true);
        await db.kvSet('plugin.a', 'list', [1, 2]);
        await db.kvSet('plugin.a', 'map', {'k': 'v'});
        expect(await db.kvGet('plugin.a', 'name'), 'Ada');
        expect(await db.kvGet('plugin.a', 'count'), 3);
        expect(await db.kvGet('plugin.a', 'ratio'), 0.5);
        expect(await db.kvGet('plugin.a', 'on'), true);
        expect(await db.kvGet('plugin.a', 'list'), [1, 2]);
        expect(await db.kvGet('plugin.a', 'map'), {'k': 'v'});
        expect(await db.kvGet('plugin.a', 'missing'), isNull);
        expect((await db.kvAll('plugin.a')).keys, hasLength(6));
      });

      test('the first write fixes the type until the key is removed '
          '[DB-009]', () async {
        await db.kvSet('plugin.a', 'k', 'text');
        await expectCode(
          db.kvSet('plugin.a', 'k', 1),
          PluxErrorCode.dbValueTypeMismatch,
        );
        await db.kvSet('plugin.a', 'k', 'other');
        expect(await db.kvGet('plugin.a', 'k'), 'other');
        expect(await db.kvRemove('plugin.a', 'k'), isTrue);
        expect(await db.kvRemove('plugin.a', 'k'), isFalse);
        await db.kvSet('plugin.a', 'k', 1);
        expect(await db.kvGet('plugin.a', 'k'), 1);
      });

      test('namespaces do not see each other [DB-004]', () async {
        await db.kvSet('plugin.a', 'k', 'a');
        await db.kvSet('plugin.b', 'k', 'b');
        await db.kvSet('app', 'k', 'app');
        expect(await db.kvGet('plugin.a', 'k'), 'a');
        expect(await db.kvGet('plugin.b', 'k'), 'b');
        expect(await db.kvGet('app', 'k'), 'app');
        expect(await db.kvAll('plugin.b'), {'k': 'b'});
      });

      test('a transaction applies all of its writes or none', () async {
        await db.kvSet('plugin.a', 'keep', 1);
        await expectLater(
          db.transaction((tx) async {
            await tx.kvSet('plugin.a', 'new', 2);
            await tx.kvRemove('plugin.a', 'keep');
            throw StateError('abort');
          }),
          throwsStateError,
        );
        expect(await db.kvGet('plugin.a', 'keep'), 1);
        expect(await db.kvGet('plugin.a', 'new'), isNull);
        await db.transaction((tx) async {
          await tx.kvSet('plugin.a', 'new', 2);
          expect(await tx.kvGet('plugin.a', 'new'), 2);
        });
        expect(await db.kvGet('plugin.a', 'new'), 2);
      });

      test('wiping a namespace leaves the others [DB-008]', () async {
        await db.kvSet('plugin.a', 'k', 1);
        await db.kvSet('plugin.b', 'k', 2);
        await db.wipe(namespace: 'plugin.a');
        expect(await db.kvGet('plugin.a', 'k'), isNull);
        expect(await db.kvGet('plugin.b', 'k'), 2);
      });

      test('wiping everything leaves an empty database [DB-008]', () async {
        await db.kvSet('plugin.a', 'k', 1);
        await db.kvSet('app', 'k', 2);
        await db.wipe();
        await db.open(requireEncryption: false);
        expect(await db.kvGet('plugin.a', 'k'), isNull);
        expect(await db.kvGet('app', 'k'), isNull);
      });

      if (reopen != null) {
        test('survives a restart [DB-001]', () async {
          await db.kvSet('plugin.a', 'k', 'kept');
          await db.close();
          final again = await reopen();
          await again.open(requireEncryption: false);
          expect(await again.kvGet('plugin.a', 'k'), 'kept');
          await again.close();
        });
      }
    });

    if (!collections) {
      test('refuses collections with PLX-5201 [DB-003]', () async {
        await expectCode(
          db.migrate([tasks('plugin.a')]),
          PluxErrorCode.dbUnavailable,
        );
        await expectCode(
          db.query('plugin.a/x', const DbQuery()),
          PluxErrorCode.dbUnavailable,
        );
      });
      return;
    }

    group('collections', () {
      late DbCollectionSchema a;
      late DbCollectionSchema b;

      setUp(() async {
        a = tasks('plugin.a');
        b = tasks('plugin.b', id: 2);
        final out = await db.migrate([a, b]);
        expect(out.every((o) => o.ok && o.version == 1), isTrue);
      });

      test('inserts, reads, updates, upserts and deletes records '
          '[DB-006]', () async {
        await db.insert(a.name, task('1', title: 'one', priority: 2));
        expect((await db.get(a.name, '1'))!['title'], 'one');
        expect(await db.get(a.name, '2'), isNull);
        await expectCode(
          db.insert(a.name, task('1')),
          PluxErrorCode.dbKeyConflict,
        );
        await db.update(a.name, '1', {'title': 'uno', 'done': true});
        final row = (await db.get(a.name, '1'))!;
        expect([row['title'], row['done'], row['priority']], ['uno', true, 2]);
        await expectCode(
          db.update(a.name, '9', {'title': 'x'}),
          PluxErrorCode.dbRecordNotFound,
        );
        await db.upsert(a.name, task('1', title: 'replaced'));
        expect((await db.get(a.name, '1'))!['priority'], isNull);
        await db.upsert(a.name, task('2'));
        expect(await db.count(a.name), 2);
        expect(await db.delete(a.name, '1'), isTrue);
        expect(await db.delete(a.name, '1'), isFalse);
        expect(await db.count(a.name), 1);
      });

      test('checks records against their typed fields [DB-004]', () async {
        await expectCode(
          db.insert(a.name, {...task('1'), 'done': 'yes'}),
          PluxErrorCode.dbRecordInvalid,
        );
        await expectCode(
          db.insert(a.name, {...task('1'), 'extra': 1}),
          PluxErrorCode.dbRecordInvalid,
        );
        await expectCode(
          db.insert(a.name, {'id': '1', 'done': false}),
          PluxErrorCode.dbRecordInvalid,
        );
        await db.insert(a.name, {'id': '1', 'title': 't', 'done': false});
        await expectCode(
          db.update(a.name, '1', {'id': '2'}),
          PluxErrorCode.dbRecordInvalid,
        );
        final row = (await db.get(a.name, '1'))!;
        expect(row['priority'], isNull);
        expect(row['score'], isNull);
      });

      test('keeps scalar types, lists and doubles exact', () async {
        await db.insert(
          a.name,
          task('1', score: 2, tags: ['x', 'y'], priority: 9007199254740991),
        );
        final row = (await db.get(a.name, '1'))!;
        expect(row['score'], 2.0);
        expect(row['score'], isA<double>());
        expect(row['tags'], ['x', 'y']);
        expect(row['priority'], 9007199254740991);
        expect(row['done'], isFalse);
      });

      group('queries', () {
        setUp(() async {
          await db.insert(a.name, task('a', title: 'Buy milk', priority: 2));
          await db.insert(
            a.name,
            task('b', title: 'Call Ada', done: true, priority: 1, tags: ['p']),
          );
          await db.insert(a.name, task('c', title: 'Dig', priority: 3));
          await db.insert(a.name, task('d', title: 'Eat', done: true));
        });

        Future<List<String>> ids(DbQuery q) async => [
          for (final r in await db.query(a.name, q)) r['id']! as String,
        ];

        test('returns every record in key order by default [DB-006]', () async {
          expect(await ids(const DbQuery()), ['a', 'b', 'c', 'd']);
        });

        test('filters with comparisons, sets, text and logic', () async {
          expect(
            await ids(const DbQuery(filter: DbCompare('done', DbOp.eq, true))),
            ['b', 'd'],
          );
          expect(
            await ids(const DbQuery(filter: DbCompare('priority', DbOp.ge, 2))),
            ['a', 'c'],
          );
          expect(
            await ids(const DbQuery(filter: DbCompare('priority', DbOp.lt, 2))),
            ['b'],
          );
          expect(
            await ids(
              const DbQuery(filter: DbCompare('priority', DbOp.isNull)),
            ),
            ['d'],
          );
          expect(
            await ids(
              const DbQuery(filter: DbCompare('priority', DbOp.notNull)),
            ),
            ['a', 'b', 'c'],
          );
          expect(
            await ids(
              const DbQuery(
                filter: DbCompare('id', DbOp.isIn, ['a', 'd', 'zz']),
              ),
            ),
            ['a', 'd'],
          );
          expect(
            await ids(
              const DbQuery(filter: DbCompare('title', DbOp.contains, 'Ada')),
            ),
            ['b'],
          );
          expect(
            await ids(
              const DbQuery(filter: DbCompare('title', DbOp.startsWith, 'D')),
            ),
            ['c'],
          );
          expect(
            await ids(
              const DbQuery(
                filter: DbAnd([
                  DbCompare('done', DbOp.eq, true),
                  DbNot(DbCompare('priority', DbOp.isNull)),
                ]),
              ),
            ),
            ['b'],
          );
          expect(
            await ids(
              const DbQuery(
                filter: DbOr([
                  DbCompare('id', DbOp.eq, 'a'),
                  DbCompare('id', DbOp.eq, 'c'),
                ]),
              ),
            ),
            ['a', 'c'],
          );
          expect(
            await ids(const DbQuery(filter: DbCompare('tags', DbOp.eq, ['p']))),
            ['b'],
          );
        });

        test(
          'sorts, with nulls first, ties by key, and pages [DB-006]',
          () async {
            expect(await ids(const DbQuery(sort: [DbSort('priority')])), [
              'd',
              'b',
              'a',
              'c',
            ]);
            expect(
              await ids(
                const DbQuery(sort: [DbSort('priority', descending: true)]),
              ),
              ['c', 'a', 'b', 'd'],
            );
            expect(
              await ids(const DbQuery(sort: [DbSort('done'), DbSort('title')])),
              ['a', 'c', 'b', 'd'],
            );
            expect(
              await ids(
                const DbQuery(sort: [DbSort('priority')], limit: 2, offset: 1),
              ),
              ['b', 'a'],
            );
            expect(await ids(const DbQuery(limit: 0)), isEmpty);
            expect(await ids(const DbQuery(offset: 9)), isEmpty);
          },
        );

        test('counts matches', () async {
          expect(
            await db.count(a.name, const DbCompare('done', DbOp.eq, true)),
            2,
          );
          expect(await db.count(a.name), 4);
        });

        test('rejects queries that do not fit the collection with '
            'PLX-5206', () async {
          for (final q in const [
            DbQuery(sort: [DbSort('nope')]),
            DbQuery(sort: [DbSort('tags')]),
            DbQuery(filter: DbCompare('nope', DbOp.eq, 1)),
            DbQuery(filter: DbCompare('done', DbOp.eq, 'x')),
            DbQuery(filter: DbCompare('done', DbOp.contains, 'x')),
            DbQuery(filter: DbCompare('tags', DbOp.lt, 'x')),
            DbQuery(limit: -1),
            DbQuery(offset: -1),
          ]) {
            await expectCode(db.query(a.name, q), PluxErrorCode.dbQueryInvalid);
          }
        });
      });

      test('keeps namespaces apart: one plugin cannot read another\'s '
          'records [DB-004]', () async {
        await db.insert(a.name, task('1', title: 'private to a'));
        expect(await db.query(b.name, const DbQuery()), isEmpty);
        await db.insert(b.name, task('1', title: 'private to b'));
        expect((await db.get(a.name, '1'))!['title'], 'private to a');
        expect((await db.get(b.name, '1'))!['title'], 'private to b');
        await expectCode(
          db.query('plugin.c/${_id(3)}', const DbQuery()),
          PluxErrorCode.dbCollectionUnknown,
        );
      });

      test('a transaction applies all of its writes or none', () async {
        await db.insert(a.name, task('1'));
        await expectLater(
          db.transaction((tx) async {
            await tx.insert(a.name, task('2'));
            await tx.delete(a.name, '1');
            await tx.kvSet('plugin.a', 'k', 1);
            throw StateError('abort');
          }),
          throwsStateError,
        );
        expect(await db.count(a.name), 1);
        expect(await db.get(a.name, '1'), isNotNull);
        expect(await db.kvGet('plugin.a', 'k'), isNull);
        final n = await db.transaction((tx) async {
          await tx.insert(a.name, task('2'));
          await tx.update(a.name, '1', {'title': 'changed'});
          return tx.count(a.name);
        });
        expect(n, 2);
        expect((await db.get(a.name, '1'))!['title'], 'changed');
      });

      test(
        'a failing write inside a transaction undoes the earlier ones',
        () async {
          await expectCode(
            db.transaction((tx) async {
              await tx.insert(a.name, task('1'));
              await tx.insert(a.name, task('1'));
              return null;
            }),
            PluxErrorCode.dbKeyConflict,
          );
          expect(await db.count(a.name), 0);
        },
      );

      group('watch', () {
        test(
          'delivers the result now and after each change [DB-006]',
          () async {
            await db.insert(a.name, task('1'));
            const q = DbQuery(
              filter: DbCompare('done', DbOp.eq, false),
              sort: [DbSort('id')],
            );
            final seen = _Emissions(db.watch(a.name, q));
            addTearDown(seen.cancel);
            expect(await seen.next(), ['1']);
            await db.insert(a.name, task('2'));
            expect(await seen.next(), ['1', '2']);
            await db.update(a.name, '1', {'done': true});
            expect(await seen.next(), ['2']);
            await db.delete(a.name, '2');
            expect(await seen.next(), isEmpty);
          },
        );

        test(
          'does not wake for other collections or unchanged results',
          () async {
            final seen = _Emissions(
              db.watch(
                a.name,
                const DbQuery(filter: DbCompare('done', DbOp.eq, true)),
              ),
            );
            addTearDown(seen.cancel);
            expect(await seen.next(), isEmpty);
            await db.insert(b.name, task('1', done: true));
            await db.insert(a.name, task('1', done: false));
            await db.update(a.name, '1', {'done': true});
            // Had either earlier write woken the watch, its (unchanged)
            // result would come first.
            expect(await seen.next(), ['1']);
          },
        );

        test('one transaction produces one update', () async {
          final seen = _Emissions(db.watch(a.name, const DbQuery()));
          addTearDown(seen.cancel);
          expect(await seen.next(), isEmpty);
          await db.transaction((tx) async {
            for (var i = 0; i < 5; i++) {
              await tx.insert(a.name, task('$i'));
            }
          });
          expect(await seen.next(), hasLength(5));
        });

        test('fails for a collection that does not exist', () async {
          await expectLater(
            db.watch('plugin.a/${_id(77)}', const DbQuery()),
            emitsError(isA<PluxException>()),
          );
        });
      });

      group('migrations', () {
        test('are idempotent for an unchanged schema [DB-005]', () async {
          await db.insert(a.name, task('1'));
          final out = await db.migrate([a]);
          expect(out.single.ok, isTrue);
          expect(await db.count(a.name), 1);
        });

        test(
          'add nullable fields and indexes without a plan [DB-005]',
          () async {
            await db.insert(a.name, task('1', title: 'kept'));
            final v2 = tasks(
              'plugin.a',
              version: 2,
              fields: [...a.fields, DbField('note', 'string?')],
              indexes: const [
                ['done'],
                ['title'],
              ],
            );
            final out = await db.migrate([v2]);
            expect(out.single.ok, isTrue);
            expect(out.single.version, 2);
            final row = (await db.get(a.name, '1'))!;
            expect(row['title'], 'kept');
            expect(row['note'], isNull);
            await db.update(a.name, '1', {'note': 'hello'});
            expect((await db.get(a.name, '1'))!['note'], 'hello');
          },
        );

        test('rename, drop and reset by plan keep the other values '
            '[DB-005]', () async {
          await db.insert(
            a.name,
            task('1', title: 'kept', priority: 5, score: 1.5),
          );
          final v2 = tasks(
            'plugin.a',
            version: 2,
            fields: [
              DbField('id', 'string'),
              DbField('name', 'string'),
              DbField('done', 'bool'),
              DbField('priority', 'string'),
              DbField('rank', 'int'),
            ],
            indexes: const [],
            migrations: const [
              DbMigrationPlan(
                from: 1,
                rename: {'name': 'title'},
                drop: ['score', 'tags'],
                reset: ['priority', 'rank'],
              ),
            ],
          );
          final out = await db.migrate([v2]);
          expect(out.single.ok, isTrue, reason: '${out.single.error}');
          final row = (await db.get(a.name, '1'))!;
          expect(row, {
            'id': '1',
            'name': 'kept',
            'done': false,
            'priority': '',
            'rank': 0,
          });
        });

        test(
          'chain the plans of the versions a device skipped [DB-005]',
          () async {
            await db.insert(a.name, task('1', title: 'kept'));
            final v3 = tasks(
              'plugin.a',
              version: 3,
              fields: [
                DbField('id', 'string'),
                DbField('label', 'string'),
                DbField('done', 'bool'),
                DbField('priority', 'int?'),
                DbField('score', 'double?'),
                DbField('tags', 'list<string>?'),
              ],
              migrations: const [
                DbMigrationPlan(from: 2, rename: {'label': 'name'}),
                DbMigrationPlan(from: 1, rename: {'name': 'title'}),
              ],
            );
            final out = await db.migrate([v3]);
            expect(out.single.ok, isTrue, reason: '${out.single.error}');
            expect((await db.get(a.name, '1'))!['label'], 'kept');
          },
        );

        test('a migration that fails leaves the collection and its data at '
            'the previous version and spares the others [DB-005]', () async {
          await db.insert(a.name, task('1', title: 'kept'));
          await db.insert(b.name, task('1', title: 'b kept'));
          final bad = tasks(
            'plugin.a',
            version: 2,
            fields: [...a.fields, DbField('required', 'string')],
          );
          final good = tasks(
            'plugin.b',
            id: 2,
            version: 2,
            fields: [...b.fields, DbField('note', 'string?')],
          );
          final out = await db.migrate([bad, good]);
          expect(out, hasLength(2));
          final failed = out.firstWhere((o) => o.collection == a.name);
          expect(failed.ok, isFalse);
          expect(failed.version, 1);
          expect(
            (failed.error! as PluxException).code,
            PluxErrorCode.dbMigrationFailed,
          );
          expect(out.firstWhere((o) => o.collection == b.name).version, 2);
          expect((await db.get(a.name, '1'))!['title'], 'kept');
          expect((await db.get(a.name, '1'))!.containsKey('required'), isFalse);
          // The stored schema is still version 1: the same migration is
          // attempted, and fails, again.
          expect((await db.migrate([bad])).single.ok, isFalse);
          expect((await db.migrate([a])).single.version, 1);
        });

        test('never go back to an older version [DB-005]', () async {
          await db.migrate([
            tasks(
              'plugin.a',
              version: 2,
              fields: [...a.fields, DbField('n', 'int?')],
            ),
          ]);
          final back = await db.migrate([a]);
          expect(back.single.ok, isFalse);
          expect(back.single.version, 2);
        });

        test('drop collections with their records [DB-005]', () async {
          await db.insert(a.name, task('1'));
          await db.dropCollections([a.name, 'plugin.a/${_id(88)}']);
          await expectCode(
            db.query(a.name, const DbQuery()),
            PluxErrorCode.dbCollectionUnknown,
          );
          expect(await db.count(b.name), 0);
        });
      });

      test('wiping a namespace empties its collections, not the others '
          '[DB-008]', () async {
        await db.insert(a.name, task('1'));
        await db.insert(b.name, task('1'));
        await db.wipe(namespace: 'plugin.a');
        expect(await db.count(a.name), 0);
        expect(await db.count(b.name), 1);
        await db.insert(a.name, task('1'));
      });

      test(
        'wiping everything removes collections and records [DB-008]',
        () async {
          await db.insert(a.name, task('1'));
          await db.wipe();
          await db.open(requireEncryption: false);
          await expectCode(
            db.query(a.name, const DbQuery()),
            PluxErrorCode.dbCollectionUnknown,
          );
          expect((await db.migrate([a])).single.ok, isTrue);
          expect(await db.count(a.name), 0);
        },
      );

      if (reopen != null) {
        test('records survive a restart [DB-001]', () async {
          await db.insert(a.name, task('1', title: 'kept'));
          await db.close();
          final again = await reopen();
          await again.open(requireEncryption: false);
          expect((await again.migrate([a])).single.ok, isTrue);
          expect((await again.get(a.name, '1'))!['title'], 'kept');
          await again.close();
        });
      }
    });
  });
}

/// The ids of each result a watch delivers, in order, awaited one by one.
final class _Emissions {
  _Emissions(Stream<List<Map<String, Object?>>> stream) {
    _sub = stream.listen((rows) {
      _queue.add([for (final r in rows) r['id']! as String]);
      final w = _wake;
      _wake = null;
      if (w != null && !w.isCompleted) w.complete();
    });
  }

  late final StreamSubscription<Object?> _sub;
  final List<List<String>> _queue = [];
  Completer<void>? _wake;

  Future<List<String>> next() async {
    while (_queue.isEmpty) {
      _wake = Completer<void>();
      await _wake!.future.timeout(const Duration(seconds: 10));
    }
    return _queue.removeAt(0);
  }

  Future<void> cancel() => _sub.cancel();
}
