// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The runtime behind the `Plux` facade (ADR-0021): start-up per the
/// startup policy (SYN-003), the sync isolate, activation at safe points
/// (SYN-004), last known good (SYN-006), kill switches (RT-022) and the
/// typed sync events (SYN-013).
library;

import 'dart:async';
import 'dart:developer' as developer;
import 'dart:io';
import 'dart:math' as math;
import 'dart:ui' as ui;

import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/actions/engine.dart';
import 'package:plux_flutter/src/actions/trace.dart';
import 'package:plux_flutter/src/actions/triggers.dart';
import 'package:plux_flutter/src/assets/assets.dart';
import 'package:plux_flutter/src/assets/avif_probe.dart';
import 'package:plux_flutter/src/assets/fonts.dart';
import 'package:plux_flutter/src/assets/icon_fonts.dart';
import 'package:plux_flutter/src/assets/image_providers.dart';
import 'package:plux_flutter/src/core/active_release.dart';
import 'package:plux_flutter/src/core/config.dart';
import 'package:plux_flutter/src/core/fallback.dart';
import 'package:plux_flutter/src/core/features.dart';
import 'package:plux_flutter/src/core/host_events.dart';
import 'package:plux_flutter/src/core/plux.dart';
import 'package:plux_flutter/src/core/plux_view.dart';
import 'package:plux_flutter/src/core/trigger_sources.dart';
import 'package:plux_flutter/src/data/services.dart';
import 'package:plux_flutter/src/data/worker.dart';
import 'package:plux_flutter/src/db/adapter.dart';
import 'package:plux_flutter/src/db/builtin_adapter.dart';
import 'package:plux_flutter/src/db/release_declarations.dart';
import 'package:plux_flutter/src/db/service.dart';
import 'package:plux_flutter/src/device/guard.dart';
import 'package:plux_flutter/src/device/services.dart';
import 'package:plux_flutter/src/devtools_api/diagnostics.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/native_catalogue/host.dart';
import 'package:plux_flutter/src/navigation/deep_links.dart';
import 'package:plux_flutter/src/navigation/delegate.dart';
import 'package:plux_flutter/src/navigation/guards.dart';
import 'package:plux_flutter/src/navigation/router.dart';
import 'package:plux_flutter/src/platform/platform_services.dart';
import 'package:plux_flutter/src/pxl/types.dart';
import 'package:plux_flutter/src/render/owner_actions.dart';
import 'package:plux_flutter/src/render/page_renderer.dart';
import 'package:plux_flutter/src/render/renderer.dart';
import 'package:plux_flutter/src/runtime_info.dart';
import 'package:plux_flutter/src/schema/limit_values.dart';
import 'package:plux_flutter/src/schema/limits.g.dart';
import 'package:plux_flutter/src/security/attestation.dart';
import 'package:plux_flutter/src/security/device_keys.dart';
import 'package:plux_flutter/src/security/platform_attestation.dart';
import 'package:plux_flutter/src/security/security_config.dart';
import 'package:plux_flutter/src/state/persistence.dart';
import 'package:plux_flutter/src/state/providers.dart';
import 'package:plux_flutter/src/store/baseline.dart';
import 'package:plux_flutter/src/store/kv_store.dart';
import 'package:plux_flutter/src/store/pointer.dart';
import 'package:plux_flutter/src/store/release_store.dart';
import 'package:plux_flutter/src/sync/api_client.dart';
import 'package:plux_flutter/src/sync/sync_engine.dart';
import 'package:plux_flutter/src/sync/sync_event.dart';
import 'package:plux_flutter/src/sync/sync_worker.dart';
import 'package:plux_flutter/src/telemetry/recorder.dart';
import 'package:plux_flutter/src/verify/bundle_verifier.dart';
import 'package:plux_flutter/src/verify/manifest.dart';

/// What `Plux.initialize` found (SYN-003).
final class PluxStartup {
  const PluxStartup._(this.sequence, this.error);

  /// The release pages render from, or null when there is none yet.
  final int? sequence;

  /// Why no release is available, when [sequence] is null.
  final PluxException? error;

  /// Whether Plux pages can render.
  bool get ready => sequence != null;

  @override
  String toString() => ready
      ? 'PluxStartup(ready, release $sequence)'
      : 'PluxStartup(no release: $error)';
}

/// Replaces platform services in tests; not part of the public API.
final class RuntimeOverrides {
  /// Creates overrides.
  const RuntimeOverrides({
    this.credentials,
    this.deviceKeys,
    this.attestation,
    this.baseline,
    this.healthyAfter = const Duration(seconds: 10),
    this.clock,
    this.random,
    this.secrets,
    this.configSecrets,
  });

  /// Creates the credential store on the sync isolate.
  final CredentialStore Function()? credentials;

  /// Creates the device keys on the sync isolate; the platform's when null.
  final DeviceKeys Function()? deviceKeys;

  /// Creates the platform attestation on the sync isolate; the platform's
  /// when null.
  final Attestation Function()? attestation;

