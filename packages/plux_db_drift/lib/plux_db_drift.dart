// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The Drift and SQLCipher database adapter for `plux_flutter` (DB-002):
/// pass a [PluxDriftAdapter] as `PluxConfig.databaseAdapter` to store
/// plugin collections.
library;

export 'src/drift_adapter.dart' show DriftOpener, PluxDriftAdapter;
