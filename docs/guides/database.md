<!--
SPDX-FileCopyrightText: 2026 Plux contributors
SPDX-License-Identifier: Apache-2.0
-->

# Database Guide

How a Plux plugin keeps data on the device: typed collections it queries, watches and
writes, and a key-value store for small settings. The examples are the `db` conformance
project ([`schema/testdata/documents/db`](../../schema/testdata/documents/db)). The design
is [ADR-0049](../adr/0049-local-persistence.md).

## 1. Install an adapter

Collections need a database adapter in the host app. `plux_db_drift` is the default: Drift
on SQLite, encrypted with SQLCipher, on its own isolate (`DB-002`, `DB-007`). `plux init
--packages plux_db_drift` adds it and registers it ([CLI](../reference/cli.md)). By hand:

```dart
PluxConfig(/* … */, databaseAdapter: PluxDriftAdapter());
```

A host with its own database can pass any `PluxDatabaseAdapter` that passes the shared
conformance suite instead (`DB-003`). Without an adapter, a plugin's collection sources and
actions fail with a typed error and the rest of the plugin runs. Publishing warns when a
targeted host build lacks the package. Under the `strict` and `maximum` security profiles,
the adapter refuses to open an unencrypted database.

The key-value store, persisted state, the response cache and the outbox do not need an
adapter: they live in the core's built-in store.

## 2. Declare a collection

A plugin declares its collections with typed fields, a primary key and indexes (`DB-004`).
They are private to the plugin. The app document's collections are shared by every plugin
of the app:

```json
"collections": [{
  "key": "tasks", "version": 2, "primaryKey": ["id"],
  "fields": [
    {"name": "id", "type": "string"}, {"name": "title", "type": "string"},
    {"name": "done", "type": "bool"}, {"name": "rank", "type": "int"},
    {"name": "note", "type": "string?"}
  ],
  "indexes": [["done"], ["rank"]],
  "migrations": [{"from": 1, "rename": {"title": "name"}}]
}]
```

Record and collection counts and sizes are bounded by the `db.*` limits
([limits](../reference/limits.md)).

## 3. Watch a query

A data source of kind `database` watches a query: a collection, an optional filter, an
order and a limit (`DB-006`). Bind it like any source; it updates whenever a write changes
its rows, and a list re-renders only the items that changed:

```json
{"name": "open", "kind": "database", "type": "list<Task>",
 "config": {"collection": "tasks", "orderBy": "rank",
            "filter": {"field": "done", "op": "eq", "value": false}}}
```

```json
{"type": "ListView", "props": {"items": {"$expr": "data.open.value ?? []"}}}
```

## 4. Write

`dbInsert`, `dbUpdate`, `dbUpsert` and `dbDelete` write records, and `dbQuery` reads them
once, all typed against the collection:

```json
{"id": "add", "action": "dbInsert",
 "input": {"collection": "tasks", "record": {"id": "c", "title": "Cleaning", "done": false, "rank": 3, "note": null}}},
{"id": "mark", "action": "dbUpdate",
 "input": {"collection": "tasks", "key": "a", "patch": {"done": true}}}
```

A write can declare an optimistic state change, like any mutation
([actions guide](actions.md)).

## 5. Change a collection

Raise `version` when a collection changes (`DB-005`). Added nullable or defaulted fields and
new indexes migrate on their own. A rename, a dropped field or collection, or a narrower
type needs a migration step such as `rename` above, and publishing it needs the
publisher to acknowledge a warning. On the device, migrations run in one transaction
before the collections are used. A failed migration rolls back and reports `PLX-5200`, and
the collection's sources fail until a release with a working migration arrives.

## 6. Small settings: the key-value store

`kvSet`, `kvGet` and `kvRemove` keep typed values per plugin without a collection
(`DB-009`), within `db.kvBytes`:

```json
{"id": "remember", "action": "kvSet", "input": {"key": "theme", "value": "dark"}}
```

## 7. Wipe on logout

`Plux.wipeData()` deletes every plugin's collections, state, key-value records, cached
responses and outbox, and `Plux.wipeData(plugin: 'todo')` deletes one plugin's
(`DB-008`). The `logout` action asks the host, through its auth delegate, and the host
decides whether to wipe ([host app guide](host-app.md)).
