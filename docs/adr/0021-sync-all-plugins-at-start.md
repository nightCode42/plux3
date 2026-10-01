# 0021. Sync all plugins at app start instead of lazy loading

- **Status:** Accepted
- **Date:** 2026-09-28
- **Requirements:** `SYN-001`–`SYN-015`, `REL-002`, `RT-004`, `RT-022`, `SEC-055`, `LIM-004`, `NFR-006`, `NFR-007`, `QA-009`, `QA-002`

## Context and problem

An app is a set of plugins, and any plugin page may navigate to any page of any other
plugin (`NAV-001`). The server therefore releases **app releases** — consistent sets of
plugin versions — never single plugins (`REL-002`, ADR-0020). On the device, the question
is *when* plugins arrive and *how* a new release replaces the one in use: fetch a plugin
when the user first navigates to it, or bring the whole release down at start? And once a
release is on disk, how is it swapped in without ever showing a mixture, surviving a crash
at any instant, and recovering by itself if the new release is broken?

## Decision drivers

- Offline navigation from any page to any page (`SYN-008`); no spinner mid-flow.
- Consistency: the device runs exactly one app release at a time, never a mixture
  (`SYN-005`), and never swaps it under a visible page (`SYN-004`).
- Crash safety at every step, including power loss (`SYN-005`, `QA-009`).
- Automatic recovery from a bad release (`SYN-006`) and a bounded disk footprint
  (`SYN-012`, `LIM-004`).
- Cheap when nothing changed: one conditional request, ≤ 1 KiB (`NFR-006`); three changed
  plugins in ≤ 3 s on a slow network (`NFR-007`).
- A start-up that never waits for the network when something usable is on disk (`RT-004`).

## Considered options

1. **Sync every plugin of the app at start (and on demand), stage the whole release, activate it atomically at a safe point.**
2. Lazy loading: fetch a plugin's bundle when navigation first reaches it.
3. Background-only updates through the operating system's scheduler.

## Decision

Chosen option: **1**, as the specification requires (`SYN-001`).

### Start-up (`SYN-003`, `RT-004`)

`Plux.initialize` reads the pointer file and the active release's record, maps the app
bundle, checks its header against the signed hash, and returns; nothing else runs on the UI
isolate. Sync then starts on a background isolate:

| Startup policy | Behaviour |
|---|---|
| `useCacheThenSync()` (default) | render the active release at once; sync in the background |
| `blockUntilSynced(timeout)` | `initialize` waits for the sync up to `timeout`, then continues with what is on disk |
| nothing on disk and no baseline | `initialize` waits for the first sync whatever the policy, and reports failure to the host, which shows its own loading and error UI; `Plux.sync()` retries |

A baseline embedded by `plux pull` (`SYN-007`) is imported on first launch on a background
isolate: every bundle is checked against its signed hash (ADR-0029) and copied into the
store, and the baseline's sequence becomes the first accepted sequence. Pages wait for the
import; `initialize` does not.

### The sync (`SYN-001`, `SYN-010`, `SYN-011`)

A sync runs on a background isolate (L-6) and is serialised: `Plux.sync()` during a sync
joins it. Steps:

1. **Credential.** On first use the device registers (`DeviceService.RegisterDevice`) and
   keeps its device secret encrypted by a platform key (ADR-0029); each sync exchanges it
   for a 15-minute access token (`TokenService`), kept in memory only. DPoP binding
   replaces this in P6.
2. **Manifest.** `ManifestService.GetManifest` with the installed sequence, the installed
   bundle hashes and the stored ETag. `not_modified` ends the sync as `upToDate`
   (`NFR-006`). The API is ConnectRPC with its JSON encoding over the `http` client, so no
   protobuf runtime is needed on the device.
3. **Verify** the manifest (ADR-0029): signature, expiry, app, environment and channel,
   anti-rollback (`SEC-055`), required features and minimum runtime of every bundle
   (`BND-008`).
4. **Plan.** Every bundle named by the signed manifest — the app bundle and every plugin —
   that is not already in the object store is obtained by the server's sync step for it:
   `delta` from an installed bundle, else `full`. A `keep` for a bundle that is missing
   locally becomes `full`. The plan itself is unsigned; what it produces is verified.
5. **Download** with `downloadParallelism` connections (default 4) over the platform HTTP
   client (HTTP/2: Cronet on Android, `URLSession` on iOS). Each object downloads to a
   `.part` file and resumes with `Range` and `If-Range`; failures retry with exponential
   backoff (0.5 s doubling to 30 s) and full jitter, at most five attempts, honouring
   `Retry-After`; `4xx` other than `408` and `429` fail at once.
6. **Rebuild and verify.** A delta is applied section by section with zstd (ADR-0030,
   ADR-0003). The result — and every full bundle — must have the signed bundle hash, every
   section its directory hash, and every section must pass the FlatBuffers verifier. On a
   delta mismatch the result is discarded and the full bundle is downloaded once
   (`SYN-011`); a second failure fails the sync.
7. **Assets.** Every asset the release's bundles reference (the chosen variant per
   density, ADR-0032) that is not stored is downloaded and checked against the hash in the
   signed bundle's asset index (`AST-001`).
8. **Stage** the release (below) and emit `staged`; then activate per policy.

Typed events are published on `Plux.syncEvents` (`SYN-013`): `checking`, `upToDate`,
`downloading(progress)`, `staged`, `activated`, `failed(error)`, `rolledBack`; each sync
emits a `sync_result` telemetry event with duration, bytes, delta ratio, plugins updated
and outcome (`SYN-015`).

### The release store (`SYN-005`, `SYN-008`, `SYN-012`)

