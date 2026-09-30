<!--
SPDX-FileCopyrightText: 2026 Plux contributors
SPDX-License-Identifier: Apache-2.0
-->

# Changelog

## 0.1.0

The first release: the runtime of Plux Phase 3, rendering.

- `Plux.initialize`, `Plux.sync`, `Plux.open`, `PluxView`, `PluxScope`, `PluxSyncTile`
  and the host setters for locale, theme mode, brand, consent, user and auth.
- Sync of every plugin at app start: deltas, resumable parallel downloads, atomic
  activation under startup and activation policies, last known good, disk quota,
  embedded baselines.
- Verification before loading: signed manifests, bundle hashes, anti-rollback, a
  generated FlatBuffers verifier and first-use section checks.
- Rendering of the Phase 3 widget set from generated builders, with bindings, overrides,
  semantics, placeholders and error boundaries with themed fallbacks, set per app or per
  plugin; theming, assets, icons and SVG.
- Telemetry by consent, batched and buffered offline.
