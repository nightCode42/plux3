# P1 Benchmarks — Compiler

Performance budgets of phase P1 (spec §30, DoD-4): compiling a 50-page plugin within 1 s (`CMP-050`) and validating one edited page within 50 ms at the 95th percentile (`SCH-042`, `NFR-033`).

## Method

- **Input.** The loan-calculator conformance project with 48 copies of its calculator page and the page's action graphs, each copy with its own IDs, keys and route: a plugin of 50 pages. Each copy is padded to 100 nodes with `Text` nodes that bind distinct PXL expressions, so no program is deduplicated (5,000 nodes, about 5,000 expressions). A 12-node variant is measured too. The input is built by `manyPages` in `backend/internal/compiler/bench_test.go`; `TestManyPagesCompile` checks that it compiles without diagnostics.
- **Compile.** `Compile` over an in-memory file system, release mode, default limits, no asset processing beyond the fixture's one image. The budget check takes the median of five runs after one warm-up run.
- **Validate.** `NewValidator` once, then `ValidatePage` on one page copy, unchanged; the budget check discards 20 warm-up runs and takes the 95th percentile of 180.
- **Commands.** `make go-budgets` (the assertions, run by CI on `ubuntu-latest`, the reference runner) and `go test -run '^$' -bench . -benchmem -count 3 ./internal/compiler` (in `backend/`).

## Results

Machine: 4 vCPU Intel Xeon @ 2.80 GHz, 16 GB RAM, Linux, Go 1.27.1, at commit following `c5a81c8` (P1 M9). Runs vary by about ±10% on this shared machine.

| Measurement | Result | Budget |
|---|---|---|
| Compile, 50 pages × 100 nodes (median) | 213 ms | 1 s (`CMP-050`) |
| Compile, 50 pages × 100 nodes (benchmark mean) | 219–228 ms, 162 MB and 1.9 M allocations per compile | — |
| Compile, 50 pages × 12 nodes (benchmark mean) | 62–70 ms | — |
| Validate one 100-node page (p50 / p95) | 6.2 ms / 9.5 ms | 50 ms p95 (`SCH-042`) |

## Findings

- A first measurement of `ValidatePage` re-checked the whole project: 61 ms for 12-node pages, over budget. Validation now checks only the edited page and its graphs, and does not build the node trees of other pages (their IDs, keys, routes and lifecycle graphs still take part): 36 ms → 12 ms for a 100-node page before the next change.
- The environment cache key of a scope was recomputed for every expression (14% of compile time); it is now computed once per scope.
- Remaining cost is spread over JSON Schema validation, PXL compilation and JSON decoding of prop values; allocation volume is the next target if the budgets tighten.
