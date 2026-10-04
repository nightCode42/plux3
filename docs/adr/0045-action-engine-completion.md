# 0045. Action engine completion: triggers, policies, errors, flows and traces on the P4 core

- **Status:** Accepted (maintainer, 2026-10-04, P5 plan §2.1, B1–B2)
- **Date:** 2026-10-04
- **Requirements:** `ACT-001`–`ACT-008`, `ACT-020`, `ACT-030`, `ACT-031`, `ACT-061`, `NFR-011`, `RT-015`, `RT-021`, `HST-013`, `SCH-012`, `LIM-001`

## Context and problem

[ADR-0039](0039-action-engine-core.md) built the engine's core in P4: runs of compiled
graphs, PXL inputs over the run's roots, typed errors to a step's `onError`, cancellation
with the owning page, the step and time bounds, and handlers for nine actions. Everything
else is refused with `PLX-4010`:

- every other action in Appendix D;
- concurrency policies other than `drop`;
- `retry` and `detached`;
- the page, plugin and app error handlers, and the themed message of `ACT-020`;
- every trigger but widget events, slot events, guards and route openings.

P5 must deliver the rest of `ACT-*` (spec §14.1). The maintainer accepted the plan's
answers (P5 plan §2.1, B2), among them D11, a new action `emitEvent` for component events,
and D15, traces in a ring buffer while P10 brings streaming to Studio.

This ADR fixes how the engine is completed, so that the state, data, database, animation
and device work of P5 (ADRs 0046–0051) adds handlers to one engine.

## Decision drivers

- **Errors never escape a run** (`RT-021`), and every error reaches a handler or a report
  (`ACT-020`).
- **Bounded** in steps, items and time, by the limits registry (`LIM-001`), including
  everything P5 adds: retries, flows, `parallel` and detached runs.
- **Cheap per step:** ≤ 20 µs p95 (`ACT-008`, `NFR-011`); ADR-0039's synchronous fast path
  stays.
- **Sensitive values never leave the value they live in** (`ACT-031`, `SCH-012`).
- **Additive contracts** (`BND-000`, `SCH-000`): an older runtime refuses what it cannot run
  through `required_features` (`BND-008`), and never runs it differently.
- **One engine.** State watchers, data-source events, timers and host events start runs
  through the same API as a tap.

## Considered options

1. **Complete ADR-0039's interpreter in place**: new triggers call its run API, policies
   sit in front of it, each action becomes a handler.
2. **A separate engine per area**: data-source events, timers and watchers each run their
   graphs their own way.
3. **A third-party workflow or state-machine library.**

## Decision

Chosen option: **1, complete the interpreter in place.**

- Option 2 multiplies error routing, bounds, cancellation and tracing, and each copy must
  be proved separately.
- Option 3 brings a dependency into every host app for a run loop the runtime already has,
  and none of the libraries knows PXL, the bundle's graphs or the limits registry.

### Triggers (`ACT-002`)

Every trigger calls `ActionHost.start(graph, trigger, event)`; the owner of the trigger is
the owner of the run (cancellation, below).

| Trigger | Owner | Source |
|---|---|---|
| Widget events (the ten kinds of `ACT-002`) | The node's page or component | `NodeContext.fire`, as in P4 |
| Page lifecycle: `onInit`, `onEnter`, `onResume`, `onLeave`, `onDispose` | The page | The page route and its visibility |
| State watchers | The scope that declares them | The state engine ([ADR-0046](0046-state-engine.md)) |
| Timers, interval or one-shot | The page or the app | A timer started with its owner and cancelled with it |
| App lifecycle: resume, pause | The app | `AppLifecycleListener` |
| Push opening | The app | P4's push routing ([ADR-0040](0040-navigation-delegate-and-router-adapters.md)), which also fires the trigger |
| Host events into Plux (`HST-013`) | The app | `Plux.sendEvent`, typed by the app document's event declarations |
| Data-source events: `loaded`, `failed`, stream messages, outbox results | The source's scope | The data layer ([ADR-0048](0048-data-layer.md)) |

Page triggers are declared in the page schema since P1. The app document has no triggers
yet, so app-level watchers, timers, lifecycle, push and host-event triggers are an
additive, optional field of the app document (`SCH-000`), whose shape is fixed in R0's
schema change. An `onDispose` run is detached by definition (below), since its page is
gone.

