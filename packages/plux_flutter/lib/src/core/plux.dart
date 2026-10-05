// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The host API (HST-001, spec Appendix I): one entry point for a host app
/// to start the runtime, sync, open pages and set what Plux renders with.
library;

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:plux_flutter/src/core/app_state.dart';
import 'package:plux_flutter/src/core/config.dart';
import 'package:plux_flutter/src/core/host_events.dart';
import 'package:plux_flutter/src/core/runtime.dart';
import 'package:plux_flutter/src/devtools_api/diagnostics.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/navigation/delegate.dart';
import 'package:plux_flutter/src/navigation/plux_page.dart';
import 'package:plux_flutter/src/pxl/types.dart';
import 'package:plux_flutter/src/render/renderer.dart';
import 'package:plux_flutter/src/state/providers.dart';
import 'package:plux_flutter/src/sync/sync_event.dart';

/// The Plux runtime's host API.
///
/// ```dart
/// await Plux.initialize(PluxConfig(appId: 'acme-mobile', endpoint: Uri.parse('https://plux.acme.example')));
/// runApp(const MyApp());
/// // anywhere: PluxView('loan-calculator'), or
/// await Plux.open(context, 'loan-calculator', params: {'productId': 'personal-12m'});
/// ```
///
/// The runtime is one per app process, as the host API's static shape
/// implies (Appendix I): it is set only by [initialize] and cleared only
/// by [dispose].
abstract final class Plux {
  static PluxRuntime? _runtime;
  static ProviderContainer? _container;
  static bool _ownsContainer = false;

  /// Starts the runtime (RT-004, SYN-003). With a cached or embedded
  /// release it returns without waiting for the network unless the startup
  /// policy says to; with neither, it waits for the first sync, and the
  /// result says whether pages can render.
  static Future<PluxStartup> initialize(PluxConfig config) =>
      initializeWith(config, const RuntimeOverrides());

  /// [initialize] with test overrides; not part of the public API.
  static Future<PluxStartup> initializeWith(
    PluxConfig config,
    RuntimeOverrides overrides,
  ) async {
    if (_runtime != null) {
      throw StateError(
        'Plux is already initialized; call Plux.dispose() first',
      );
    }
    final container =
        config.container ??
        ProviderContainer(
          parent: config.parentContainer,
          overrides: config.parentContainer == null
              ? const []
              : pluxScopedProviders,
        );
    final (rt, startup) = await PluxRuntime.start(config, overrides: overrides);
    _runtime = rt;
    _container = container;
    _ownsContainer = config.container == null;
    container.read(pluxRuntimeProvider.notifier).set(rt);
    rt.environment = () => container.read(environmentProvider);
    rt.connect(container);
    container
        .read(environmentProvider.notifier)
        .replace(
          PluxEnvironment(
            locale: config.locale,
            themeMode: config.themeMode,
            brand: config.brand,
            consent: config.consent,
            authDelegate: config.authDelegate,
          ),
        );
    return startup;
  }

  /// Whether the runtime is running.
  static bool get isInitialized => _runtime != null;

  static PluxRuntime get _rt =>
      _runtime ?? (throw StateError('call Plux.initialize() first'));

  /// The Riverpod container Plux uses (RT-003).
  static ProviderContainer get container =>
      _container ?? (throw StateError('call Plux.initialize() first'));

  /// Syncs every plugin now (SYN-002); a sync in progress is joined.
  /// Awaiting it gives the result, which says whether a new release was
  /// staged; [SyncRun.progress] carries this run's events, and
  /// [syncEvents] those of every run.
  static SyncRun sync() => _rt.sync();

  /// The typed sync events (SYN-013).
  static Stream<SyncEvent> get syncEvents => _rt.events;

  /// What debugging tools such as `plux_devtools` show: reported problems,
  /// the active release and the sync status. Empty in release builds.
  static PluxDiagnostics get diagnostics => _rt.diagnostics;

  /// Opens the page [route] by its app-wide name only, wherever it is
  /// (NAV-003): pushed on the navigator of [context], or presented as a
  /// dialog or bottom sheet when its page kind says so, through the
  /// navigation delegate. Completes when the page pops, with its result in
  /// the JSON form of its declared type when that is a [T]; another value
  /// is reported and completes with null. An unknown name shows the
  /// not-found page and reports `PLX-4100` (NAV-011); parameters are
  /// checked on entry (NAV-007).
  static Future<T?> open<T extends Object?>(
    BuildContext context,
    String route, {
    Map<String, Object?> params = const {},
  }) => _rt.router.open<T>(context, route, params);

  /// The page [route] for a declarative `Navigator.pages` list (NAV-006).
  static PluxPage<T> pageFor<T>(
    String route, {
    Map<String, Object?> params = const {},
    LocalKey? key,
  }) => PluxPage<T>(
    router: _rt.router,
    route: route,
    params: params,
    key: key ?? ValueKey(route),
  );

