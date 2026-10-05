// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/actions/clock.dart';
import 'package:plux_flutter/src/actions/engine.dart';
import 'package:plux_flutter/src/actions/graph.dart';
import 'package:plux_flutter/src/actions/handlers.dart';
import 'package:plux_flutter/src/actions/run.dart';
import 'package:plux_flutter/src/actions/trace.dart';
import 'package:plux_flutter/src/actions/triggers.dart';
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/pxl/values.dart';

import 'engine_test.dart' show FakeNavigator, id, input, lit;

/// Time the test drives: timers fire on [advance], sleeps complete at once
/// and are recorded, jitter is [jitter].
final class FakeClock extends ActionClock {
  Duration t = Duration.zero;
  double jitter = 0.5;
  final List<Duration> sleeps = [];
  final List<_FakeTimer> _timers = [];

  @override
  Duration get now => t;

  @override
  Timer timer(Duration d, void Function() f) =>
      _add(_FakeTimer(t + d, null, f));

  @override
  Timer periodic(Duration d, void Function() f) =>
      _add(_FakeTimer(t + d, d, f));

  Timer _add(_FakeTimer timer) {
    _timers.add(timer);
    return timer;
  }

  @override
  Future<void> sleep(Duration d) {
    sleeps.add(d);
    return Future.value();
  }

  @override
  double random() => jitter;

  /// Moves time on by [d], firing the timers due in order.
  Future<void> advance(Duration d) async {
    final end = t + d;
    for (;;) {
      final due = _timers.where((x) => x.active && x.due <= end).toList()
        ..sort((a, b) => a.due.compareTo(b.due));
      if (due.isEmpty) break;
      final next = due.first;
      t = next.due;
      next.fire();
      await pumpEventQueue();
    }
    t = end;
    await pumpEventQueue();
  }
}

final class _FakeTimer implements Timer {
  _FakeTimer(this.due, this.period, this.f);

  Duration due;
  final Duration? period;
  final void Function() f;
  bool active_ = true;
  int ticks = 0;

  bool get active => active_;

  void fire() {
    ticks++;
    if (period case final p?) {
      due += p;
    } else {
      active_ = false;
    }
    f();
  }

  @override
  void cancel() => active_ = false;

  @override
  bool get isActive => active_;

  @override
  int get tick => ticks;
}

/// Custom actions answered by [answer], per call.
final class ScriptedActions implements NativeActions {
  ScriptedActions(this.answer);

  final FutureOr<Object?> Function(String name, int call) answer;
  final List<String> calls = [];

  @override
  Future<Object?> call(String name, Map<String, Object?> input) async {
    calls.add(name);
    return answer(name, calls.length);
  }
}

/// State in a map.
final class MapState implements ActionState {
  final Map<String, Object?> values = {};
  final List<String> writes = [];

  @override
  Object? read(String path) => values[path];

  @override
  void write(String path, Object? value) {
    writes.add('$path=$value');
    values[path] = value;
  }
}

/// Flows by name.
final class MapFlows implements FlowResolver {
  MapFlows(this.flows);

  final Map<String, ActionGraph> flows;

  @override
  FlowTarget? flow(String name) {
    final g = flows[name];
    return g == null ? null : FlowTarget(g, () => const {'flags': {}});
  }
}

GraphStep native(
  String sid,
  String action, {
  int next = -1,
  int onError = -1,
  RetryPolicy? retry,
}) => GraphStep(
  id: sid,
  action: id('callNative'),
  inputs: {input('callNative', 'action'): lit(action)},
  next: next,
  onError: onError,
  retry: retry,
);

ActionGraph graph(List<GraphStep> steps) => ActionGraph(id: 'g', steps: steps);

