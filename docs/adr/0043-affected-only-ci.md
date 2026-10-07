# 0043. Affected-only CI: a pull request runs what it can affect, `main` runs everything

- **Status:** Accepted
- **Date:** 2026-10-01
- **Requirements:** `CI-002`, `CI-001`, `CI-009`, `QA-006`, `QA-007`

## Context and problem

Until P4, CI chose jobs per toolchain. A path filter (`dorny/paths-filter`) marked Go,
Dart, Studio and the API contract as changed, and a shared list (the `Makefile`, `ci.yml`,
`tools/**`, `schema/**`, `test/**`) marked all of them at once. Any change to a build file
therefore ran every heavy job. A change to one Go library ran every Go job, and a Dart
change ran the size jobs whether or not it touched the shipped runtime.

The full suite took about 16 minutes of wall clock. Main run 36886856869 (2026-10-01):

| Job | Minutes | Where the time went |
|---|---:|---|
| Runtime benchmark | 15.6 | 22 runs of 32 s (warm-up and ten of each side, alternating), 1.6 min of builds, 1.2 min of `apt-get` |
| Device end-to-end (iOS) | 8.8 | simulator boot, Go build, a 150 s Xcode build, then the flows, one after another |
| Device end-to-end (Android 26) | 8.3 | as Android 35 |
| Sync benchmark | 7.9 | the benchmark itself, on the simulated slow network |
| Device end-to-end (Android 35) | 7.8 | 1.5 min of emulator boot, 1.2 min of Go build, 3.9 min of cold Gradle build, 12 s of flows, one after another |
| Size (Android) | 7.3 | six release builds |
| Compose stack | 5.3 | image build and the integration tests |
| Go test | 5.3 | tests with the race detector |
| Go lint | 3.7 | 2.6 min of it building `golangci-lint`, `buf`, the `protoc` plugins and `sqlc` from source |

The phase plan (P4 §1, §7; maintainer decision D8) asks for:

- a pull request that changes only documentation finished in at most 3 minutes;
- a focused Go or Dart change finished in at most 8 minutes;
- the full suite faster than before;
- no gate removed.

## Decision drivers

- **No gate is removed.** Whatever a pull request skips still runs on `main` and every night.
- **A missed dependency is the failure that matters.** A job wrongly run costs minutes;
  a job wrongly skipped lets a broken change merge. When in doubt, run.
- **Explainable.** Every decision names the file and the root that caused it.
- **Repository tooling stays standard library only** (`tools/AGENTS.md`).
- **No new third-party action** (maintainer, 2026-10-01).
- **A pull request cannot change what `main` trusts**: no cache it writes is read by `main`.

## Considered options

1. Keep the per-toolchain path filters and refine their globs.
2. A dependency-aware selector in `tools/`, with committed rules, per job.
3. A build system with affected-target detection (Bazel, Nx).

## Decision

Chosen option: **2, a dependency-aware selector**, together with work on the long jobs.
Option 1 cannot express "the jobs that build this library" without hand-kept globs that
drift from the imports. Option 3 replaces the whole build and adds dependencies the
allowlist does not hold.

### Selection

`tools/cmd/affected` writes one decision per job of `ci.yml` from the rules in
`ci/affected.json`. A job's roots are any of:

- **path globs**, anchored at the root, with `**` for any depth and `!` to exclude;
- **Go packages**, whose in-repository imports are followed transitively, with or without
  their tests' imports (`go`, `goTest`). The graph is read from the import declarations and
  `//go:embed` directives of every Go file, ignoring build constraints (a superset), with
  `go/parser`. No toolchain runs and nothing is downloaded, so the job that selects takes
  seconds;
- **Dart packages**, whose workspace dependencies are followed: all of the package itself,
  and the dependencies without their tests, examples and Markdown (`dart`).

On a pull request the change is the merge commit against its first parent, which is exactly
what merging adds. Three rules keep the selection on the safe side:

- **`ci.yml` is compared job by job.** A job whose block changed runs; a change outside the
  job blocks (triggers, `env`, `defaults`) runs every job. Blocks are found by indentation;
  no YAML library is needed.
- **The `Makefile` is split by area.** The root holds the pins, the shared variables, the
  set-up and `check`, and includes `mk/go.mk`, `codecs.mk`, `dart.mk`, `bench.mk`,
  `studio.mk`, `stack.mk`, `device.mk` and `repo.mk`. A fragment runs its area's jobs; the
  root runs every job. `reqtrace` reads `# Verifies:` evidence in the fragments as in the
  root, so no evidence moved out of reach.
- **Unknown runs everything.** A changed file that no root names and that is not in
  `noJob` (documentation, local scripts, inputs only the always-on checks read) runs every
  job, and the summary names it. A change to the rules or to the tool runs every job.

Pushes to `main`, the merge queue, the daily run and manual runs run every job; the codec
rebuild alone, which takes up to 45 minutes, is left out of pushes and the merge queue, as
before.