  /// Resolves the route a URL names, for router adapters such as
  /// `plux_go_router` (ADR-0040): each value of [query] is converted by the
  /// route's declared parameter type, as a deep link's is, names the route
  /// does not declare are left out, and the route's guards run. Completes
  /// with the route to show: the route itself, a redirect's target, or the
  /// fallback of a refused route. Text that does not convert shows the
  /// page's error fallback on entry (`PLX-4101`).
  static Future<PluxRouteSpec> resolveLocation(
    String route, {
    Map<String, String> query = const {},
  }) => _rt.resolveLocation(route, query);

  /// Opens the page [link] names through the app's deep links (NAV-008):
  /// `https://<host>/p/<route-name>?…`, or a path pattern of the app
  /// document's `navigation.deepLinks`, on one of its hosts or custom
  /// schemes. The route's guards run first; parameters are converted by
  /// their declared types. Completes with whether a route opened: false,
  /// reported, for a link nothing maps (`PLX-4103`) or without
  /// `PluxConfig.navigatorKey` (`PLX-4102`).
  static Future<bool> handleDeepLink(Uri link) => _rt.handleDeepLink(link);

  /// Opens the page a notification names (NAV-008): the host's push SDK
  /// calls this when the user opens a notification, with its data payload,
  /// whose payload key (`plux` unless the app document names another)
  /// holds `{"route": …, "params": {…}}`, as an object or JSON text. Plux
  /// ships no push SDK. Completes as [handleDeepLink] does.
  static Future<bool> handlePushPayload(Map<String, Object?> payload) =>
      _rt.handlePushPayload(payload);

  /// The typed events plugins emit with `emitHostEvent` (HST-013).
  static Stream<PluxHostEvent> get events => _rt.hostEvents;

  /// The host events named [name] (HST-013, ADR-0023).
  static Stream<PluxHostEvent> eventsNamed(String name) =>
      events.where((e) => e.name == name);

  /// The value of the app's feature flag [name] in the active release, in
  /// the JSON form of its type when that is a [T]; null before a release
  /// is active, for an undeclared flag or another type (ABT-006). Plugins
  /// read the same value as `flags.<name>`; `plux codegen` writes typed
  /// getters over it (HST-030).
  static T? flag<T>(String name) {
    final release = _rt.active.value;
    final renderer = _rt.renderer;
    if (release == null || renderer is! PluxRenderer) return null;
    final value = toJson(renderer.flagsRoot(release)[name]);
    return value is T ? value : null;
  }

  /// A handle on the exposed app state entry [name] (HST-021, ADR-0023):
  /// read, write and watch it; `plux codegen` writes typed accessors over
  /// it (HST-030).
  static PluxState<T> state<T>(String name) => PluxState<T>(name, container);

  /// Sends host event [name] into Plux with [payload] in its JSON form
  /// (HST-013): the triggers that handle it run. Completes with false,
  /// reported with PLX-5307, when nothing accepts it: the app does not
  /// declare the event for the host to send, or no trigger handles it. A
  /// payload without the declared fields and types is refused the same
  /// way, reported with PLX-5500 (the app bundle's `hostEvents` give the
  /// types). `plux codegen` writes typed senders over it.
  static Future<bool> sendEvent(
    String name, [
    Map<String, Object?> payload = const {},
  ]) async {
    final rt = _rt;
    final sink = rt.hostEventSink;
    final converted = fromHost(payload) as Map<String, Object?>;
    try {
      if (sink != null && sink.deliver(PluxHostEvent(name, converted))) {
        return true;
      }
    } on PluxException catch (e) {
      rt.reportProblem(e);
      return false;
    }
    rt.reportProblem(
      PluxException(
        PluxErrorCode.hostEventRefused,
        sink == null
            ? 'no trigger handles host event $name'
            : 'host event $name is not declared for the host to send, or nothing handles it',
        details: {'event': name},
      ),
    );
    return false;
  }

  /// Removes the data Plux keeps on the device for this app (HST-001):
  /// session, persisted and secure state with the stores' keys, and every
  /// cached data-source response; app and
  /// plugin state start again from their defaults. Pages already shown
  /// keep their in-memory values until they close.
  static Future<void> wipeData() async {
    final rt = _rt;
    await rt.statePersistence.wipe();
    // Cached responses go too, so the next user never sees them.
    await rt.clearDataCache();
    final c = container;
    c.read(appStateProvider.notifier).restart();
    final keys = [
      for (final b in rt.active.value?.record.bundles ?? const <Never>[]) b.key,
    ];
    for (final key in keys) {
      if (key.isNotEmpty && c.exists(pluginStateProvider(key))) {
        c.read(pluginStateProvider(key).notifier).restart();
      }
    }
  }

  static void _environment(PluxEnvironment Function(PluxEnvironment) f) {
    final n = container.read(environmentProvider.notifier);
    n.replace(f(container.read(environmentProvider)));
  }

  /// Sets the locale of Plux pages; null follows the host (I18N-005).
  static void setLocale(Locale? locale) => _environment(
    (e) => PluxEnvironment(
      locale: locale,
      themeMode: e.themeMode,
      brand: e.brand,
      consent: e.consent,
      user: e.user,
      authDelegate: e.authDelegate,
    ),
  );

