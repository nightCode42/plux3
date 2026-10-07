<!--
SPDX-FileCopyrightText: 2026 Plux contributors
SPDX-License-Identifier: Apache-2.0
-->

# Changelog

## 0.3.0

The runtime of Plux Phase 5: actions, state, data, local database and animation. Not
released yet.

- The action engine is complete (P5 R1, plan §5.1): every concurrency policy (`parallel`,
  `drop`, `restart`, `queue`, `debounce`, `throttle`) with per-trigger defaults; detached
  runs that outlive their page; retries with exponential backoff, jitter and a
  retryable-error filter; optimistic updates rolled back with a failed step or run; `forEach`
  bounded by `action.forEachItems`; `parallel`, whose first error cancels the other lanes;
  `callFlow` with flows of other plugins; `switch`, `delay`, `trackEvent`, `sync` and
  `emitEvent`.
- Triggers besides widget events: timers, state watchers, app lifecycle, push opening, host
  events and data-source events, through a `TriggerHub` that later milestones feed; errors no
  `onError` handled go to the page's, the plugin's and the app's error handlers, and
  otherwise show a built-in message.
- Every run is traced into a bounded ring buffer (`action.traceRuns`, `action.traceSteps`)
  with its steps, status and errors; values are recorded only in debug builds and never when
  sensitive; each run is a timeline task in DevTools.
- Bundles may require `actions.triggers.v1`, `actions.concurrency.v1`, `actions.retry.v1`,
  `actions.flows.v1` and `actions.control.v1`, which this runtime is the first to support.
- Data layer I (ADR-0048): REST and GraphQL sources and operations on the
  runtime's HTTP client, on a data isolate; base URLs per environment; the
  auth delegate's token, refreshed once on `401`; every request checked
  against the plugin's network domains first (`PLX-5100`); size and time
  limits from the registry; mapping by selectors and PXL transforms; the
  `networkOnly`, `cacheFirst`, `networkFirst` and `staleWhileRevalidate`
  cache policies with optional AES-GCM encryption; cursor, page and offset
  pagination bound to lists through `data.<name>.status` and `hasMore`;
  `apiCall` and `refreshData` (with `more`); mock states in tests and
  development builds; `api_call` telemetry without payloads. Requires the
  feature `data.v1`.
- PXL `matches` (`pxl.regex.v1`): a bounded, linear-time RE2 subset (schema/pxl/regex.md)
  on the runtime's own Pike VM, identical to the compiler's; patterns are literals checked
  at publish time (`PLX-2020`–`PLX-2022`) and bounded by `pxl.regexPatternLength`,
  `pxl.regexProgramSize` and `pxl.regexRepeat`.
- PXL `isPhone` (`pxl.phone.v1`): phone validation by region from libphonenumber v9.0.40
  metadata (schema/pxl/phone.md); `format.phone` moves to the `format` group (P8).
- Form validators (`STA-020`, internal until forms land): required, length, range, regex,
  e-mail, phone by region, IBAN, date range and decimal precision.
- State engine (P5 R2): scopes `app`, `plugin`, `page`, `component` and `run` as Riverpod
  providers with `select`-level rebuilds; `setState`, `patchState` and `resetState` with
  typed writes (`PLX-5301`, `PLX-5302`); computed entries evaluated only when what they
  read changes; `session`, `persisted` and `secure` entries in a built-in store encrypted
  with AES-256-GCM under per-installation keys kept by the platform's secure storage, off
  the UI isolate, versioned by type fingerprint and migrated (`PLX-5303`–`PLX-5306`); plugin
  writes to exposed app state reach `Plux.state<T>`; `Plux.sendEvent` (`PLX-5307`) and
  `Plux.wipeData`; features `state.write.v1`, `state.computed.v1`, `state.persistence.v1`.