The tool's tests check the rules against the repository: every job of `ci.yml` always runs
or has a rule, every selectable job runs on its decision, CI OK waits for every job, every
tracked file is named by a rule (so a new kind of file must be classified), every generated
file runs Go lint, and real past changes select exactly the jobs listed for them — among
them PR #11, which changed the root `Makefile` and still runs everything, and the same
change written today in `mk/stack.mk`, which runs the two image jobs.

### CI OK

CI OK passes a skipped job only when the selection skipped it, or for the commit and
dependency checks outside a pull request. A failed selection, a missing output or a broken
condition fails it.

### The long jobs

- **Runtime benchmark.** It runs as three parallel jobs, one per part of a run: start-up,
  opening the catalog page, and the native control with scrolling (`PLUX_BENCH_SCENARIOS`).
  Each job builds both runtimes and runs them alternately on one runner, ten measured runs
  each, with the same statistics and the same 10% gate, so no comparison crosses runners.
  The parts are balanced by the time a run of each takes (here: start-up 11.5 s, opening
  9 s, the control 9 s, scrolling 6.5 s, against 33 s for a whole run). A policy test checks
  that the jobs together measure every part exactly once, and `benchcmp` fails a comparison
  that holds no metric. A local run measures every part by default. The Linux desktop
  packages' archives are cached: the runners' mirror once took 16 minutes for their 38 MB
  in two of the three jobs (run 36894816035), and apt still installs what the current
  index names.
- **Device jobs.** On Android, the Go driver and the starter app are built while the
  emulator boots, so the build the flows start recompiles only the Dart code with their
  defines (21 s instead of 231 s), and Gradle's distribution and caches are restored.
  The iOS job keeps its steps in order: an extra Xcode build during the simulator's boot
  made it take 16 minutes instead of 9 on the three-core macOS runner (run 36894816035).
  Android 26 runs on `main`, the daily and manual runs, and pull requests that change
  Android-specific files or `pubspec.lock`; Android 35 runs on every affected pull
  request. On pull requests, iOS runs for the runtime, the starter app and their
  dependencies, iOS-specific files and the flows' fixture; a server-only change is run on
  a device by Android 35 (maintainer, 2026-10-01, after the timings below).
- **Go lint.** The pinned Go tools are restored from a cache keyed on the `Makefile` (the
  pins) and `backend/go.mod` (the toolchain) instead of being built on every run.

### Caches

Caches are written by `actions/cache/save` only in runs that are not pull requests, and
read by every run with `actions/cache/restore`; both are parts of `actions/cache`, already
pinned. A pull request's cache could not be read by `main` anyway, but this keeps the
number of entries small and their content traceable to `main`.

### What was considered and not done

- **Go's test-result cache across runs.** Restoring `GOCACHE` would let `go test` skip
  unchanged packages, but the test cache keys every file a test opens on its modification
  time and size, and a fresh checkout gives every file a new modification time. Setting
  times from history would make a same-size edit in the same second look unchanged.
  Go test is not on the critical path, so it keeps running in full.
- **Reusing the Go build job's server binary** in the device and sync jobs. Depending on
  that job's artifact would start them three minutes later than building in place.
- **Docker layer caching** for the Compose job (`type=gha`) needs
  `docker/setup-buildx-action`, a new action.
- **An emulator snapshot.** The boot now overlaps the builds, which take longer.
- **Sharding Go tests.** Go test is not on the critical path.

## Timings

The full suite before (main run 36886856869) and after, with the caches warm (manual run
36897308652, which runs every job; the codec rebuild, which pushes leave out, took
1.4 minutes):

| Job | Before (min) | After (min) |
|---|---:|---:|
| Runtime benchmark | 15.6 | 8.2, 7.2 and 6.7 (three parts in parallel) |
| Device end-to-end (iOS) | 8.8 | 9.4 |
| Sync benchmark | 7.9 | 7.5 |
| Device end-to-end (Android 35) | 7.8 | 7.2 |
| Device end-to-end (Android 26) | 8.3 | 6.3 |
| Size (Android) | 7.3 | 6.6 |
| Go lint | 3.7 | 1.7 |
| **Wall clock, to CI OK** | **16.2** | **10.6** |

A pull request runs the jobs its change selects, all in parallel, after the selection
(0.3 minutes, plus up to a minute waiting for a runner). From the durations above:

