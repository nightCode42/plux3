# Action Engine Reference

How the runtime runs action graphs: what starts a run, how steps execute, how errors and
bounds behave, and which actions this runtime runs. The design is
[ADR-0039](../adr/0039-action-engine-core.md), completed in P5 by
[ADR-0045](../adr/0045-action-engine-completion.md). The [action reference](actions.md) lists
every action with its inputs. The [document model](document-model.md#5-action-graphs) says
how a graph is written.

## 1. What runs

The runtime (`plux_flutter` 0.3.0) runs every action of the
[action reference](actions.md) that is tagged P4 or P5. Navigation, dialogs, sheets, tabs,
`callNative`, `condition`, `switch`, `stop`, `emitHostEvent` and `emitEvent` arrived in
P4 and P5's core; P5 adds state (`setState`, `patchState`, `resetState`), forms, data
(`apiCall`, `refreshData`, `subscribe`, `unsubscribe`), the database and key-value store,
animation control, device and feedback actions, `forEach`, `parallel`, `delay`,
`callFlow`, `logout`, `trackEvent` and `sync`. Device actions of an optional package run when the
host installs it ([host app guide](../guides/host-app.md)); without it the step fails with
`permission`. Actions of later phases (`invokeFunction`, `biometricAuth`,
`signTransaction`, `startPayment`, `setLocale`) fail their step with
`PLX-4010` (`custom`), which the step's `onError` can handle.

## 2. Runs

A **trigger** starts a run of one graph: widget events, such as a button's `onPressed`;
route guards, which run before a page is entered
([navigation](navigation.md#4-guards-nav-009)); native slot events; and (`ACT-002`) the
triggers pages, plugins and the app declare: lifecycle (pages
only), timers, state watchers over the state engine's change stream, app resume and pause,
push opening, host events sent with `Plux.sendEvent`, and data-source `onLoaded` and
`onFailed`. The app's and the plugins' triggers run from the activation of their release
until another replaces it; a page's while it is shown. A data-source trigger hears only
loads of its own plugin, and a page's only the source its scope names. An error no
`onError` handled goes to the page's error handler, then the plugin's, then the app's
(`ACT-020`); an error handler that fails passes the error on.

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

**Concurrency** (`ACT-003`). A handler's policy decides what a trigger does while the
handler's previous run is active: `parallel` starts another run, `drop` ignores the
trigger, `restart` cancels the active run and starts, `queue` runs after it in order (its
length a registry limit), `debounce` starts once the trigger has been quiet for its
milliseconds, and `throttle` starts at most once per its milliseconds. Without a policy, a
widget event and a timer `drop`, a state watcher `restart`s, and app lifecycle, push, host
and data-source events `queue`, so a double tap never submits twice and no event is lost.

**Retries** (`ACT-006`). A step's `retry` holds a count, a base and a maximum delay and the
retryable error kinds; the wait before each attempt is random up to
`min(maximum, base × 2ⁿ)`. With no kinds listed, `network` and `timeout` are retried; an
`http` status only when listed. A cancelled run stops retrying.

**Optimistic updates** (`ACT-007`). A mutating step may declare an `optimistic` state
change, applied before the step runs; if the step fails, each path still holding the
optimistic value gets its previous value back. A queued `offlineCapable` mutation keeps the
change until the outbox reports its result.

**Flows** (`ACT-061`). `callFlow` runs a plugin's named flow, or another plugin's exported
one, as a nested frame of the run: it shares the run's bounds, cancellation and trace, and
runs with its own plugin's state and capabilities.

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
| `custom` | `PLX-4205` | The host's custom action, native route or slot builder threw; the report names only the exception's type |
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

**Cancellation.** A run belongs to the page or component instance that started it, and is
cancelled when that is disposed: a component's runs end with the instance, even while its
page stays. Its pending step is abandoned and nothing after it runs. A `detached` run
outlives its owner, bounded by the run timeout (`ACT-004`).

**Component events.** Inside a component, `emitEvent` emits one of its declared events
(`SCH-030`): the instance's handler for that event runs in the page, or, for a component a
host shows with `PluxView`, `onEvent` receives a `PluxViewEvent` with the payload in its
JSON form.

## 5. Traces

Every run is traced (`ACT-030`) into a ring buffer bounded by `action.traceRuns` and
`action.traceSteps`: its ID, trigger, steps with their start and end, status and errors,
which `plux_devtools` and tests read through `Plux.diagnostics`. Input and output values
are recorded only in debug builds and never when the compiler marked them sensitive
(`ACT-031`, `SCH-012`); everywhere else a trace holds `‹redacted›`. Each run is also a
timeline task in DevTools.

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

The engine's own cost per step is measured by `make bench-steps` (`NFR-011`,
[p4-routing.md](../benchmarks/p4-routing.md) and [p5-actions-data.md](../benchmarks/p5-actions-data.md));
`ACT-008`'s target is checked on the reference device. An action that completes synchronously runs
without timers or cancellation hooks; only a step waiting on a future gets them.
