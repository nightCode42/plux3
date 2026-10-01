# P3 Runtime Benchmarks

Measurements for the runtime targets of Phase 3 (spec §30.2, DoD-4) and the regression
gates of `QA-007`, taken on 2026-09-30, with the method needed to repeat them. The
device targets (`NFR-001`–`NFR-003`, `NFR-008`) are defined on reference phones
(spec §30.1), which neither CI nor this project's cloud sessions have. What is measured
here is the same benchmark on a Linux desktop build, which gates regressions and shows
where the time goes; the reference-device numbers are recorded by the maintainer (see
[Reference devices](#reference-devices)) and until then those requirements stay `WIP`.

## Machine

The development container: 4 vCPU (Intel Xeon, 2.8 GHz, x86-64), 16 GiB RAM, Linux 6.18,
Flutter 3.47.5, Go 1.27.1, PostgreSQL 16. Flutter's Linux embedder draws with Impeller
on OpenGL ES through Mesa's software rasterizer under `xvfb` — there is no GPU — so
raster times are the CPU's and far above a phone's GPU.

## Runtime benchmark

[test/bench/runtime](../../test/bench/runtime/README.md) is a Flutter app built in
profile mode that starts Plux from an embedded, signed release of the benchmark project
(`backend/internal/benchproject`): fifty plugins, one with a page of exactly 300 nodes
(`catalog`), one with a 500-item list bound through PXL (`feed`). The server address is
unreachable, so every run renders what is on the device. One run is one process:

| Metric | What |
|---|---|
| `initialize_first_ms`, `initialize_ms` | `Plux.initialize` with the cached release: first in the process, then ten times after `Plux.dispose` |
| `memory_overhead_mib` | resident memory the first `Plux.initialize` adds, fifty plugins installed |
| `open_first_frame_cold_ms`, `open_first_frame_ms` | `Plux.open` of the catalog page to the end of its first frame's rasterization, first in the process, then ten times |
| `page_frame_ui_cold_ms`, `page_frame_ui_ms` | that first frame's UI-thread time: build, layout and paint of the page |
| `native_open_first_frame_ms`, `native_page_frame_ui_ms` | the same for the catalog page written directly in Flutter — the same 300 widgets — ten times: what Flutter alone costs |
| `scroll_build_ms`, `scroll_raster_ms`, `scroll_janky_pct` | every frame while the feed scrolls 20,000 px in 4 s; janky frames miss the display's frame budget |

Frames are identified by time: a frame's timing is the one whose build ended just before
the post-frame callback that followed the push, on the clock `Timeline.now` reads (the
benchmark checks both clocks agree). The frame's own time stamp is not used, because
embedders set it differently: on Linux it is the vsync's target time, not the build's
start.

```bash
make bench-runtime RUNS=5
```

Five runs on 2026-09-30 (`build/bench-runtime/report.md`):

| Metric | Samples | p50 | p95 | Max |
|---|---:|---:|---:|---:|
| `initialize_first_ms` | 5 | 12.76 | 13.64 | 13.77 |
| `initialize_ms` | 50 | 10.55 | 12.45 | 14.11 |
| `memory_overhead_mib` | 5 | 12.22 | 13.36 | 13.64 |
| `open_first_frame_cold_ms` | 5 | 69.12 | 97.95 | 100.74 |
| `open_first_frame_ms` | 50 | 46.72 | 66.08 | 266.60 |
| `page_frame_ui_cold_ms` | 5 | 49.42 | 67.74 | 71.37 |
| `page_frame_ui_ms` | 50 | 12.80 | 20.49 | 22.68 |
| `native_open_first_frame_ms` | 50 | 45.56 | 63.33 | 75.74 |
| `native_page_frame_ui_ms` | 50 | 9.65 | 14.52 | 15.84 |
| `scroll_build_ms` | 639 | 3.24 | 5.86 | 16.73 |
| `scroll_raster_ms` | 639 | 8.55 | 26.34 | 148.82 |
| `scroll_janky_pct` | 5 | 13.85 | 46.13 | 53.57 |

The same benchmark on CI's `ubuntu-24.04` runner (run 36703654548, ten runs): `page_frame_ui_ms`
p50 5.58 ms against 4.20 ms native, `open_first_frame_ms` p50 21.8 ms, cold 36.3 ms,
`page_frame_ui_cold_ms` 28.3 ms, `scroll_build_ms` p95 0.98 ms, no janky frame. Runners
differ this much from one another, which is why the gate compares alternating runs on one
machine and never these numbers.

**What it shows.**

- `Plux.initialize` with fifty cached plugins takes about 11–13 ms at the median on this
  machine and adds about 12 MiB of resident memory, before any page is drawn. After pages
  are drawn, the process's size moves by tens of MiB with Flutter's rendering and the
  timing of garbage collection, far more than the runtime's own share, so the benchmark
  does not report it; the section cache is bounded by `runtime.sectionCacheBytes`.
- Warm, the catalog page's first frame costs about 12.8 ms of UI-thread time through
  Plux against 9.7 ms for the same widgets written in Flutter: Plux's share of that
  frame is about 3 ms here. `NFR-003`'s 8 ms is the page build alone on a
  mid-tier phone; this frame also lays out and paints, so it is an upper bound, and it
  says the native page by itself is near the budget on this CPU.
- Opening the page to its first rasterized frame takes about 47 ms warm — the native
  page, 46 ms: most of it is the software rasterizer — and 69 ms the first time in the
  process, when the bundle's sections are first checked and the page's glyphs first
  laid out. The benchmark's host shows a native screen with text before it opens a Plux
  page, as every host does: the first page a process draws costs 77–94 ms of UI-thread
  time on this machine whoever builds it (measured by opening the native page first),
  which is Flutter's first text layout, not Plux's.

## Regression gate (`QA-007`)

Timings on shared CI runners differ between machines by more than the 10% the gate
allows, so no committed timing is ever compared with a new one. `make bench-runtime-ab
BASE=<ref>` builds the same benchmark app twice — against this checkout's runtime and
against `BASE`'s, in a temporary worktree — and `tools/cmd/benchcmp` runs them
alternately (base, head, head, base, …) on the same machine, ten runs each after a
warm-up. For every metric it takes each run's median and fails when the change's median
over runs is more than 10% above the base's **and** a one-sided Mann–Whitney U test that
the change's run medians exceed 1.1 × the base's gives p < 0.01. Runs, not frames, are the
observations: the frames of one run share its machine's state.

The CI job *Runtime benchmark* runs it on every change to the runtime against the pull
request's base (on `main`, the previous head).

**Checked both ways.** The same build on both sides (an A/A run, 10 runs each, with other
builds competing for the CPU) passed every metric, with the smallest p-value 0.63. With
3 ms of busy work added to `Plux.initialize` and to the page host's build, 6 runs each
flagged `initialize_ms` (+30%, p = 0.0025) and `page_frame_ui_ms` (+36%, p = 0.0065) and
nothing else.

## Sync on the slow network (`NFR-006`, `NFR-007`)

**Network.** Spec §30.1: 750 kbit/s down, 250 kbit/s up, 300 ms round trip, 1% loss.
`backend/internal/netsim` puts it between the device and the server: each direction is a
link shared by all connections, which serialises bytes at its bandwidth and delivers
them half a round trip later; a new connection's first bytes wait a round trip more (the
TCP handshake). Loss is modelled as the throughput TCP sustains under it (Mathis et al.:
MSS/RTT × 1.22/√p), which caps the down link at 475 kbit/s and leaves the up link at
250 kbit/s. TLS is not modelled, so HTTPS adds its handshake and record overhead on top.

**Workload.** `TestSyncOnSlowNetwork` (`make bench-sync`) starts the server with both
roles, publishes the fifty-plugin benchmark project and promotes it; the real runtime,
under `flutter test` on the host, then syncs through the simulated network: a first
launch with nothing on the device, five up-to-date checks, and ten updates, each a new
revision that changes one text in three plugins. The proxy records every request with
its bytes on the wire, headers included. The test fails when an update takes more than
3 s at the 95th percentile, or when a phase costs more than 10% over the bytes committed
in `test/bench/runtime/sync-baseline.json`.

| Measure | Value |
|---|---:|
| First launch (registration, token, manifest, 51 full bundles, 2 icon fonts), bytes | 310,467 |
| Up-to-date check, requests | 2 |
| Up-to-date check, bytes (median) | 6,639 |
| Update of three plugins (token, manifest, 3 deltas), bytes (median) | 17,159 |
| Update of three plugins, p50 | 1,944 ms |
| Update of three plugins, p95 | 1,979 ms |

**`NFR-007` — met** on the simulated network: an update of three plugins syncs in
1,979 ms at the 95th percentile. It is dominated by round trips: the token, the
manifest (7.9 KB down for fifty plugins, 5.6 KB up) and three deltas of under 1 KB each,
fetched in parallel.

**`NFR-006` — not met as written:** the up-to-date check at app start is two requests and
6,639 bytes: a device token (`IssueDeviceToken`, 683 bytes) and the conditional manifest
request (`GetManifest`, 5,956 bytes, of which 5,627 are the request: the installed hash of
each of the 51 bundles, which `REL-032` requires). The answer itself is 329 bytes. See
the work log for the options put to the maintainer.

**Found and fixed by this benchmark.** The server's ETag covered the manifest and the
installed bundles the request named; a device stores the ETag of the response it synced
from, which was computed for the bundles it held *before*, so the first check after every
update never matched and downloaded the whole manifest again (7.6 KB for fifty plugins).
The ETag now names the manifest alone, and "not modified" needs the device to hold exactly
the manifest's bundles — so a device whose download failed still gets its plan
(`backend/internal/release/serve.go`, `TestManifestAndDeltas`).

## Size (`RT-061`, `NFR-009`)

**Target:** at most 3 MiB added to a host app's download per platform, against a blank
Flutter app. Measured in CI by `make size-android` (`ubuntu-latest`) and `make size-ios`
(`macos-latest`); [test/size](../../test/size/README.md) has the method. The
[size journey](size.md) records every measurement round; its round 1 is the P3 state:

| Build | Added by `plux_flutter` |
|---|---:|
| iOS IPA, arm64 | **2.35 MiB — met** |
| Android App Bundle download, arm64-v8a / armeabi-v7a / x86_64 | **2.45 / 2.69 / 2.47 MiB** |
| Android APK file, arm64-v8a / armeabi-v7a / x86_64 | **5.69 / 6.40 / 5.90 MiB** |

The APK stores the Dart AOT code uncompressed (5.4 MB on arm64, 2.2 MB compressed), so the
APK file exceeds 3 MiB while every App Bundle download meets it. The maintainer restates
the budget per build (3 MiB for the App Bundle download, a separate APK budget); until
`RT-061` is reworded the *Size (Android)* job gates the arm64 APK and stays red (work log,
open decisions).

## Reference devices

`NFR-001`–`NFR-003` and `NFR-008` are defined on the mid-tier and low-end Android phones
and the iOS reference of spec §30.1. The maintainer records them from the same app:

1. In `test/bench/runtime`, create the platform folder:
   `flutter create --platforms=android --project-name plux_bench_runtime --org dev.plux .`
   (or `ios`).
2. Run it in profile mode on the device, five times: `flutter run --profile > run-N.log`.
   Each run prints its samples as `PLUX_BENCH` lines and exits.
3. Summarise: `go run ./tools/cmd/benchcmp report ../test/bench/runtime/run-*.log`
   (from `tools/`), and record device, OS, date, commit and the p95 of each metric here.

| Reference device | `NFR-001` p95 | `NFR-002` p95 | `NFR-003` p95 | `NFR-008` | Date, commit |
|---|---|---|---|---|---|
| — | not yet measured | not yet measured | not yet measured | not yet measured | — |
