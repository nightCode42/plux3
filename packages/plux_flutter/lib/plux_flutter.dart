// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The Plux runtime for Flutter.
///
/// The runtime syncs every plugin of an app at start-up, verifies signed
/// bundles before loading them, and renders them as native widgets (spec
/// §12). Start it with [Plux.initialize], show pages with [PluxView] or
/// [Plux.open], and follow updates on [Plux.syncEvents].
library;

export 'src/core/config.dart'
    show
        ActivationPolicy,
        PluxAuthDelegate,
        PluxConfig,
        PluxConsent,
        PluxErrorHandler,
        PluxFallbackBuilder,
        PluxPublicKey,
        PluxThemeSource,
        PluxUser,
        StartupPolicy;
export 'src/core/fallback.dart' show PluxDefaultFallback;
export 'src/core/host_events.dart' show PluxHostEvent;
export 'src/core/plux.dart' show Plux, PluxScope, PluxSyncTile;
export 'src/core/plux_view.dart' show PluxView;
export 'src/core/runtime.dart' show PluxStartup;
export 'src/devtools_api/diagnostics.dart'
    show PluxDiagnostic, PluxDiagnostics, PluxPluginInfo, PluxReleaseInfo;
export 'src/errors/plux_exception.dart' show PluxErrorCode, PluxException;
export 'src/native_catalogue/registration.dart'
    show
        PluxNativeAction,
        PluxNativeRoute,
        PluxNativeSlot,
        PluxRouterAdapter,
        PluxSlot;
export 'src/navigation/delegate.dart'
    show
        PluxNavigationDelegate,
        PluxNavigatorDelegate,
        PluxPageRoute,
        PluxPresentation,
        PluxRouteSpec,
        PluxTransition;
export 'src/navigation/plux_page.dart' show PluxPage;
export 'src/navigation/router.dart' show PluxNotFoundPage;
export 'src/navigation/shell.dart' show PluxShell, PluxShellScope;
export 'src/runtime_info.dart';
export 'src/sync/sync_event.dart'
    show
        SyncActivated,
        SyncChecking,
        SyncDownloading,
        SyncEvent,
        SyncFailed,
        SyncOutcome,
        SyncResult,
        SyncRolledBack,
        SyncRun,
        SyncStaged,
        SyncUpToDate;