  /// Reads the baseline on the sync isolate.
  final BaselineReader? baseline;

  /// How long a page must render without failure before a launch counts as
  /// healthy (SYN-006).
  final Duration healthyAfter;

  /// The clock telemetry reads; the wall clock when null.
  final DateTime Function()? clock;

  /// Where telemetry sampling draws from; a new generator when null.
  final math.Random? random;

  /// The secure storage that keeps the state stores' keys; the platform's
  /// when null (plan p5 D6).
  final SecretStore Function()? secrets;

  /// Creates, on the sync isolate, the secret store that keeps the remote
  /// security configuration (SEC-182); the platform's when null.
  final SecretStore Function()? configSecrets;
}

/// The runtime.
final class PluxRuntime with WidgetsBindingObserver {
  PluxRuntime._(
    this.config,
    this.overrides,
    this.features,
    this._store,
    this._worker,
    this._limits,
    this._root,
    this.assets,
  ) {
    active.addListener(() => unawaited(_loadFonts(active.value)));
    active.addListener(() {
      final r = active.value;
      telemetry
        ..releaseSequence = r?.sequence ?? 0
        ..appSampling = r?.telemetrySampling ?? const {};
    });
  }

  /// Starts the runtime: opens the store, starts the sync isolate, imports
  /// the baseline when there is no release, counts the launch, and syncs
  /// per the startup policy.
  static Future<(PluxRuntime, PluxStartup)> start(
    PluxConfig config, {
    RuntimeOverrides overrides = const RuntimeOverrides(),
  }) async {
    final task = developer.TimelineTask()..start('plux.initialize');
    try {
      final features = RuntimeFeatures();
      final base = config.storageDirectory ?? await platformStorageDirectory();
      final root =
          '$base/${_safe(config.appId)}/${_safe(config.environment)}/${_safe(config.channel)}';
      Directory(root).createSync(recursive: true);
      final keys = config.rootKeys.isNotEmpty
          ? config.rootKeys
          : await _bundledKeys(config.baseline);
      final store = ReleaseStore.open(root);
      final limits = VerifierLimits(
        maxDepth: PluxLimit.bundleVerifierDepth.defaultValue,
        maxVisits: PluxLimit.bundleVerifierTables.defaultValue,
      );
      final credentials = overrides.credentials ?? _platformCredentials(config);
      final assets = AssetDevice(
        pixelRatio:
            ui
                .PlatformDispatcher
                .instance
                .views
                .firstOrNull
                ?.devicePixelRatio ??
            3,
        avif: await decodesAvif(),
      );
      final worker = await SyncWorker.start(
        SyncWorkerConfig(
          storeRoot: root,
          endpoint: config.endpoint,
          httpClient: config.httpClient ?? platformHttpClient,
          credentials: credentials,
          // Host tests replace the platform's services, including this one;
          // without that replacement they run on the built-in defaults
          // rather than wait on a platform that is not there.
          configSecrets:
              overrides.configSecrets ??
              (overrides.credentials == null ? PlatformSecretStore.new : null),
          deviceKeys: overrides.deviceKeys ?? PlatformDeviceKeys.new,
          attestation: overrides.attestation ?? _platformAttestation(config),
          parallelism: config.downloadParallelism,
          baseline: overrides.baseline,
          rootIsolateToken: RootIsolateToken.instance,
          sync: SyncConfig(
            appId: config.appId,
            environment: config.environment,
            channel: config.channel,
            keys: [for (final k in keys) k.toTrustedKey()],
            device: DeviceInfo(
              platform: Platform.operatingSystem,
              osVersion: deviceOsVersion(Platform.operatingSystemVersion),
              runtimeVersion: PluxRuntimeInfo.version,
              hostBuild: config.hostBuild,
            ),
            supportsFeature: features.supports,
            verifierLimits: limits,
            diskQuota: config.diskQuota ?? PluxLimit.deviceDiskQuota.max,
            assets: assets,
          ),
        ),
        baselineAssets: overrides.baseline == null ? config.baseline : null,
      );
      final rt = PluxRuntime._(
        config,
        overrides,
        features,
        store,
        worker,
        limits,
        root,
        assets,
      );
      await rt.statePersistence.load();
      final startup = await rt._startup();
      WidgetsBinding.instance.addObserver(rt);
      rt._flushEvery = Zone.root.createPeriodicTimer(
        flushInterval,
        (_) => unawaited(rt._inForeground ? rt.flushTelemetry() : null),
      );
      return (rt, startup);
    } finally {
      task.finish();
    }
  }

  /// The configuration.
  final PluxConfig config;

  /// Test overrides.
  final RuntimeOverrides overrides;

  /// What this runtime supports (BND-008).
  final RuntimeFeatures features;

  final ReleaseStore _store;
  final SyncWorker _worker;
  final VerifierLimits _limits;
  final String _root;

  /// Which file of each asset this device uses (ADR-0027 Revision).
  final AssetDevice assets;

  final VerifiedAssets _verified = VerifiedAssets();
  final Set<String> _fonts = {};
  final StreamController<SyncEvent> _events = StreamController.broadcast();

