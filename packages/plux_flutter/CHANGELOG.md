<!--
SPDX-FileCopyrightText: 2026 Plux contributors
SPDX-License-Identifier: Apache-2.0
-->

# Changelog

## 0.3.0 (unreleased)

The runtime of Plux Phase 5. Not released yet; this entry grows with each milestone.

- PXL `matches` (`pxl.regex.v1`): a bounded, linear-time RE2 subset (schema/pxl/regex.md)
  on the runtime's own Pike VM, identical to the compiler's; patterns are literals checked
  at publish time (`PLX-2020`–`PLX-2022`) and bounded by `pxl.regexPatternLength`,
  `pxl.regexProgramSize` and `pxl.regexRepeat`.
- PXL `isPhone` (`pxl.phone.v1`): phone validation by region from libphonenumber v9.0.40
  metadata (schema/pxl/phone.md); `format.phone` moves to the `format` group (P8).
- Form validators (`STA-020`, internal until forms land): required, length, range, regex,
  e-mail, phone by region, IBAN, date range and decimal precision.

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
