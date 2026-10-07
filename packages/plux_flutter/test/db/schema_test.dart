// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_flutter/src/db/source_spec.dart';

DbCollectionSchema _schema({
  int version = 1,
  List<DbField>? fields,
  List<DbMigrationPlan> plans = const [],
}) => DbCollectionSchema(
  name: 'plugin.a/id',
  id: 'id',
  key: 'items',
  version: version,
  fields:
      fields ??
      [DbField('id', 'string'), DbField('n', 'int'), DbField('t', 'string?')],
  primaryKey: const ['id'],
  migrations: plans,
);

/// Verifies: DB-004, DB-005, DB-006.
void main() {
  group('filters [DB-006]', () {
    test('read from and write to their JSON form', () {
      final json = {
        'and': [
          {'field': 'n', 'op': 'ge', 'value': 2},
          {
            'or': [
              {'field': 't', 'op': 'isNull'},
              {
                'not': {
                  'field': 'id',
                  'op': 'in',
                  'value': ['a', 'b'],
                },
              },
            ],
          },
        ],
      };
      final filter = DbFilter.fromJson(json);
      expect(filter.toJson(), json);
      expect(filter, isA<DbAnd>());
      filter.check(_schema());
      expect(
        filter.matches({'id': 'z', 'n': 3, 't': 'x'}),
        isTrue,
        reason: 'not in [a, b]',
      );
      expect(filter.matches({'id': 'a', 'n': 3, 't': 'x'}), isFalse);
      expect(filter.matches({'id': 'z', 'n': 1, 't': null}), isFalse);
    });

    test('a malformed filter is PLX-5206', () {
      for (final bad in <Object?>[
        1,
        {'field': 'n'},
        {'field': 'n', 'op': 'nearly'},
        {
          'and': [1],
        },
      ]) {
        expect(
          () => DbFilter.fromJson(bad),
          throwsA(
            isA<PluxException>().having(
              (e) => e.code,
              'code',
              PluxErrorCode.dbQueryInvalid,
            ),
          ),
        );
      }
    });
  });

  group('database source specs', () {
    test('read their literal configuration', () {
      final spec = DatabaseQuerySpec.fromConfig({
        'collection': 'items',
        'filter': {'field': 'n', 'op': 'eq', 'value': 1},
        'orderBy': 'n',
        'descending': true,
        'limit': 5,
      });
      expect(spec.collection, 'items');
      expect(spec.filter, isA<DbCompare>());
      expect(
        [spec.orderBy, spec.descending, spec.limit, spec.offset],
        ['n', true, 5, 0],
      );
      expect(
        () => DatabaseQuerySpec.fromConfig({'limit': 1}),
        throwsA(isA<PluxException>()),
      );
    });
  });

  group('records [DB-004]', () {
    test('are checked and normalized by their fields', () {
      final s = _schema();
      expect(s.checked({'id': 'a', 'n': 1}), {'id': 'a', 'n': 1, 't': null});
      expect(s.keyOf({'id': 'a', 'n': 1}), 'a');
      expect(
        () => s.checked({'id': 'a', 'n': 1.5}),
        throwsA(isA<PluxException>()),
      );
      expect(
        () => s.checked({'id': 'a', 'n': 1, 'x': 1}),
        throwsA(isA<PluxException>()),
      );
      expect(
        () => s.checked({'n': 1, 'id': null}),
        throwsA(isA<PluxException>()),
      );
    });

    test('a composite key is a JSON list', () {
      final s = DbCollectionSchema(
        name: 'app/x',
        id: 'x',
        key: 'x',
        version: 1,
        fields: [DbField('a', 'string'), DbField('b', 'int')],
        primaryKey: const ['a', 'b'],
      );
      expect(s.keyOf({'a': 'x', 'b': 2}), '["x",2]');
    });

    test('error messages name fields, never values [SEC-092]', () {
      try {
        _schema().checked({'id': 'a', 'n': 'secret-value'});
        fail('accepted');
      } on PluxException catch (e) {
        expect(e.message, contains('"n"'));
        expect(e.message, isNot(contains('secret-value')));
      }
    });
  });

  group('migration plans [DB-005]', () {
    test('rename, drop and reset convert a stored row', () {
      final old = _schema();
      final next = _schema(
        version: 2,
        fields: [
          DbField('id', 'string'),
          DbField('count', 'int'),
          DbField('label', 'string'),
        ],
        plans: const [
          DbMigrationPlan(
            from: 1,
            rename: {'count': 'n'},
            drop: ['t'],
            reset: ['label'],
          ),
        ],
      );
      final plan = DbMigrator.plan(old, next);
      expect(plan.destructive, isTrue);
      expect(plan.row({'id': 'a', 'n': 4, 't': 'gone'}), {
        'id': 'a',
        'count': 4,
        'label': '',
      });
    });

    test('a change no plan explains is PLX-5200', () {
      final old = _schema();
      for (final next in [
        _schema(version: 2, fields: [DbField('id', 'string')]),
        _schema(
          version: 2,
          fields: [..._schema().fields, DbField('new', 'int')],
        ),
        _schema(
          version: 2,
          fields: [DbField('id', 'string'), DbField('n', 'string')],
        ),
      ]) {
        expect(
          () => DbMigrator.plan(old, next),
          throwsA(
            isA<PluxException>().having(
              (e) => e.code,
              'code',
              PluxErrorCode.dbMigrationFailed,
            ),
          ),
        );
      }
    });

    test('plans below the stored version are not applied again', () {
      final stored = _schema(version: 2);
      final target = _schema(
        version: 3,
        fields: [..._schema().fields, DbField('x', 'int?')],
        plans: const [
          DbMigrationPlan(from: 1, drop: ['gone']),
        ],
      );
      expect(DbMigrator.plan(stored, target).destructive, isFalse);
    });
  });
}
