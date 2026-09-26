// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The Plux runtime for Flutter.
///
/// The runtime syncs every plugin of an app at start-up, verifies signed
/// bundles before loading them, and renders them as native widgets (spec §12).
/// Capabilities arrive phase by phase; see `docs/requirements.md` §5.
library;

export 'src/runtime_info.dart';