  /// Records telemetry (ANL-001, ADR-0034); the sync isolate buffers and
  /// sends what it keeps.
  late final TelemetryRecorder telemetry = TelemetryRecorder(
    post: (lines) =>
        _worker.recordTelemetry(lines, limit(PluxLimit.telemetryBufferBytes)),
    withdraw: _worker.purgeTelemetry,
    consent: config.consent,
    hostSampling: config.telemetrySampling,
    random: overrides.random,
    clock: _clock,
  );

  /// How often buffered telemetry is sent while the app is in the
  /// foreground (ADR-0034).
  static const flushInterval = Duration(minutes: 15);

  /// How long the app may stay in the background before returning starts
  /// a new session.
  static const sessionTimeout = Duration(minutes: 30);

  DateTime Function() get _clock => overrides.clock ?? DateTime.now;
  Timer? _flushEvery;
  DateTime? _foregroundSince;
  DateTime? _backgroundSince;
  bool get _inForeground => _backgroundSince == null;

  /// The route of the page shown last, the source of the next
  /// `screen_view`.
  String lastRoute = '';

  /// A limit's value in the active app bundle (LIM-004), or its default.
  int limit(PluxLimit l) =>
      (active.value?.limits ?? const <String, int>{}).valueOf(l);

  /// Sends the buffered telemetry now. A failure keeps the events for the
  /// next attempt and is not reported: telemetry never adds to the
  /// problems it measures.
  Future<void> flushTelemetry() async {
    if (_disposed) return;
    try {
      await _worker.flushTelemetry(limit(PluxLimit.telemetryEventsPerRequest));
    } on Object catch (e) {
      developer.log('telemetry flush: $e', name: 'plux');
    }
  }

  void _startSession() {
    final now = _clock();
    _foregroundSince = now;
    _backgroundSince = null;
    final view = ui.PlatformDispatcher.instance.views.firstOrNull;
    final size = view == null
        ? null
        : view.physicalSize / view.devicePixelRatio;
    telemetry.record(
      'session_start',
      fields: {
        'runtime_version': PluxRuntimeInfo.version,
        'host_build': config.hostBuild,
        'platform': Platform.operatingSystem,
        'os_version': deviceOsVersion(Platform.operatingSystemVersion),
        if (size != null)
          'device_class': size.shortestSide >= 600 ? 'tablet' : 'phone',
        if (telemetry.consent.analytics)
          'locale': ui.PlatformDispatcher.instance.locale.toLanguageTag(),
      },
    );
  }

  void _endStretch() {
    final since = _foregroundSince;
    if (since == null) return;
    _foregroundSince = null;
    telemetry.record(
      'session_end',
      fields: {'duration_ms': _clock().difference(since).inMilliseconds},
    );
  }

  /// The release pages render from; replaced only by activation or revert.
  final ValueNotifier<ActiveRelease?> active = ValueNotifier(null);

  /// The latest sync event, for status displays.
  final ValueNotifier<SyncEvent?> lastEvent = ValueNotifier(null);

  /// The security settings in force (SEC-182): the built-in defaults until
  /// the stored configuration has been read, then the verified remote
  /// configuration on top of them. Read-only; it changes when a sync
  /// applies a new configuration, so what shapes a running session can
  /// listen and apply at once.
  final ValueNotifier<SecuritySettings> settings = ValueNotifier(
    SecuritySettings.builtIn,
  );

  late final LazyDataWorker _dataWorker = LazyDataWorker(
    () => DataWorker.start(
      httpClient: config.httpClient ?? platformHttpClient,
      webSocketClient: config.webSocketClient,
      cacheDirectory: '$_root/data',
      // The encrypted cache's key, kept like the secure state store's
      // (DAT-010, plan p5 D6).
      keys: SecretCacheKeys(_secrets, 'data-cache.$_keyId'),
    ),
  );

  /// The data layer's services (ADR-0048): loads and failures of data
  /// sources become trigger events (ACT-002).
  late final DataServices _dataServices = DataServices(
    transport: _dataWorker,
    plainStore: _dataWorker.store(secure: false),
    secureStore: _dataWorker.store(secure: true),
    authDelegate: () => environment().authDelegate,
    environment: config.environment,
    record: telemetry.record,
    report: _report,
    events: DataTriggers(triggers),
    ioEvents: DataTriggers(triggers),
    streamTransport: _dataWorker,
    transferTransport: _dataWorker,
    outboxStore: _outboxStore,
    runtimeRoot: _root,
    downloadDirectory: '$_root/downloads',
    database: database,
  );

