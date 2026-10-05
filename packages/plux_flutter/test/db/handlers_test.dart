// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/actions/engine.dart';
import 'package:plux_flutter/src/actions/graph.dart';
import 'package:plux_flutter/src/actions/handlers.dart';
import 'package:plux_flutter/src/actions/run.dart';
import 'package:plux_flutter/src/db/service.dart';
import 'package:plux_flutter/src/pxl/types.dart';

import '../actions/engine_test.dart' show FakeNavigator, id, input, lit;

String _uuid(int n) =>
    '01a0c450-6c00-7014-8000-00000008${n.toString().padLeft(4, '0')}';

final class _Declarations implements DbDeclarations {
  _Declarations(this.byPlugin);

  final Map<String, List<DbCollectionSchema>> byPlugin;

  @override
  int get sequence => 1;

  @override
  bool get requireEncryption => false;

  @override
  Map<String, int> get limits => const {};

  @override
  List<DbCollectionSchema> collectionsOf(String plugin) =>
      byPlugin[plugin] ?? const [];

  @override
  List<String> droppedOf(String plugin) => const [];

  @override
  NamedType? Function(String) typesOf(String plugin) =>
      (_) => null;
}

DbCollectionSchema _tasks(String plugin, int n) => DbCollectionSchema(
  name: '${dbNamespace(plugin)}/${_uuid(n)}',
  id: _uuid(n),
  key: 'tasks',
  version: 1,
  fields: [
    DbField('id', 'string'),
    DbField('title', 'string'),
    DbField('done', 'bool'),
    DbField('rank', 'int'),
  ],
  primaryKey: const ['id'],
);

DbCollectionSchema _secrets(String plugin, int n) => DbCollectionSchema(
  name: '${dbNamespace(plugin)}/${_uuid(n)}',
  id: _uuid(n),
  key: 'secrets',
  version: 1,
  fields: [DbField('id', 'string'), DbField('value', 'string')],
  primaryKey: const ['id'],
);

Map<String, Object?> _task(String id, int rank, {bool done = false}) => {
  'id': id,
  'title': 'Task $id',
  'done': done,
  'rank': rank,
};

