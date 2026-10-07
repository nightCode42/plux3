# 0049. Local persistence: one adapter interface, a built-in store in the core, Drift with SQLCipher for collections

- **Status:** Accepted (maintainer, 2026-10-04, P5 plan §2.1, B1–B2)
- **Date:** 2026-10-04
- **Supersedes:** the planned ADR-0016 (spec §32), whose decision this record makes (P5 plan §2.1, B5)
- **Requirements:** `DB-001`–`DB-009`, `STA-003`, `DAT-010`, `DAT-020`, `SCH-012`, `SEC-073`, `HST-010`, `RT-060`, `RT-061`, `LIM-004`

## Context and problem

`DB-001` puts all local persistence behind a `PluxDatabaseAdapter` interface; `DB-002`
makes Drift on SQLite the default adapter, with SQLCipher available and required under the
`strict` and `maximum` profiles; `RT-060` puts Drift in the optional package
`plux_db_drift`. Yet every app needs some persistence even without collections: persisted
and secure state (`STA-003`), the key-value store (`DB-009`), the response cache
(`DAT-010`) and the outbox (`DAT-020`).

The maintainer decided (plan D5, D6, accepted in B2; spec 1.3.0 rewords `DB-001`):

- the core holds a built-in store that implements `PluxDatabaseAdapter` for state,
  key-value, cache and outbox; collections use `plux_db_drift` by default or a host
  adapter;
- keys are generated per installation and wrapped by the runtime's existing Keystore and
  Keychain secret storage, with AES-GCM from `cryptography`, already a dependency;
- `secure` state and the outbox are always encrypted, the cache when a source asks for it;
- `plux_db_drift` uses SQLCipher, and P5 already refuses a plain database under `strict`
  and `maximum`; P6 adds the rest of `SEC-073` (keys wrapped by secure hardware per
  profile).

`DB-003`'s ObjectBox, Hive CE and Sembast adapters are deferred to a later phase (B1); its
`MUST` half, a host-supplied adapter, is delivered.

## Decision drivers

- **One interface** for every kind of local data, so wiping, limits and tests apply once
  (`DB-001`, `DB-008`).
- **Nothing heavy in the core** (`RT-060`, `RT-061`): SQLite and SQLCipher only for apps
  that declare collections.
- **Off the UI isolate** (`DB-007`, L-6).
- **Encrypted where it must be** (`SCH-012`, `STA-003`, `DAT-020`, `DB-002`), with keys
  never stored in the clear.
- **Schema changes decided at publish** (`DB-005`), never discovered on a device.

## Considered options

1. **A built-in store in the core for the core's needs; Drift with SQLCipher in
   `plux_db_drift` for collections; one adapter interface for both.**
2. **Every persistent feature requires `plux_db_drift`.**
3. **SQLite in the core.**

## Decision

Chosen option: **1** (plan D5). Option 2 makes every app that persists one setting ship
SQLite and SQLCipher, several megabytes per ABI. Option 3 puts the same weight into every
host app, against `RT-061` and the IPA budget.

### The interface (`DB-001`)

`PluxDatabaseAdapter` is part of `plux_flutter`'s public API:

- collections: create, migrate and drop, from the bundle's declarations;
- typed queries: filter, sort, limit and offset, and watched queries as streams;
- writes: insert, update, upsert and delete, inside transactions;
- key-value records per namespace;
- wiping a namespace or everything.

One shared adapter test suite runs against every adapter — the built-in store,
`plux_db_drift` and the reference custom adapter in the tests — as the router adapters
share the delegate suite ([ADR-0040](0040-navigation-delegate-and-router-adapters.md)).

### The built-in store (core)

- It holds persisted and secure state ([ADR-0046](0046-state-engine.md)), the key-value
  store, the response cache and the outbox ([ADR-0048](0048-data-layer.md)). It does not
  hold collections: a release whose plugin declares collections needs `plux_db_drift` or a
  host adapter.
- Records are grouped per plugin and kind, each group a file in the app's private storage,
  written atomically (a new file, then a rename) in a background isolate. The format is
  versioned and private to the runtime; R2 fixes it, and changing it later is a stop for
  the maintainer, as the release store's layout is (`packages/AGENTS.md`).
- Its size is bounded by registry limits; it evicts cache entries first and refuses new
  outbox entries with a typed error when full (`LIM-004`).

### Encryption at rest (plan D6)

- **The key.** A random 256-bit key is generated per installation on first use and stored
  through the runtime's existing secret storage, which encrypts it under a key held by the
  Android Keystore or the iOS Keychain ([ADR-0029](0029-on-device-verification.md),
  `PluxFlutterPlugin`). It is held in memory while the runtime runs and never written in
  the clear.
- **The cipher.** AES-256-GCM from `cryptography`, already a dependency, with a random
  96-bit nonce per record. The record's plugin, kind and key are the associated data, so a
  record moved to another place fails to decrypt.
- **What is encrypted.** `secure` state and the outbox, always; cache entries when their
  source asks or their type holds a `sensitive` field; collections through SQLCipher
  (below). `persisted` state that is not sensitive is stored plain.
- **A lost key** — a restore from backup without the platform key, for instance — makes
  encrypted records unreadable. They are discarded and reported, never fatal.
- P6's `SEC-073` wraps the key with secure hardware per profile; until then the platform
  key store is the wrapper.

### `plux_db_drift` (`DB-002`, `DB-004`, `DB-006`, `DB-007`)