void main() {
  late FakeClock clock;
  late List<PluxException> reports;
  late NativeActions natives;
  late MapState state;
  FlowResolver flows = const NoFlows();
  late List<(String, Map<String, Object?>)> tracked;
  late List<(String, Object?)> emitted;
  late int syncs;

  ActionHost host({
    ActionLimits limits = const ActionLimits(
      stepsPerRun: 100,
      stepTimeout: Duration(seconds: 5),
      runTimeout: Duration(seconds: 10),
      queueLengthLimit: 2,
    ),
    TraceBuffer? traces,
    void Function(ActionError)? presenter,
    bool component = false,
    void Function() Function()? lease,
  }) => ActionHost(
    lease: lease,
    context: StepContext(
      navigator: FakeNavigator(),
      emit: (_, _) {},
      nativeActions: natives,
      state: state,
      flows: flows,
      clock: clock,
      track: (n, p) => tracked.add((n, p)),
      sync: () => syncs++,
      emitEvent: component ? (n, p) => emitted.add((n, p)) : null,
    ),
    limits: limits,
    report: reports.add,
    record: (_, {fields = const {}, route = '', pluginKey = ''}) {},
    route: 'home',
    pluginKey: 'p',
    debug: true,
    traces: traces,
    presenter: presenter,
  );

  setUp(() {
    clock = FakeClock();
    reports = [];
    natives = ScriptedActions((_, _) => null);
    state = MapState();
    flows = const NoFlows();
    tracked = [];
    emitted = [];
    syncs = 0;
  });

  group('concurrency policies [ACT-003]', () {
    late Completer<Object?> gate;
    setUp(() {
      gate = Completer();
      natives = ScriptedActions((_, _) => gate.future);
    });
    final slow = graph([native('wait', 'slow')]);

    test(
      'parallel starts every trigger; restart cancels the run in progress',
      () async {
        final h = host();
        const parallel = RunPolicy(ConcurrencyPolicy.parallel);
        final a = h.start(
          slow,
          roots: () => const {},
          key: 'k',
          policy: parallel,
        );
        final b = h.start(
          slow,
          roots: () => const {},
          key: 'k',
          policy: parallel,
        );
        await pumpEventQueue();
        expect(h.running, 2);
        const restart = RunPolicy(ConcurrencyPolicy.restart);
        final c = h.start(
          slow,
          roots: () => const {},
          key: 'k',
          policy: restart,
        );
        expect((await a)?.outcome, RunOutcome.cancelled);
        expect((await b)?.outcome, RunOutcome.cancelled);
        gate.complete(null);
        expect((await c)?.outcome, RunOutcome.ok);
      },
    );

    test(
      'queue runs triggers in order, bounded by action.queueLength',
      () async {
        final h = host();
        const queue = RunPolicy(ConcurrencyPolicy.queue);
        final order = <int>[];
        Future<void> go(int n) async {
          final r = await h.start(
            slow,
            roots: () => const {},
            key: 'k',
            policy: queue,
          );
          if (r != null) order.add(n);
        }

        final runs = [for (var n = 0; n < 4; n++) go(n)];
        await pumpEventQueue();
        expect(reports.single.code, PluxErrorCode.actionQueueFull);
        gate.complete(null);
        await Future.wait(runs);
        expect(order, [
          0,
          1,
          2,
        ], reason: 'the fourth trigger overflowed the queue of 2');
      },
    );

    test(
      'debounce runs the newest trigger once quiet; throttle one per interval',
      () async {
        natives = ScriptedActions((_, _) => null);
        final h = host();
        const debounce = RunPolicy(
          ConcurrencyPolicy.debounce,
          Duration(milliseconds: 300),
        );
        final seen = <Object?>[];
        final g = graph([
          GraphStep(
            id: 'c',
            action: id('condition'),
            inputs: {
              input('condition', 'when'): (roots) {
                seen.add(roots['event']);
                return true;
              },
            },
          ),
        ]);
        final first = h.start(
          g,
          roots: () => const {},
          key: 'k',
          event: 1,
          policy: debounce,
        );
        await clock.advance(const Duration(milliseconds: 200));
        final second = h.start(
          g,
          roots: () => const {},
          key: 'k',
          event: 2,
          policy: debounce,
        );
        await clock.advance(const Duration(milliseconds: 299));
        expect(seen, isEmpty);
        await clock.advance(const Duration(milliseconds: 1));
        expect(await first, isNull);
        expect((await second)?.outcome, RunOutcome.ok);
        expect(seen, [2]);

        const throttle = RunPolicy(
          ConcurrencyPolicy.throttle,
          Duration(milliseconds: 100),
        );
        expect(
          await h.start(g, roots: () => const {}, key: 't', policy: throttle),
          isNotNull,
        );
        clock.t += const Duration(milliseconds: 50);
        expect(
          await h.start(g, roots: () => const {}, key: 't', policy: throttle),
          isNull,
        );
        clock.t += const Duration(milliseconds: 50);
        expect(
          await h.start(g, roots: () => const {}, key: 't', policy: throttle),
          isNotNull,
        );
      },
    );
  });

  test('a P4 bundle\'s absent policy, encoded as parallel, runs as the trigger\'s default [ACT-003]', () {
    fbs.Handler h(fbs.Concurrency c) => fbs.Handler(
      fbs.HandlerObjectBuilder(concurrency: c, intervalMs: 300).toBytes(),
    );
    final old = RunPolicy.of(
      h(fbs.Concurrency.Parallel),
      honoursParallel: false,
    );
    expect(old.kind, ConcurrencyPolicy.drop);
    final lifecycle = RunPolicy.of(
      h(fbs.Concurrency.Parallel),
      honoursParallel: false,
      absent: ConcurrencyPolicy.queue,
    );
    expect(lifecycle.kind, ConcurrencyPolicy.queue);
    expect(
      RunPolicy.of(h(fbs.Concurrency.Parallel), honoursParallel: true).kind,
      ConcurrencyPolicy.parallel,
    );
    final debounce = RunPolicy.of(
      h(fbs.Concurrency.Debounce),
      honoursParallel: true,
    );
    expect(
      (debounce.kind, debounce.interval),
      (ConcurrencyPolicy.debounce, const Duration(milliseconds: 300)),
    );
  });

  test(
    'disposing the owner cancels its runs but not detached ones [ACT-004]',
    () async {
      final gate = Completer<Object?>();
      natives = ScriptedActions((_, _) => gate.future);
      final h = host();
      final g = graph([native('wait', 'slow')]);
      final owned = h.start(g, roots: () => const {}, key: 'a');
      final detached = h.start(
        g,
        roots: () => const {},
        key: 'b',
        detached: true,
      );
      await pumpEventQueue();
      h.dispose();
      gate.complete('done');
      expect((await owned)?.outcome, RunOutcome.cancelled);
      expect((await detached)?.outcome, RunOutcome.ok);
    },
  );

  group('bounds [ACT-005]', () {
    test('forEach runs its body per item with item and index', () async {
      final seen = <String>[];
      final r = await host().start(
        graph([
          GraphStep(
            id: 'loop',
            action: id('forEach'),
            inputs: {
              input('forEach', 'items'): lit(['a', 'b']),
            },
            branches: const {'body': 1},
          ),
          GraphStep(
            id: 'body',
            action: id('condition'),
            inputs: {
              input('condition', 'when'): (roots) {
                seen.add('${roots['index']}:${roots['item']}');
                return true;
              },
            },
          ),
        ]),
        roots: () => const {},
        key: 'k',
      );
      expect(r?.outcome, RunOutcome.ok);
      expect(seen, ['0:a', '1:b']);
      expect(r?.steps, 3);
    });

    test('forEach refuses more items than action.forEachItems', () async {
      final r =
          await host(
            limits: const ActionLimits(
              stepsPerRun: 100,
              stepTimeout: Duration(seconds: 5),
              runTimeout: Duration(seconds: 10),
              forEachItemsLimit: 1,
            ),
          ).start(
            graph([
              GraphStep(
                id: 'loop',
                action: id('forEach'),
                inputs: {
                  input('forEach', 'items'): lit([1, 2]),
                },
              ),
            ]),
            roots: () => const {},
            key: 'k',
          );
      expect(r?.error?.code, PluxErrorCode.actionForeachLimitExceeded);
    });

    test(
      'the step limit ends the run inside a loop whatever onError says',
      () async {
        final r =
            await host(
              limits: const ActionLimits(
                stepsPerRun: 5,
                stepTimeout: Duration(seconds: 5),
                runTimeout: Duration(seconds: 10),
              ),
            ).start(
              graph([
                GraphStep(
                  id: 'loop',
                  action: id('forEach'),
                  inputs: {input('forEach', 'items'): lit(List.filled(10, 0))},
                  branches: const {'body': 1},
                  onError: 2,
                ),
                GraphStep(
                  id: 'body',
                  action: id('condition'),
                  inputs: {input('condition', 'when'): lit(true)},
                ),
                GraphStep(id: 'handled', action: id('sync')),
              ]),
              roots: () => const {},
              key: 'k',
            );
        expect(r?.error?.code, PluxErrorCode.actionStepLimitExceeded);
        expect(syncs, 0);
      },
    );
  });

  group('retries [ACT-006]', () {
    test(
      'a retryable failure is retried with exponential backoff and jitter',
      () async {
        natives = ScriptedActions(
          (_, call) => call < 3
              ? throw const ActionError(
                  ActionErrorKind.network,
                  PluxErrorCode.actionCustomError,
                  'down',
                )
              : 'ok',
        );
        final traces = TraceBuffer(capacity: 5, values: true);
        final r = await host(traces: traces).start(
          graph([
            native(
              'call',
              'flaky',
              retry: const RetryPolicy(
                count: 3,
                backoffMs: 100,
                maxBackoffMs: 150,
                jitter: true,
              ),
            ),
          ]),
          roots: () => const {},
          key: 'k',
        );
        expect(r?.outcome, RunOutcome.ok);
        expect(r?.steps, 3, reason: 'each attempt counts as a step');
        // 100 then 200 capped at 150, each scaled by jitter 0.5 → ¾.
        expect(clock.sleeps, const [
          Duration(milliseconds: 75),
          Duration(milliseconds: 113),
        ]);
        expect(traces.runs.single.steps.single.attempts, 3);
      },
    );

    test(
      'errors outside the filter, and attempts beyond the count, fail the step',
      () async {
        natives = ScriptedActions(
          (_, _) => throw const ActionError.validation('bad'),
        );
        final r = await host().start(
          graph([native('call', 'bad', retry: const RetryPolicy(count: 3))]),
          roots: () => const {},
          key: 'k',
        );
        expect(r?.error?.kind, ActionErrorKind.validation);
        expect((natives as ScriptedActions).calls, hasLength(1));
        expect(
          const RetryPolicy(
            count: 1,
            on: {ActionErrorKind.http},
          ).retries(ActionErrorKind.http),
          isTrue,
        );
        expect(
          const RetryPolicy(
            count: 1,
            on: {ActionErrorKind.cancelled},
          ).retries(ActionErrorKind.cancelled),
          isFalse,
        );
      },
    );
  });

  group('optimistic updates [ACT-007]', () {
    test('a step\'s optimistic changes are applied before it and rolled back when it fails', () async {
      state.values['page.liked'] = false;
      final r = await host().start(
        graph([
          GraphStep(
            id: 'save',
            action: id('apiCall'),
            inputs: {
              input('apiCall', 'operation'): lit('like'),
              input('apiCall', 'optimistic'): lit({'page.liked': true}),
            },
            onError: 1,
          ),
          GraphStep(id: 'after', action: id('sync')),
        ]),
        roots: () => const {},
        key: 'k',
      );
      expect(r?.outcome, RunOutcome.ok);
      expect(state.writes, ['page.liked=true', 'page.liked=false']);
    });

    test(
      'the run\'s log rolls back newest first, and commits keep changes',
      () {
        final log = OptimisticLog(state)
          ..apply('a', 1)
          ..apply('a', 2);
        log.rollback();
        expect(state.values['a'], isNull);
        log
          ..apply('b', 1)
          ..commit()
          ..rollback();
        expect(state.values['b'], 1);
      },
    );
  });

  group('errors [ACT-020, RT-021]', () {
    final failing = graph([
      GraphStep(
        id: 'boom',
        action: id('stop'),
        inputs: {input('stop', 'error'): lit('E1')},
      ),
    ]);

    test('an unhandled error goes to the error handlers, then the fallback message', () async {
      final shown = <ActionError>[];
      final h = host(presenter: shown.add);
      final routed = <ActionError>[];
      h.errors = (e) async {
        routed.add(e);
        return routed.length == 1;
      };
      await h.start(failing, roots: () => const {}, key: 'a');
      expect(shown, isEmpty, reason: 'the first error was handled');
      await h.start(failing, roots: () => const {}, key: 'b');
      expect(shown.single.code, PluxErrorCode.actionCustomError);
      await h.start(
        failing,
        roots: () => const {},
        key: 'c',
        errorHandler: true,
      );
      expect(
        routed,
        hasLength(2),
        reason: 'an error handler\'s failure is not routed again',
      );
      expect(fallbackMessage(shown.single), isNotEmpty);
    });

    test('typed errors carry their status and code to steps.<id>.error', () {
      const e = ActionError(
        ActionErrorKind.http,
        PluxErrorCode.actionCustomError,
        'm',
        status: 404,
        errorCode: 'X',
      );
      expect(e.toPxl(), {
        'kind': 'http',
        'message': 'm',
        'code': 'X',
        'status': 404,
      });
    });
  });

  group('flows [ACT-061]', () {
    test(
      'callFlow runs the flow with its input and returns its result',
      () async {
        flows = MapFlows({
          'tasks/double': ActionGraph(
            id: 'f',
            key: 'double',
            steps: [
              GraphStep(
                id: 'out',
                action: id('stop'),
                inputs: {
                  input('stop', 'result'): (roots) =>
                      ((roots['params']! as Map)['n']! as int) * 2,
                },
              ),
            ],
          ),
        });
        Object? output;
        final r = await host().start(
          graph([
            GraphStep(
              id: 'call',
              action: id('callFlow'),
              inputs: {
                input('callFlow', 'flow'): lit('tasks/double'),
                input('callFlow', 'input'): lit({'n': 21}),
              },
              next: 1,
            ),
            GraphStep(
              id: 'read',
              action: id('condition'),
              inputs: {
                input('condition', 'when'): (roots) {
                  output = ((roots['steps']! as Map)['call']! as Map)['output'];
                  return true;
                },
              },
            ),
          ]),
          roots: () => const {},
          key: 'k',
        );
        expect(r?.outcome, RunOutcome.ok);
        expect(output, 42);
      },
    );

    test('a missing flow fails the step with PLX-5007', () async {
      final r = await host().start(
        graph([
          GraphStep(
            id: 'call',
            action: id('callFlow'),
            inputs: {input('callFlow', 'flow'): lit('nope')},
          ),
        ]),
        roots: () => const {},
        key: 'k',
      );
      expect(r?.error?.code, PluxErrorCode.flowNotFound);
    });
  });

  group('control actions', () {
    test('switch takes the case\'s branch, else default [ACT-001]', () async {
      Future<int> pick(String v) async {
        await host().start(
          graph([
            GraphStep(
              id: 's',
              action: id('switch'),
              inputs: {
                input('switch', 'value'): lit(v),
                input('switch', 'cases'): lit(['a']),
              },
              branches: const {'a': 1, 'default': 2},
            ),
            GraphStep(id: 'x', action: id('sync')),
            GraphStep(
              id: 'y',
              action: id('trackEvent'),
              inputs: {input('trackEvent', 'name'): lit('other')},
            ),
          ]),
          roots: () => const {},
          key: 'k',
        );
        return syncs;
      }

      expect(await pick('a'), 1);
      await pick('b');
      expect(tracked.single.$1, 'other');
    });

    test('a run holds its release from start to end, even after its owner is gone, so a replaced release is not unmapped under it', () async {
      var held = 0;
      var taken = 0;
      void Function() lease() {
        held++;
        taken++;
        return () => held--;
      }

      final h = host(lease: lease);
      final run = h.start(
        graph([
          GraphStep(
            id: 'd',
            action: id('delay'),
            inputs: {input('delay', 'duration'): lit(const PxlDuration(250))},
          ),
        ]),
        roots: () => const {},
        key: 'ok',
      );
      // The owner goes away while the run is still in its step: the run
      // keeps its hold until it ends.
      h.dispose();
      await run;
      expect(taken, 1);
      expect(held, 0);
      await host(lease: lease).start(
        graph([
          GraphStep(
            id: 'x',
            action: id('callNative'),
            inputs: {input('callNative', 'action'): lit('missing')},
          ),
        ]),
        roots: () => const {},
        key: 'native',
      );
      expect(taken, 2);
      expect(held, 0);
    });

    test('delay waits on the clock; emitEvent reaches the component; sync starts a sync', () async {
      final r = await host(component: true).start(
        graph([
          GraphStep(
            id: 'd',
            action: id('delay'),
            inputs: {input('delay', 'duration'): lit(const PxlDuration(250))},
            next: 1,
          ),
          GraphStep(
            id: 'e',
            action: id('emitEvent'),
            inputs: {
              input('emitEvent', 'event'): lit('onPicked'),
              input('emitEvent', 'payload'): lit('x'),
            },
          ),
        ]),
        roots: () => const {},
        key: 'k',
      );
      expect(r?.outcome, RunOutcome.ok);
      expect(clock.sleeps, [const Duration(milliseconds: 250)]);
      expect(emitted, [('onPicked', 'x')]);
      final outside = await host().start(
        graph([
          GraphStep(
            id: 'e',
            action: id('emitEvent'),
            inputs: {input('emitEvent', 'event'): lit('onPicked')},
          ),
        ]),
        roots: () => const {},
        key: 'k',
      );
      expect(outside?.error?.kind, ActionErrorKind.validation);
    });

    test(
      'parallel joins its lanes, and the first error cancels the others',
      () async {
        final never = Completer<Object?>();
        natives = ScriptedActions(
          (name, _) => switch (name) {
            'fail' => throw const ActionError(
              ActionErrorKind.network,
              PluxErrorCode.actionCustomError,
              'x',
            ),
            'hang' => never.future,
            _ => 'ok',
          },
        );
        ActionGraph lanes(String second) => graph([
          GraphStep(
            id: 'par',
            action: id('parallel'),
            inputs: {
              input('parallel', 'lanes'): lit(['a', 'b']),
            },
            branches: const {'a': 1, 'b': 2},
            onError: 3,
          ),
          native('one', 'ok'),
          native('two', second),
          GraphStep(id: 'handled', action: id('sync')),
        ]);
        expect(
          (await host().start(
            lanes('ok'),
            roots: () => const {},
            key: 'k',
          ))?.outcome,
          RunOutcome.ok,
        );
        expect(syncs, 0);
        final failing = graph([
          GraphStep(
            id: 'par',
            action: id('parallel'),
            inputs: {
              input('parallel', 'lanes'): lit(['a', 'b']),
            },
            branches: const {'a': 1, 'b': 2},
            onError: 3,
          ),
          native('one', 'hang'),
          native('two', 'fail'),
          GraphStep(id: 'handled', action: id('sync')),
        ]);
        final r = await host().start(failing, roots: () => const {}, key: 'k');
        expect(r?.outcome, RunOutcome.ok);
        expect(
          syncs,
          1,
          reason: 'the parallel step failed and its onError ran',
        );
      },
    );
  });

  group('traces [ACT-030, ACT-031]', () {
    test(
      'a run records its steps; sensitive inputs and outputs are redacted',
      () async {
        final traces = TraceBuffer(capacity: 1, values: true);
        final h = host(traces: traces);
        final g = graph([
          GraphStep(
            id: 'secret',
            action: id('emitHostEvent'),
            inputs: {
              input('emitHostEvent', 'event'): lit('e'),
              input('emitHostEvent', 'payload'): lit({'pin': '1234'}),
            },
            redact: {input('emitHostEvent', 'payload')},
            next: 1,
          ),
          GraphStep(
            id: 'plain',
            action: id('condition'),
            inputs: {input('condition', 'when'): lit(true)},
          ),
        ]);
        await h.start(g, roots: () => const {}, key: 'a');
        await h.start(g, roots: () => const {}, key: 'b');
        final run = traces.runs.single;
        expect(run.status, PluxTraceStatus.ok);
        expect(run.steps.map((s) => s.id), ['secret', 'plain']);
        expect(run.steps[0].inputs, {
          'event': 'e',
          'payload': PluxStepTrace.redacted,
        });
        expect(run.steps[0].output, PluxStepTrace.redacted);
        expect(run.steps[1].inputs, {'when': true});
        await pumpEventQueue();
        expect(traces.listenable.value, hasLength(1));
        expect(
          traces.runs.single.runId,
          2,
          reason: 'the ring buffer keeps the newest',
        );
      },
    );

    test('release builds record no values at all [SCH-012]', () async {
      final traces = TraceBuffer(capacity: 5, values: false);
      await host(traces: traces).start(
        graph([
          GraphStep(
            id: 'c',
            action: id('condition'),
            inputs: {input('condition', 'when'): lit(true)},
          ),
        ]),
        roots: () => const {},
        key: 'k',
      );
      expect(traces.runs.single.steps.single.inputs, {
        'when': PluxStepTrace.redacted,
      });
    });
  });

  group('triggers [ACT-002]', () {
    final handler = _handler();

    test(
      'timers tick with their count; watchers and dispatched events fire',
      () async {
        final fired = <String>[];
        final watches = _Watches();
        final owner = OwnerTriggers(
          specs: [
            TriggerSpec(
              kind: TriggerKind.timer,
              name: 'poll',
              interval: const Duration(seconds: 1),
              repeat: true,
              handler: handler,
            ),
            TriggerSpec(
              kind: TriggerKind.timer,
              name: 'once',
              interval: const Duration(seconds: 2),
              handler: handler,
            ),
            TriggerSpec(
              kind: TriggerKind.stateChange,
              name: 'page.count',
              handler: handler,
            ),
            TriggerSpec(
              kind: TriggerKind.hostEvent,
              name: 'paid',
              handler: handler,
            ),
            TriggerSpec(kind: TriggerKind.appResume, handler: handler),
            TriggerSpec(kind: TriggerKind.error, handler: handler),
          ],
          fire: (s, p) => fired.add('${s.kind.name}:${s.name}:$p'),
          clock: clock,
          watches: watches,
        )..start();
        final hub = TriggerHub();
        final unregister = hub.register(owner);
        await clock.advance(const Duration(seconds: 3));
        watches.change('page.count', 7);
        hub
          ..hostEvent('paid', {'amount': 1})
          ..hostEvent('other', const {})
          ..appResumed();
        unregister();
        hub.appResumed();
        owner.dispose();
        await clock.advance(const Duration(seconds: 3));
        expect(fired, [
          'timer:poll:1',
          'timer:poll:2',
          'timer:once:1',
          'timer:poll:3',
          'stateChange:page.count:7',
          'hostEvent:paid:{amount: 1}',
          'appResume::null',
        ]);
        expect(owner.errorHandler?.kind, TriggerKind.error);
        expect(watches.cancelled, isTrue);
      },
    );
  });
}

/// A handler of trigger specs whose runs the test records itself.
fbs.Handler _handler() =>
    fbs.Handler(fbs.HandlerObjectBuilder(event: 0).toBytes());

final class _Watches implements StateWatchSource {
  final Map<String, void Function(Object?)> _on = {};
  bool cancelled = false;

  @override
  StateWatch watch(String path, void Function(Object? value) onChange) {
    _on[path] = onChange;
    return _Cancel(() => cancelled = true);
  }

  void change(String path, Object? v) => _on[path]?.call(v);
}

final class _Cancel implements StateWatch {
  _Cancel(this._f);

  final void Function() _f;

  @override
  void cancel() => _f();
}
