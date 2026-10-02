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
