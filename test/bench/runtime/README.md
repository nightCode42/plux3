# Runtime benchmark

The runtime half of `QA-007`: a Flutter app that starts Plux from an embedded, signed
release of fifty plugins and measures what a user waits for, in profile mode. The method
and the reference numbers are in [p3-runtime.md](../../../docs/benchmarks/p3-runtime.md).

## What one run measures

One run is one process ([lib/src/bench.dart](lib/src/bench.dart)). The server address is
the loopback discard port, so every run renders what is on the device, as an app start
without a network does.

| Metric | What | Requirement |
|---|---|---|
| `initialize_first_ms` | `Plux.initialize` with the cached release, first in the process | `NFR-001` |
| `memory_overhead_mib` | resident memory that first `Plux.initialize` adds, fifty plugins installed, before any page is drawn | `NFR-008` |
| `initialize_ms` | `Plux.initialize` again after `Plux.dispose`, ten times | `NFR-001` |
| `open_first_frame_cold_ms` | `Plux.open` of the 300-node catalog page to the end of its first frame's rasterization, first in the process | `NFR-002` |
| `open_first_frame_ms` | the same, ten more times | `NFR-002` |
| `page_frame_ui_ms`, `page_frame_ui_cold_ms` | the UI-thread time of that first frame — build, layout and paint of the page — warm, and for the cold open | `NFR-003` |
| `native_open_first_frame_ms`, `native_page_frame_ui_ms` | the same two for the catalog page written directly in Flutter (`NativeCatalog`, the same 300 widgets): what Flutter alone costs, so Plux's share can be told apart | control |
| `scroll_build_ms`, `scroll_raster_ms` | every frame while the 500-item feed, bound through PXL, scrolls 20,000 px in 4 s | frame times |
| `scroll_janky_pct` | frames whose build or raster missed the display's frame budget | frame times |

The embedded release in [assets/plux](assets/plux) is compiled from
`backend/internal/benchproject` and signed with a benchmark-only key;
`go test ./internal/benchproject -run TestRuntimeBaseline -update` (in `backend/`)
rewrites it.

## Running it

The platform folders are not committed: `bench.sh` makes the Linux one with
`flutter create` when it is missing. On Linux with the GTK development packages and
`xvfb` installed:

```bash
make bench-runtime                      # five runs of this checkout, summarised
make bench-runtime-ab BASE=origin/main  # A/B against BASE's runtime; fails beyond 10%
```

Results, logs and each side's release store are written to `build/bench-runtime`;
`report.md` there is the summary. `make bench-runtime-ab` builds this checkout's app
twice — once against this checkout's `packages/` and once against those of `BASE`, in a
temporary worktree — and `tools/cmd/benchcmp` runs the two alternately. It fails when a
metric's median over runs is more than 10% above the base's and a one-sided Mann–Whitney
test puts that at better than 99% confidence. Timings on shared CI runners vary between
machines by more than 10%, so the gate never compares with committed numbers. A base
that predates the benchmark has no runtime that can run it; then the change is measured
alone and the report says there was no comparison.

On a reference device, create the platform folder and run the app in profile mode:

```bash
flutter create --platforms=android --project-name plux_bench_runtime --org dev.plux .
flutter run --profile > device.log      # prints PLUX_BENCH lines, then exits
(cd ../../../tools && go run ./cmd/benchcmp report ../test/bench/runtime/device.log)
```

The sync benchmark's device half, [test/sync_test.dart](test/sync_test.dart), lives
here too; `make bench-sync` runs it (see p3-runtime.md).