  /// The offline outbox's store: encrypted under a key of this
  /// installation kept by the platform's secure storage, like the secure
  /// state store's (DAT-020, plan p5 D6). Its size follows the active app
  /// bundle's `data.outboxBytes`.
  late final EncryptedFileStore _outboxStore = () {
    final store = EncryptedFileStore(
      path: '$_root/data/outbox.pxk',
      secrets: _secrets,
      keyName: 'data-outbox.$_keyId',
      label: 'data-outbox/$_keyId',
      maxBytes: PluxLimit.dataOutboxBytes.defaultValue,
    );
    active.addListener(() {
      store.maxBytes =
          active.value?.limits[PluxLimit.dataOutboxBytes.key] ??
          PluxLimit.dataOutboxBytes.defaultValue;
    });
    return store;
  }();

  /// The host says whether the network is available
  /// (`Plux.setNetworkAvailable`, decision D13): streams waiting to
  /// reconnect do so and the outbox replays when it is back.
  void setNetworkAvailable(bool available) =>
      _dataServices.setNetworkAvailable(available);

  /// Ends the user's session (HST-010, the `logout` action): removes every
  /// cached response, so the next user never sees the previous user's
  /// data, then tells the host's auth delegate.
  Future<void> logout() async {
    await _dataServices.clearCache();
    environment().authDelegate?.onLogout();
  }

  /// Removes every cached response (HST-001, `Plux.wipeData`).
  Future<void> clearDataCache() => _dataServices.clearCache();

  /// The platform's secure storage, which keeps the installation keys of
  /// the secure state store and the data cache (plan p5 D6).
  late final SecretStore _secrets =
      overrides.secrets?.call() ?? const PlatformSecretStore();

  /// The name part of this installation's keys.
  String get _keyId =>
      '${_safe(config.appId)}.${_safe(config.environment)}'.toLowerCase();

  /// Where session, persisted and secure state lives (STA-003): two
  /// stores under the runtime's directory; `persisted` is a plain file,
  /// `secure` is encrypted under its own key kept by the platform's secure
  /// storage (plan p5 D5, D6). A store that fails keeps its values in
  /// memory for the session.
  late final StatePersistence statePersistence = () {
    final id = _keyId;
    final persisted = PlainFileStore(
      path: '$_root/plux-state/persisted.json',
      maxBytes: PluxLimit.statePersistedBytes.defaultValue,
    );
    final secure = EncryptedFileStore(
      path: '$_root/plux-state/secure.pxk',
      secrets: _secrets,
      keyName: 'state-secure.$id',
      label: 'plux-state/secure/$id',
      maxBytes: PluxLimit.stateSecureBytes.defaultValue,
    );
    // The limits of the active app bundle (LIM-001).
    active.addListener(() {
      final limits = active.value?.limits ?? const <String, int>{};
      persisted.maxBytes = limits.valueOf(PluxLimit.statePersistedBytes);
      secure.maxBytes = limits.valueOf(PluxLimit.stateSecureBytes);
    });
    return StatePersistence(
      persisted: persisted,
      secure: secure,
      report: _report,
    );
  }();

  /// Where plugins' collections and key-value entries live (DB-001): the
  /// host's adapter, or the core's built-in store, whose file is sealed
  /// under a key of this installation like the secure state store's
  /// (ADR-0049).
  late final PluxDatabaseAdapter _dbAdapter =
      config.databaseAdapter ??
      BuiltInDatabaseAdapter(
        store: EncryptedFileStore(
          path: '$_root/plux-db/kv.pxk',
          secrets: _secrets,
          keyName: 'db-kv.$_keyId',
          label: 'plux-db/kv/$_keyId',
          maxBytes: PluxLimit.dbKvBytes.max * 4,
        ),
        encrypted: true,
        report: _report,
      );

  final Expando<ReleaseDbDeclarations> _dbDeclarations = Expando();

  /// The local database (DB-001): the layer under the database actions,
  /// `Plux.wipeData` and watched queries.
  late final PluxDatabase database = PluxDatabase(
    adapter: _dbAdapter,
    report: _report,
    declarations: () {
      final release = active.value;
      final r = renderer;
      if (release == null || r is! PluxRenderer) return null;
      return _dbDeclarations[release] ??= ReleaseDbDeclarations(release, r);
    },
  );

  /// Where `Plux.sendEvent` delivers host events (HST-013): the
  /// host-event triggers of the app, its plugins and the pages shown.
  late HostEventSink? hostEventSink = TypedHostEvents(
    release: () => active.value,
    types: (r) => switch (renderer) {
      final PluxRenderer p => p.typesOf(r, ''),
      _ => const {},
    },
    next: HostEventTriggers(triggers),
  );

  /// Turns page sections into widgets (ADR-0031).
  late PageRenderer? renderer = PluxRenderer(
    config: config,
    report: _report,
    failure: (e) => unawaited(failure(e)),
    imageCacheDirectory: '$_root/images',
    statePersistence: statePersistence,
    assets: assets,
    verified: _verified,
    data: _dataServices,
    actions: ActionServices(
      router: router,
      emit: emitHostEvent,
      record: telemetry.record,
      nativeActions: HostActions(
        registered: config.nativeActions,
        declarations: natives,
      ),
      triggers: triggers,
      traces: traces,
      sync: () => unawaited(sync().then((_) {}, onError: (Object _) {})),
      logout: logout,
      services: {
        ...deviceServices(
          packages: config.devicePackages,
          openLink: handleDeepLink,
        ),
        PluxDatabase: database,
      },
      deviceGuard: _deviceGuard,
    )..ownerErrors = _ownerErrors,
  );

