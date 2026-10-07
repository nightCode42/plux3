# 0053. Remote security configuration: one settings source, versions and signed deltas

- **Status:** Proposed (P6 plan §2.1 B5, B14; §2.2 Q2, Q3)
- **Date:** 2026-10-07
- **Requirements:** `SEC-180`, `SEC-181`, `SEC-182`, `SEC-030`, `NFR-006`, `LIM-001`

## Context and problem

Security behaviour depends on settings (key policy, assurance thresholds, outage grace,
RASP responses, encryption, screenshot blocking, inactivity lock). The maintainer requires
every setting to be configurable per app and environment, with defaults built into the
runtime and the server, and changes delivered on the manifest request the device already
makes, at no cost when nothing changed (`NFR-006`'s up-to-date check).

## Decision drivers

- Device and server defaults can never disagree.
- Configuration cannot be altered in transit or rolled back.
- No extra request at app start; nothing sent when nothing changed.
- Operators can tighten, never loosen, a profile (Q3).

## Considered options

1. **One schema (`schema/security/settings.json`), generated Go and Dart defaults, a
   monotonic version per app and environment, merge-patch deltas bound to the signed
   manifest.**
2. A separate configuration endpoint polled by the device.
3. Configuration embedded in each bundle.

## Decision

Chosen option: **1**.

- **Settings model.** Each setting has a key, type (`bool`, `seconds`, `count`, `enum`),
  ordered values for enums, hard bounds, the direction that is tighter, a preset per
  profile (`standard`, `strict`, `maximum`), and whether it travels to the device. `make
  gen` generates `backend/internal/security/settings` and the runtime's `settings.g.dart`
  (travelling settings only) from it, so defaults agree by construction.
- **Effective configuration** = the profile's preset, then the operator's overrides, then
  tightenings from the app document's `security.settings` (and, for page-level settings,
  plugins and pages, `SEC-181`). Every override must be within bounds (`PLX-6042`) and at
  least as tight as the preset (`PLX-6041`). Loosening means choosing a lower profile,
  which is audited and shown in each release's effective configuration (Q3).
- **Versions, not timestamps.** Each change to an app and environment's effective
  configuration increments `version` (a database sequence, compare-and-set on
  `expected_version`). Clocks disagree and two edits can share a second; a counter cannot.
- **Delivery.** `GetManifestRequest.config_version` carries the version the device holds.
  The signed manifest carries `config: {version, sha256}` of the canonical (JCS) device
  configuration. If the device's version is current, nothing more is sent. Otherwise the
  response, outside the signed part, carries `config_patch`, an RFC 7396 merge patch from
  the device's version to the current one when the server still has that version, else
  `config_full_required` with the full device configuration. The device applies the patch,
  canonicalises, checks the hash against the signed manifest (`PLX-6040` on mismatch:
  discard, keep the old configuration, report), then stores it with the manifest.
  Rollback is impossible because the manifest is anti-rollback protected (`SEC-055`).
- **Server-only settings** (token lifetime, nonce rotation, `iat` window, replay fallback,
  verdict and risk thresholds) never travel.
- **Sizes** are registry limits (`securityConfig.bytes`, `securityConfig.patchBytes`).
- **Visibility.** `GetEffectiveSecurityConfig` and `plux security config get` show the
  effective configuration with each value's origin; each release's metadata records the
  version and hash in force when it was published (`SEC-180`; Studio in P11).
- **Direct data sources (Q2).** `allowDirectDataSources` is one of the settings: allowed
  under `standard` and `strict`, where the compiler warns if a direct source requires an
  assurance level; refused under `maximum` (`PLX-6063`).

## Consequences

- **Positive:** zero cost when unchanged; tamper-evident; one source for every default.
- **Negative:** the server keeps recent configuration versions to compute patches (bounded:
  older versions get the full configuration).
- **Follow-up:** S4 builds the store, the API, the CLI, the manifest wiring and the
  runtime's application; settings of later phases join their preset in their phase (B14).

## Options in detail

### Option 2: a configuration endpoint

An extra round trip at app start, a second freshness and anti-rollback mechanism, and a
second thing to sign.

### Option 3: configuration in bundles

Changing a setting would need a new release of every plugin, and settings are per
environment while bundles are shared.