  /// Sets light, dark or the system setting (THM-002).
  static void setThemeMode(ThemeMode mode) => _environment(
    (e) => PluxEnvironment(
      locale: e.locale,
      themeMode: mode,
      brand: e.brand,
      consent: e.consent,
      user: e.user,
      authDelegate: e.authDelegate,
    ),
  );

  /// Selects a white-label brand overlay, or none (THM-003).
  static void setBrand(String? brand) => _environment(
    (e) => PluxEnvironment(
      locale: e.locale,
      themeMode: e.themeMode,
      brand: brand,
      consent: e.consent,
      user: e.user,
      authDelegate: e.authDelegate,
    ),
  );

  /// Records the user's consent (SEC-161).
  static void setConsent(PluxConsent consent) {
    // Telemetry follows at once; withdrawing a category deletes its
    // buffered events (ADR-0034).
    _rt.telemetry.consent = consent;
    _environment(
      (e) => PluxEnvironment(
        locale: e.locale,
        themeMode: e.themeMode,
        brand: e.brand,
        consent: consent,
        user: e.user,
        authDelegate: e.authDelegate,
      ),
    );
  }

  /// Sets the pseudonymous user and targeting attributes (HST-011).
  static void setUserContext(PluxUser? user) => _environment(
    (e) => PluxEnvironment(
      locale: e.locale,
      themeMode: e.themeMode,
      brand: e.brand,
      consent: e.consent,
      user: user,
      authDelegate: e.authDelegate,
    ),
  );

  /// Sets the delegate that supplies the end user's token (HST-010).
  static void setAuthDelegate(PluxAuthDelegate? delegate) => _environment(
    (e) => PluxEnvironment(
      locale: e.locale,
      themeMode: e.themeMode,
      brand: e.brand,
      consent: e.consent,
      user: e.user,
      authDelegate: delegate,
    ),
  );

  /// Stops the runtime: the sync isolate ends, mappings are released, and
  /// the container is disposed if Plux created it. A host that shares its
  /// container with Plux calls this before disposing it.
  static Future<void> dispose() async {
    final rt = _runtime;
    _runtime = null;
    final c = _container;
    _container = null;
    // The app's and the plugins' triggers stop before their container.
    rt?.disconnect();
    // A container Plux created is disposed (a no-op when the host disposed
    // its parent first); a shared one is left without the runtime.
    if (c != null) {
      if (_ownsContainer) {
        c.dispose();
      } else {
        c.read(pluxRuntimeProvider.notifier).set(null);
      }
    }
    await rt?.dispose();
  }
}

/// Makes Plux's container available below it, unless an enclosing scope
/// already provides it (ADR-0008). `Plux.open` wraps its pages in one;
/// host apps that do not use Riverpod wrap their app, or each `PluxView`.
final class PluxScope extends StatelessWidget {
  /// Creates a scope.
  const PluxScope({super.key, required this.child});

  /// The subtree.
  final Widget child;

  @override
  Widget build(BuildContext context) {
    final container = Plux.container;
    ProviderContainer? enclosing;
    try {
      enclosing = ProviderScope.containerOf(context, listen: false);
    } on StateError {
      enclosing = null;
    }
    if (identical(enclosing, container)) return child;
    return UncontrolledProviderScope(container: container, child: child);
  }
}

/// A ready-made settings tile for manual sync (SYN-002): the active
/// release, the last sync's outcome, and a button that syncs now.
final class PluxSyncTile extends ConsumerWidget {
  /// Creates the tile.
  const PluxSyncTile({super.key, this.title = 'Content updates'});

  /// The tile's title.
  final String title;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final release = ref.watch(activeReleaseProvider);
    final event = ref.watch(syncStatusProvider);
    final busy = event is SyncChecking || event is SyncDownloading;
    final subtitle = switch (event) {
      SyncChecking() => 'Checking…',
      SyncDownloading(:final progress) =>
        'Downloading ${(progress * 100).round()}%',
      SyncStaged(:final sequence) => 'Release $sequence ready',
      SyncActivated(:final sequence) ||
      SyncUpToDate(
        sequence: final int sequence,
      ) => 'Up to date (release $sequence)',
      SyncFailed(:final error) => 'Update failed (${error.code.id})',
      SyncRolledBack(:final to) => 'Restored release $to',
      _ => release == null ? 'No content yet' : 'Release ${release.sequence}',
    };
    return ListTile(
      title: Text(title),
      subtitle: Text(subtitle),
      trailing: busy
          ? const SizedBox.square(
              dimension: 24,
              child: CircularProgressIndicator(strokeWidth: 2),
            )
          : IconButton(
              tooltip: 'Check for updates',
              icon: const Icon(Icons.refresh),
              onPressed: () => unawaited(Plux.sync()),
            ),
      onTap: busy ? null : () => unawaited(Plux.sync()),
    );
  }
}
