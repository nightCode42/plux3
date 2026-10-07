// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// What a `database` data source watches (DB-006): a collection, a typed
/// filter, a sort, a limit and an offset, as the compiler checked them.
/// Lists bind to the source like to any other (`data.<name>.value`), and
/// rows that did not change between two results are the same objects.
library;

import 'package:plux_flutter/src/db/query.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';

/// The query of a `database` source.
final class DatabaseQuerySpec {
  /// Creates the spec.
  const DatabaseQuerySpec({
    required this.collection,
    this.filter,
    this.orderBy,
    this.descending = false,
    this.limit,
    this.offset = 0,
  });

  /// Reads the literal configuration of a source; throws a [PluxException]
  /// (`bundleMalformed`) for a malformed one.
  factory DatabaseQuerySpec.fromConfig(Map<String, Object?> config) {
    final collection = config['collection'];
    if (collection is! String) {
      throw const PluxException(
        PluxErrorCode.bundleMalformed,
        'a database source names no collection',
      );
    }
    final filter = config['filter'];
    try {
      return DatabaseQuerySpec(
        collection: collection,
        filter: filter == null ? null : DbFilter.fromJson(filter),
        orderBy: config['orderBy'] as String?,
        descending: config['descending'] as bool? ?? false,
        limit: config['limit'] as int?,
        offset: config['offset'] as int? ?? 0,
      );
    } on PluxException {
      throw const PluxException(
        PluxErrorCode.bundleMalformed,
        'a database source has a malformed filter',
      );
    } on TypeError {
      throw const PluxException(
        PluxErrorCode.bundleMalformed,
        'a database source has a malformed configuration',
      );
    }
  }

  /// The collection's key, as the plugin sees it.
  final String collection;

  /// The condition, or null for every record.
  final DbFilter? filter;

  /// The field to sort by, or null for key order.
  final String? orderBy;

  /// Whether larger values come first.
  final bool descending;

  /// The most records, or null for `db.queryRows`.
  final int? limit;

  /// The matching records skipped.
  final int offset;
}