- Integration of R1, R2 and R4: state watchers listen to the state engine's change stream;
  `Plux.sendEvent` reaches the host-event triggers; data-source loads and failures fire
  `onLoaded` and `onFailed`; the app's and the plugins' triggers and error handlers run with
  their release, and an unhandled error goes from the page to the plugin to the app;
  `emitEvent` reaches the component instance's handler or `PluxView.onEvent`; a component's
  runs are cancelled with it; `apiCall`'s and `patchState`'s optimistic changes roll back on
  failure; the encrypted data cache gets a per-installation key; the `logout` action and
  `Plux.wipeData` clear cached responses.
- `persisted` state is kept in a plain, atomically written file (plan D6, maintainer
  decision): encryption made saves and loads about ten times slower; `secure` state stays
  encrypted.
- Forms (P5 R3, `STA-020`, ADR-0047): pages and components declare forms of typed fields
  whose state (`values`, `errors`, `dirty`, `touched`, `status`, `valid`, `validating`)
  lives in their scope; eleven validator kinds, among them regex (`pxl.regex.v1`), phone by
  region (`pxl.phone.v1`), IBAN, custom PXL rules and debounced asynchronous checks that
  discard stale results; `validateForm`, `submitForm` and `resetForm`. Requires `forms.v1`.
- `FormScope` scopes a declared form to its subtree (`PLX-1240` for an unknown form);
  `submitForm` follows its `invalid` branch when the step wires one.
- Data layer II (P5 R5, ADR-0048): WebSocket, Server-Sent Events and GraphQL subscription
  streams bound to state, with reconnection, capped backoff and resubscription; an encrypted
  offline outbox for `offlineCapable` mutations, replayed in order with idempotency keys on
  resume or reconnection, with its success, failure and conflict events; uploads and
  downloads with progress, cancellation and size limits. Every limit is a registry entry.
- `PluxConfig.webSocketClient` creates the `dart:io` client WebSockets connect with, for a
  host that trusts its own certificate authority; `httpClient` does not reach WebSockets.
- An `http` failure keeps its status for the step's error (`steps.<id>.error.status`).
- Local database (P5 R6, ADR-0049): the `PluxDatabaseAdapter` interface; the built-in store
  behind persisted state, the key-value store (`kvGet`, `kvSet`, `kvRemove`), the response
  cache and the outbox; collections with `dbQuery`, `dbInsert`, `dbUpdate`, `dbUpsert` and
  `dbDelete`, watched queries as data sources whose list items are reused by key, and
  `PluxConfig.databaseAdapter` for `plux_db_drift` or the host's own adapter.
- Animation (P5 R7, ADR-0050): implicit animation of bound props, enter and exit
  transitions, Hero between Plux and native pages, timelines with keyframes, staggering and
  `startAnimation`/`controlAnimation`, the platform's reduce-motion setting, custom route
  transitions from timelines (`NAV-010`), and native slots for `plux_lottie` and
  `plux_rive`.
- Device and feedback actions (P5 R8, ADR-0051): `showSnackbar`, `showToast`, `haptic`,
  `copyToClipboard`, `openUrl`, `share`, `requestPermission`, and through
  `PluxConfig.devicePackages` the pickers, camera, scanner and location of `plux_media`,
  `plux_scanner` and `plux_location`; every operation a plugin did not declare in its
  capabilities, or the app did not approve, is blocked and reported (`SEC-080`).
- `Plux.sendEvent` checks a payload against the host event the app declares (`PLX-5307`).
- Start-up opens only the bundles that may declare triggers, and a bundle carries only the
  limits it overrides.

## 0.2.0

The runtime of Plux Phase 4: actions and routing. Not released yet; this entry grows with
each milestone of the phase.

- Actions run (ADR-0039): widget events start runs of their action graphs on a bounded,
  cancellable engine that routes failures to `onError` and never lets an error reach host
  code. It runs `navigate`, `pop`, `openDialog`, `openBottomSheet`, `switchTab`,
  `callNative`, `condition`, `emitHostEvent` and `stop`; every other action fails its step
  with `PLX-4010`.
