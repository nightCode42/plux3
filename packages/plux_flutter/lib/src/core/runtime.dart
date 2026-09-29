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
import 'dart:ui' as ui;

import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';
import 'package:plux_flutter/src/assets/assets.dart';
import 'package:plux_flutter/src/assets/avif_probe.dart';
import 'package:plux_flutter/src/assets/fonts.dart';
import 'package:plux_flutter/src/assets/image_providers.dart';
import 'package:plux_flutter/src/core/active_release.dart';
import 'package:plux_flutter/src/core/config.dart';
import 'package:plux_flutter/src/core/features.dart';
import 'package:plux_flutter/src/devtools_api/diagnostics.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/platform/platform_services.dart';
import 'package:plux_flutter/src/render/page_renderer.dart';
import 'package:plux_flutter/src/render/renderer.dart';
import 'package:plux_flutter/src/runtime_info.dart';
import 'package:plux_flutter/src/schema/limits.g.dart';
import 'package:plux_flutter/src/store/baseline.dart';
import 'package:plux_flutter/src/store/pointer.dart';
import 'package:plux_flutter/src/store/release_store.dart';
import 'package:plux_flutter/src/sync/api_client.dart';
import 'package:plux_flutter/src/sync/sync_engine.dart';
import 'package:plux_flutter/src/sync/sync_event.dart';
import 'package:plux_flutter/src/sync/sync_worker.dart';
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
  });

  /// Creates the credential store on the sync isolate.
  final CredentialStore Function()? credentials;

  /// Reads the baseline on the sync isolate.
  final BaselineReader? baseline;

  /// How long a page must render without failure before a launch counts as
  /// healthy (SYN-006).
  final Duration healthyAfter;
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
          baseline:
              overrides.baseline ??
              (config.baseline == null
                  ? null
                  : assetBaseline(config.baseline!)),
          rootIsolateToken: RootIsolateToken.instance,
          sync: SyncConfig(
            appId: config.appId,
            environment: config.environment,
            channel: config.channel,
            keys: [for (final k in keys) k.toTrustedKey()],
            device: DeviceInfo(
              platform: Platform.operatingSystem,
              osVersion: Platform.operatingSystemVersion,
              runtimeVersion: PluxRuntimeInfo.version,
              hostBuild: config.hostBuild,
            ),
            supportsFeature: features.supports,
            verifierLimits: limits,
            diskQuota:
                config.diskQuota ?? PluxLimit.deviceDiskQuota.defaultValue,
            assets: assets,
          ),
        ),
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
  );

  /// Loads the app's font assets under the families their files name, so
  /// `fontFamily` tokens and script fallbacks find them (THM-004). A font
  /// that cannot be loaded is reported; text falls back to other families.
  Future<void> _loadFonts(ActiveRelease? release) async {
    if (release == null) return;
    try {
      await loadFonts(
        {
          for (final a in assetsOf(release.bundle('').container))
            if (a.mediaType == 'font/ttf' || a.mediaType == 'font/otf')
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
    // Reaching the background normally ends the launch healthily.
    if (state == AppLifecycleState.paused && _healthyScheduled) {
      unawaited(_worker.markHealthy());
    }
  }

  void _report(PluxException e) {
    developer.log(e.toString(), name: 'plux');
    diagnostics.record(e);
    config.onError?.call(e, null);
  }

  /// Stops the sync isolate and releases every mapping.
  Future<void> dispose() async {
    _disposed = true;
    WidgetsBinding.instance.removeObserver(this);
    _healthy?.cancel();
    diagnostics.dispose();
    _worker.close();
    await _events.close();
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
