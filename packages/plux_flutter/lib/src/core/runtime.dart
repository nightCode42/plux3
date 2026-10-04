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
import 'package:plux_flutter/src/devtools_api/diagnostics.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/native_catalogue/host.dart';
import 'package:plux_flutter/src/navigation/deep_links.dart';
import 'package:plux_flutter/src/navigation/delegate.dart';
import 'package:plux_flutter/src/navigation/guards.dart';
import 'package:plux_flutter/src/navigation/router.dart';
import 'package:plux_flutter/src/platform/platform_services.dart';
import 'package:plux_flutter/src/pxl/types.dart';
import 'package:plux_flutter/src/render/page_renderer.dart';
import 'package:plux_flutter/src/render/renderer.dart';
import 'package:plux_flutter/src/runtime_info.dart';
import 'package:plux_flutter/src/schema/limits.g.dart';
import 'package:plux_flutter/src/state/providers.dart';
import 'package:plux_flutter/src/store/baseline.dart';
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
    this.baseline,
    this.healthyAfter = const Duration(seconds: 10),
    this.clock,
    this.random,
  });

  /// Creates the credential store on the sync isolate.
  final CredentialStore Function()? credentials;

  /// Reads the baseline on the sync isolate.
  final BaselineReader? baseline;

  /// How long a page must render without failure before a launch counts as
  /// healthy (SYN-006).
  final Duration healthyAfter;

  /// The clock telemetry reads; the wall clock when null.
  final DateTime Function()? clock;

  /// Where telemetry sampling draws from; a new generator when null.
  final math.Random? random;
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
  int limit(PluxLimit l) => active.value?.limits[l.key] ?? l.defaultValue;

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

  /// Turns page sections into widgets (ADR-0031).
  late PageRenderer? renderer = PluxRenderer(
    config: config,
    report: _report,
    failure: (e) => unawaited(failure(e)),
    imageCacheDirectory: '$_root/images',
    assets: assets,
    verified: _verified,
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
    ),
  );

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
  Future<void> _pointerChange = Future.value();

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
    final next = _pointerChange.then((_) => step());
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
    if (state == AppLifecycleState.resumed) triggers.appResumed();
    if (state == AppLifecycleState.paused) {
      // Reaching the background normally ends the launch healthily.
      if (_healthyScheduled) unawaited(_worker.markHealthy());
      if (_inForeground) {
        _endStretch();
        _backgroundSince = _clock();
        unawaited(flushTelemetry());
      }
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

  static CredentialStore Function() _platformCredentials(PluxConfig c) {
    final name = 'device.${_safe(c.appId)}.${_safe(c.environment)}'
        .toLowerCase();
    return () => PlatformCredentialStore(name);
  }
}