/// Verifies: DB-004, DB-006, DB-009 through the action engine.
void main() {
  late MemoryDatabaseAdapter adapter;
  late PluxDatabase db;
  late List<PluxException> reports;

  ActionHost host(String plugin) => ActionHost(
    context: StepContext(
      navigator: FakeNavigator(),
      emit: (_, _) {},
      nativeActions: const NoNativeActions(),
      services: {PluxDatabase: db},
    ),
    limits: const ActionLimits(
      stepsPerRun: 100,
      stepTimeout: Duration(seconds: 5),
      runTimeout: Duration(seconds: 10),
    ),
    report: reports.add,
    record: (_, {fields = const {}, route = '', pluginKey = ''}) {},
    route: 'home',
    pluginKey: plugin,
    debug: false,
  );

  /// Runs [steps] in order as plugin [plugin] and returns the run.
  Future<RunResult> run(
    String plugin,
    List<GraphStep Function(int next)> steps,
  ) async {
    final built = [
      for (var i = 0; i < steps.length; i++)
        steps[i](i + 1 < steps.length ? i + 1 : -1),
    ];
    return (await host(plugin).start(
      ActionGraph(id: 'g', steps: built),
      roots: () => const {},
      key: 'k',
    ))!;
  }

  GraphStep Function(int) step(
    String stepId,
    String action,
    Map<String, InputReader> inputs, {
    int onError = -1,
  }) =>
      (next) => GraphStep(
        id: stepId,
        action: id(action),
        inputs: {for (final e in inputs.entries) input(action, e.key): e.value},
        next: next,
        onError: onError,
      );

  GraphStep Function(int) stop(InputReader result) =>
      (_) => GraphStep(
        id: 'stop',
        action: id('stop'),
        inputs: {input('stop', 'result'): result},
      );

  setUp(() {
    adapter = MemoryDatabaseAdapter(encrypted: true);
    reports = [];
    final declarations = _Declarations({
      '': [_tasks('', 1)],
      'alpha': [_secrets('alpha', 2)],
      'beta': [_secrets('beta', 3)],
    });
    db = PluxDatabase(
      adapter: adapter,
      report: reports.add,
      declarations: () => declarations,
    );
  });

  test('dbInsert returns the key; dbUpdate, dbUpsert and dbDelete change '
      'the record [DB-006]', () async {
    final r = await run('alpha', [
      step('ins', 'dbInsert', {
        'collection': lit('tasks'),
        'record': lit(_task('1', 1)),
      }),
      step('ups', 'dbUpsert', {
        'collection': lit('tasks'),
        'key': lit('2'),
        'record': lit(_task('2', 2)),
      }),
      step('upd', 'dbUpdate', {
        'collection': lit('tasks'),
        'key': lit('1'),
        'patch': lit({'done': true}),
      }),
      step('del', 'dbDelete', {'collection': lit('tasks'), 'key': lit('2')}),
      step('del2', 'dbDelete', {'collection': lit('tasks'), 'key': lit('2')}),
      step('all', 'dbQuery', {'collection': lit('tasks')}),
      stop(
        (roots) => {
          'key': ((roots['steps']! as Map)['ins'] as Map)['output'],
          'rows': ((roots['steps']! as Map)['all'] as Map)['output'],
        },
      ),
    ]);
    expect(r.outcome, RunOutcome.ok, reason: '${r.error}');
    final out = r.result! as Map;
    expect(out['key'], '1');
    expect((out['rows']! as List).single, {
      'id': '1',
      'title': 'Task 1',
      'done': true,
      'rank': 1,
    });
  });

  test('dbQuery evaluates its condition on each record as `record`, then '
      'sorts, offsets and limits [DB-006]', () async {
    for (var i = 1; i <= 6; i++) {
      await db.insert('alpha', 'tasks', _task('$i', i, done: i.isEven));
    }
    final r = await run('alpha', [
      step('q', 'dbQuery', {
        'collection': lit('tasks'),
        'where': (roots) => (roots['record']! as Map)['done'] == true,
        'orderBy': lit('rank'),
        'descending': lit(true),
        'limit': lit(2),
        'offset': lit(1),
      }),
      stop((roots) => ((roots['steps']! as Map)['q'] as Map)['output']),
    ]);
    expect(r.outcome, RunOutcome.ok, reason: '${r.error}');
    expect([for (final t in r.result! as List) (t! as Map)['id']], ['4', '2']);
  });

  test('a condition that is not a bool fails the step', () async {
    await db.insert('alpha', 'tasks', _task('1', 1));
    final r = await run('alpha', [
      step('q', 'dbQuery', {
        'collection': lit('tasks'),
        'where': (roots) => 'yes',
      }),
    ]);
    expect(r.outcome, RunOutcome.failed);
    expect(r.error?.code, PluxErrorCode.dbQueryInvalid);
  });

  test('errors are step errors an onError branch can read', () async {
    final r = await run('alpha', [
      step('dup1', 'dbInsert', {
        'collection': lit('tasks'),
        'record': lit(_task('1', 1)),
      }),
      step('dup2', 'dbInsert', {
        'collection': lit('tasks'),
        'record': lit(_task('1', 1)),
      }, onError: 3),
      stop(lit('not reached')),
      stop(
        (roots) =>
            (((roots['steps']! as Map)['dup2'] as Map)['error']!
                as Map)['kind'],
      ),
    ]);
    expect(r.outcome, RunOutcome.ok, reason: '${r.error}');
    expect(r.result, 'validation');
  });

  test('a plugin cannot name another plugin\'s collection: the step fails '
      'with a permission error and PLX-5209 [DB-004]', () async {
    await db.insert('alpha', 'secrets', {'id': '1', 'value': 'alpha only'});
    final r = await run('beta', [
      step('read', 'dbQuery', {'collection': lit('secrets')}),
      stop((roots) => ((roots['steps']! as Map)['read'] as Map)['output']),
    ]);
    // beta declares its own `secrets`: it reads its own, empty one.
    expect(r.outcome, RunOutcome.ok, reason: '${r.error}');
    expect(r.result, isEmpty);

    final gamma = await run('gamma', [
      step('read', 'dbQuery', {'collection': lit('secrets')}),
    ]);
    expect(gamma.outcome, RunOutcome.failed);
    expect(gamma.error?.code, PluxErrorCode.dbCollectionUnknown);
    expect(gamma.error?.kind, ActionErrorKind.permission);

    final app = await run('', [
      step('read', 'dbQuery', {'collection': lit('secrets')}),
    ]);
    expect(app.error?.code, PluxErrorCode.dbCollectionUnknown);
  });

  test(
    'kvSet, kvGet and kvRemove keep a typed value per plugin [DB-009]',
    () async {
      final r = await run('alpha', [
        step('set', 'kvSet', {'key': lit('theme'), 'value': lit('dark')}),
        step('get', 'kvGet', {'key': lit('theme')}),
        step('bad', 'kvSet', {
          'key': lit('theme'),
          'value': lit(1),
        }, onError: 4),
        stop(lit('not reached')),
        step('rm', 'kvRemove', {'key': lit('theme')}),
        step('gone', 'kvGet', {'key': lit('theme')}),
        stop((roots) {
          final s = roots['steps']! as Map;
          return [
            (s['get'] as Map)['output'],
            ((s['bad'] as Map)['error']! as Map)['kind'],
            (s['gone'] as Map)['output'],
          ];
        }),
      ]);
      expect(r.outcome, RunOutcome.ok, reason: '${r.error}');
      expect(r.result, ['dark', 'validation', null]);
      final other = await run('beta', [
        step('get', 'kvGet', {'key': lit('theme')}),
        stop((roots) => ((roots['steps']! as Map)['get'] as Map)['output']),
      ]);
      expect(other.result, isNull);
    },
  );

  test('without a database service a step fails with PLX-5201', () async {
    final h = ActionHost(
      context: StepContext(
        navigator: FakeNavigator(),
        emit: (_, _) {},
        nativeActions: const NoNativeActions(),
      ),
      limits: const ActionLimits(
        stepsPerRun: 10,
        stepTimeout: Duration(seconds: 5),
        runTimeout: Duration(seconds: 10),
      ),
      report: reports.add,
      record: (_, {fields = const {}, route = '', pluginKey = ''}) {},
      route: 'home',
      pluginKey: 'alpha',
      debug: false,
    );
    final r = (await h.start(
      ActionGraph(
        id: 'g',
        steps: [
          GraphStep(
            id: 'k',
            action: id('kvGet'),
            inputs: {input('kvGet', 'key'): lit('x')},
          ),
        ],
      ),
      roots: () => const {},
      key: 'k',
    ))!;
    expect(r.error?.code, PluxErrorCode.dbUnavailable);
  });
}
