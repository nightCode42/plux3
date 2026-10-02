# Runtime telemetry

What the Plux runtime reports from devices, what the user's consent allows, and how an app
or host tunes it. The design and its reasons are in
[ADR-0034](../adr/0034-runtime-telemetry.md); the server side is `TelemetryService`
([api.md](api.md)); the cost on a device is in
[p3-telemetry-cost.md](../benchmarks/p3-telemetry-cost.md).

## Consent

The host passes the user's consent in `PluxConfig.consent` and changes it with
`Plux.setConsent` (`SEC-161`). Each event belongs to one category:

| Category | Sent | Events |
|---|---|---|
| necessary | always; anonymous and operational | `session_start` (without locale), `sync_result`, `error`; `rasp_detection` from P6 |
| analytics | with `PluxConsent(analytics: true)` | `session_end`, `screen_view`, `render_perf`; `action_run` from P4 ([action engine](action-engine.md)); `api_call`, `custom` from P5; `function_call` from P7 |
| experiments | with `PluxConsent(experiments: true)` | `experiment_exposure` from P9 |

Withdrawing consent deletes the buffered, unsent events of that category at once.

```dart
Plux.setConsent(const PluxConsent(analytics: true)); // after the user agrees
Plux.setConsent(PluxConsent.necessaryOnly);          // after they withdraw
```

## Events and their fields

| Event | When | Fields |
|---|---|---|
| `session_start` | start, and back in the foreground after 30 minutes or more away | `runtime_version`, `host_build`, `platform`, `os_version`, `device_class` (`phone`, `tablet`), `locale` (with analytics consent) |
| `session_end` | leaving the foreground, and `Plux.dispose` | `duration_ms` of that stretch in the foreground |
| `screen_view` | a page is removed | `source_route`, `duration_ms`; the route and plugin |
| `render_perf` | a page that drew a frame is removed | `build_ms`, `first_frame_ms`, `frames`, `janky_frame_pct` (frames whose build or raster missed the display's frame budget), `node_count` |
| `sync_result` | a sync ends | `duration_ms`, `bytes`, `delta_ratio`, `plugins_updated`, `outcome`, and `reason`, `code` on failure |
| `error` | the runtime reports a problem | `code`, `reason`, `node_path`, `fingerprint`; the route and plugin |

Events carry the release sequence. They never carry page parameters, state, user input,
URLs or error message text; a field whose name looks sensitive (`token`, `email`, …) is
dropped before it leaves the device (`SCH-012`).

## Sampling

The app document sets the share of each consent-gated event kept (`ANL-003`), compiled
into the signed app bundle:

```json
{
  "telemetry": {
    "sampling": { "screen_view": 0.1, "render_perf": 0.25 }
  }
}
```

The host can lower a rate further, never raise it:

```dart
PluxConfig(
  // …
  telemetrySampling: const {'render_perf': 0.05},
)
```

The effective rate is the lower of the two, 1 when neither sets one. Necessary events are
never sampled.

## Buffering and sending

Events are buffered on the device in the release store's `telemetry/` directory, within
the limit `telemetry.bufferBytes` (256 KiB by default, oldest dropped first), and sent
gzip-compressed by the sync isolate after each sync, every 15 minutes in the foreground
and on leaving the foreground, at most `telemetry.eventsPerRequest` events a request
([limits.md](limits.md)). Nothing is sent from the background. A session's `session_start`
also updates the server's record of the device's runtime version and host build, which
the compatibility check before a promotion counts (`REL-080`).
