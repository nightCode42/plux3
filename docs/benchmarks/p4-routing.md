# P4 — Action engine and navigation

Results of Phase 4's benchmarks, with their method (DoD-4, `QA-007`). R2 adds the action
engine's cost per step, an early measurement of `NFR-011`; R11 adds the navigation path from
a tap to the first frame of a cached page (`NFR-002`).

## Engine cost per step (`NFR-011`, early; not gated)

`NFR-011` (`ACT-008`) asks for at most 20 µs of interpreter overhead per step, p95, on the
mid-tier reference device, not counting the step's own work. P5 gates it on that device. P4
records the method and a first number, so P5 does not start from a slow design
([ADR-0039](../adr/0039-action-engine-core.md), *Measurement*).

**Method.** `make bench-steps` runs `packages/plux_flutter/test/actions/step_bench_test.dart`.

- A graph of 50 steps of one kind runs 400 times, after 50 warm-up runs, on an `ActionHost`
  with a fake navigator.
- Inputs are literals, so the time is the engine's own: reading the inputs, choosing the
  handler, recording the output and choosing the next step.
- The figure is each run's time divided by its steps; p50 and p95 are taken over the runs.

The step kinds measured:

- `condition`, with a literal input;
- `emitHostEvent`, with a map input posted to a no-op sink;
- `condition` reading an earlier step's record through `steps`.

**Results** (2026-10-02, this repository's cloud container: Intel Xeon, `flutter test` on
the Dart VM with JIT; not the reference device, not AOT):

| Step | p50 (µs) | p95 (µs) |
|---|---:|---:|
| `condition` | 1.8 | 4.8 |
| `emitHostEvent` | 2.6 | 5.3 |
| `condition` reading `steps` | 1.6 | 3.5 |

**What changed on the way.** The first version awaited every step behind a timeout timer and
a cancellation hook. It measured 20 µs p50 and 31 µs p95 per `condition`. Handlers now
return their result at once when they do their work synchronously. A step only gets a timer
and a cancellation hook when its handler returns a future: a presented page, or a custom
action.

**Not measured here.** Evaluating a PXL input is the step's own work; the runtime
benchmark's bound pages measure PXL evaluation ([p3-runtime.md](p3-runtime.md)). Steps that
navigate are measured with the navigation path in R11.

## Navigation path (`NFR-002`)

`NFR-002` asks for at most 100 ms p95 from a tap to the first frame of a cached page of up to
300 nodes on the mid-tier reference device. P3 measured the open with runtime 0.1.0, whose
`Plux.open` pushed a `MaterialPageRoute` directly ([p3-runtime.md](p3-runtime.md)). Runtime
0.2.0 opens every page through the router: it resolves the app-wide route name, runs the
route's guards and pushes through the navigation delegate, and the page checks its
parameters against their declared types on entry (`NAV-001`, `NAV-003`, `NAV-009`,
[ADR-0040](../adr/0040-navigation-delegate-and-router-adapters.md)). The question for P4 is
what that path adds.

**Method.** The runtime benchmark's `open` scenario times `Plux.open` of the 300-node catalog
page, which has no guard, to the end of its first frame's rasterization; the `native`
scenario pushes the same 300 widgets written in Flutter, as a control
([test/bench/runtime](../../test/bench/runtime/README.md)). Both runtimes ran alternately in
this repository's cloud container, the machine of [p3-runtime.md](p3-runtime.md#machine):

```bash
make bench-runtime-ab BASE=8b9c249 SCENARIOS=open,native RUNS=10
```

`8b9c249` is the last commit before runtime 0.2.0. The gate fails a metric whose median is
more than 10% above the base's with better than 99% confidence.

**Results** (2026-10-03, ten runs per runtime; medians over the runs):

| Metric | 0.1.0 (ms) | 0.2.0 (ms) | Change | Gate |
|---|---:|---:|---:|---|
| `open_first_frame_ms` | 29.97 | 29.71 | −0.9% | pass |
| `open_first_frame_cold_ms` | 59.42 | 57.26 | −3.6% | pass |
| `page_frame_ui_ms` | 8.74 | 9.00 | +3.0% | pass |
| `page_frame_ui_cold_ms` | 40.94 | 38.29 | −6.5% | pass |
| `native_open_first_frame_ms` (control) | 25.19 | 25.39 | +0.8% | pass |
| `native_page_frame_ui_ms` (control) | 6.18 | 6.41 | +3.7% | pass |

Runtime 0.2.0's samples:

| Metric | Samples | p50 (ms) | p95 (ms) | Max (ms) |
|---|---:|---:|---:|---:|
| `open_first_frame_ms` | 100 | 30.53 | 41.93 | 48.56 |
| `open_first_frame_cold_ms` | 10 | 57.25 | 71.81 | 72.70 |
| `native_open_first_frame_ms` | 100 | 25.49 | 34.65 | 39.44 |

**What it shows.**

- The router adds nothing measurable. Every difference is within the spread of the control,
  which did not change between the runs.
- Resolving the route and running its guards finish before the first frame after the call;
  that frame still shows the page complete, which the benchmark checks on every open.
- Plux's own share is the difference from the control: about 4–5 ms at the median for a warm
  open on this machine. Most of the time to the first frame is Flutter building and
  rasterizing 300 widgets with no GPU.
- A guarded route adds its guard graph's steps, which the engine runs in microseconds each
  ([above](#engine-cost-per-step-nfr-011-early-not-gated)), plus the work the graph itself
  does.

`NFR-002` stays `WIP`. Its bound is set on the reference devices, which the maintainer
measures ([p3-runtime.md](p3-runtime.md#reference-devices)).