  /// Checks the device operations of plugin runs against the capabilities
  /// each plugin declares in the active release, and the host's
  /// `allowedCapabilities` (SEC-080).
  late final DeviceGuard _deviceGuard = DeviceGuard(
    capabilities: (plugin) {
      try {
        final caps = active.value?.meta(plugin).capabilities;
        return (
          deviceApis: caps?.deviceApis ?? const <String>[],
          networkDomains: caps?.networkDomains ?? const <String>[],
        );
      } on PluxException {
        return (deviceApis: const <String>[], networkDomains: const <String>[]);
      }
    },
    deepLinks: () => active.value?.meta('').deepLinks,
    report: _report,
    allowed: config.allowedCapabilities,
    limits: () => active.value?.limits ?? const <String, int>{},
  );

  OwnerLifetime? _owners;

  Future<bool> _ownerErrors(String plugin, ActionError error) =>
      _owners?.current?.errors(plugin, error) ?? Future.value(false);

  /// Runs the app's and the plugins' triggers and error handlers (ACT-002,
  /// ACT-020) with the active release, reaching state through
  /// [container]; `Plux.initialize` connects it.
  void connect(ProviderContainer container) {
    _owners?.dispose();
    _owners = OwnerLifetime(active, (release) {
      final r = renderer;
      if (r is! PluxRenderer) return null;
      return ReleaseOwners(
        renderer: r,
        release: release,
        container: container,
        environment: () => environment(),
        navigatorKey: () => navigatorKey,
      );
    });
  }

  /// Stops the app's and the plugins' triggers, before the container
  /// they read goes.
  void disconnect() {
    _owners?.dispose();
    _owners = null;
  }

  /// What Plux renders with: locale, theme, consent, the user context and
  /// the auth delegate. `Plux.initialize` connects it to its container.
  PluxEnvironment Function() environment = () => const PluxEnvironment();

  /// Decides each entry of a route (NAV-009, ADR-0040).
  late final RouteGuards guards = RouteGuards(
    release: () => active.value,
    run: (release, page, guard, params) =>
        renderer?.runGuard(release, page, guard, params, environment()) ??
        Future.value(const GuardFallsBack('no renderer runs guards')),
    convert: (release, page, params) {
      final r = renderer;
      if (r == null) {
        throw const FormatException('no renderer converts parameters');
      }
      return r.textParams(release, page, params);
    },
    report: _report,
  );

  /// The active release's native catalogue, or null before one
  /// (ADR-0041).
  NativeDeclarations? natives() {
    final release = active.value;
    final r = renderer;
    if (release == null || r is! PluxRenderer) return null;
    try {
      final app = r.view(release, '');
      return (
        routes: app.nativeRoutes,
        slots: app.nativeSlots,
        actions: app.nativeActions,
        types: r.typesOf(release, ''),
      );
    } on Object catch (e) {
      _report(
        PluxException(PluxErrorCode.bundleMalformed, 'native catalogue: $e'),
      );
      return null;
    }
  }

  /// The root navigator deep links and push payloads open on.
  GlobalKey<NavigatorState>? get navigatorKey =>
      config.navigatorKey ?? config.router?.navigatorKey;

  /// Resolves names and opens routes (ADR-0040).
  late final PluxRouter router = PluxRouter(
    release: () => active.value,
    delegate:
        config.navigationDelegate ??
        config.router?.delegate ??
        const PluxNavigatorDelegate(),
    nativeRoutes: {...?config.router?.routes, ...config.nativeRoutes},
    natives: natives,
    page: (route, params, {required guarded}) =>
        PluxScope(child: routedPluxView(route, params, guarded: guarded)),
    guards: guards,
    fallback: (context, error, plugin) =>
        buildFallback(context, config, error, plugin: plugin),
    types: (release, plugin) {
      final r = renderer;
      return r is PluxRenderer
          ? r.typesOf(release, plugin)
          : const <String, NamedType>{};
    },
    report: _report,
    notFoundBuilder: config.notFoundBuilder,
  );

  /// Opens the route [link] maps to (NAV-008), through its guards, on the
  /// navigator of `PluxConfig.navigatorKey`; false, reported, when no
  /// mapping matches (`PLX-4103`) or there is no navigator (`PLX-4102`).
  Future<bool> handleDeepLink(Uri link) async {
    final r = active.value;
    final target = r == null
        ? null
        : resolveDeepLink(r.meta('').deepLinks, link);
    if (r == null || target == null) {
      // Only the link's scheme, host and path: its query may carry user
      // data (SEC-092).
      final where = '${link.scheme}://${link.host}${link.path}';
      _report(
        PluxException(
          PluxErrorCode.deepLinkUnmapped,
          r == null
              ? 'no release yet to resolve $where'
              : 'no deep link maps $where',
          details: {'link': where},
        ),
      );
      return false;
    }
    return _openTarget(r, target);
  }

