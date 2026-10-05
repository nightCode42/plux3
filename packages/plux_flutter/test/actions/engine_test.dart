// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/actions/engine.dart';
import 'package:plux_flutter/src/actions/graph.dart';
import 'package:plux_flutter/src/actions/handlers.dart';
import 'package:plux_flutter/src/actions/run.dart';
import 'package:plux_flutter/src/actions/state_handlers.dart';
import 'package:plux_flutter/src/render/values.dart';
import 'package:plux_flutter/src/schema/registry.g.dart';

/// The permanent ID of an action.
int id(String action) =>
    actionDescriptors.firstWhere((d) => d.name == action).id;

/// The permanent ID of an action's input.
int input(String action, String name) =>
    actionDescriptors.firstWhere((d) => d.name == action).inputs[name]!;

/// A literal input.
InputReader lit(Object? v) =>
    (_) => v;

/// Records the navigation runs ask for; [present] answers after [delay].
final class FakeNavigator implements RunNavigator {
  final List<String> calls = [];
  Object? answer;
  Duration delay = Duration.zero;
  Completer<Object?>? pending;

  @override
  Future<void> navigate(
    String route,
    Map<String, Object?> params,
    String mode,
    String? until,
  ) async => calls.add('$mode $route $params ${until ?? ''}'.trim());

  @override
  Future<Object?> present(
    String route,
    Map<String, Object?> params, {
    required bool sheet,
    required bool dismissible,
  }) async {
    calls.add('${sheet ? 'sheet' : 'dialog'} $route');
    if (pending case final p?) return p.future;
    await Future<void>.delayed(delay);
    return answer;
  }

  @override
  void pop(Object? result) => calls.add('pop $result');

  @override
  void switchTab(String tab) => calls.add('tab $tab');
}