### Concurrency (`ACT-003`)

A handler's policy decides what a trigger does while the handler's previous run is active:

- `parallel`: start another run;
- `drop`: ignore the trigger;
- `restart`: cancel the active run, then start;
- `queue`: run after the active run, in order; the queue's length is a registry limit;
- `debounce(ms)`: start once the trigger has been quiet for `ms`;
- `throttle(ms)`: start at most once per `ms`, on the leading edge.

The default depends on the trigger: `drop` for every widget event, so a double tap never
submits twice; `restart` for state watchers, so the latest value wins; `drop` for timers,
so a slow tick skips the next; `queue` for app lifecycle, push, host and data-source
events, so none is lost.

The compiler has encoded an absent policy as `parallel` since P1, which ADR-0039 left open.
From runtime 0.3.0 the compiler encodes an absent policy as "the trigger's default", and a
bundle that relies on a policy other than `drop` declares a `required_features` key, so a
P4 runtime, which runs every handler as `drop`, refuses it rather than running it
differently (`BND-008`).

### Cancellation and detached runs (`ACT-004`)

- Each run carries a cancellation token. A handler that waits — a request, a timer, a
  presented page, a custom action — races its work against the token; a cancelled step
  completes as `cancelled`, and nothing after it runs.
- A run is cancelled when its owner is disposed, when a `restart` policy restarts it, or
  when the host calls `Plux.dispose`.
- A `detached` run is owned by the app, not its page. It is still bounded by the per-run
  timeout, and it is cancelled by `Plux.dispose` and by `Plux.wipeData` of its plugin
  ([ADR-0049](0049-local-persistence.md)). A detached run cannot navigate or present a
  page, because its page may be gone; the compiler refuses those actions in a detached
  handler's graph.

### Bounds (`ACT-005`)

- Steps per run (`action.stepsPerRun`, 10,000), step time (`action.stepTimeout`, 30 s)
  and run time (`action.runTimeout`, 120 s) are the existing registry entries; a step's
  `timeout_ms` shortens its time, never lengthens it.
- `forEach` runs over at most `action.forEachItems` items (default 1,000); a longer list
  fails the step with a `validation` error before the first item.
- Steps of a called flow, every retry attempt and every `parallel` branch count against the
  same run's step budget and run time, so composition cannot escape the bounds.
- `delay` is a step like any other: its duration is bounded by the step time, and the
  compiler refuses a constant delay longer than the registry's step timeout.

### Retries (`ACT-006`)

A step's `retry` (compiled since P1) holds a count, a base delay, a maximum delay and the
retryable error kinds:

- the delay before attempt *n* is a random value between zero and
  `min(maximum, base × 2ⁿ)` ("full jitter");
- each attempt has the full step time; the waits count against the run time;
- with no kinds listed, `network` and `timeout` are retried; an `http` status is retried
  only when listed;
- a cancelled run stops retrying at once.

### Optimistic updates (`ACT-007`)

A mutating step (`apiCall`, a database write, a function call from P7) may declare an
`optimistic` state change. The engine applies it through the state engine before the step
runs and keeps an undo record of the previous values. If the step fails, each path whose
current value is still the optimistic one gets its previous value back; a path written
since by someone else keeps that newer value. An `offlineCapable` mutation queued in the
outbox keeps its optimistic change until the outbox reports success, failure or conflict
([ADR-0048](0048-data-layer.md)).

### Errors (`ACT-020`)

- The kinds are those of `ACT-020`: `network`, `http(status)`, `timeout`, `validation`,
  `function(code)`, `permission`, `cancelled` and `custom`; the bundle's `ErrorKind` gains
  any it lacks, additively.
- A failing step routes to its `onError`. Without one, the error goes to the page's error
  handler, then the plugin's, then the app's: each is a graph whose `event` is the error,
  and each may handle the error or rethrow it with `stop`. Page and plugin handlers are
  declared in their documents; the app's handler joins the app document's triggers
  (above).
- An error no handler takes shows a themed message on the page — the runtime's built-in
  strings until P8 brings localisation — and is reported through the diagnostics and the
  `error` telemetry event with its code, graph and step, never a payload.
- An error inside an error handler is reported, never routed again, so handlers cannot
  loop. `cancelled` is never shown to the user.

### Flows (`ACT-061`)