  /// Opens the route a notification's [payload] names under the app's
  /// push payload key (NAV-008), as [handleDeepLink] opens a link.
  Future<bool> handlePushPayload(Map<String, Object?> payload) async {
    final r = active.value;
    final target = r == null
        ? null
        : resolvePushPayload(r.meta('').push, payload);
    if (r == null || target == null) {
      _report(
        const PluxException(
          PluxErrorCode.deepLinkUnmapped,
          'the push payload names no route of this app',
        ),
      );
      return false;
    }
    final opened = await _openTarget(r, target);
    if (opened) triggers.pushOpened();
    return opened;
  }

  Future<bool> _openTarget(ActiveRelease release, LinkTarget target) async {
    if (navigatorKey?.currentContext == null) {
      _report(
        PluxException(
          PluxErrorCode.navigationRefused,
          'no navigator to open ${target.route} on: set PluxConfig.navigatorKey',
          details: {'route': target.route},
        ),
      );
      return false;
    }
    final params = await _linkParams(release, target);
    final route = await router.resolve(target.route, params);
    final context = navigatorKey?.currentContext;
    if (context == null || !context.mounted) return false;
    unawaited(router.delegate.push<Object?>(context, route));
    return true;
  }

  /// The route a URL names, for router adapters (ADR-0040): [query]'s text
  /// converted by the route's declared types, as a deep link's is, then
  /// resolved through the route's guards.
  Future<PluxRouteSpec> resolveLocation(
    String route,
    Map<String, String> query,
  ) async {
    final r = active.value;
    final params = r == null
        ? query
        : await _linkParams(r, (route: route, params: query));
    return router.resolve(route, params);
  }

  /// A link's parameters in the host's form: text converted by the route's
  /// declared types, and names it does not declare left out, such as a
  /// campaign's query parameters. Text that does not convert stays text,
  /// so entering the page reports it (`PLX-4101`).
  Future<Map<String, Object?>> _linkParams(
    ActiveRelease release,
    LinkTarget target,
  ) async {
    final r = renderer;
    final PageRef? page;
    try {
      page = release.page(target.route);
      if (page == null || r == null) return target.params;
      await release.gate.prepare(
        release.bundle(page.plugin).container.sections,
      );
    } on Object {
      return target.params; // the page host reports it
    }
    final declared = r.paramNames(release, page);
    final out = <String, Object?>{};
    for (final MapEntry(:key, :value) in target.params.entries) {
      if (!declared.contains(key)) continue;
      if (value is! String) {
        out[key] = value;
        continue;
      }
      try {
        out.addAll(r.textParams(release, page, {key: value}));
      } on FormatException {
        out[key] = value;
      }
    }
    return out;
  }

  final StreamController<PluxHostEvent> _hostEvents =
      StreamController.broadcast();

  /// The host events plugins emit (HST-013).
  Stream<PluxHostEvent> get hostEvents => _hostEvents.stream;

  /// Posts a host event; the payload, in PXL form, reaches the host in its
  /// JSON form.
  void emitHostEvent(String name, Map<String, Object?> payload) {
    if (_hostEvents.isClosed) return;
    _hostEvents.add(
      PluxHostEvent(name, {
        for (final e in payload.entries) e.key: toJson(e.value),
      }),
    );
  }

  /// Reports a problem to the diagnostics, telemetry and the host.
  void reportProblem(PluxException e) => _report(e);

  /// Loads the app's font assets under the families their files name, so
  /// `fontFamily` tokens and script fallbacks find them (THM-004). A font
  /// that cannot be loaded is reported; text falls back to other families.
  Future<void> _loadFonts(ActiveRelease? release) async {
    if (release == null) return;
    try {
      await loadFonts(
        {
          for (final a in assetsOf(release.bundle('').container))
            if ((a.mediaType == 'font/ttf' || a.mediaType == 'font/otf') &&
                !isIconFontKey(a.key ?? ''))
              hexEncode(a.hash ?? const []): ?release.assetPath(
                hexEncode(a.hash ?? const []),
              ),
        },
        _fonts,
        _verified,
      );
    } on PluxException catch (e) {
      _report(e);
    }
  }

  /// What debugging tools see (`plux_devtools`).
  late final RuntimeDiagnostics diagnostics = RuntimeDiagnostics(
    active,
    lastEvent,
    traces: traces.listenable,
  );

  /// Dispatches app lifecycle, push, host and data-source events to the
  /// triggers that handle them (ACT-002).
  final TriggerHub triggers = TriggerHub();

  /// The latest action run traces (ACT-030).
  final TraceBuffer traces = TraceBuffer(
    capacity: PluxLimit.actionTraceRuns.defaultValue,
  );

  int _mounted = 0;
  bool _pendingActivation = false;
  bool _pendingRevert = false;
  bool _healthyScheduled = false;
  Timer? _healthy;
  bool _disposed = false;
  // Null while nothing ran: a future, even a completed one, delivers its
  // result in a microtask of the zone that created it, so a first future made
  // in a widget test's fake-async zone would stall a later caller's chain.
  Future<void>? _pointerChange;