- **Drift on SQLite, without code generation.** Collections arrive in bundles, so their
  tables do not exist when the host app is built. The adapter uses Drift's runtime API
  (custom statements and selects with the tables they read), which keeps Drift's isolate
  management and its invalidation of watched queries, and needs neither `drift_dev` nor
  `build_runner` in host apps.
- **Isolate.** The database runs in Drift's background isolate; only results cross to the
  UI isolate (`DB-007`).
- **Namespaces.** Plugin-private collections are tables prefixed by the plugin's key, and
  app-shared collections by the app's, so a plugin's queries can name only its own private
  tables; the compiler refuses a reference to another plugin's private collection
  (`DB-004`).
- **Watched queries** deliver lists keyed by primary key; the list binding compares keys
  and values, so only changed items rebuild (`DB-006`). The 1,000-row watch benchmark
  measures `DB-007` in the Dart job, and on the reference device per plan D3.
- **SQLCipher.** The database is opened with a key derived from the per-installation key.
  Under `strict` and `maximum` the adapter refuses to open a plain database (`DB-002`).
- **Size.** The package's cost per ABI is measured when R6 adds it and recorded in the size
  journey; it is not counted against the core's budget.

### Migrations (`DB-005`)

- At publish, the compiler compares each collection with the channel's previous release.
  New collections, new nullable or defaulted fields and new indexes migrate
  automatically. Dropping a field or a collection, or narrowing a type, needs an explicit
  migration plan in the document and a warning the publisher acknowledges, through P4's
  acknowledgement of publish warnings.
- The release carries numbered migrations. On the device they run in one transaction
  before the plugin's collections are used. A failure rolls back, reports `PLX-5200`, and
  leaves the collections at their previous version; sources reading them fail with typed
  errors until a release with a working migration arrives, while the rest of the plugin
  runs.

### Wiping and the key-value store (`DB-008`, `DB-009`, `HST-010`)

- `Plux.wipeData()` and `Plux.wipeData(plugin:)` close and delete the plugin's collections,
  state, key-value records, cache and outbox, or everyone's, and cancel the detached runs
  of the plugins they wipe ([ADR-0045](0045-action-engine-completion.md)).
- The `logout` action signals the host through its auth delegate; the host decides whether
  to wipe.
- `kvGet`, `kvSet` and `kvRemove` read and write typed values per plugin in the built-in
  store, the type fixed by the action's type argument at compile time.

### Custom adapters (`DB-003`)

`PluxConfig.databaseAdapter` takes a host adapter, for example one over the host's existing
database. It must pass the shared suite. The ObjectBox, Hive CE and Sembast adapters are
deferred (B1).

### Missing adapters

A plugin with collections on a host without `plux_db_drift` or a custom adapter fails its
collection sources and actions with a typed "adapter missing" error. `plux native scan`
records the Plux packages a host build installs, so publishing warns about a release whose
plugins need a package a targeted build lacks
([ADR-0051](0051-device-actions-packages-and-capabilities.md)).

### Dependencies (approved by the maintainer, 2026-10-04, B2)

Each is checked before use, as `dependencies.md` §1 requires, and its version, maintenance
and size are recorded here when R6 adds it.

| Library | Licence | Notes |
|---|---|---|
| `drift` (publisher simonbinder.eu) | MIT | Runtime API only; no `drift_dev` |
| `sqlite3` (same author) | MIT | Dart bindings to SQLite; SQLite itself is in the public domain |
| A SQLCipher build: `sqlcipher_flutter_libs`, or the build-hook option of the pinned `sqlite3` major version | MIT (the package); SQLCipher Community Edition is under a BSD-style licence | R6 checks which of the two the pinned `sqlite3` version supports and maintains, and records the licence of SQLCipher's crypto provider on Android before use |

## Consequences

- **Positive.**
  - Apps without collections pay nothing for SQLite.
  - One interface, one wipe, one test suite for every kind of local data.
  - Sensitive data is encrypted with a key that never touches disk in the clear.
- **Negative.**
  - The built-in store is the runtime's own storage format, which it must keep readable
    across versions.
  - Drift without code generation gives up Drift's typed tables; the adapter's tests carry
    that weight.
  - A plugin with collections needs the host to add a package, which publishing checks.
- **Follow-up.**
  - R2: the built-in store and the key for state.
  - R5: cache and outbox on the store.
  - R6: the interface's collection half, `plux_db_drift`, migrations, the database
    actions, `Plux.wipeData`, the key-value actions, custom adapters.
  - P6: `SEC-073`.
  - Later: the deferred adapters of `DB-003`.

## Options in detail

### Option 2: `plux_db_drift` for everything

Simple to explain, but a counter persisted in state would bring SQLite and SQLCipher into
the app, and the outbox and cache — needed by any app with data sources — would make the
optional package mandatory in practice.

### Option 3: SQLite in the core

SQLite with SQLCipher adds several megabytes per ABI. The core has about half a megabyte
left under the IPA budget (size journey, round 3), and most apps would not use the
database.

## Revision (2026-10-07, P6 plan)

Key wrapping per profile is revised by ADR-0058: under `strict` and `maximum` every local store — database, response cache, outbox, `persisted` state and key-value store — is encrypted with keys wrapped by secure hardware and unwrapped once per launch. P5's B6 (plain `persisted` under every profile) is superseded for those profiles.