- A plugin declares named flows: graphs with typed inputs and outputs. A flow is private to
  its plugin unless the plugin exports it.
- `callFlow` runs a flow as a nested frame of the calling run: it shares the run's bounds,
  cancellation and trace, and its output is the step's output.
- A flow runs with its own plugin's state and capabilities. Only its declared inputs and
  output cross between plugins, and only through exports the release resolves.
- The compiler builds the call graph of a release and refuses a cycle, including one across
  plugins, so `ACT-001`'s acyclic graphs stay acyclic once flows are inlined.

### Control, feedback and component actions

- `switch` branches on a PXL value; `forEach` runs its body once per item, in order;
  `parallel` runs its branches concurrently, cancels the others when one fails, and joins
  their outputs; `delay` waits.
- `trackEvent` sends a custom telemetry event behind analytics consent
  ([ADR-0034](0034-runtime-telemetry.md)); the compiler refuses a payload that reads a
  `sensitive` value.
- `sync` starts a manual sync (`SYN-*`) and completes with its result.
- **`emitEvent`** (spec 1.3.0, Appendix D; plan D11): inside a component's graph, it emits
  one of the component's declared events (`SCH-030`). The compiler checks the event's name
  and its payload against the declaration; the instance's event handler in the enclosing
  page, or `PluxView.onEvent` for a component view ([ADR-0023](0023-mixed-screens-slots-and-plux-view.md)),
  receives it. Outside a component the step is a compile error.
- The state, form, data, database, animation and device actions are handlers of this
  engine, specified in ADRs 0046–0051.

### Traces (`ACT-030`, `ACT-031`, `RT-015`)

- Every run produces a trace: run ID, graph, trigger, each step with its start, end,
  status and error, and the run's outcome.
- **Values.** Debug builds record each step's inputs and output; release builds record
  their types and sizes only. A value whose type or field is tagged `sensitive` is
  replaced by a marker when it is recorded, so it is never held in a trace, in any build.
- **Where traces go (D15).** A bounded ring buffer per runtime (its size a registry
  limit), read by `plux_devtools` and exported by tests; the `action_run` telemetry event
  (Appendix G), sampled and behind consent, carries no values at all. Streaming to paired
  Studio sessions arrives with `DEV-020` in P10, so `ACT-030` stays `WIP` until then.
- Runs and steps are `TimelineTask`s named `plux.action` (ADR-0031), visible in DevTools.

### Performance (`ACT-008`, `NFR-011`)

The synchronous fast path stays: a step whose handler completes synchronously and needs no
timer costs no future and no timer. Policies, retries and tracing cost nothing for a
handler that does not use them. The step benchmark of P4 gains a policy step, a retried
step and a traced step, and `bench-steps` gates every one A/B in CI; the absolute target is
measured on the reference device (plan D3).

### Registries

New limits (queue length, trace buffer, nesting of flows), error codes in `PLX-5000`–`5999`
and the `required_features` keys are fixed by the milestone that needs them, in
`schema/limits.json`, `schema/errors.json` and the bundle's feature list. No number in this
ADR is a limit except those already in the registry.

## Consequences

- **Positive.**
  - Every trigger, policy, bound and error path is one engine's, tested once.
  - Composition (flows, `parallel`, retries) cannot exceed a run's bounds.
  - P4 runtimes refuse what they cannot run, through `required_features`.
- **Negative.**
  - The engine grows into the largest part of the runtime's Dart code, and its size is
    counted against `RT-061` at every milestone.
  - A cross-plugin flow couples releases of two plugins; the release compiler, not the
    plugin compiler, sees the whole call graph.
- **Follow-up.**
  - R1: triggers, policies, cancellation, bounds, retries, errors, flows, control actions,
    traces; `plux_flutter` 0.3.0.
  - R2–R8: the handlers of ADRs 0046–0051.
  - P10: trace streaming (`DEV-020`).

## Options in detail

### Option 2: an engine per area

Data sources would run their event graphs from the data layer, the state engine its
watchers, the timers their own. Each would need its own error chain, bounds, cancellation
and traces, and `ACT-020`'s single chain from step to app would not hold.

### Option 3: a workflow library

General workflow and state-machine packages model states and transitions, not typed graphs
of PXL inputs. Adapting the bundle's graphs to one would add a translation layer and a
dependency to every host app, and the per-step budget would be the library's, not ours.
