// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Reading limits from the values a bundle carries (LIM-001).
library;

import 'package:plux_flutter/src/schema/limits.g.dart';

/// A bundle's limit overrides, by key.
///
/// The compiler writes only the limits whose value differs from the
/// registry default, so every read of a bundle's limits goes through
/// [valueOf]: an absent key is the registry default.
extension PluxLimitValues on Map<String, int> {
  /// The effective value of [limit]: the bundle's, else its registry
  /// default.
  int valueOf(PluxLimit limit) => this[limit.key] ?? limit.defaultValue;
}
