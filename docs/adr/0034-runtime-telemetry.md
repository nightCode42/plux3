# 0034. Runtime telemetry: consent-gated events buffered and sent by the sync isolate

- **Status:** Accepted
- **Date:** 2026-09-29
- **Requirements:** `ANL-001`, `ANL-002`, `ANL-003`, `SEC-161`, `SCH-012`, `SYN-015`, `RT-015`, `REL-080`, `NFR-041`, `LIM-001`, `LIM-004`

## Context and problem

The runtime must report what happens on devices (Appendix G.2) so that operators see sync
health, errors, performance and adoption, and so that the server knows which runtime
versions and host builds are installed (`REL-080`). The server has ingested events since P2
(`TelemetryService.IngestEvents`: catalogued names, flat scalar fields, names that look
sensitive refused, 30-day retention). P3 adds the device side. It must batch, compress and
buffer events offline within a bound (`ANL-002`), respect the user's consent and support
sampling per event type (`ANL-003`), never carry sensitive values (`SCH-012`), cost less
than 1% battery and 100 KiB a day (`NFR-041`), and never do network I/O or compression on
the UI isolate (L-6).

Only some Appendix G.2 events have a source in P3. Actions, data sources and `trackEvent`
arrive in P5, functions in P7, RASP in P6 and experiments in P9; each emits its event when
it exists.

## Decision drivers

- Privacy: send nothing a user did not agree to, and nothing personal without consent.
- The UI isolate stays free of I/O; telemetry must never slow a frame.
- Battery and data: few radio wake-ups, small payloads.
- Operators tune what is collected without a host app release.
- Nothing lost while offline, within a bound.

## Considered options

1. **Events recorded on the UI isolate, buffered and sent by the sync isolate** (chosen).
2. A separate telemetry isolate with its own HTTP client.
3. Send each event as it happens from the UI isolate.

## Decision

Chosen option: **1**, because the sync isolate already owns the HTTP client, the device's
token and the store directory, and sends at the moments the radio is awake anyway.

### Consent (`SEC-161`, `ANL-003`; maintainer, 2026-09-29)

Following the ePrivacy "strictly necessary" exemption and GDPR data minimisation, as the
App Store and Play privacy disclosures treat diagnostics, each event has a category:

| Category | Events | Sent |
|---|---|---|
| `necessary` | `session_start` (runtime version, host build, platform, OS version, device class, release; no locale), `sync_result`, `error` (code, reason, node path, route, fingerprint; never message text); from P6 `rasp_detection` | always: anonymous operational data needed to deliver releases, keep them working and decide which release a device can run |
| `analytics` | `session_end` (duration), `screen_view`, `render_perf`; from P5 `action_run`, `api_call`, `custom`; from P7 `function_call` | only with `PluxConsent.analytics` |
| `experiments` | `experiment_exposure` (P9) | only with `PluxConsent.experiments` |

`session_start` adds `locale` only with analytics consent. Withdrawing consent also deletes
the buffered events of that category.

### Sampling (`ANL-003`; maintainer, 2026-09-29: both sources)

The app document may set `telemetry.sampling`, a rate between 0 and 1 per event name,
compiled into the signed app bundle (`Meta.telemetry_sampling`, in thousandths); the host
may set `PluxConfig.telemetrySampling`, which can only lower a rate. The effective rate is
the lower of the two, 1 by default. Only `analytics` and `experiments` events are sampled:
`necessary` ones are always kept, because each is rare and each matters (a lost
`session_start` hides a device from `REL-080`). Each event is kept or dropped on its own,
with a per-runtime random generator.

### Redaction (`SCH-012`, `SEC-092`)

Events carry only fields the runtime builds from its own measurements: numbers, codes,
route names and node paths from the signed bundle. They never carry page parameters, state,
user input, error message text or URLs. As a second line, the recorder drops any field
whose name the server would refuse as sensitive, any non-scalar value and any string
longer than 256 bytes, so an event is never refused whole.

### Buffering and sending (`ANL-002`, `NFR-041`; limits approved by the maintainer)

The UI isolate records an event (a small map) and posts it to the sync isolate, which
appends it as one JSON line to `<store>/telemetry/events.jsonl`. The file is bounded by
the new limit `telemetry.bufferBytes` (256 KiB, from the app bundle's limits, `LIM-004`);
the oldest events are dropped first. The sync isolate sends after every sync, every 15
minutes while the app is in the foreground and when it moves to the background: batches
of at most `telemetry.eventsPerRequest` events, as one Connect JSON request compressed with
gzip (`Content-Encoding: gzip`). A batch the server answers is removed, events it refuses
included, so a malformed event is never retried for ever; one that fails for a retryable
reason stays for the next attempt. DPoP arrives with the rest of device DPoP in P6
(`SEC-021`); until then the device's bearer token authenticates the request, as for sync.

### Events in P3

| Event | Recorded when | Fields |
|---|---|---|
| `session_start` | the runtime starts, and on returning to the foreground after 30 minutes or more away | `runtime_version`, `host_build`, `platform`, `os_version`, `device_class`, (`locale`) |
| `session_end` | the app leaves the foreground, and on dispose: one per stretch of foreground time, whose durations add up to the session's (a stretch recorded on leaving is not lost if the app is then killed) | `duration_ms` |
| `screen_view` | a page host is disposed | `route`, `source_route`, `duration_ms` |
| `render_perf` | a page host is disposed after its first frame | `route`, `build_ms`, `first_frame_ms`, `janky_frame_pct`, `frames`, `node_count` |
| `sync_result` | a sync ends (`SYN-015`) | `SyncResult.toTelemetry()` |
| `error` | the runtime reports a `PluxException` (`ANL-001`, ADR-0031) | `code`, `reason`, `node_path`, `route`, `fingerprint` |

A frame is janky when its build or raster time exceeds the display's frame budget. The
fingerprint is the first 16 hex digits of SHA-256 over code, node path and route, so errors
group without their text (`ANL-040` builds on it).

### Device facts on the server (`REL-080`)

A `session_start` updates the device's recorded runtime version and host build, so the
compatibility count before a promotion reflects the runtime devices run now, not the one
they registered with.

## Consequences

- **Positive:** no I/O on the UI isolate; one radio wake per sync; privacy by category;
  sampling tunable per release; nothing personal leaves a device without consent.
- **Negative:** events recorded between the last flush and a crash of the whole process are
  lost (they are posted, not written synchronously); per-event sampling does not keep whole
  sessions together.
- **Follow-up:** P5–P9 emit their events through the same recorder; P6 adds DPoP to the
  request; `NFR-041`'s reference-device numbers are recorded by the maintainer
  ([p3-telemetry-cost.md](../benchmarks/p3-telemetry-cost.md)).

## Options in detail

### Option 2 — a separate telemetry isolate

Isolates the concerns, but duplicates the HTTP client, the credential store and the token,
and wakes the radio on its own schedule.

### Option 3 — send from the UI isolate

Simplest, but network I/O and compression on the UI isolate break L-6, and one request per
event costs battery and data.