void main() {
  late FakeNavigator nav;
  late List<PluxException> reports;
  late List<(String, Map<String, Object?>)> events;
  late List<Map<String, Object?>> telemetry;

  ActionHost host({
    ActionLimits limits = const ActionLimits(
      stepsPerRun: 100,
      stepTimeout: Duration(seconds: 5),
      runTimeout: Duration(seconds: 10),
    ),
    bool debug = false,
  }) => ActionHost(
    context: StepContext(
      navigator: nav,
      emit: (n, p) => events.add((n, p)),
      nativeActions: const NoNativeActions(),
    ),
    limits: limits,
    report: reports.add,
    record: (name, {fields = const {}, route = '', pluginKey = ''}) {
      if (name == 'action_run') telemetry.add(fields);
    },
    route: 'home',
    pluginKey: 'nav',
    debug: debug,
  );

  Future<RunResult> run(
    List<GraphStep> steps, {
    ActionHost? on,
    Map<String, Object?> roots = const {},
    Object? event,
  }) async => (await (on ?? host()).start(
    ActionGraph(id: 'g', steps: steps),
    roots: () => roots,
    key: 'k',
    event: event,
  ))!;

  setUp(() {
    nav = FakeNavigator();
    reports = [];
    events = [];
    telemetry = [];
  });

  test('condition takes its branch, and later steps read outputs and the event [RT-021]', () async {
    final r = await run([
      GraphStep(
        id: 'check',
        action: id('condition'),
        inputs: {input('condition', 'when'): (roots) => roots['event'] == 3},
        branches: const {'then': 1, 'else': 2},
      ),
      GraphStep(
        id: 'yes',
        action: id('emitHostEvent'),
        inputs: {
          input('emitHostEvent', 'event'): lit('yes'),
          input('emitHostEvent', 'payload'): (roots) => {
            'seen': (roots['steps']! as Map)['check'],
          },
        },
      ),
      GraphStep(
        id: 'no',
        action: id('emitHostEvent'),
        inputs: {input('emitHostEvent', 'event'): lit('no')},
      ),
    ], event: 3);
    expect(r.outcome, RunOutcome.ok);
    expect(events.single.$1, 'yes');
    expect(events.single.$2['seen'], {'output': null, 'error': null});
    expect(telemetry.single, containsPair('result', 'ok'));
    expect(telemetry.single, containsPair('steps', 2));
  });

  test('an action of a later phase fails its step with PLX-4010, which onError handles [RT-021]', () async {
    final r = await run([
      GraphStep(
        id: 'toast',
        action: id('showToast'),
        inputs: {input('showToast', 'message'): lit('hi')},
        onError: 1,
      ),
      GraphStep(
        id: 'recover',
        action: id('emitHostEvent'),
        inputs: {
          input('emitHostEvent', 'event'): lit('recovered'),
          input('emitHostEvent', 'payload'): (roots) => {
            'error': ((roots['steps']! as Map)['toast'] as Map)['error'],
          },
        },
      ),
    ]);
    expect(r.outcome, RunOutcome.ok);
    final error = events.single.$2['error']! as Map;
    expect(error['kind'], 'custom');
    expect(error['message'], contains('showToast arrives in P5'));
    expect(reports, isEmpty);
  });

  test('an unhandled error ends the run and is reported with its graph and step [RT-021]', () async {
    final r = await run([
      GraphStep(id: 'toast', action: id('showToast'), next: 1),
      GraphStep(
        id: 'never',
        action: id('emitHostEvent'),
        inputs: {input('emitHostEvent', 'event'): lit('never')},
      ),
    ]);
    expect(r.outcome, RunOutcome.failed);
    expect(r.failingStep, 'toast');
    expect(events, isEmpty);
    final e = reports.single;
    expect(e.code, PluxErrorCode.actionsNotAvailable);
    expect(e.details, containsPair('graph', 'g'));
    expect(e.details, containsPair('step', 'toast'));
    expect(telemetry.single, containsPair('result', 'failed'));
    expect(telemetry.single, containsPair('failing_step', 'toast'));
  });

  test(
    'an action ID this runtime does not know is refused like a later one',
    () async {
      final r = await run([const GraphStep(id: 'x', action: 9999)]);
      expect(r.error?.code, PluxErrorCode.actionsNotAvailable);
    },
  );

  test('stop ends the run with its result, or fails it with a custom error (PLX-5004) [NAV-009]', () async {
    final ok = await run([
      GraphStep(
        id: 'done',
        action: id('stop'),
        inputs: {
          input('stop', 'result'): lit({'decision': 'allow'}),
        },
        next: 1,
      ),
      GraphStep(
        id: 'never',
        action: id('emitHostEvent'),
        inputs: {input('emitHostEvent', 'event'): lit('never')},
      ),
    ]);
    expect(ok.outcome, RunOutcome.ok);
    expect(ok.result, {'decision': 'allow'});
    expect(events, isEmpty);

    final failed = await run([
      GraphStep(
        id: 'stop',
        action: id('stop'),
        inputs: {input('stop', 'error'): lit('E_CUSTOM')},
      ),
    ]);
    expect(failed.error?.code, PluxErrorCode.actionCustomError);
    expect(failed.error?.message, contains('E_CUSTOM'));
  });

  test('a binding that fails is a validation error (PLX-5003)', () async {
    final r = await run([
      GraphStep(
        id: 'check',
        action: id('condition'),
        inputs: {
          input('condition', 'when'): (_) =>
              throw const BindingError('no such root'),
        },
      ),
    ]);
    expect(r.error?.kind, ActionErrorKind.validation);
    expect(r.error?.code, PluxErrorCode.actionValueInvalid);
  });

  test('a run that executes more steps than action.stepsPerRun stops with PLX-5002, whatever onError says [ACT-005]', () async {
    GraphStep cond(String id_, int next) => GraphStep(
      id: id_,
      action: id('condition'),
      inputs: {input('condition', 'when'): lit(true)},
      branches: {'then': next},
      onError: 4,
    );
    final r = await run(
      [
        cond('a', 1),
        cond('b', 2),
        cond('c', 3),
        cond('d', 4),
        GraphStep(
          id: 'e',
          action: id('emitHostEvent'),
          inputs: {input('emitHostEvent', 'event'): lit('e')},
        ),
      ],
      on: host(
        limits: const ActionLimits(
          stepsPerRun: 3,
          stepTimeout: Duration(seconds: 5),
          runTimeout: Duration(seconds: 10),
        ),
      ),
    );
    expect(r.error?.code, PluxErrorCode.actionStepLimitExceeded);
    expect(r.steps, 3);
    expect(events, isEmpty);
  });

  test('a step longer than action.stepTimeout fails with a timeout (PLX-5001) that onError handles [ACT-005]', () async {
    final never = Completer<void>();
    final h = ActionHost(
      context: StepContext(
        navigator: nav,
        emit: (n, p) => events.add((n, p)),
        nativeActions: _Slow(never.future),
      ),
      limits: const ActionLimits(
        stepsPerRun: 10,
        stepTimeout: Duration(milliseconds: 30),
        runTimeout: Duration(seconds: 10),
      ),
      report: reports.add,
      record: (_, {fields = const {}, route = '', pluginKey = ''}) {},
      route: 'home',
      pluginKey: 'nav',
      debug: false,
    );
    final r = await run([
      GraphStep(
        id: 'scan',
        action: id('callNative'),
        inputs: {input('callNative', 'action'): lit('scan')},
        onError: 1,
      ),
      GraphStep(
        id: 'tell',
        action: id('emitHostEvent'),
        inputs: {
          input('emitHostEvent', 'event'): lit('timedOut'),
          input('emitHostEvent', 'payload'): (roots) => {
            'kind':
                (((roots['steps']! as Map)['scan'] as Map)['error']
                    as Map)['kind'],
          },
        },
      ),
    ], on: h);
    expect(r.outcome, RunOutcome.ok);
    expect(events.single.$2['kind'], 'timeout');
  });

  test(
    'time waiting for the user in a dialog counts against no bound [ACT-005]',
    () async {
      nav
        ..delay = const Duration(milliseconds: 80)
        ..answer = true;
      final r = await run(
        [
          GraphStep(
            id: 'ask',
            action: id('openDialog'),
            inputs: {input('openDialog', 'route'): lit('confirm')},
          ),
        ],
        on: host(
          limits: const ActionLimits(
            stepsPerRun: 10,
            stepTimeout: Duration(milliseconds: 10),
            runTimeout: Duration(milliseconds: 20),
          ),
        ),
      );
      expect(r.outcome, RunOutcome.ok, reason: '${r.error}');
      expect(nav.calls, ['dialog confirm']);
    },
  );

  test('disposing the owner cancels its runs: the pending step is abandoned and nothing after it runs [ACT-004]', () async {
    nav.pending = Completer();
    final h = host();
    final result = h.start(
      ActionGraph(
        id: 'g',
        steps: [
          GraphStep(
            id: 'ask',
            action: id('openDialog'),
            inputs: {input('openDialog', 'route'): lit('confirm')},
            next: 1,
          ),
          GraphStep(
            id: 'tell',
            action: id('emitHostEvent'),
            inputs: {input('emitHostEvent', 'event'): lit('after')},
          ),
        ],
      ),
      roots: () => const {},
      key: 'k',
    );
    await Future<void>.delayed(Duration.zero);
    expect(h.running, 1);
    h.dispose();
    final r = (await result)!;
    expect(r.outcome, RunOutcome.cancelled);
    nav.pending!.complete(true);
    await Future<void>.delayed(Duration.zero);
    expect(events, isEmpty);
    expect(reports, isEmpty);
    expect(
      await h.start(
        const ActionGraph(id: 'g', steps: []),
        roots: () => const {},
        key: 'other',
      ),
      isNull,
      reason: 'a disposed engine starts nothing',
    );
  });

  test('a trigger is dropped while the same handler still runs, so a double tap cannot navigate twice', () async {
    nav.pending = Completer();
    final h = host();
    final graph = ActionGraph(
      id: 'g',
      steps: [
        GraphStep(
          id: 'ask',
          action: id('openDialog'),
          inputs: {input('openDialog', 'route'): lit('confirm')},
        ),
      ],
    );
    final first = h.start(graph, roots: () => const {}, key: 'button');
    final second = await h.start(graph, roots: () => const {}, key: 'button');
    expect(second, isNull);
    nav.pending!.complete(null);
    expect((await first)?.outcome, RunOutcome.ok);
    expect(nav.calls, ['dialog confirm']);
  });

  test('the navigation actions call the navigator with their inputs and defaults [NAV-005]', () async {
    await run([
      GraphStep(
        id: 'push',
        action: id('navigate'),
        inputs: {
          input('navigate', 'route'): lit('detail'),
          input('navigate', 'params'): lit({'itemId': '1'}),
        },
        next: 1,
      ),
      GraphStep(
        id: 'replace',
        action: id('navigate'),
        inputs: {
          input('navigate', 'route'): lit('detail'),
          // A literal enum carries its permanent value ID.
          input('navigate', 'mode'): lit(2),
        },
        next: 2,
      ),
      GraphStep(
        id: 'tab',
        action: id('switchTab'),
        inputs: {input('switchTab', 'tab'): lit('second')},
        next: 3,
      ),
      GraphStep(
        id: 'back',
        action: id('pop'),
        inputs: {input('pop', 'result'): lit('x')},
      ),
    ]);
    expect(nav.calls, [
      'push detail {itemId: 1}',
      'replace detail {}',
      'tab second',
      'pop x',
    ]);
  });

  test(
    'a custom action the host did not register fails with PLX-4202',
    () async {
      final r = await run([
        GraphStep(
          id: 'scan',
          action: id('callNative'),
          inputs: {input('callNative', 'action'): lit('scan')},
        ),
      ]);
      expect(r.error?.code, PluxErrorCode.nativeActionNotRegistered);
    },
  );

  test('the engine runs every action Appendix D tags up to P4 and the P5 ones delivered so far, and refuses the rest (ADR-0039)', () {
    for (final d in actionDescriptors) {
      final handler = handlerFor(d);
      if (runsInThisRuntime(d) || stateHandlers.containsKey(d.name)) {
        expect(handler, isNot(isA<RefusingHandler>()), reason: d.name);
        expect(int.parse(d.phase.substring(1)), lessThanOrEqualTo(5));
      } else {
        expect(handler, isA<RefusingHandler>(), reason: d.name);
        expect(d.phase, isNot('P4'), reason: d.name);
      }
    }
    expect(
      [
        for (final d in actionDescriptors)
          if (runsInThisRuntime(d)) d.name,
      ]..sort(),
      [
        'apiCall',
        'callFlow',
        'callNative',
        'condition',
        'dbDelete',
        'dbInsert',
        'dbQuery',
        'dbUpdate',
        'dbUpsert',
        'delay',
        'emitEvent',
        'emitHostEvent',
        'forEach',
        'kvGet',
        'kvRemove',
        'kvSet',
        'logout',
        'navigate',
        'openBottomSheet',
        'openDialog',
        'parallel',
        'patchState',
        'pop',
        'refreshData',
        'resetForm',
        'resetState',
        'setState',
        'stop',
        'submitForm',
        'switch',
        'switchTab',
        'sync',
        'trackEvent',
        'validateForm',
      ],
    );
  });

  test('ActionLimits come from the app bundle, else the registry defaults [LIM-001]', () {
    final d = ActionLimits.of(const {});
    expect(d.stepsPerRun, 10000);
    expect(d.stepTimeout, const Duration(seconds: 30));
    expect(d.runTimeout, const Duration(seconds: 120));
    expect(ActionLimits.of(const {'action.stepsPerRun': 7}).stepsPerRun, 7);
  });
}

/// Custom actions that never answer.
final class _Slow implements NativeActions {
  const _Slow(this._never);

  final Future<void> _never;

  @override
  Future<Object?> call(String name, Map<String, Object?> input) async {
    await _never;
    return null;
  }
}