One store per app, environment and channel, in the app's private, non-backed-up storage:

```
plux/<app>/<environment>/<channel>/
  objects/bundles/<hash>     verified bundles, named by bundle hash, immutable
  objects/assets/<sha256>    verified asset files, content-addressed, shared by all plugins
  objects/tmp/               downloads in progress (<hash>.part), resumable
  releases/<sequence>.json   one immutable record per release: the signed manifest bytes,
                             signatures, bundle and asset lists
  state                      the pointer file
```

The pointer file is small JSON with a SHA-256 of its own content: `active`, `staged`,
`lastKnownGood`, `pinned`, the highest accepted sequence (`SEC-055`), the manifest ETag,
and the trial counters below. It is only ever replaced by writing `state.tmp`, `fsync`,
`rename` to `state`, `fsync` of the directory.

**Staging** writes, in this order, each step durable before the next begins:

| Step | Writes | Durable by |
|---|---|---|
| S1 | each missing object: `tmp/<hash>.part` → verified → `objects/<kind>/<hash>` | `fsync` file, `rename`, `fsync` directory |
| S2 | `releases/<sequence>.json` | write `.tmp`, `fsync`, `rename`, `fsync` directory |
| S3 | pointer: `staged = sequence` | pointer protocol above |

**Activation** is one pointer write: `lastKnownGood = active`, `active = staged`,
`staged = none`, trial counters reset.

**Why a crash leaves one complete release.** The pointer names only records that are
already durable (S2 precedes S3), and a record names only objects that are already durable
(S1 precedes S2). `rename` is atomic, so after power loss the pointer is either the old
file or the new one. Objects are content-addressed and verified before they are renamed
into place, so a partially written file is only ever a `.part`. Garbage collection runs
only after a pointer write has completed, and deletes only what the durable pointer does
not reach. A kill test runs the staging and activation in a subprocess, kills it at every
step, restarts, and checks that the old or the new release is fully active (`QA-009`).

A pointer that fails its checksum (storage corruption) is treated as no store: the
baseline is imported again and the next sync re-establishes the release.

### Activation (`SYN-004`)

The runtime counts mounted Plux pages — every `PluxView` and every page opened with
`Plux.open` holds a lease on the release it renders. A staged release is activated:

| Policy | When |
|---|---|
| `immediate` | at once if no Plux page is mounted, otherwise as soon as none is |
| `atSafePoint` (default) | the next time no Plux page is mounted; from P4 also when the user returns to the navigation root |
| `nextLaunch` | at the next `Plux.initialize` |
| `forced` (mandatory updates, `REL-070`) | as `immediate`; the `staged` event carries `mandatory: true` so the host can close Plux pages |

A release is never swapped under a mounted page: pages keep their lease, and mapped files
are released only when the last lease on them ends. The app bundle's policy is used unless
the host overrides it in `PluxConfig`.

### Last known good (`SYN-006`)

A newly activated release is on **trial** for its first two launches. The trial counts
failures attributable to Plux: a page whose root error boundary rendered the fallback, an
uncaught error whose stack is in the runtime, and a launch that ended without reaching a
healthy point (a Plux page's first frame followed by ten seconds without a failure, or the
app moving to the background) — which is how a crash is recognised on the next launch.
Counters are persisted when they change. At three failures within the first two launches
the runtime makes `lastKnownGood` active, **pins** it (a manifest with the same sequence as
the reverted release is ignored; a higher sequence clears the pin), reports `PLX-3020` and
emits `rolledBack`. The baseline serves as last known good when there is no earlier
release.

### Garbage collection and quota (`SYN-012`, `LIM-004`)

After each pointer write the store deletes release records other than active, staged,
last known good and pinned, objects none of those reference, and `.part` files that the
current plan does not need. Before staging, the runtime computes the store's size with the
new objects; above `device.diskQuota` — from the signed app bundle's limits, tightened by
`PluxConfig.diskQuota` — the sync fails with `PLX-3030` and writes nothing. A write that
fails for lack of space deletes what it wrote, fails the sync with `PLX-3030`, and leaves
the active release untouched.

### Kill switch and verification failures (`RT-022`)

The signed manifest's `control` names plugins that must not render; a plugin whose bundle
fails verification on load is treated the same way. Every route into such a plugin renders
its declared fallback page, else the app-level fallback.

### Background sync (`SYN-014`, a `SHOULD`)

An optional package, `plux_background_sync`, schedules the same sync through Android
WorkManager and iOS `BGTaskScheduler` with platform channels and no third-party
dependency, so an update can be staged before the next start.

## Consequences

- **Positive:** navigation never waits for the network; a device always runs one complete,
  verified release; recovery from a bad release is automatic; one conditional request
  costs an unchanged device almost nothing.
- **Negative:** every device downloads every plugin, including ones its user never opens;
  the disk quota bounds this, and deltas keep updates small. The runtime owns a store with
  a crash-safety protocol that must be tested by killing processes.
- **Follow-up:** safe points at the navigation root with `NAV-*` in P4; the signed control
  document and push-triggered checks with `SYN-060` in P9; DPoP-bound tokens in P6.

## Options in detail

### Option 2 — lazy loading

Smaller first download, but navigation to an uncached plugin needs the network, a
plugin fetched later may belong to a newer release than the pages already open, and
cross-plugin references cannot be checked on the device as a set. Rejected by the
specification (`SYN-001`).

### Option 3 — background-only updates

Operating-system schedulers give no guarantee of when, or whether, a task runs; an app
could keep an old release for days. Kept as an optional addition (`SYN-014`), not the
mechanism.