- Navigation by route name (ADR-0040): `Plux.open<T>` returns the page's typed result and
  presents dialog and sheet pages as such; `Plux.pageFor` builds a `PluxPage` for Navigator
  2.0 pages lists; `PluxShell` shows a tabbed shell with one stack per tab; page transitions,
  with Android's predictive back; parameters checked on entry (`PLX-4101`); unknown routes
  show the not-found page (`PLX-4100`). `PluxConfig.navigationDelegate` is the seam every
  navigation goes through, and `PluxConfig.notFoundBuilder` the host's not-found page.
- `Plux.events`: the typed events plugins emit (`PluxHostEvent`).
- Route guards (ADR-0040): before a page is entered, its plugin's kill switch, the
  assurance level it requires and its guard graphs decide whether it opens, redirects or
  shows its fallback (`PLX-4102`). Pages that require an assurance level above `AL0` fail
  closed until attestation arrives. Bundles with guarded pages require
  `navigation.guards.v1`, which this runtime supports.
- `Plux.handleDeepLink` and `Plux.handlePushPayload` open the pages links and
  notifications name, through the app document's deep links and push key and the route's
  guards, on the navigator of the new `PluxConfig.navigatorKey`; a link nothing maps
  reports `PLX-4103`.
- `Plux.setUserContext` attributes are typed by the app document's `userContext`;
  undeclared or ill-typed ones are left out and reported (`PLX-4204`). The user's ID is no
  longer readable as `user.id`, which the compiler never accepted.
- Native routes, slots and custom actions (ADR-0041): `PluxConfig.nativeRoutes`,
  `nativeSlots` and `nativeActions` register what plugins use, with
  `PluxNativeRoute<P, R>`, `PluxNativeSlot` and `PluxNativeAction<I, O>`; every value
  crossing into host code is checked against the app bundle's native catalogue both ways,
  and a failing host handler reports `PLX-4205`. `PluxConfig.router` takes a router
  adapter (`PluxRouterAdapter`), such as `plux_go_router`'s or `plux_auto_route`'s.
- For router adapters: `Plux.resolveLocation` resolves the route a URL names through its
  guards, `PluxShell.routed` shows a shell's tab bar around a router's tab stacks, and
  `PluxShellTab` shows a tab's first page.
- Registry value types reaching expressions, such as a graph's `GuardResult`, are keyed by
  their field names; they were keyed by mismatched strings.
- Breaking: `PluxAuthDelegate` gains `isAuthenticated`, which PXL reads as
  `user.authenticated`.
- `PluxView` of an unknown route reports `PLX-4100` (was `PLX-8031`).
- Mixed screens (ADR-0023): `PluxView` shows a page or an exported component by name, with
  `onEvent` (`PluxViewEvent`; an inline page's `pop` arrives as the `pop` event) and
  `sizing` (`PluxViewSizing.intrinsic`, `expand`, `fixed`). Breaking: its `route` and
  `params` are now `name` and `inputs`.
- Exposed app state (ADR-0023): `Plux.state<T>(name)` reads, writes and watches an
  exposed entry of the app document, type-checked (`PLX-4203`); plugin bindings read app
  state as `app.<name>` and rebuild when the host writes it. `Plux.eventsNamed` filters
  host events by name, and `Plux.flag<T>` reads a feature flag of the active release.

## 0.1.0

The first release: the runtime of Plux Phase 3, rendering.

- `Plux.initialize`, `Plux.sync`, `Plux.open`, `PluxView`, `PluxScope`, `PluxSyncTile`
  and the host setters for locale, theme mode, brand, consent, user and auth.
- Sync of every plugin at app start: deltas, resumable parallel downloads, atomic
  activation under startup and activation policies, last known good, disk quota,
  embedded baselines. An unchanged release costs one manifest request of a few hundred
  bytes: the device sends a digest of its installed bundles.
- Verification before loading: signed manifests, bundle hashes, anti-rollback, a
  generated FlatBuffers verifier and first-use section checks.
- Rendering of the Phase 3 widget set from generated builders, with bindings, overrides,
  semantics, placeholders and error boundaries with themed fallbacks, set per app or per
  plugin; theming, assets, icons and SVG.
- Telemetry by consent, batched and buffered offline.
