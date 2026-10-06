<!--
SPDX-FileCopyrightText: 2026 Plux contributors
SPDX-License-Identifier: Apache-2.0
-->

# P5 — Actions, state, data and the local database

Results of Phase 5's benchmarks, with their method (DoD-4, `QA-007`). The targets are set
for the mid-tier reference device, which CI does not have: the requirements stay `WIP` until
the maintainer runs them there (plan p5 D3). Every number below comes from this
repository's cloud container (Intel Xeon @ 2.80 GHz, 4 cores; 2026-10-06), so it says how
the code behaves, not whether a phone meets the target.

| Requirement | Target (reference device) | Command |
|---|---|---|
| `ACT-008`, `NFR-011` | ≤ 20 µs interpreter overhead per step, p95 | `make bench-steps` |
| `PXL-004`, `NFR-010` | ≤ 2 µs per typical binding (≤ 20 operations), p95 | `make bench-pxl` |
| `NFR-004` | ≤ 1% janky frames at 60 Hz and no frame over 32 ms while a 1,000-item list scrolls | `make bench-runtime SCENARIOS=list` |
| `DB-007` | A watched query over 1,000 rows delivers a one-row change within 16 ms | `make bench-db` |

## Engine cost per step (`NFR-011`)

**Method.** As in [P4](p4-routing.md#engine-cost-per-step-nfr-011-early-not-gated): a graph
of 50 steps of one kind runs 400 times after 50 warm-up runs, with literal inputs, under
`flutter test` (Dart VM, JIT).

| Step | p50 (µs) | p95 (µs) | P4 p95 (µs) |
|---|---:|---:|---:|
| `condition` | 4.7 | 10.4 | 4.8 |
| `emitHostEvent` | 4.0 | 7.0 | 5.3 |
| `condition` reading `steps` | 3.0 | 7.8 | 3.5 |

The engine now does more per step than P4's core did: concurrency bookkeeping, the
cancellation token, trace recording into the ring buffer and the optimistic-update record.
That roughly doubles the p95, which stays under half of the target on JIT. AOT on a device
is typically faster than JIT here, but only the reference-device run settles it.

## PXL binding evaluation (`NFR-010`)

**Method.** `make bench-pxl` runs `packages/plux_flutter/test/pxl/pxl_bench_test.dart`. It
evaluates typical bindings, compiled by the real compiler and taken from the PXL
conformance vectors (`schema/testdata`), many times each after warm-up, on the Dart VM
(JIT); p50 and p95 are per evaluation.

| Binding | p50 (µs) | p95 (µs) |
|---|---:|---:|
| String concatenation with field reads | 2.32 | 7.44 |
| Arithmetic with a comparison | 0.44 | 1.57 |
| Conditional with a field read | 0.57 | 1.02 |
| Null-coalescing field read | 0.22 | 0.56 |
| `len` | 0.37 | 1.45 |
| `format.iban` | 0.08 | 0.14 |

Five of the six are within 2 µs at p95 on JIT. String concatenation allocates a new string
per evaluation and is the one to watch on the device.

## A 1,000-item list (`NFR-004`)

**Method.** The runtime benchmark's `list` scenario opens a page of 1,000 rows bound through
PXL, from the benchmark project's third plugin, and scrolls it end to end. It records every
frame's build and raster time, the share of frames over the display's frame budget
(`list_janky_pct`) and the slowest frame (`list_max_frame_ms`). The rows come from page
state, like the feed scenario's, and not from a paginated data source as plan §7 proposed:
the A/B gate builds the same benchmark app against the base branch's runtime, and `main`'s
P4 runtime has no data sources. The list is what `NFR-004` measures; how it is fed does not
change the frames.

| Metric | Value |
|---|---:|
| Build, p95 | 3.3 ms |
| Raster, p95 | 18.4 ms |
| Slowest frame | 23.2 ms |
| Janky frames | 12.5% |
| Frames | 609 |

Profile build, Linux, under `xvfb` with software rendering (llvmpipe). Raster is bound by the
software rasterizer, so the janky share says nothing about a phone's GPU; the build time,
which is Plux's part, is a fifth of the frame budget. No frame passed 32 ms. The scenario
also runs in CI's "native control, scroll and list" shard, A/B against the base.

## Watched query (`DB-007`)

**Method.** `make bench-db` runs `packages/plux_db_drift/test/watch_bench_test.dart`. It
inserts 1,000 rows into a `plux_db_drift` collection, watches a query over all of them,
changes one row 200 times, and times each change from the write to the watch delivering the
updated list. SQL runs on the adapter's own isolate, off the UI isolate.

| p50 | p95 |
|---:|---:|
| 8.4 ms | 12.7 ms |

Within the 16 ms target at p95 on this machine.

## Not measured here

The reference-device run (D3) for all four targets, and an A/B comparison of the `list`
scenario against `main`, which CI's benchmark shards run on the pull request.