  /// The typed sync events (SYN-013).
  Stream<SyncEvent> get events => _events.stream;

  /// Plux pages currently mounted.
  int get mountedPages => _mounted;

  Future<PluxStartup> _startup() async {
    var pointer = _store.pointer;
    if (pointer.active == null || pointer.staged == null) {
      try {
        await _worker.importBaseline();
      } on PluxException catch (e) {
        _report(e);
      }
    }
    // A release staged before the last exit activates now, before any page
    // shows (nextLaunch, or an app killed before its safe point).
    if (_reopen().staged != null) {
      pointer = await _worker.activate();
    }
    try {
      settings.value = await _worker.loadSettings() ?? settings.value;
    } on StateError catch (e) {
      _report(
        PluxException(PluxErrorCode.syncFailed, 'settings: ${e.message}'),
      );
    }
    final before = _reopen().active;
    pointer = await _worker.beginLaunch();
    if (before != null && pointer.active != before) {
      _emit(SyncRolledBack(before, pointer.active!));
      _report(
        PluxException(
          PluxErrorCode.revertedToLastKnownGood,
          'release $before failed its trial; back to ${pointer.active}',
        ),
      );
    }
    _load();
    // Before the first sync, so the flush after it carries the session's
    // start with the release the device runs.
    _startSession();
    final timeout = config.startup.timeout;
    if (active.value == null) {
      final r = await sync();
      if (r.outcome == SyncOutcome.staged) await _activateNow();
      final ready = active.value?.sequence;
      return PluxStartup._(
        ready,
        ready == null
            ? r.error ??
                  const PluxException(PluxErrorCode.syncFailed, 'no release')
            : null,
      );
    }
    if (timeout != null) {
      try {
        final r = await sync().timeout(timeout);
        if (r.outcome == SyncOutcome.staged) await _activateNow();
      } on TimeoutException {
        // Continue with what is on disk; the sync goes on in the background.
      }
    } else {
      unawaited(sync());
    }
    return PluxStartup._(active.value?.sequence, null);
  }

  /// Syncs now (SYN-002); a running sync is joined, and [SyncRun.progress]
  /// carries its events from now on.
  SyncRun sync() {
    final progress = StreamController<SyncEvent>();
    final forward = _events.stream.listen(progress.add);
    Future<SyncResult> run() async {
      try {
        final r = await _worker.sync(_emit);
        if (r.settings != null) settings.value = r.settings!;
        if (r.configError != null) _report(r.configError!);
        if (r.outcome == SyncOutcome.staged) _onStaged();
        // The radio is awake: record the result and send what is buffered
        // (SYN-015, ADR-0034).
        telemetry.record('sync_result', fields: r.toTelemetry());
        unawaited(flushTelemetry());
        return r;
      } finally {
        await forward.cancel();
        // Not awaited: it completes only once someone reads the stream.
        unawaited(progress.close());
      }
    }

    return SyncRun(run(), progress.stream);
  }

  void _emit(SyncEvent e) {
    lastEvent.value = e;
    if (e is SyncFailed) _report(e.error);
    if (!_events.isClosed) _events.add(e);
  }

  void _onStaged() {
    if (config.activation == ActivationPolicy.nextLaunch) return;
    _pendingActivation = true;
    unawaited(_atSafePoint());
  }

  /// Activates or reverts when no Plux page is mounted (SYN-004, SYN-006).
  Future<void> _atSafePoint() => _serially(() async {
    if (_mounted > 0 || _disposed) return;
    if (_pendingRevert) {
      _pendingRevert = false;
      final from = active.value?.sequence;
      final p = await _worker.revert();
      if (from != null && p.active != from) {
        _load();
        _emit(SyncRolledBack(from, p.active!));
        _report(
          PluxException(
            PluxErrorCode.revertedToLastKnownGood,
            'release $from failed its trial; back to ${p.active}',
          ),
        );
      }
    }
    if (_pendingActivation) {
      _pendingActivation = false;
      await _activate();
    }
  });

  Future<void> _activateNow() => _serially(_activate);

  /// Runs pointer changes one at a time: a sync that stages during startup
  /// schedules a safe point while startup activates the same release.
  Future<void> _serially(Future<void> Function() step) {
    final next = (_pointerChange ?? Future<void>.value()).then((_) => step());
    _pointerChange = next.catchError((Object _) {});
    return next;
  }

  Future<void> _activate() async {
    if (_reopen().staged == null) return;
    await _worker.activate();
    _load();
    final seq = active.value?.sequence;
    if (seq != null) _emit(SyncActivated(seq));
  }

  StorePointer _reopen() => ReleaseStore.open(_store.root).pointer;

