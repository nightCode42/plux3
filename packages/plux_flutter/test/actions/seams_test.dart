// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/actions/engine.dart';
import 'package:plux_flutter/src/actions/graph.dart';
import 'package:plux_flutter/src/actions/handlers.dart';
import 'package:plux_flutter/src/actions/run.dart';
import 'package:plux_flutter/src/actions/triggers.dart';
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/core/trigger_sources.dart';
import 'package:plux_flutter/src/data/client.dart';
import 'package:plux_flutter/src/data/failure.dart';
import 'package:plux_flutter/src/data/services.dart';
import 'package:plux_flutter/src/data/source.dart';
import 'package:plux_flutter/src/data/spec.dart';
import 'package:plux_flutter/src/data/transport.dart';
import 'package:plux_flutter/src/render/scope.dart';
import 'package:plux_flutter/src/schema/registry.g.dart';
import 'package:plux_flutter/src/state/access.dart';

import 'engine_r1_test.dart' show MapState;
import 'engine_test.dart' show FakeNavigator, id, input, lit;

/// The seams between the action engine (R1), the state engine (R2) and
/// the data layer (R4).
void main() {
  OwnerTriggers owner(
    List<TriggerSpec> specs,
    List<(TriggerSpec, Object?)> fired, {
    String? plugin,
    String? Function(String)? sourceOf,
    StateWatchSource watches = const NoStateWatches(),
  }) => OwnerTriggers(
    specs: specs,
    fire: (s, p) => fired.add((s, p)),
    plugin: plugin,
    sourceOf: sourceOf,
    watches: watches,
  );

  TriggerSpec spec(TriggerKind kind, String name) => TriggerSpec(
    kind: kind,
    name: name,
    handler: fbs.Handler(fbs.HandlerObjectBuilder(event: 0).toBytes()),
  );

  test('data-source loads and failures fire the dataLoaded and dataFailed triggers of the plugin and the source they name [ACT-002]', () {
    final hub = TriggerHub();
    final page = <(TriggerSpec, Object?)>[];
    final other = <(TriggerSpec, Object?)>[];
    final app = <(TriggerSpec, Object?)>[];
    hub
      ..register(
        owner(
          [
            spec(TriggerKind.dataLoaded, 'tasks'),
            spec(TriggerKind.dataFailed, 'tasks'),
          ],
          page,
          plugin: 'shop',
          sourceOf: (n) => n == 'tasks' ? 'id-1' : null,
        ),
      )
      ..register(
        owner([spec(TriggerKind.dataLoaded, 'tasks')], other, plugin: 'b'),
      )
      ..register(owner([spec(TriggerKind.dataLoaded, 'tasks')], app));
    final bridge = DataTriggers(hub);
    bridge.loaded(
      const DataSourceEvent(
        pluginKey: 'shop',
        source: 'tasks',
        sourceId: 'id-1',
        value: [1, 2],
      ),
    );
    // Another page's source of the same name is not the page's.
    bridge.loaded(
      const DataSourceEvent(
        pluginKey: 'shop',
        source: 'tasks',
        sourceId: 'id-2',
        value: [3],
      ),
    );
    bridge.failed(
      const DataSourceEvent(
        pluginKey: 'shop',
        source: 'tasks',
        sourceId: 'id-1',
        error: {'kind': 'network'},
      ),
    );
    expect(
      [for (final (s, _) in page) s.kind],
      [TriggerKind.dataLoaded, TriggerKind.dataFailed],
    );
    expect(
      [for (final (_, p) in page) p],
      [
        [1, 2],
        {'kind': 'network'},
      ],
    );
    expect(other, isEmpty);
    expect(
      [for (final (_, p) in app) p],
      [
        [1, 2],
        [3],
      ],
    );
  });

  test('Plux.sendEvent\'s sink delivers a host event to the triggers that handle it and refuses one nothing handles [HST-013]', () {
    final hub = TriggerHub();
    final fired = <(TriggerSpec, Object?)>[];
    final unregister = hub.register(
      owner([spec(TriggerKind.hostEvent, 'ping')], fired),
    );
    final sink = HostEventTriggers(hub);
    expect(sink.deliver(const PluxHostEvent('ping', {'n': 2})), isTrue);
    expect(sink.deliver(const PluxHostEvent('nope', {})), isFalse);
    expect(fired.single.$2, {'n': 2});
    unregister();
    expect(sink.deliver(const PluxHostEvent('ping', {'n': 3})), isFalse);
  });

  test('state watchers listen to the state engine\'s change stream and stop with their owner [ACT-002] [STA-001]', () {
    final c = ProviderContainer();
    addTearDown(c.dispose);
    final page = PageInstance({'count': 0});
    final keep = c.listen(pageStateProvider(page), (_, _) {});
    addTearDown(keep.close);
    final access = ScopeStateAccess(container: c, plugin: 'p', page: page);
    final fired = <(TriggerSpec, Object?)>[];
    final o = owner(
      [spec(TriggerKind.stateChange, 'page.count')],
      fired,
      watches: StateChangeWatches(access),
    )..start();
    c.read(pageStateProvider(page).notifier).set('count', 1);
    c.read(pageStateProvider(page).notifier).set('count', 1);
    c.read(pageStateProvider(page).notifier).set('count', 2);
    o.dispose();
    c.read(pageStateProvider(page).notifier).set('count', 3);
    expect([for (final (_, v) in fired) v], [1, 2]);
  });

  test('an apiCall that fails rolls back its optimistic state changes through the engine\'s log [ACT-007] [DAT-001]', () async {
    final state = MapState()..values['page.liked'] = false;
    final services = DataServices(
      transport: const _Offline(),
      authDelegate: () => null,
      environment: 'production',
      record: (name, {fields = const {}, route = '', pluginKey = ''}) {},
      report: (_) {},
    );
    final source = DataSourceController(
      spec: const DataSourceSpec(
        id: 's',
        name: 'posts',
        kind: DataKind.rest,
        type: 'int',
        baseUrls: {'production': 'https://api.example.com'},
        path: '/posts',
        operations: {
          'like': OperationSpec(name: 'like', path: '/like', auth: false),
        },
      ),
      context: services,
      caller: const DataCaller(pluginKey: 'p', domains: ['api.example.com']),
      types: const {},
    );
    final scope = DataScope(
      services: services,
      own: [source],
      shared: const [],
      roots: () => const {},
    );
    addTearDown(scope.dispose);
    final host = ActionHost(
      context: StepContext(
        navigator: FakeNavigator(),
        emit: (_, _) {},
        nativeActions: const NoNativeActions(),
        state: state,
        data: scope,
      ),
      limits: ActionLimits.of(const {}),
      report: (_) {},
      record: (name, {fields = const {}, route = '', pluginKey = ''}) {},
      route: 'r',
      pluginKey: 'p',
    );
    addTearDown(host.dispose);
    final r = await host.start(
      ActionGraph(
        id: 'g',
        steps: [
          GraphStep(
            id: 'like',
            action: id('apiCall'),
            inputs: {
              input('apiCall', 'operation'): lit('posts.like'),
              input('apiCall', 'optimistic'): lit({'page.liked': true}),
            },
          ),
        ],
      ),
      roots: () => const {},
      key: 'k',
    );
    expect(r?.outcome, RunOutcome.failed);
    expect(r?.error?.kind, ActionErrorKind.network);
    expect(state.writes, ['page.liked=true', 'page.liked=false']);
  });

  test('runs a component instance started end with it, unless detached; its later triggers start nothing [ACT-004]', () async {
    final never = Completer<void>();
    final host = ActionHost(
      context: StepContext(
        navigator: FakeNavigator(),
        emit: (_, _) {},
        nativeActions: _Waiting(never.future),
      ),
      limits: ActionLimits.of(const {}),
      report: (_) {},
      record: (name, {fields = const {}, route = '', pluginKey = ''}) {},
      route: 'r',
      pluginKey: 'p',
    );
    addTearDown(host.dispose);
    final g = ActionGraph(
      id: 'g',
      steps: [
        GraphStep(
          id: 'wait',
          action: id('callNative'),
          inputs: {input('callNative', 'action'): lit('wait')},
        ),
      ],
    );
    final instance = Object();
    final owned = host.start(
      g,
      roots: () => const {},
      key: 'a',
      owner: instance,
    );
    final detached = host.start(
      g,
      roots: () => const {},
      key: 'b',
      owner: instance,
      detached: true,
    );
    await pumpEventQueue();
    host.cancelOwned(instance);
    expect((await owned)?.outcome, RunOutcome.cancelled);
    expect(host.running, 0);
    expect(
      await host.start(g, roots: () => const {}, key: 'c', owner: instance),
      isNull,
    );
    never.complete();
    expect((await detached)?.outcome, RunOutcome.ok);
  });

  test('logout clears the cached responses and signals the host; the handler fails where no runtime ends a session [HST-010]', () async {
    var ended = 0;
    final ctx = StepContext(
      navigator: FakeNavigator(),
      emit: (_, _) {},
      nativeActions: const NoNativeActions(),
      logout: () async => ended++,
    );
    final logout = handlerFor(
      actionDescriptors.firstWhere((d) => d.name == 'logout'),
    );
    await logout.run(ctx, const {});
    expect(ended, 1);
    await expectLater(
      Future.sync(
        () => logout.run(
          StepContext(
            navigator: FakeNavigator(),
            emit: (_, _) {},
            nativeActions: const NoNativeActions(),
          ),
          const {},
        ),
      ),
      throwsA(isA<ActionError>()),
    );
  });

  test(
    'patchState with optimistic set is undone when the run fails [ACT-007]',
    () async {
      final c = ProviderContainer();
      addTearDown(c.dispose);
      final page = PageInstance({
        'draft': {'done': false, 'title': 'a'},
      });
      final keep = c.listen(pageStateProvider(page), (_, _) {});
      addTearDown(keep.close);
      final access = ScopeStateAccess(container: c, plugin: 'p', page: page);
      final run = ActionRun(
        graph: ActionGraph(
          id: 'g',
          steps: [
            GraphStep(
              id: 'patch',
              action: id('patchState'),
              inputs: {
                input('patchState', 'path'): lit('page.draft'),
                input('patchState', 'patch'): lit({'done': true}),
                input('patchState', 'optimistic'): lit(true),
              },
              next: 1,
            ),
            GraphStep(
              id: 'fail',
              action: id('stop'),
              inputs: {input('stop', 'error'): lit('boom')},
            ),
          ],
        ),
        roots: () => const {},
        context: StepContext(
          navigator: FakeNavigator(),
          emit: (_, _) {},
          nativeActions: const NoNativeActions(),
          state: access,
        ),
        limits: ActionLimits.of(const {}),
      );
      final r = await run.execute();
      expect(r.outcome, RunOutcome.failed);
      expect(c.read(pageStateProvider(page))['draft'], {
        'done': false,
        'title': 'a',
      });
    },
  );
}

/// A transport with no network.
final class _Offline implements DataTransport {
  const _Offline();

  @override
  Future<DataResponse> send(DataRequest request) => Future.error(
    const DataFailure(
      ActionErrorKind.network,
      PluxErrorCode.dataNetworkFailed,
      'offline',
    ),
  );

  @override
  Future<void> close() async {}
}

/// Custom actions that answer when [until] completes.
final class _Waiting implements NativeActions {
  const _Waiting(this.until);

  final Future<void> until;

  @override
  Future<Object?> call(String name, Map<String, Object?> input) async {
    await until;
    return null;
  }
}
