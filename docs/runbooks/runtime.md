# Runtime Runbook

Failure modes introduced in P3 and P4 (DoD-9): what the `plux_flutter` runtime does on a device
when something goes wrong, how it shows, and what the app team or operator does. The runtime
never crashes the host app for these: it keeps or restores a working release, contains a failing
page, action or piece of host code, and reports the problem
([threat model](../security/threat-model.md#p3--runtime-rendering),
[P4](../security/threat-model.md#p4--routing-host-integration-and-no-code-generation)).
Background: [host app guide](../guides/host-app.md), [routing guide](../guides/routing.md),
[telemetry](../reference/telemetry.md), [error codes](../reference/errors.md).

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
| `PLX-4010` | A step names an action a later phase delivers (the nine P4 actions run), or an exposed state entry declares a persistence (P5) | Expected until that phase; the step's `onError` can handle it ([actions](../reference/actions.md) lists each action's phase). |
| `PLX-6030` | A remote image's host is not among the plugin's declared domains, or the URL is not HTTPS | Declare the domain in the plugin, or serve the image as an asset. |
| A crash or red screen from layout or painting | Layout and paint errors are contained by Flutter, not Plux (ADR-0031) | They reach the host's `FlutterError.onError` (Crashlytics, Sentry); the node is identified in the stack. |
| Blank areas where content should be | A failing page or component whose fallback is empty, or an image that failed | The default fallback (`PluxDefaultFallback`) is a themed panel; a host `fallbackBuilder` or `pluginFallbackBuilders` entry replaces it. Look for `PLX-4001` events. |

## Navigation and actions

| Symptom | Likely cause | Action |
|---|---|---|
| `PLX-4100`; the not-found page shows | A route name no page of the active release has: a stale link, a host call with a typo, a page removed in a newer release | The app's `navigation.notFound` page, `PluxConfig.notFoundBuilder` or Plux's own page shows. Fix the caller, or map old names to new pages with a redirect guard. `plux codegen` makes host-side names compile-time checked. |
| `PLX-4101`; the error fallback shows | Parameters missing, unknown or of the wrong type on entry; for links, text that does not convert | Fix the caller; for links, the app document's pattern or the page's parameter types. |
| `PLX-4102` | A guard refused, failed or redirected in a cycle; a guard tried to navigate; no navigator for a link (`PluxConfig.navigatorKey` missing); `switchTab` outside a shell; a `PluxView` page popped without an `onEvent` | The details name the route. Guards fail closed by design: check the guard graph and what it reads (`user.authenticated` comes from the auth delegate). |
| `PLX-4103` | A link or payload maps nothing: another host or scheme, or a pattern the app document does not declare | Expected for links the host owns; Plux returns false so the host handles them. Otherwise add the pattern, or the push key. |
| `PLX-5001`, `PLX-5002` | A step or run passed `action.stepTimeout`, `action.runTimeout` or `action.stepsPerRun` | The run ends whatever `onError` says. Simplify the graph, or raise the limit in the app's limits. Time waiting for the user in a dialog or sheet does not count. |
| `PLX-5003` | A value of the wrong type: a presented page's or native route's result, a custom action's output, an exposed state write | Align the page's `result`, the native catalogue's declaration or the host's code with each other. |
| `PLX-5004` | A run's `stop` ended it with a custom error | The graph's own outcome; nothing is wrong in the runtime. |
| `action_run` events with `result: failed` | Steps failing in production | The event names the graph, trigger, failing step and duration, never inputs or outputs (`ANL-001`). |

## Native catalogue and host builds

| Symptom | Likely cause | Action |
|---|---|---|
| `PLX-4200`, `PLX-4201`, `PLX-4202` | A page uses a native route, slot or custom action this host build does not register: an older app version, or a registration missing from `PluxConfig` | Slots show a neutral placeholder, the step fails. Register it, or publish the host build's catalogue with `plux native sync` so publishing keeps such builds on a compatible release (`REL-080`). |
| `PLX-4205` | Host code threw: a custom action's handler, a native route's builder or a slot's widget | Contained to the step or the node's boundary; the report names only the exception's type. Reproduce with the host team; Crashlytics or Sentry hold the stack. |
| `PLX-4203` | The host wrote an exposed state entry with a value of another type, or before a release was active | Use the typed accessors `plux codegen` writes (`PluxAppState`). |
| `PLX-4204` | The host's user context carries attributes the app document does not declare, or of another type | Reported by name only; declare them in `userContext`, or stop sending them. |
| Publishing fails with `PLX-1124` | The host registers a custom action with a built-in action's name, which a step of that name would run instead | Rename the host's action and run `plux native scan` again ([typed API guide](../guides/typed-api.md) §3). |
| Some devices stay on an older release after a publish | Their host build's catalogue lacks a native entry the new release uses (`PLX-8054` at publishing) | Expected (`WGT-032`, `REL-080`). Ship the host build that registers it; `ReleaseService.GetCompatibility` lists the builds held back. |

## Generated projects and add-to-app

| Symptom | Likely cause | Action |
|---|---|---|
| A generated project shows an old release on first launch | Its embedded release is the one current when `plux create` ran | It syncs at start (`GEN-005`). Generate again before each store release so fresh installs start recent. |
| A generated app's store build is signed with the debug key | No `android/key.properties` or iOS export options | Follow the project's README, "Release builds"; the CI templates read their secrets by name. |
| An add-to-app screen shows the module's status text, "Plux did not start" | The host passed no `appId` or `endpoint` on the `dev.plux/host` channel, or a malformed root key | Pass the runtime's settings from the native side ([add-to-app](../../apps/add_to_app/README.md)). |
| An iOS add-to-app host crashes when it first opens a page: "Setting a message handler before the FlutterEngine has been run" | The host set a channel handler before `FlutterEngine.run()` | Run the engine first, then set handlers ([host app guide](../guides/host-app.md), add-to-app). |
| An add-to-app page never shows a newer release | A Plux page stays mounted, so no safe point comes | Activation waits until no Plux page is mounted; close the Flutter screen, or use another activation policy. |

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
