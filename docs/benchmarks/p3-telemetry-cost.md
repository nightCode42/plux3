# P3 Telemetry Cost on the Device

The method for `NFR-041` (telemetry costs a typical user less than 1% of battery and less
than 100 KiB of data a day, `ANL-002`) and what is measured so far. The design is
[ADR-0034](../adr/0034-runtime-telemetry.md).

## Data: measured in CI

**Target:** < 100 KiB a day for a typical user.

**Typical day.** Five sessions, each with a sync and eight pages viewed; one error in the
day; full analytics consent, the worst case, since every event is then sent. The runtime
flushes after each sync and when the app leaves the foreground, so the day is ten
requests carrying 96 events: per session `session_start`, `sync_result`, eight
`screen_view` and eight `render_perf`, `session_end`, plus the error.

**Measurement.** `packages/plux_flutter/test/telemetry/cost_test.dart` builds that day
with the runtime's own event encoder (`eventJson`, `redactFields`), compresses each
request as the runtime does (gzip of the Connect JSON body) and adds an estimate of the
request and response headers (600 and 250 bytes a request; the runtime's request
headers are about 300 bytes). It runs in every CI run and fails above 25 KiB, a quarter
of the budget.

| Measured on 2026-09-30 | Value |
|---|---|
| Events | 96 in 10 requests |
| Compressed bodies | 6,124 bytes |
| With header estimates | 14,624 bytes (14.3 KiB, 14% of the budget) |

A day without analytics consent sends only `session_start`, `sync_result` and `error`:
about a tenth of the events.

## Battery: by design, confirmed on reference devices

**Target:** < 1% of battery a day for a typical user.

Telemetry adds no radio wake-up of its own while the app is in use beyond one flush every
15 minutes in the foreground: it sends after a sync, when the radio is already awake, and
when the app leaves the foreground. It never wakes the app in the background, schedules
no background work and holds no wake lock. Recording an event is a small map posted to
the sync isolate; the buffer is one appended line per event. On the typical day that is
ten requests of about 1.5 KiB.

**On reference devices** (spec §30.1; recorded by the maintainer, as for the other device
`NFR`s, because cloud sessions start no emulators and CI has no reference hardware):

1. Build the example host app (R8) in profile mode with analytics consent granted.
2. Android: reset battery statistics (`adb shell dumpsys batterystats --reset`), run the
   typical day's script, then read the app's estimated power use with Battery Historian
   and the network bytes with `adb shell dumpsys netstats detail`.
   iOS: record the same script with Xcode's Energy Log and the Network instrument.
3. Repeat with telemetry turned off (`PluxConfig.telemetrySampling` at 0 for every
   sampled event and a server that refuses `IngestEvents`); the difference is
   telemetry's cost.
4. Record device, OS, date, commit and both results here.

| Reference device | Battery (share of a day) | Data a day | Date, commit |
|---|---|---|---|
| — | not yet measured | not yet measured | — |

`NFR-041` stays `WIP` until the reference-device row is filled in.
