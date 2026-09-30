# Runtime Runbook

Failure modes introduced in P3 (DoD-9): what the `plux_flutter` runtime does on a device when
something goes wrong, how it shows, and what the app team or operator does. The runtime never
crashes the host app for these: it keeps or restores a working release and reports the problem
([threat model](../security/threat-model.md#p3--runtime-rendering)). Background:
[host app guide](../guides/host-app.md), [telemetry](../reference/telemetry.md),
[error codes](../reference/errors.md).

**Where problems show.** On the device: `PluxConfig.onError`, `Plux.syncEvents`, the
`PluxSyncTile`, and the `plux_devtools` overlay in debug builds. On the server: the devices'
`sync_result` and `error` events (`TelemetryService`, [api.md](../reference/api.md)), which are
sent without consent and carry the error code, the node path and the route — never the message.
Search for the `PLX-` code first; each is explained in [errors.md](../reference/errors.md).

## Sync

| Symptom | Likely cause | Action |
|---|---|---|
| `sync_result` with `PLX-3050`; devices stay on an older release | Server unreachable or failing, or downloads fail after five attempts | Check the server ([server runbook](server.md)) and the CDN. Nothing to do on devices: they keep their release and sync again at the next start or `Plux.sync()`. A `Retry-After` from the server is honoured, capped at five minutes. |
| `PLX-3001` on every device of an app | The app embeds other keys than the environment signs with: a baseline pulled from another environment or installation, or a key replaced on the server | Compare `plux keys --env <key>` with the app's `assets/plux/keys.json` or `PluxConfig.rootKeys`. Rebuild the app after `plux pull` from the right environment. Key rotation without an app update arrives in P6 (`SEC-051`). |
| `PLX-3001` on some devices only | Something on their network path rewrites responses (a captive portal, a proxy) | Expected: the manifest is refused and the release kept. Nothing to fix server-side. |
| `PLX-3002` | The server has not re-signed the channel's manifest within seven days (worker down), or the device clock runs ahead | Fix the worker ([server runbook](server.md#manifests-and-devices)); promoting any release re-signs at once. For single devices: the clock. |
| `PLX-3003` | A channel was pointed back at an older sequence, or a replayed manifest | Roll back with `plux release rollback`, which creates a new, higher sequence (`REL-006`); devices never accept a lower one. |
| `PLX-3010` | The release needs a feature this runtime does not have (`BND-008`) | Devices keep their last compatible release. Ship the host app with a newer `plux_flutter`, or publish without the feature; raise the app's minimum runtime to make the requirement explicit. |
| `PLX-3011` or `PLX-3012`, followed by success | A stored delta was made against another base or is corrupt | The runtime downloaded the full bundle instead; nothing to do. If it persists, see the server runbook's delta row. |
| `PLX-3040`–`PLX-3045` | A bundle or asset file failed its hash, container or FlatBuffers check: corrupted in transit, in the CDN, or tampered with | Nothing is stored. Check the CDN and any proxy; compare the object's SHA-256 with its name. Persistent failures from one network suggest interference. |
| `PLX-3030` | The release does not fit the device's Plux disk quota, or the device is out of storage | The active release is untouched. Reduce the release's assets, or raise `device.diskQuota` in the app's limits (it is delivered in the signed app bundle, `LIM-004`); the user can free storage. |
| Devices take long to show a new release | Activation waits for a safe point: no Plux page on screen (`atSafePoint`), or the next launch (`nextLaunch`) | Expected (`SYN-004`). Use `ActivationPolicy.immediate` or `forced` for apps that need it, or `Plux.sync()` with the tile. |
| First launch shows a loading state, then the fallback | No embedded baseline, no cache, and the first sync failed | Embed a baseline with `plux pull` (`SYN-007`), so a first launch renders offline. |

## Releases on devices

| Symptom | Likely cause | Action |
|---|---|---|
| `PLX-3020` in `error` events after a release | The new release caused three failures within its first two launches, so devices returned to the last known good release and pinned it (`SYN-006`) | Look at the release's `error` events (`PLX-4001` with node paths) and fix the pages; publishing a newer release unpins devices. |
| A plugin misbehaves in production | — | Switch it off with the channel's kill switch (`ControlService`, `RT-022`): its routes show the plugin's declared fallback page, or the host's fallback. The switch reaches devices with the next manifest; it needs no new release. Switch the whole app off only as a last resort. |
| `PLX-4020` warnings | A kill switch is on | Expected while it is on; turn it off once fixed. |
| A device lost its store (reinstall, cleared data, corrupt pointer) | The pointer's checksum failed or the files are gone | The runtime starts from the embedded baseline, or waits for the first sync, and registers again when its credential is gone. Nothing to do. |

## Rendering

| Symptom | Likely cause | Action |
|---|---|---|
| `PLX-4001` with a node path | A page or component failed to build: a required value could not be produced, a builder threw, or a section failed its first-use check | The page's or component's boundary shows the fallback (`RT-020`). Find the node from its path (the development bundle's source map names the document location) and publish a fix; if many users are affected, switch the plugin off meanwhile. |
| `PLX-4002` warnings | A prop's value or binding was not usable; the prop took its default | Fix the value or expression; the page still rendered. |
| `PLX-4003` | A page uses a widget this runtime does not know | Update the host app's runtime, or raise the app's minimum runtime so older ones keep their release. |
| `PLX-4010` in debug builds | An event handler fired; actions arrive in P5 | Expected in P3. |
| `PLX-6030` | A remote image's host is not among the plugin's declared domains, or the URL is not HTTPS | Declare the domain in the plugin, or serve the image as an asset. |
| A crash or red screen from layout or painting | Layout and paint errors are contained by Flutter, not Plux (ADR-0031) | They reach the host's `FlutterError.onError` (Crashlytics, Sentry); the node is identified in the stack. |
| Blank areas where content should be | A failing page or component whose fallback is empty, or an image that failed | The default fallback (`PluxDefaultFallback`) is a themed panel; a host `fallbackBuilder` or `pluginFallbackBuilders` entry replaces it. Look for `PLX-4001` events. |

## Telemetry

| Symptom | Likely cause | Action |
|---|---|---|
| No `screen_view` or `render_perf` events | The user has not consented to analytics, or sampling drops them | Expected (`SEC-161`, [telemetry](../reference/telemetry.md#consent)); necessary events still arrive. |
| Events arrive late or in bursts | Batches are sent after each sync, every 15 minutes in the foreground and on leaving it; offline devices buffer | Expected. The buffer is bounded (`telemetry.bufferBytes`); the oldest events are dropped when it is full. |

## Host builds

| Symptom | Likely cause | Action |
|---|---|---|
| Android build fails: two libraries declare the namespace `org.chromium.net` | Android Gradle plugin 9 with Play Services Cronet (`cronet_http`) | Add `android.uniquePackageNames=false` to `android/gradle.properties` ([host app guide](../guides/host-app.md)). |
| Release Android builds cannot reach a development server | Release builds forbid cleartext HTTP | Use HTTPS; only debug builds allow HTTP, for `make dev`. |
