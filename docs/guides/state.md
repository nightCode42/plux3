<!--
SPDX-FileCopyrightText: 2026 Plux contributors
SPDX-License-Identifier: Apache-2.0
-->

# State Guide

How a Plux project holds the values its pages show and its actions change: typed entries in
five scopes, computed entries, watchers, persistence that survives restarts and releases,
and state shared with the host app. The examples are the `state` conformance project
([`schema/testdata/documents/state`](../../schema/testdata/documents/state)). The design is
[ADR-0046](../adr/0046-state-engine.md).

## 1. Declare an entry

Every entry has a name, a type and a default (`STA-002`). Where it is declared decides who
sees it and how long it lives (`STA-001`):

| Scope | Declared in | Read as | Lives |
|---|---|---|---|
| `app` | `app.json` | `app.<name>` | As long as the release, shared by every plugin |
| `plugin` | `plugin.json` | `plugin.<name>` | As long as the plugin's release |
| `page` | a page | `page.<name>` | One page instance |
| `component` | a component | `component.<name>` | One component instance |
| `run` | an action graph | `run.<name>` | One action run |

```json
"state": [
  {"name": "counter", "type": "int", "default": 0, "persistence": "persisted", "exposed": true},
  {"name": "token", "type": "string", "default": "", "persistence": "secure", "sensitive": true}
]
```

A binding rebuilds only when the exact path it reads changes (`STA-010`), so a page that
shows `app.counter` does not rebuild when `app.theme` changes.

## 2. Write

`setState` replaces an entry or a path inside it, `patchState` merges into a record or map,
and `resetState` restores the default. The compiler checks every write against the entry's
type. A value from outside — an API response, the host — is checked when it arrives, and a
wrong one fails the step with a `validation` error and leaves the entry as it was:

```json
{"id": "add", "action": "setState",
 "input": {"path": "app.counter", "value": {"$expr": "app.counter + run.step"}}}
```

The writes of one step notify once, so a step that writes three paths causes one rebuild.

## 3. Compute and watch

A computed entry is PXL over other entries (`STA-004`). It is memoised, recomputed only when
something it reads changes, and cannot be written:

```json
{"name": "total", "type": "int", "computed": {"$expr": "page.count + app.counter"}}
```

A watcher runs a graph when a path changes. A newer change restarts a run still going, so
the latest value wins:

```json
"triggers": {"watch": [{"path": "app.pings", "handler": {"steps": [
  {"id": "copy", "action": "setState", "input": {"path": "page.mirror", "value": {"$expr": "event"}}}]}}]}
```

## 4. Keep values

`persistence` says how long a value lasts (`STA-003`):

| Persistence | Survives |
|---|---|
| `memory` (default) | Nothing beyond its scope |
| `session` | Its page or component, until the app is killed |
| `persisted` | Restarts; a plain file written atomically |
| `secure` | Restarts; encrypted, with the key in Keystore or Keychain |

Values are stored by the entry's permanent ID, so renaming an entry keeps its value. A
`sensitive` entry must be `memory`, `session` or `secure`; the compiler refuses
`sensitive` with `persisted`, and sensitive values never appear in traces or logs
(`SCH-012`). `Plux.wipeData` clears stored state on logout ([database guide](database.md)).

## 5. Change a stored type

Publishing compares every `persisted` and `secure` entry with the previous release
(`STA-040`). A changed type needs a `migration` from the old type, reading the old value as
`previous`, or an explicit reset; otherwise publishing is refused:

```json
{"name": "level", "type": "int", "default": 0, "persistence": "persisted",
 "migration": {"from": "string", "value": {"$expr": "len(previous)"}}}
```

On the device, a value from the previous release is migrated. A value from an older one,
or one whose migration fails, is reset to the default and the reset is reported.

## 6. Share state with the host

An `exposed` app entry is readable, writable and observable by the host app (`STA-030`,
`HST-021`), so native and Plux content on one screen stay in step. `plux codegen` writes
typed handles ([typed API guide](typed-api.md)):

```dart
final counter = Plux.state<int>('counter');
counter.watch().listen((v) => print('counter is $v'));
await counter.set(4);
```

Host events go the other way: `Plux.sendEvent` sends a typed event the app document
declares into Plux, and its `hostEvents` triggers run (`HST-013`).
