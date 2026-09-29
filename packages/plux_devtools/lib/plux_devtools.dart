// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// A debug overlay for host apps that use the Plux runtime (RT-060).
///
/// Wrap the app with [PluxDevtools] to see the sync status, the active
/// release and the problems the runtime reported. It renders nothing in
/// release builds. The page node tree arrives with the renderer.
library;

export 'src/devtools_overlay.dart' show PluxDevtools, describeSyncEvent;
