# 0039. Action engine core in P4, the action catalogue in P5

- **Status:** Accepted (maintainer, 2026-10-02, at R1's review)
- **Date:** 2026-10-01; revised 2026-10-04 (see [Revision](#revision-2026-10-04-p5-completes-the-engine))
- **Requirements:** `RT-021`, `NAV-005`, `NAV-009`, `HST-013`, `ACT-060`, `LIM-001`; early parts of `ACT-001`–`ACT-005`, `ACT-020`, `NFR-011`

## Context and problem

The spec tags the action engine to P5: `ACT-001`–`ACT-008`, the triggers of `ACT-002`, and
almost every action in Appendix D. P4's navigation needs an engine first:

- `NAV-001` and `NAV-005` are actions run from widget events: `navigate`, `pop`,
  `openDialog`, `openBottomSheet` and `switchTab`;
- `NAV-002` and `ACT-060` add `callNative`;
- `NAV-009`'s guards are action graphs;
- `HST-013`'s plugin-to-host events are emitted by an action.

The compiler has produced the `actions` section since P1 (`graphs.go`): graphs of steps,
inputs compiled to PXL programs keyed by the action's permanent input IDs, `next`,
`onSuccess`, `onError` and named branches, plus retry and timeout fields. In P3 the
runtime wires every event, but each handler is a no-op that reports `PLX-4010` in debug
builds (ADR-0031, *What P3 does not do*).

The maintainer decided (P4 plan, D2):

- P4 builds the real core of the engine;
- it implements the six P4 actions, plus `condition` and `emitHostEvent`, which move to P4
  in spec 1.2.0, and `stop`, which moved at R1's review (below);
- every other action is refused with `PLX-4010`;
- P5 continues from this core.

This ADR fixes the core and the boundary, so that P5 adds actions and triggers without
rewriting the engine.

## Decision drivers

- **Errors never escape a run** (`RT-021`). A failing action, a bad input or a missing
  handler ends in `onError` or in a reported error. It never reaches host code or the
  frame.
- **Bounded.** Steps and time per run are limited by the limits registry (`LIM-001`),
  never by numbers written in code.
- **No half-implemented P5 feature** (AGENTS §5). Whatever P4 does not run is refused
  visibly, never run differently from its specification.
- **P5 extends, it does not rewrite.** Adding an action is adding a handler, and adding a
  trigger is adding a call to the run API.
- **Cheap per step.** `ACT-008` and `NFR-011` (≤ 20 µs per step, p95) are P5 gates. P4
  measures the core early, so P5 does not inherit a slow design.
- **UI isolate rules (L-7).** A run does no network I/O, decompression or large hashing on
  the UI isolate. P4's actions do none of these.

## Considered options

1. **The general engine core**, with handlers for the nine P4 actions and one refusing
   handler for every other action.
2. **A navigation-only interpreter in P4**, replaced by the general engine in P5.
3. **The whole engine and catalogue in P4.**

## Decision

Chosen option: **1, the general engine core.**

- Option 2 builds the same run loop twice, and P5 would have to migrate guards and
  `callNative` onto the new engine.
- Option 3 pulls state, data, forms and device actions into P4. Each of those depends on
  P5 features that P4 does not have.

### Where

The engine lives in `packages/plux_flutter/lib/src/actions/`:

- `engine.dart`: `ActionHost`, the engine of one page or view, which starts and owns runs;
- `run.dart`: `ActionRun`, which executes one run, and the bounds it reads;
- `graph.dart`: graphs decoded once from the actions section, with each step's inputs as
  readers over the run's roots;
- `handlers.dart`: one handler per action this runtime runs, and the refusing handler;
- `action_error.dart`: the typed errors.

Actions are dispatched by permanent ID through `registry.g.dart`, which `make gen` writes
from `schema/actions/*.json` and which carries each action's input IDs and its phase. An
action whose phase is later than the runtime's gets the refusing handler. A test checks that
the actions with a handler are exactly those Appendix D tags up to P4, so the handler table
and Appendix D cannot drift apart.

R2 revised this section: a separate generated `dispatch.g.dart` would have repeated what
`registry.g.dart` holds, so the phase was added to the generated descriptors instead.

### Runs

A **trigger** starts a **run** of one graph. In P4 the triggers are:

- widget events, through `NodeContext.fire`, which replaces P3's no-op;
- native slot events (ADR-0023);
- guards, before a route is entered (ADR-0040);
- deep-link and push-payload openings, which run the target route's guards (ADR-0040).

The other triggers of `ACT-002` are P5's: lifecycle, state watchers, timers, push opening
as a trigger, host events into Plux, and data-source events.

A run executes steps from `steps[0]` and follows the compiled edges:

- `next`;
- `onSuccess`, or `next` when it is absent;
- `onError`;
- the named branches.

Each step's inputs are PXL programs, evaluated by the existing VM (`lib/src/pxl/vm.dart`)
over the run's scope:

- `params`;
- `app`, `plugin` and `page` state, which a run cannot change in P4;
- `event`, the trigger's payload;
- `steps.<id>.output` and `steps.<id>.error`;
- `flags`, `env`, `user` (including `user.authenticated`, ADR-0040), `device` and `now`.

These are the roots the compiler already types (`docs/reference/compiler.md` §3). A
handler's output is checked against its descriptor's output type before later steps can
read it. A value of the wrong type fails the step with a `validation` error, because the
compiler's types and the runtime's values must agree.

`condition` takes `then` or `else`. A run ends when a step has no successor, or when a
handler ends it: `pop` ends its run, because its page is gone.

### Errors (`RT-021`, early `ACT-020`)

A failing step routes to its own `onError` edge. When it has none, the run ends and the
error is reported through the runtime's diagnostics and the `error` telemetry event,
with the code, the graph and the step, and never a payload. The error kinds are the
bundle's `ErrorKind`. P4's actions fail with:

- `validation`: bad inputs or outputs;
- `cancelled`;
- `custom`: `PLX-4010`, a refused navigation, or an unregistered native action;
- `timeout`.

Page, plugin and app error handlers, and the themed message of `ACT-020`, are P5's. A
handler that throws is caught at the run boundary and reported in the same way.

### Cancellation and bounds (early `ACT-004`, `ACT-005`)

- **Cancellation.** A run belongs to the page or component that started it, and is
  cancelled when that page or component is disposed. Its pending step completes as
  `cancelled` and nothing after it runs. `detached` runs are P5's.
- **Bounds.** Steps per run, step time and run time are bounded by the registry entries
  `action.stepsPerRun`, `action.stepTimeout` and `action.runTimeout`. A step's own
  `timeout_ms` can shorten its time, but never lengthen it.
  - Time spent waiting for the user is not the engine's work: while an `openDialog` or
    `openBottomSheet` step shows its page, neither its step time nor the run's time
    runs, so a dialog left open does not fail its run.
  - These entries exist today and are tagged P5. R2 re-tags them P4 together with the
    engine that reads them.
  - `action.forEachItems` stays P5's, with `forEach`.

### What P4 refuses (the P5 boundary)

| Declared in a graph | P4 runtime | P5 |
|---|---|---|
| An action other than the nine | The step fails with `PLX-4010` (`custom`). Its `onError` can handle it, and the run otherwise ends safely. | Each action replaces its refusing handler. |
| A handler's concurrency policy | Every trigger runs as `drop`: a trigger is ignored while the handler's previous run is still active. This is the spec's default for taps, so a double tap cannot push a page twice. A declared `restart`, `queue`, `debounce` or `throttle` reports `PLX-4010` once per handler in debug builds. The compiler encodes an absent policy as `parallel` (P1), so a declared `parallel` cannot be told apart from no policy, and it also runs as `drop`. | All six policies of `ACT-003`, and the encoding of an absent policy (`drop` for taps). |
| A step's `retry` | The step runs once, and its declaration reports `PLX-4010` once per step in debug builds. | `ACT-006`. |
| `detached` | The run is cancelled with its page, and this reports `PLX-4010` once in debug builds. | `ACT-004`. |

`PLX-4010`'s registered cause changes in R2 from "actions arrive in phase 5" to "this
action or option is not available in this runtime". Its code and reason are permanent
(ADR-0018).

### Handlers

One `ActionHandler` per descriptor:

- `navigate`, `pop`, `openDialog`, `openBottomSheet` and `switchTab` call the navigation
  delegate (ADR-0040);
- `callNative` calls the host's registered action and checks its input and output against
  the native catalogue's types (ADR-0041);
- `condition` evaluates its boolean;
- `emitHostEvent` posts a typed event to `Plux.events` (ADR-0023), in the JSON form of its
  fields; the compiler checked the payload against the app document's `hostEvents`
  declaration, which spec 1.2.0 adds for `HST-013`;
- `stop` ends the run with its result, or fails it with a custom error (below).

A handler gets its inputs in PXL form and a context: the page's navigation, the host-event
sink and the host's custom actions. It returns its result at once when its work is
synchronous, or a future when it waits, as a presented page or a custom action does; only a
future is bounded by a timer and raced against cancellation. It fails with a typed error.
Handlers hold no state between runs.

### Telemetry

Each run emits the action-run events of Appendix G (graph, trigger, steps, outcome and
duration) for the actions P4 runs, behind analytics consent (ADR-0034). Inputs, outputs
and payloads are never recorded.

### Measurement (early `NFR-011`)

R2 adds a micro-benchmark per step kind: an empty step, `condition`, and a step with a
three-operation input. Its numbers are recorded in `docs/benchmarks/p4-routing.md` and
not gated. P5 gates `NFR-011` on the reference device with the same method.

### How a graph returns its result: `stop` (maintainer, 2026-10-02)

A guard graph declares the registry type `GuardResult` as its output (P4 plan §2.1, A14).
Appendix D has one action that ends a run with a result: `stop`, whose `result` input has
the enclosing graph's output type. Two options were put to the maintainer at R1's review:
move `stop` to P4, or special-case guard graphs so that the value of their last step is
their result. The maintainer chose the first, so spec 1.2.0 re-tags `stop` to P4 and no
second way of returning a value exists:

- the compiler binds `stop`'s `T` to the graph's `output`, and refuses a `result` in a
  graph that declares no output (`PLX-1106`);
- the runtime ends the run with the result, or, when `error` is set, fails it with a
  `custom` error carrying that code.

## Revision (2026-10-04, P5 completes the engine)

P5 completes the engine on this core, as this ADR intended; the design is
[ADR-0045](0045-action-engine-completion.md). Every row of *What P4 refuses* gets its P5
behaviour there: the rest of the catalogue as handlers, all six concurrency policies with
a default per trigger (the compiler stops encoding an absent policy as `parallel`, and a
`required_features` key keeps P4 runtimes from running such bundles differently),
`retry`, `detached` runs, the page, plugin and app error handlers with the themed message,
and every trigger of `ACT-002`. Flows, traces and the new action `emitEvent` (spec 1.3.0)
are added. The run loop, the handler interface, the bounds and the synchronous fast path
stay as described here.

## Consequences

- **Positive.**
  - P4's navigation, guards, native actions and host events run on the engine that P5
    completes.
  - Every non-P4 action fails visibly and safely.
  - The P4/P5 boundary is one generated table and one refusing handler.
- **Negative.**
  - Graphs written for P5 actions compile in P4, but their steps fail at run time with
    `PLX-4010`. A developer learns this from the action reference page, which marks what
    runs in P4 (R11), and from debug-build diagnostics.
  - Concurrency is `drop` for every handler until P5.
- **Follow-up.**
  - R2 builds the engine.
  - R4 enforces the guard graph's output type.
  - P5 adds the rest of the catalogue and the triggers (plan §9.1).

## Options in detail

### Option 2: a navigation-only interpreter

It is quicker to write for six actions. Guards and `callNative` would have their own
code paths, which P5 replaces. Two error models would coexist for one phase, and P5's
work would grow by a migration.

### Option 3: the whole catalogue in P4

State writes need P5's state engine. Data actions need data sources. Form actions need
forms. Device actions need P5's permissions model. Pulling them in would move most of P5
into P4, against the plan's scope (D1, D2).
