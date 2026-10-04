# 0046. State engine: scoped providers, typed writes, computed entries, persistence and migrations

- **Status:** Accepted (maintainer, 2026-10-04, P5 plan §2.1, B1–B2)
- **Date:** 2026-10-04
- **Requirements:** `STA-001`–`STA-004`, `STA-010`, `STA-030`, `STA-040`, `HST-001`, `HST-013`, `HST-021`, `RT-012`, `SCH-012`, `ACT-007`

## Context and problem

[ADR-0008](0008-riverpod-runtime-state-engine.md) chose Riverpod and built the read side in
P3: page state as an immutable map per page instance, and node subscriptions from the
compiler's read sets with `select`, so a node rebuilds only when a value it reads changes
(`RT-012`). P4 made app state live for exposed entries and host writes
([ADR-0023](0023-mixed-screens-slots-and-plux-view.md)). Nothing inside a plugin writes
state yet, computed entries are null, and persistence is reported as unavailable
(`PLX-4010`).

P5 must deliver every scope (`STA-001`), typed writes (`STA-002`), persistence
(`STA-003`), computed entries (`STA-004`), plugin-side writes to exposed state (`STA-030`,
`HST-021`), host events into Plux (`HST-013`) and versioned persisted state (`STA-040`).
The maintainer decided where persisted values live (plan D5, D6): the core's built-in store
([ADR-0049](0049-local-persistence.md)), with `secure` values encrypted under a
per-installation key.

## Decision drivers

- **Fine-grained rebuilds stay** (`STA-010`, `RT-012`): a write rebuilds exactly the nodes
  that read a changed path.
- **Types hold at run time** (`STA-002`): a value of the wrong type never enters state.
- **No I/O on the UI isolate** (L-6): persistence reads and writes run elsewhere.
- **Sensitive values are persisted only encrypted** (`SCH-012`).
- **A release never misreads a value an older release stored** (`STA-040`).
- **No new dependency** (plan D5, D6).

## Considered options

1. **One provider per scope instance**, holding an immutable map, written in batches per
   step, with write-behind persistence on the built-in store.
2. **One provider per state entry.**
3. **Persistence through `shared_preferences` and `flutter_secure_storage`.**

## Decision

Chosen option: **1.** It keeps ADR-0008's read path unchanged, and persistence on the
built-in store needs no new dependency. Option 2 multiplies providers by entries for no
gain, since `select` already narrows rebuilds to a path; option 3 adds two plugins to every
host app and a second storage format beside the built-in store (D5).

### Scopes (`STA-001`, `STA-010`)

| Scope | Provider | Lifetime |
|---|---|---|
| `app` | `appStateProvider` (P4) | The active release; values kept across releases while an entry keeps its ID and type |
| `plugin` | `pluginStateProvider(plugin)` | The active release of the plugin |
| `page` | `pageStateProvider(instance)` (P3), auto-dispose | The page instance |
| `component` | `componentStateProvider(instance)`, auto-dispose | The component instance |
| `run` | None: variables of the `ActionRun` | One run; no widget binds to them |

Bindings keep ADR-0008's rule: a node subscribes once per root with `select` over its read
set, so each root's provider notifies only the nodes that read a changed path.

### Writes (`STA-002`)

- `setState` replaces an entry or a path inside it, `patchState` merges into a map or
  record value, and `resetState` restores the declared default.
- The compiler type-checks every write against the entry's declared type. A value from
  outside the document — an API response, a host write, a host event's payload — is
  checked at run time; the wrong type fails the step with a `validation` error and leaves
  the value unchanged.
- An entry's optional validation, a PXL predicate (an additive field of the state entry,
  whose shape R0's schema change fixes), runs on every write; a failing write is a
  `validation` error.
- The writes of one step are applied together and notify once, so a step that writes three
  paths causes one rebuild of each node that reads them.
- Optimistic writes (`ACT-007`, [ADR-0045](0045-action-engine-completion.md)) use the same
  path with an undo record.
- Computed entries cannot be written; the compiler refuses it.

### Computed entries (`STA-004`)

A computed entry is a provider that evaluates its PXL program and watches the `select` of
its read set, so it is memoised and recomputed only when a value it reads changes. The
compiler refuses a cycle between computed entries. A computed entry that fails at run time
holds null and reports `PLX-4002`, as a binding does.

### Watchers