  /// Maps the active release from the store's current pointer.
  void _load() {
    final store = ReleaseStore.open(_store.root);
    final seq = store.pointer.active;
    final record = seq == null ? null : store.record(seq);
    final previous = active.value;
    if (record == null) {
      active.value = null;
    } else {
      try {
        active.value = ActiveRelease.open(
          store,
          record,
          _limits,
          report: _report,
        );
      } on PluxException catch (e) {
        _report(e);
        active.value = null;
      }
    }
    previous?.retire();
  }

  /// A page mounted: it holds the active release until [unmount].
  ActiveRelease? mount() {
    final r = active.value;
    if (r == null) return null;
    r.acquire();
    _mounted++;
    return r;
  }

  /// A page unmounted; at zero mounted pages a pending activation runs.
  void unmount(ActiveRelease release) {
    release.releaseLease();
    if (_mounted > 0) _mounted--;
    if (_mounted == 0) unawaited(_atSafePoint());
  }

  /// A Plux page rendered its first frame; after the healthy period the
  /// launch counts as healthy (SYN-006).
  void firstFrame() {
    if (_healthyScheduled) return;
    _healthyScheduled = true;
    // A runtime timer, not a widget's: it lives in the root zone.
    _healthy = Zone.root.createTimer(
      overrides.healthyAfter,
      () => unawaited(_worker.markHealthy()),
    );
  }

  /// A failure attributed to Plux (a page's root boundary fell back, or an
  /// uncaught runtime error): counts against the trial (SYN-006).
  Future<void> failure(PluxException error) async {
    _report(error);
    final p = await _worker.recordFailure();
    final t = p.trial;
    if (t != null && t.failures >= 3 && p.active == t.sequence) {
      _pendingRevert = true;
      await _atSafePoint();
    }
  }

  @override
  void didHaveMemoryPressure() {
    final r = renderer;
    if (r is PluxRenderer) r.memoryPressure();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.paused) triggers.appPaused();
    if (state == AppLifecycleState.resumed) {
      triggers.appResumed();
      _dataServices.appResumed();
    }
    if (state == AppLifecycleState.paused) {
      // Reaching the background normally ends the launch healthily.
      if (_healthyScheduled) unawaited(_worker.markHealthy());
      if (_inForeground) {
        _endStretch();
        _backgroundSince = _clock();
        unawaited(flushTelemetry());
      }
      unawaited(statePersistence.flush());
    } else if (state == AppLifecycleState.resumed && !_inForeground) {
      final away = _clock().difference(_backgroundSince!);
      if (away >= sessionTimeout) {
        _startSession();
      } else {
        _foregroundSince = _clock();
        _backgroundSince = null;
      }
    }
  }

  void _report(PluxException e) {
    developer.log(e.toString(), name: 'plux');
    diagnostics.record(e);
    telemetry.error(e);
    config.onError?.call(e, null);
  }

  /// Stops the sync isolate and releases every mapping.
  Future<void> dispose() async {
    disconnect();
    WidgetsBinding.instance.removeObserver(this);
    _flushEvery?.cancel();
    if (_inForeground) _endStretch();
    // The sync isolate handles messages in order: once this answers, every
    // event recorded before it is in the buffer, for the next launch. A
    // sync in progress would hold the answer, so the wait is bounded.
    await _worker.settle().timeout(
      const Duration(seconds: 1),
      onTimeout: () {},
    );
    await statePersistence.flush();
    // Open streams and the outbox's replay timer stop with the runtime.
    await _dataServices.close();
    // A host's adapter is the host's to close.
    if (config.databaseAdapter == null) await _dbAdapter.close();
    _disposed = true;
    _healthy?.cancel();
    diagnostics.dispose();
    _worker.close();
    await _events.close();
    await _hostEvents.close();
    active.value?.retire();
    active.value = null;
  }

  static String _safe(String s) => s.replaceAll(RegExp('[^A-Za-z0-9._-]'), '_');

  static Future<List<PluxPublicKey>> _bundledKeys(String? baseline) async {
    if (baseline == null) return const [];
    try {
      return PluxPublicKey.parseKeysJson(
        await rootBundle.loadString('$baseline/keys.json', cache: false),
      );
    } on FlutterError {
      return const [];
    }
  }

  /// The platform's attestation, created on the sync isolate. This is the
  /// one place `PlatformAttestation` is constructed, from the app, the
  /// environment, `playIntegrityCloudProjectNumber` and `hostBuild`.
  static Attestation Function() _platformAttestation(PluxConfig c) {
    // Copied out of the config: the closure runs on the sync isolate and
    // must not capture PluxConfig, which is not sendable.
    final appId = c.appId;
    final environment = c.environment;
    final cloudProjectNumber = c.playIntegrityCloudProjectNumber;
    final buildId = c.hostBuild;
    return () => PlatformAttestation(
      appId: appId,
      environment: environment,
      cloudProjectNumber: cloudProjectNumber,
      buildId: buildId,
    );
  }

  static CredentialStore Function() _platformCredentials(PluxConfig c) {
    final name = 'device.${_safe(c.appId)}.${_safe(c.environment)}'
        .toLowerCase();
    return () => PlatformCredentialStore(name);
  }
}
