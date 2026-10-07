// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The seam to local persistence (DB-001, ADR-0049): everything the
/// runtime stores for plugins goes through a [PluxDatabaseAdapter] —
/// collections with typed records and queries, reactive watches,
/// transactions and migrations, and the plugins' key-value stores. The
/// core's built-in adapter serves the key-value store on its own files;
/// `plux_db_drift` stores collections in SQLite; a host app may supply its
/// own through `PluxConfig.databaseAdapter` (DB-003).
///
/// An adapter stores JSON records, checked by [DbCollectionSchema.checked];
/// it knows nothing of plugins: the runtime names every collection and
/// key-value namespace, so that plugin-private data cannot be reached by
/// another plugin (DB-004).
library;

import 'package:plux_flutter/src/db/query.dart';
import 'package:plux_flutter/src/db/schema.dart';

/// The operations on records and key-value entries, as an adapter offers
/// them and a transaction repeats them. Collections are addressed by their
/// physical name, [DbCollectionSchema.name]. Failures are
/// `PluxException`s with the codes `PLX-5203` to `PLX-5208`.
abstract interface class DbOperations {
  /// The record with [key], or null.
  Future<Map<String, Object?>?> get(String collection, String key);

  /// Adds [record]; fails with `PLX-5205` if a record has its key.
  Future<void> insert(String collection, Map<String, Object?> record);

  /// Replaces the fields in [patch] of the record with [key]; fails with
  /// `PLX-5204` if there is none.
  Future<void> update(
    String collection,
    String key,
    Map<String, Object?> patch,
  );

  /// Adds [record], or replaces the record with its key.
  Future<void> upsert(String collection, Map<String, Object?> record);

  /// Removes the record with [key]; true if there was one.
  Future<bool> delete(String collection, String key);

  /// The records matching [query].
  Future<List<Map<String, Object?>>> query(String collection, DbQuery query);

  /// How many records match [filter], or exist when it is null.
  Future<int> count(String collection, [DbFilter? filter]);

  /// The value of key-value entry [key] in [namespace], or null.
  Future<Object?> kvGet(String namespace, String key);

  /// Stores [value], a JSON value other than null, under [key]. The first
  /// write fixes the JSON type of the key (string, int, double, bool, list
  /// or map) until it is removed; another type fails with `PLX-5210`.
  Future<void> kvSet(String namespace, String key, Object value);

  /// Removes [key]; true if it existed.
  Future<bool> kvRemove(String namespace, String key);

  /// Every entry of [namespace].
  Future<Map<String, Object?>> kvAll(String namespace);
}

/// What a migration of one collection did.
final class DbMigrationOutcome {
  /// Creates the outcome.
  const DbMigrationOutcome({
    required this.collection,
    required this.version,
    this.error,
  });

  /// The collection's physical name.
  final String collection;

  /// The version the collection is at now: the schema's on success, the
  /// previous one on failure, 0 if it does not exist.
  final int version;

  /// Why the migration failed (`PLX-5200`), or null.
  final Object? error;

  /// Whether the collection is at the schema's version.
  bool get ok => error == null;
}

/// The seam to local persistence.
abstract interface class PluxDatabaseAdapter implements DbOperations {
  /// Whether the adapter stores collections. The core's built-in adapter
  /// does not: its collection operations fail with `PLX-5201`.
  bool get supportsCollections;

  /// Whether the data is encrypted at rest.
  bool get encrypted;

  /// Opens the database; calling it again is harmless. With
  /// [requireEncryption], an adapter that cannot encrypt, or whose
  /// database is not encrypted, fails with `PLX-5202` and stays closed:
  /// the `strict` and `maximum` security profiles require it (DB-002).
  Future<void> open({required bool requireEncryption});

  /// Brings every collection of [schemas] to its version: creates new
  /// ones, adds fields and indexes, and applies the plans of
  /// [DbCollectionSchema.migrations] (DB-005). Each collection migrates in
  /// its own transaction: one that fails is left at its previous version,
  /// reported in the outcome, and does not stop the others.
  Future<List<DbMigrationOutcome>> migrate(List<DbCollectionSchema> schemas);

  /// Removes the collections named [names], records and structure; a name
  /// that is not stored is ignored (DB-005, `droppedCollections`).
  Future<void> dropCollections(List<String> names);

  /// Runs [body] atomically: its operations all take effect, or, if it
  /// throws, none does.
  Future<R> transaction<R>(Future<R> Function(DbOperations tx) body);

  /// The records matching [query], now and again after every write to the
  /// collection that changes them. Operations of one transaction produce
  /// one update. Closing the subscription stops the watch.
  Stream<List<Map<String, Object?>>> watch(String collection, DbQuery query);

  /// Removes data. With [namespace] (`app` or `plugin.<key>`), the records
  /// of that namespace's collections and its key-value entries; the
  /// collections themselves stay. Without, everything the adapter stores,
  /// including collections and any key it holds (DB-008); the database is
  /// opened again, empty, by the next [open].
  Future<void> wipe({String? namespace});

  /// Closes the database.
  Future<void> close();
}

/// The JSON type name that fixes a key-value key (DB-009), or null for a
/// value that is not JSON.
String? kvTypeOf(Object? value) => switch (value) {
  String() => 'string',
  int() => 'int',
  double() => 'double',
  bool() => 'bool',
  List<Object?>() => 'list',
  Map<String, Object?>() => 'map',
  _ => null,
};
