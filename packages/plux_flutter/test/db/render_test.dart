// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:async';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_flutter/src/core/runtime.dart';

import '../support/harness.dart';

const _tasks = 'plugin.todo/01e0c450-6c00-7000-8000-000000000606';
const _labels = 'app/01e0c450-6c00-7000-8000-000000000604';

DbCollectionSchema _tasksV1() => DbCollectionSchema(
  name: _tasks,
  id: '01e0c450-6c00-7000-8000-000000000606',
  key: 'tasks',
  version: 1,
  fields: [
    DbField('id', 'string'),
    DbField('name', 'string'),
    DbField('done', 'bool'),
    DbField('rank', 'int'),
  ],
  primaryKey: const ['id'],
  indexes: const [
    ['done'],
  ],
);

/// The local database through a compiled bundle: collections with
/// versions and migrations, database data sources bound to a list,
/// actions, key-value entries and wiping (DB-004 to DB-009).
///
/// Verifies: DB-001, DB-004, DB-005, DB-006, DB-008, DB-009, BND-008.
void main() {
  late Harness h;
  late MemoryDatabaseAdapter adapter;

  Uint8List file(String name) =>
      File('../../schema/testdata/bundles/db/$name.pxb').readAsBytesSync();

  setUp(() async {
    h = await Harness.create();
    adapter = MemoryDatabaseAdapter(encrypted: true);
    // A device that ran version 1 of the plugin: its tasks keep a `name`.
    await adapter.open(requireEncryption: false);
    await adapter.migrate([_tasksV1()]);
    await adapter.insert(_tasks, {
      'id': 'a',
      'name': 'Alpha',
      'done': false,
      'rank': 1,
    });
    await adapter.insert(_tasks, {
      'id': 'b',
      'name': 'Beta',
      'done': false,
      'rank': 2,
    });
  });
  tearDown(() => h.close());

  Future<void> start(WidgetTester tester) async {
    tester.view
      ..physicalSize = const Size(800, 1600)
      ..devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    await tester.runAsync(
      () => h.startFrom(file('db'), {
        'todo': file('todo'),
      }, databaseAdapter: adapter),
    );
    await tester.pumpWidget(
      const MaterialApp(home: PluxScope(child: PluxView('list'))),
    );
    await settle(tester, 6);
  }

  Future<void> tap(WidgetTester tester, String label) async {
    await tester.tap(find.text(label));
    await settle(tester, 6);
  }

  PluxRuntime runtime() => h.runtime;

  /// Runs a database call inside the test's fake-async zone and pumps until
  /// it completes: the runtime's own futures live in that zone, so waiting
  /// for them in `runAsync` would never see their microtasks run.
  Future<T> call<T>(WidgetTester tester, Future<T> Function() op) async {
    T? value;
    Object? error;
    StackTrace? stack;
    var done = false;
    unawaited(
      op().then(
        (v) {
          value = v;
          done = true;
        },
        onError: (Object e, StackTrace s) {
          error = e;
          stack = s;
          done = true;
        },
      ),
    );
    for (var i = 0; i < 200 && !done; i++) {
      await settle(tester, 1);
    }
    if (!done) fail('the call did not finish');
    if (error != null) Error.throwWithStackTrace(error!, stack!);
    return value as T;
  }

  testWidgets('the bundle needs db.v1 and the collections migrate when the '
      'plugin first uses them [DB-005] [BND-008]', (tester) async {
    await start(tester);
    // Version 1's `name` is version 2's `title`, and the new field is null.
    expect(find.text('Alpha'), findsOneWidget);
    expect(find.text('Beta'), findsOneWidget);
    final stored = await call(tester, () => adapter.get(_tasks, 'a'));
    expect(stored, {
      'id': 'a',
      'title': 'Alpha',
      'done': false,
      'rank': 1,
      'note': null,
    });
    expect(h.errors.where((e) => e.code != PluxErrorCode.syncFailed), isEmpty);
    expect(
      runtime().active.value!.meta('todo').requiredFeatures,
      contains('db.v1'),
    );
  });

  testWidgets('a list bound to a database source shows the records in order '
      'and follows the collection [DB-006]', (tester) async {
    await start(tester);
    expect(find.text('Open 2'), findsOneWidget);
    expect(
      tester.getTopLeft(find.text('Alpha')).dy,
      lessThan(tester.getTopLeft(find.text('Beta')).dy),
    );
    await tap(tester, 'add');
    expect(find.text('Cleaning'), findsOneWidget);
    expect(find.text('Open 3'), findsOneWidget);
    await tap(tester, 'mark');
    expect(find.text('Open 2'), findsOneWidget);
    expect(find.text('Alpha'), findsOneWidget);
  });

  testWidgets('a change re-renders only the changed item of a watched list '
      '[DB-006]', (tester) async {
    await start(tester);
    final alpha = tester.widget(find.text('Alpha'));
    final beta = tester.widget(find.text('Beta'));
    // Renaming Alpha behind the page's back: one row changes.
    await call(tester, () => adapter.update(_tasks, 'a', {'title': 'Alef'}));
    await settle(tester, 6);
    expect(find.text('Alef'), findsOneWidget);
    expect(find.text('Alpha'), findsNothing);
    expect(
      identical(tester.widget(find.text('Beta')), beta),
      isTrue,
      reason: 'Beta did not change, so its item was not built again',
    );
    expect(identical(tester.widget(find.text('Alef')), alpha), isFalse);
  });

  testWidgets('kvSet writes the plugin\'s key-value store, and an upsert '
      'the app\'s shared collection [DB-009] [DB-004]', (tester) async {
    await start(tester);
    await tap(tester, 'remember');
    await tap(tester, 'label');
    final db = runtime().database;
    expect(await call(tester, () => db.kvGet('todo', 'theme')), 'dark');
    expect(await call(tester, () => db.kvGet('other', 'theme')), isNull);
    final labels = await call(
      tester,
      () => adapter.query(_labels, const DbQuery()),
    );
    expect(labels, [
      {'id': 'l1', 'name': 'Home'},
    ]);
  });

  testWidgets('Plux.wipeData removes collections and key-value entries, per '
      'plugin or all [DB-008]', (tester) async {
    await start(tester);
    await tap(tester, 'remember');
    await tap(tester, 'label');
    await call(tester, () => Plux.wipeData(plugin: 'todo'));
    expect(
      await call(tester, () => adapter.count(_tasks)),
      0,
      reason: 'the plugin\'s private collection is empty',
    );
    expect(
      await call(tester, () => adapter.kvGet('plugin.todo', 'theme')),
      isNull,
    );
    expect(await call(tester, () => adapter.count(_labels)), 1);
    await call(tester, Plux.wipeData);
    await call(tester, () => adapter.open(requireEncryption: false));
    await expectLater(
      call(tester, () => adapter.count(_labels)),
      throwsA(isA<PluxException>()),
      reason: 'everything, the shared collections included, is gone',
    );
  });
}
