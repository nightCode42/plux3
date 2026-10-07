// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The key of a row of a watched query, remembered by the row's object:
/// a list that renders such rows reuses the item it built for a row that
/// did not change, and builds only the items whose row is a new object
/// (DB-006). Rows of other lists have no key.
library;

final Expando<String> _keys = Expando('plux db row keys');

/// Remembers that [row] is the record with [key] of a watched query.
void markDbRow(Map<String, Object?> row, String key) => _keys[row] = key;

/// The key of [row] if it is a record of a watched query, else null.
String? dbRowKeyOf(Object? row) =>
    row is Map<String, Object?> ? _keys[row] : null;
