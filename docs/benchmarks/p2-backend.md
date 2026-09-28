# P2 Backend Benchmarks

Measurements for the performance targets of Phase 2 (spec §30.2, DoD-4), taken on
2026-09-28 at commit `00aa978` and later, with the method needed to repeat them.

## Machine

The development container used for every run below: 4 vCPU (x86-64), 16 GiB RAM, Linux
6.18, Go 1.27.1, PostgreSQL 16.13, Redis 7.0.15 (the Valkey protocol), SeaweedFS 4.47. The
load generator runs on the same machine, so each measurement pins the server to two cores
(`taskset -c 0,1`, `GOMAXPROCS=2`) — the size of one reference `api` replica (§30.1) — and
the generator to the other two. PostgreSQL shares the machine; Redis shares the server's
cores, which charges the rate limiter's cost to the replica.

## NFR-020 — manifest endpoint throughput

**Target:** ≥ 5,000 requests/s per `api` replica at p99 ≤ 50 ms (cache hit).

**Workload.** 3,000 registered devices, each synced once, then each device's "anything
new?" check — `GetManifest` with its installed bundles and the ETag it holds, answered
`not modified` — at a constant arrival rate for 60 s. Every device sends its own
`X-Forwarded-For` through a trusted proxy, so the per-address (300/min) and per-device
(120/min) rate limits apply per device as in the field, and every call runs the whole
interceptor chain: authentication, both rate-limit counters in Redis, tracing, metrics.

```bash
plux-server serve -config plux.yaml     # api+worker, S3, Valkey, trustedProxies: [127.0.0.1/32]
plux-server seed -config plux.yaml -out -   # then: plux publish --promote staging
go run ./tools/cmd/manifestload -server http://127.0.0.1:18200 -app <id> -rate 5000
```

| Rate asked | Served | Failures | p50 | p95 | p99 | max |
|---|---|---|---|---|---|---|
| 5,000/s | 4,999/s | 0 | 1.28 ms | 6.0 ms | **27.6 ms** | 170 ms |
| 5,500/s | 5,493/s | 0 | 1.29 ms | 7.2 ms | 36.2 ms | 139 ms |

**Met.** Two changes made it so, both measured before and after:

| Change | Before | After |
|---|---|---|
| Each replica caches a channel's newest manifest for 1 s and a device token until its expiry (at most 30 s) | 640 req/s, p99 281 ms | ≈ 3,000 req/s, p99 9 ms |
| The Valkey client keeps up to 256 idle connections instead of 8 — under load every other counter call dialled and closed a TCP connection | — | 5,000 req/s, p99 28 ms |

The manifest cache delays a new manifest by at most a second, far inside the minute a
rollback may take to reach online devices (`REL-006`).

`test/load/manifest.js` is the same workload for k6, which the nightly `Load` workflow runs
against the Compose stack. k6 itself needs more than two cores to generate 5,000 requests a
second, which is why the reference measurement above uses the lighter
`tools/cmd/manifestload`.

## SRV-053 / NFR-021 — publish of a 50-page plugin with deltas

**Target:** ≤ 15 s p95 on the reference deployment, including deltas for the last 10
versions.

**Workload.** The loan calculator with 48 copies of its result page (50 pages, a 61 KB
bundle), published ten times with one page changed each time; then five more publishes,
each timed from the request to the last delta stored: compile, check, sign, store, record
the version, and compute and store the deltas from the ten previous versions and the most
held bundles. `TestPublishFiftyPagePluginWithDeltas` in `backend/internal/release` runs it
against PostgreSQL and fails if the slowest of the five exceeds 15 s.

| Publishes | min | median | max |
|---|---|---|---|
| 5 | 317 ms | 355 ms | **395 ms** |

**Met**, with a wide margin.

## NFR-005 — delta size for a single text change

**Target:** ≤ 2 KiB.

One translated message changed in the loan calculator: a **635 B** delta against a
2,231 B compressed app bundle (`TestManifestAndDeltas`, which fails above 2 KiB). The same
delta is fetched and applied by the end-to-end test `TestPublishWithTheCLIAndSyncADevice`.

## Repeating

- Throughput: start a server as above (or `make compose-seed
  COMPOSE_OVERLAY=deploy/compose/compose.load.yaml`), then run `manifestload` or
  `k6 run test/load/manifest.js`.
- Publish and delta size: `make go-test` with `PLUX_TEST_DATABASE_URL` set.