A state-watcher trigger listens to its path with `ProviderContainer.listen` and a `select`,
outside the widget tree, and starts its graph with the trigger's concurrency policy
(`restart` by default, ADR-0045). Watchers belong to their scope and stop with it.

### Persistence (`STA-003`)

| Kind | Where | Survives |
|---|---|---|
| `memory` (default) | The scope's provider | Nothing beyond the scope |
| `session` | A process-wide map keyed by the entry's scope | Disposal of its page or component, until the process ends |
| `persisted` | The built-in store (ADR-0049) | Restarts |
| `secure` | The built-in store, encrypted with AES-GCM under the per-installation key (ADR-0049) | Restarts |

- Stored values are keyed by plugin, scope and the entry's permanent ID, never its name, so
  renaming an entry keeps its value. Page-scoped values are keyed by route, since page
  instances do not survive a restart.
- Values are loaded off the UI isolate before the first frame of their scope, and written
  behind: a write updates the provider at once and is persisted in a batch, off the UI
  isolate. A persistence failure is reported and never fails the write that caused it.
- An entry tagged `sensitive` must be `memory`, `session` or `secure`; the compiler refuses
  `sensitive` with `persisted`, so a sensitive value is never stored in the clear
  (`SCH-012`). A `secure` value is treated as sensitive in traces and diagnostics.
- The bytes each plugin persists are bounded by a registry limit, fixed in R2; past it a
  write is refused with a typed error and the runtime degrades as `LIM-004` requires.
- `Plux.wipeData` clears persisted and secure state per plugin or for all (`DB-008`,
  ADR-0049).

### Versioned persisted state (`STA-040`)

- A stored value carries the fingerprint of its declared type (a hash of the canonical type
  expression).
- At publish, the compiler compares each persisted or secure entry with the same entry in
  the channel's previous release. An unchanged type passes. A changed type needs either a
  migration expression — PXL from the old type to the new, reading the old value as `old`
  — or an explicit reset; otherwise publishing is refused. The two fields are additive in
  the state entry; R0's schema change fixes their shape.
- On the device, a stored value whose fingerprint matches the current type is used as is;
  one that matches the previous release's type is migrated; any other value, and any value
  whose migration fails, is reset to the default and the reset is reported. A device that
  skips releases therefore resets rather than guessing.
- The comparison needs the previous release's documents, which the server keeps
  (`SRV-020`). If R2 finds it needs data the server does not keep, the API change is put to
  the maintainer (plan §4.3).

### Exposed state and host events (`STA-030`, `HST-021`, `HST-013`)

- Plugin-side writes to exposed entries take the same path as host writes, so
  `Plux.state<T>(name).watch()` sees them, and every view on a screen rebuilds in the same
  frame. Exposed entries may now declare persistence; P4's `PLX-4010` report for them goes.
- `Plux.sendEvent(name, payload)` sends a typed event into Plux. The payload is checked
  against the app document's event declaration; a wrong payload is refused with a typed
  error, and a valid one starts the host-event triggers (ADR-0045).
- `plux codegen` writes typed senders for the declared events and writable handles for
  exposed entries.

## Consequences

- **Positive.**
  - The read path of P3 is unchanged; writes add notifications, not a new mechanism.
  - Values survive renames, and type changes are decided at publish, not discovered on
    devices.
  - Nothing sensitive is stored in the clear.
- **Negative.**
  - Write-behind persistence can lose the last writes if the process is killed before the
    batch is written; `secure` and `persisted` values are flushed when the app pauses to
    narrow the window.
  - A device that skips a release loses values whose type changed twice.
- **Follow-up.**
  - R2 builds the scopes, writes, computed entries, watchers, persistence, migrations,
    `Plux.sendEvent` and the codegen additions.
  - P6's `SEC-073` wraps the per-installation key with secure hardware per profile.

## Options in detail

### Option 2: a provider per entry

Each entry would be its own provider, and a binding would watch the entries it reads. The
number of providers grows with entries times instances, page creation slows, and batches
of writes across entries notify more often. `select` over one map per scope already gives
path-level rebuilds.

### Option 3: `shared_preferences` and `flutter_secure_storage`

Both are maintained and well known, but they add two platform plugins to every host app,
store values in formats Plux does not control, and leave two storage systems beside the
built-in store that the outbox and cache need anyway (D5). Wiping a plugin's data would
span three stores.
