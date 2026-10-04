// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Mock selection (DAT-080): tests and development builds show a source's
/// declared `loading`, `empty`, `error` or `success` state instead of
/// loading it. Release builds never do: [dataMocksAllowed] is a constant
/// false there, so the selection is compiled out.
library;

import 'package:flutter/foundation.dart';

/// Whether this build may select mocks: debug and profile builds only.
const bool dataMocksAllowed = !kReleaseMode;

/// The mock states of a data source.
enum DataMockState {
  /// Loading, forever.
  loading,

  /// The declared empty mock, or an empty list.
  empty,

  /// The declared error.
  error,

  /// The source's design-time mock.
  success,
}

/// The selected mock states, by `<plugin>/<source>` or by `<source>` for
/// every plugin.
final class DataMocks {
  /// Creates the selection; [allowed] is [dataMocksAllowed] outside tests.
  const DataMocks([this.selected = const {}, this.allowed = dataMocksAllowed]);

  /// The selection.
  final Map<String, DataMockState> selected;

  /// Whether mocks are honoured at all.
  final bool allowed;

  /// The state selected for [source] of [plugin], or null to load it.
  DataMockState? of(String plugin, String source) {
    if (!allowed || selected.isEmpty) return null;
    return selected['$plugin/$source'] ?? selected[source];
  }
}
