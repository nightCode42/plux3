<!--
SPDX-FileCopyrightText: 2026 Plux contributors
SPDX-License-Identifier: Apache-2.0
-->

# plux_db_drift

The database adapter of [`plux_flutter`](https://pub.dev/packages/plux_flutter) that stores
plugin collections in SQLite through [Drift](https://pub.dev/packages/drift), encrypted with
SQLCipher.

- One table per collection, a typed column per field, the declared indexes.
- All SQL runs on a background isolate; watched queries re-run only after a write to their
  collection and deliver a result only when it changed.
- Migrations from the app's documents run per collection in one transaction; a migration that
  fails leaves the collection at its previous version.
- The database key is made for each installation and kept in the platform's secure storage,
  like the state stores' keys. Under the `strict` and `maximum` security profiles an
  unencrypted database is refused (`PLX-5202`).

## Usage

```dart
await Plux.initialize(PluxConfig(
  appId: 'app_…',
  endpoint: Uri.parse('https://plux.example.com'),
  databaseAdapter: PluxDriftAdapter(),
));
```

Select an encrypting SQLite build in the app's `pubspec.yaml`:

```yaml
hooks:
  user_defines:
    sqlite3:
      source: sqlcipher # or sqlite3mc
```

Without it the adapter stores an unencrypted database, which the `standard` profile accepts.

`PluxDriftAdapter(path: …)` places the file; `PluxDriftAdapter.custom` takes any Drift
executor. `Plux.wipeData()` removes the database file and its key.
