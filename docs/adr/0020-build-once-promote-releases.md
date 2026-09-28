# 0020. Build-once-promote app releases as the unit of activation

- **Status:** Accepted
- **Date:** 2026-09-27
- **Requirements:** `REL-001`–`REL-007`, `REL-030`, `REL-033`, `REL-080`, `REL-081`, `GOV-010`, `CMP-002`

## Context and problem

Any plugin page may navigate to any other plugin's page, and plugins share the app's theme,
translations, components and state schemas. A device that activated plugin versions one at
a time would sooner or later hold a set that was never validated together: a page
navigating to a route that the other plugin removed in the version it happens to have. What
is the unit that a device activates, and how does it move between environments?

## Decision drivers

- A device must never hold a combination of plugin versions that was not validated as a set
  (`REL-003`).
- What was tested in staging must be bit-for-bit what runs in production (`REL-004`).
- Rollback must not weaken anti-rollback protection on the device (`REL-006`, `SEC-055`).
- The manifest must be a pure function of its inputs (`REL-033`).
- Deltas must remain computable from anything a device may still have installed
  (`REL-007`, `REL-020`).

## Considered options

1. **An immutable app release — exactly one version per active plugin plus one app bundle — built once and promoted unchanged between environments.**
2. Per-plugin activation, with compatibility ranges declared between plugins.
3. Rebuilding for each environment from the same sources.

## Decision

Chosen option: **1**, as the specification requires (`REL-002`, `REL-004`).

### The model

Publishing freezes a plugin draft at a revision into an immutable **plugin version**,
numbered by a monotonically increasing integer per plugin, optionally labelled and annotated
(`REL-001`). An **app release** is an immutable set of exactly one version per active
plugin plus one app bundle, numbered by a monotonically increasing sequence per app
(`REL-002`). Devices activate releases; nothing else.

Creating a release runs the cross-set validation that a single publish cannot: every
cross-plugin link, every native route reference against the targeted host catalogues, every
shared state and collection schema, and every function reference. A release with an
unresolved reference is not created (`REL-003`).

### Promotion

A release is built once and **promoted** between environments — development → staging →
production — by pointing an environment's channel at it. Promotion copies no bytes and
recompiles nothing: the artifacts are content-addressed and already stored, so what runs in
production is the object that was tested (`REL-004`). Each environment has its own
channels, default `production` plus any others such as `beta` or `internal`; a device
follows exactly one (`REL-005`).

Environments differ in their variables, secrets, data-source URLs and signing keys
(`GOV-010`). Those are inputs the manifest resolves per environment, not reasons to
recompile: the bundle carries flag and variable *names*, and the manifest carries the
values the device may see.

### Rollback

Rolling back publishes a **new** release sequence whose content equals an earlier release —
the same plugin versions, the same app bundle, a higher sequence number (`REL-006`). The
device's anti-rollback rule ("never accept a sequence lower than the highest accepted for
this channel", `SEC-055`) therefore never has to be relaxed, and the rollback is visible in
the release history instead of being an absence.

### Determinism

Because the compiler is deterministic (`CMP-002`) and a release is a set of hashes, the
manifest is a pure function of the release, the channel and the device's declared inputs:
the same inputs always produce the same manifest bytes, which is what makes the ETag of
`REL-031` sound and what `REL-033` requires. Rollout, targeting and experiment rules (P9)
are evaluated server-side as part of those inputs, never as a source of randomness per
request.

### Retention and compatibility

Every plugin version and app release referenced by any release inside the retention window
is kept — for ever in production, 90 days in development by default — so a device that has
been offline for a year can still be given a delta rather than a full download (`REL-007`,
`REL-020`). Each release records which runtime versions and host builds can use it, and a
device that cannot use the newest release is given the newest one it can (`REL-080`). Each
release carries an auto-generated changelog — pages added, removed and changed, actions and
data sources changed, translations changed — alongside human notes (`REL-081`).

## Consequences

- **Positive:** a device can only ever hold a validated set; staging and production run identical bytes; rollback is an ordinary release; the manifest is cacheable because it is deterministic; deltas stay computable from any retained version.
- **Negative:** a one-line fix in one plugin still creates a new app release, so release numbers move quickly and the release list needs good filtering; retention "for ever in production" grows storage, bounded only by content addressing and deduplication.
- **Follow-up:** staged rollouts, targeting, health gates and the kill switch (P9) operate on this unit; approvals (P9) bind to the release's content hash (ADR-0019).

## Options in detail

### Option 2 — per-plugin activation with compatibility ranges

Lets one plugin ship without the others, which is attractive for large apps. It requires
every cross-plugin reference to carry a version range, every device to solve a constraint
problem offline, and the server to validate a combinatorial set rather than one. It moves a
failure that is currently impossible into the runtime, on the device, at navigation time.
Rejected: `REL-003` exists to make that failure unreachable.

### Option 3 — rebuild per environment

Would let environment-specific values be baked in, and would break the one guarantee that
matters: production would run bytes that were never tested. It also makes the signature of
a staging artifact worthless in production. Rejected.

## Implementation notes (P2)

- **Sources.** A version records every draft snapshot it was compiled from. Publishing a plugin
  compiles its draft, frozen at the requested revision, with the app-level documents and every
  other plugin at their newest published versions — or their current drafts, for those never
  published — so a version is always compiled against what a release would hold. Every snapshot a
  version was compiled from is kept for as long as the version exists (`SRV-031`).
- **Consistency by recompilation.** Creating a release compiles the chosen versions' sources
  together (`REL-003`) and requires every bundle to come out byte-identical to the one its version
  stored (`CMP-002` makes that a sound test). A plugin version compiled against other app-level
  documents or assets than the release's is refused with `PLX-8050`, asking for it to be published
  again; so a release never pairs bundles that were not built together.
- **Keys.** A version is signed with the key of the environment it was published in; manifests
  (N7) are signed with the key of the environment that serves them, so promotion still copies no
  bytes. The app bundle is published like a plugin, with an empty plugin key.
- **Retention.** A release never promoted to a production environment, current on no channel and
  not its app's newest is deleted after `retention.developmentReleaseDays`; versions no remaining
  release holds, except each plugin's newest, go with them, and their snapshots return to ordinary
  history retention (`REL-007`).