| Change | Longest selected job | Wall clock (min) | Target |
|---|---|---:|---|
| Documentation only (PR #12) | the always-on checks | about 1.5 | ≤ 3: met |
| Studio | Studio | about 1.5 | — |
| A Dart package outside the runtime (`plux_widget_api`) | Dart and Flutter, 4.1 | about 5 | ≤ 8: met |
| A Go tool (`tools/cmd/sizegate`) | Size (Android), 6.6 | about 8 | ≤ 8: met |
| The compiler or the server | Sync benchmark, 7.5 | about 8.5 | ≤ 8: just over |
| The runtime | Device end-to-end (iOS), 9.4 | about 10.5 | ≤ 8: **not met** |

A change the runtime builds runs the device flows, and the iOS job, which builds the app in
Xcode and runs it under XCTest on a three-core runner, is then the longest. With these
timings in hand the maintainer kept iOS for such changes and dropped it for server-only
ones (above); a cache of Xcode's build products is the remaining lever. A server-only
change then waits on the sync benchmark, which is the benchmark itself.

## Consequences

- **Positive:** a pull request runs the jobs its change can affect, and the summary of the
  selection says why each one runs. The full suite's longest job is no longer the runtime
  benchmark. Nothing that was gated before is ungated: a skipped job runs on `main`, in the
  merge queue and every day.
- **Negative:** a dependency the rules do not express — a file a test reads by a relative
  path, a script a job calls — must be added to the rules. Unknown files run everything,
  and the tests require every tracked file to be named, which limits the risk to files
  that a rule names for the wrong job. A change to `main` can break a job that its pull
  request skipped; the push to `main` runs it, and the daily run catches what changed
  outside the repository.
- **Follow-up:** each new job, package or top-level directory gets its rule in
  `ci/affected.json` in the pull request that adds it ([ci.md §6](../engineering/ci.md#6-adding-a-component)).

## Options in detail

### Option 1: refined path filters

Simple and already in place. Every library change still needs hand-kept globs for every
job that builds it; they drift from the imports and nothing checks them, so the safe
reading is the coarse one that existed.

### Option 2: a dependency-aware selector

The Go and Dart graphs are read from the source, so the selection follows the code. The
rules that remain by hand (files tests read, scripts jobs call) are few, are checked
against the repository by tests, and fail safe. It is a small standard-library tool, run
locally by `make check-changed`.

### Option 3: a build system

Bazel or Nx would compute affected targets from a full build graph, at the cost of
rewriting every build, a new toolchain to pin, and dependencies outside the allowlist.

## Revision (2026-10-07, P6: device end-to-end shards and build caches)

P5's reference apps and generated-project test made the device jobs the slowest in CI:
iOS 46 min, Android 26 14 min, Android 35 13 min (run 37613709956). Each flow builds a
whole app from scratch, one after another; the flows themselves take seconds.
Maintainer decision (2026-10-07), with a target of 13 to 15 minutes for the device jobs:

- **Shards.** Each device job is a matrix with one shard per flow, selected by
  `E2E_SHARD` (`mk/device.mk`): `starter`, `generated`, `hosts` (the add-to-app module
  and hosts), `bank`, `express`, or several joined by `+`. iOS runs four jobs, `starter`,
  `hosts`, `bank` and `express+generated` (the two shortest flows), because GitHub runs at
  most five macOS jobs at once and the iOS size job is the fifth (run 37630248033 queued a
  sixth for six minutes; run 37633642167's `bank+express` took 24.5 minutes). A first cut
  of three shards (run 37623043856) left the iOS starter-and-generated shard at 25
  minutes. Every flow still runs on every selected
  platform; `make e2e-starter` without a shard runs them all, as before. While the
  emulator or simulator boots, the server's tests and the shard's first app are built
  (a second app took longer than the boot it hid). A
  booted emulator whose adb shell stays silent gets adb's server restarted, which drops
  the stale connection (runs 36966274717, 37630248033); `adb reconnect` is not used, as
  it can leave two connections under one serial (run 37633642167).
- **The add-to-app hosts start Flutter with the app.** The hosts' flows were the most
  frequent device failure (15 jobs in 10 of the last 45 runs, both platforms: "nothing on
  screen reads 'Welcome to Plux'", an instrumentation crash, a UI-query timeout). Each
  host created its engine when the first Plux page opened, so that page waited for a
  cold debug-mode engine and runtime — about 40 seconds on a busy simulator in run
  37633642167, with the app's main thread too busy to answer XCUITest. Both hosts now
  start the engine as the app starts, as Flutter's add-to-app guide recommends.
- **Build caches.** On iOS one per shard: Xcode DerivedData (the add-to-app host's build
  kept there through `PLUX_E2E_DERIVED_DATA`), CocoaPods and the apps' Flutter build
  outputs, keyed on the shard, Xcode, Flutter and the hash of the lockfiles and the
  packages' native code. On Android one Gradle cache for every shard, saved by the
  starter's shard, whose build has every plugin; the Android shards are short enough
  without build outputs, which would crowd the repository's cache quota. A restore key on
  the same toolchain lets a changed lockfile start from the previous build.
- **Who writes.** Every run restores; only a passing run on `main` (not a pull request,
  not another branch, Android from API 35 only) saves, so no branch can write a cache that
  `main` or another branch reads (cache poisoning), and a broken build is never saved. A
  miss is a clean build, as before. Incremental builds are Xcode's and Gradle's own, which
  track inputs by content.
