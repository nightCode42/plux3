# Action Engine Reference

How the runtime runs action graphs: what starts a run, how steps execute, how errors and
bounds behave, and which actions this runtime runs. The design, and the boundary with P5,
is [ADR-0039](../adr/0039-action-engine-core.md). The [action reference](actions.md) lists
every action with its inputs. The [document model](document-model.md#5-action-graphs) says
how a graph is written.

## 1. What runs in P4

The P4 runtime (`plux_flutter` 0.2.0) runs these actions:

| Action | Does |
|---|---|
| `navigate` | Pushes (default), replaces, pops until or clears and pushes a route ([navigation](navigation.md)) |
| `pop` | Pops the page, returning its typed result |
| `openDialog`, `openBottomSheet` | Present a route; the step's output is the route's result |
| `switchTab` | Selects a tab of the enclosing shell |
| `callNative` | Calls a custom action of the host (registration arrives with [ADR-0041](../adr/0041-native-catalogue-and-host-builds.md); until then `PLX-4202`) |
| `condition` | Takes the `then` or `else` branch |
| `emitHostEvent` | Posts a typed event to `Plux.events` (`HST-013`) |
| `stop` | Ends the run with the graph's result, or fails it with a custom error |

Every other action fails its step with `PLX-4010` (`custom`). The step's `onError` can
handle that error; otherwise the run ends and the error is reported. P5 replaces each
refusing handler with the action's own.

## 2. Runs

A **trigger** starts a run of one graph. In P4 the triggers are widget events, such as a
button's `onPressed`, and route guards, which run before a page is entered
([navigation](navigation.md#4-guards-nav-009)). Native slot events start runs from P4 R6;
lifecycle, timers, state watchers and host events into Plux arrive in P5 (`ACT-002`).

A run executes `steps[0]` first. After each step it follows:

1. the branch the step took, for `condition`; a branch that names no step ends the run;
2. otherwise `onSuccess` when it is set, else `next`;
3. on a failure, `onError`.

A step with no successor ends the run.

**Inputs** are evaluated when the step executes, over the roots of the page or component
that started the run:

- `params`, `page`, `props`, `component`, `item` and `index` as they apply;
- `flags`, `user` (including `user.authenticated`), `device` and `now`;
- `event`, the trigger's payload;
- `steps.<id>`, holding each step's `output` and `error` (`PluxActionError`: `kind` and
  `message`). A step that has not run reads as nulls.

**Concurrency.** A handler runs as `drop` in P4: a trigger is ignored while the same
handler's previous run is still in progress, so a double tap cannot navigate twice. P5 adds
the other policies (`ACT-003`).

## 3. Errors

A failing step routes to its own `onError` edge. Without one, the run ends: the error is
reported through the runtime's diagnostics, the `error` telemetry event and the host's
`onError`, with the graph, the step, the node, the route and the plugin. It never reaches
host code. Payloads are never reported.

| Kind | Code | When |
|---|---|---|
| `validation` | `PLX-5003` | An input binding fails, or a value does not have its declared type |
| `timeout` | `PLX-5001` | A step or the run takes longer than its bound (below) |
| `custom` | `PLX-4010` | The action, or an option of it, is not run by this runtime |
| `custom` | `PLX-5004` | `stop` sets an `error` |
| `custom` | `PLX-4102` | A navigation is refused: nothing to pop, no shell holds the tab |
| `custom` | `PLX-4200` | `navigate` names a native route the host has not registered |
| `custom` | `PLX-4202` | `callNative` names a custom action the host has not registered |
| `cancelled` | — | The page or component that started the run was disposed |

## 4. Bounds

Each run is bounded by entries of the limits registry (`LIM-001`, [limits](limits.md)). The
app bundle carries them, and an installation may set them:

| Limit | Default | Bounds |
|---|---|---|
| `action.stepsPerRun` | 10,000 | Steps one run executes; beyond it the run ends with `PLX-5002`, whatever `onError` says |
| `action.stepTimeout` | 30 s | One step; a step's own `timeoutMs` can shorten it, never lengthen it |
| `action.runTimeout` | 120 s | One run; beyond it the run ends with `PLX-5001` |

Time spent waiting for the user is not the engine's: while an `openDialog` or
`openBottomSheet` step shows its page, neither the step's time nor the run's time runs.

**Cancellation.** A run belongs to the page or component that started it, and is cancelled
when that is disposed. Its pending step is abandoned and nothing after it runs. Detached runs
arrive in P5 (`ACT-004`).

## 5. Options this runtime ignores

A graph may declare options that P5 delivers. The P4 runtime runs the graph without them,
and reports each once in debug builds with `PLX-4010`:

- a concurrency policy of `restart`, `queue`, `debounce` or `throttle`: it runs as `drop`. A
  declared `parallel` also runs as `drop`, and is not reported: the compiler encodes an
  absent policy as `parallel`, so the two cannot be told apart until P5;
- a step's `retry`: the step runs once (`ACT-006`);
- `detached`: the run is cancelled with its page.

## 6. Telemetry

Each run records `action_run` with these fields, under analytics consent
([telemetry](telemetry.md)):

- `graph_id`;
- `trigger`: `event` or `guard`;
- `duration_ms`;
- `result`: `ok`, `failed` or `cancelled`;
- `steps`;
- `failing_step`, when the run failed.

Inputs, outputs and payloads are never recorded.

## 7. Cost

The engine's own cost per step is measured by `make bench-steps`. It is an early
measurement of `NFR-011`, which P5 gates on the reference device
([p4-routing.md](../benchmarks/p4-routing.md)). An action that completes synchronously runs
without timers or cancellation hooks; only a step waiting on a future gets them.
