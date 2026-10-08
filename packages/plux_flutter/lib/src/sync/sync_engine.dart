// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// One sync of every plugin of the app (SYN-001, ADR-0021): credential,
/// manifest, verification, plan, downloads, delta application and
/// verification with one full-bundle retry (SYN-011), and staging. It runs
/// on the sync isolate (L-6); activation is the runtime's decision
/// (SYN-004).
library;

import 'dart:io';
import 'dart:math' as math;
import 'dart:typed_data';

import 'package:crypto/crypto.dart';
import 'package:flutter/foundation.dart' show kReleaseMode;
import 'package:plux_flutter/src/assets/assets.dart';
import 'package:plux_flutter/src/bundle/container.dart';
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/delta/delta.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/mmap/mapped_file.dart';
import 'package:plux_flutter/src/schema/limits.g.dart';
import 'package:plux_flutter/src/security/attestation.dart';
import 'package:plux_flutter/src/security/device_keys.dart';
import 'package:plux_flutter/src/security/pins.dart';
import 'package:plux_flutter/src/security/security_config.dart';
import 'package:plux_flutter/src/store/kv_store.dart' show SecretStore;
import 'package:plux_flutter/src/store/metadata_state.dart';
import 'package:plux_flutter/src/store/release_record.dart';
import 'package:plux_flutter/src/store/release_store.dart';
import 'package:plux_flutter/src/sync/api_client.dart';
import 'package:plux_flutter/src/sync/device_auth.dart';
import 'package:plux_flutter/src/sync/downloader.dart';
import 'package:plux_flutter/src/sync/metadata_sync.dart';
import 'package:plux_flutter/src/sync/sync_event.dart';
import 'package:plux_flutter/src/verify/bundle_verifier.dart';
import 'package:plux_flutter/src/verify/manifest.dart';

/// Keeps the device credential; the platform implementation encrypts it
/// under a platform-held key (ADR-0029).
abstract interface class CredentialStore {
  /// The stored credential, or null.
  Future<DeviceCredential?> read();

  /// Stores [credential].
  Future<void> write(DeviceCredential credential);

  /// Forgets the credential.
  Future<void> clear();
}

/// A credential store in memory, for tests and development.
final class MemoryCredentialStore implements CredentialStore {
  DeviceCredential? _value;

  @override
  Future<DeviceCredential?> read() async => _value;

  @override
  Future<void> write(DeviceCredential credential) async => _value = credential;

  @override
  Future<void> clear() async => _value = null;
}

/// A secret store in memory, for tests and development; the platform's
/// keeps secrets encrypted under a key it holds (ADR-0029).
final class MemorySecretStore implements SecretStore {
  /// The secrets, by name.
  final Map<String, String> values = {};

  @override
  Future<String?> read(String name) async => values[name];

  @override
  Future<void> write(String name, String value) async => values[name] = value;

  @override
  Future<void> delete(String name) async => values.remove(name);
}

/// What a sync needs to know about the app and this runtime.
final class SyncConfig {
  /// Creates the configuration.
  const SyncConfig({
    required this.appId,
    required this.environment,
    required this.channel,
    required this.keys,
    required this.device,
    required this.supportsFeature,
    required this.verifierLimits,
    this.diskQuota = 200 * 1024 * 1024,
    this.maxBundleSize = 20 * 1024 * 1024,
    this.assets = AssetDevice.plain,
    this.production = kReleaseMode,
    this.rootDocument,
    this.pins,
  });

  /// The app.
  final String appId;

  /// The environment key.
  final String environment;

  /// The channel key.
  final String channel;

  /// The embedded keys (SEC-051).
  final List<TrustedKey> keys;

  /// This device and runtime.
  final DeviceInfo device;

  /// Whether this runtime supports a required feature (BND-008).
  final bool Function(String feature) supportsFeature;

  /// The FlatBuffers verifier's limits.
  final VerifierLimits verifierLimits;

  /// The most bytes the store may use whatever the app allows: the host's
  /// `PluxConfig.diskQuota`. The quota in effect is the `device.diskQuota`
  /// of the release's signed app bundle within this cap (SYN-012,
  /// LIM-004).
  final int diskQuota;

  /// The largest bundle accepted (`bundle.pluginSize`).
  final int maxBundleSize;

  /// What decides which file of each asset this device downloads.
  final AssetDevice assets;

  /// Whether this is a production runtime, which refuses update metadata
  /// and keys of the `development` environment type (SEC-056). A release
  /// build by default.
  final bool production;

  /// The root file the app embeds (`root.json` beside `keys.json`), the
  /// trust anchor of the update metadata in preference to the root made
  /// from [keys] (SEC-051).
  final Uint8List? rootDocument;

  /// The pins of the Plux server the HTTP client enforces; the pins of a
  /// verified root replace them (SEC-041). It must be the very object the
  /// client was made with.
  final PinSet? pins;
}

/// Runs syncs against one store.
final class SyncEngine {
  /// Creates an engine.
  SyncEngine({
    required this.config,
    required this.store,
    required this.api,
    required this.downloader,
    required this.credentials,
    required this.keys,
    required this.attestation,
    SecretStore? configSecrets,
    DateTime Function()? clock,
  }) : _clock = clock ?? DateTime.now,
       _configs = configSecrets == null
           ? null
           : SecurityConfigStore(
               configSecrets,
               config.appId,
               config.environment,
             ) {
    _auth = DeviceAuth(
      api: api,
      keys: keys,
      attestation: attestation,
      credentials: credentials,
      appId: config.appId,
      environment: config.environment,
      device: config.device,
      clock: _clock,
    );
    _metadata = MetadataSync(
      api: api,
      store: MetadataStore(store.root),
      keys: config.keys,
      embeddedRoot: config.rootDocument,
      host: api.endpoint.host,
      pins: config.pins,
      appId: config.appId,
      environment: config.environment,
      production: config.production,
      clock: _clock,
    );
  }

  /// The configuration.
  final SyncConfig config;

  /// The release store.
  final ReleaseStore store;

  /// The API client.
  final PluxApiClient api;

  /// The downloader.
  final Downloader downloader;

  /// Where the device credential is kept.
  final CredentialStore credentials;

  /// The hardware-held device keys (SEC-001).
  final DeviceKeys keys;

  /// The platform's attestation services (SEC-002).
  final Attestation attestation;

  final DateTime Function() _clock;
  late final DeviceAuth _auth;
  late final MetadataSync _metadata;
  final SecurityConfigStore? _configs;

  /// The settings in force: the verified stored configuration, or the
  /// built-in defaults when there is none or it no longer verifies
  /// (SEC-182). Null when the engine keeps no configuration.
  Future<SecuritySettings?> loadSettings() async =>
      _settingsOf(await _configs?.read());

  SecuritySettings? _settingsOf(StoredSecurityConfig? stored) =>
      _configs == null
      ? null
      : stored == null
      ? SecuritySettings.builtIn
      : SecuritySettings.fromDocument(stored.document, version: stored.version);

  /// Syncs once, reporting [SyncEvent]s to [emit]. Never throws: a failure
  /// is a [SyncResult] with [SyncOutcome.failed], and the active release
  /// is untouched.
  Future<SyncResult> run(void Function(SyncEvent) emit) async {
    final started = _clock();
    final bytes = _Counter();
    try {
      emit(const SyncChecking());
      _auth.beginSync();
      final token = await _auth.token();
      final pointer = store.pointer;
      final active = pointer.active == null
          ? null
          : store.record(pointer.active!);
      final hashes = {
        for (final b in active?.bundles ?? const <RecordBundle>[])
          b.key: b.hash,
      };
      final etag = active == null ? '' : pointer.etag;
      // The configuration the device holds, verified again against the
      // hash stored with it, and its version for the request (SEC-182).
      final stored = await _configs?.read();
      final sentConfig = stored?.version ?? 0;
      var settings = _settingsOf(stored);
      PluxException? configError;
      // An up-to-date check sends the digest of the installed bundles,
      // and the bundles themselves only when the server asks: when the
      // manifest changed, its plan's deltas depend on them (NFR-006).
      Future<ManifestResponse> ask({
        required bool list,
        String? ifNoneMatch,
        int configVersion = -1,
      }) => api.manifest(
        token: token,
        appId: config.appId,
        environment: config.environment,
        channel: config.channel,
        installedSequence: active?.sequence ?? 0,
        installed: {
          if (list)
            for (final MapEntry(:key, :value) in hashes.entries)
              key: 'sha256:$value',
        },
        ifNoneMatch: ifNoneMatch ?? etag,
        installedDigest: list ? const [] : installedDigest(hashes),
        configVersion: configVersion < 0 ? sentConfig : configVersion,
      );
      var res = await ask(list: etag.isEmpty);
      if (res.installedRequired) {
        res = await ask(list: true);
      }
      if (res.notModified) {
        // The timestamp is looked at on every sync (SEC-050): a server that
        // goes on answering "not modified" cannot hold the device on
        // metadata that has expired.
        await _metadata.checkTimestamp(token);
        emit(SyncUpToDate(pointer.active));
        return SyncResult(
          outcome: SyncOutcome.upToDate,
          sequence: pointer.active,
          duration: _clock().difference(started),
          settings: settings,
        );
      }
      // The metadata chain first (SEC-050): its root, not the app's
      // embedded keys, says who signs the manifest once keys rotate.
      MetadataTrust? trust;
      Future<VerifiedManifest> verify(ManifestResponse r) async {
        trust = await _metadata.verify(token, r);
        return verifyManifest(
          r.signed!,
          [for (final s in r.signatures) DocumentSignature.fromJson(s)],
          VerificationContext(
            keys: trust?.targetsKeys ?? config.keys,
            app: config.appId,
            environment: config.environment,
            channel: config.channel,
            now: _clock(),
            highestAccepted: pointer.highestAccepted,
            runtimeVersion: config.device.runtimeVersion,
            supportsFeature: config.supportsFeature,
          ),
        );
      }

      var m = await verify(res);
      if (_configs != null && m.config != null) {
        ConfigApplied applied = applyConfigUpdate(
          ref: m.config!,
          current: stored,
          sentVersion: sentConfig,
          patch: res.configPatch,
          fullRequired: res.configFullRequired,
        );
        if (applied is ConfigRejected) {
          // Ask once more for the whole configuration, from version 0,
          // and a manifest to match (SEC-182, PLX-6040).
          res = await ask(list: true, ifNoneMatch: '', configVersion: 0);
          m = await verify(res);
          applied = m.config == null
              ? const ConfigCurrent()
              : applyConfigUpdate(
                  ref: m.config!,
                  current: stored,
                  sentVersion: 0,
                  patch: res.configPatch,
                  fullRequired: res.configFullRequired,
                );
        }
        switch (applied) {
          case ConfigCurrent():
            break;
          case ConfigUpdated(:final config):
            try {
              await _configs.write(config);
              settings = _settingsOf(config);
            } on PluxException catch (e) {
              configError = e;
            }
          case ConfigRejected(:final error):
            configError = error;
        }
      }
      final accepted = trust;
      if (accepted != null) _metadata.commit(accepted);
      store.accept(m.releaseSequence, res.etag);
      final control = ControlState(
        sequence: m.releaseSequence,
        killSwitches: m.control.killSwitches,
        appKillSwitch: m.control.appKillSwitch,
        mandatory: m.control.mandatory,
        message: m.control.message,
      );
      store.writeControl(control);
      final seq = m.releaseSequence;
      if (seq == pointer.active ||
          seq == pointer.staged ||
          seq == pointer.rejected) {
        emit(SyncUpToDate(pointer.active));
        return SyncResult(
          outcome: SyncOutcome.upToDate,
          sequence: pointer.active,
          duration: _clock().difference(started),
          settings: settings,
          configError: configError,
        );
      }
      final served = {for (final b in res.bundles) b.key: b};
      final wanted = <RecordBundle>[
        RecordBundle(
          key: '',
          version: 0,
          hash: hexEncode(m.appBundle.hash),
          features: m.appBundle.requiredFeatures,
        ),
        for (final p in m.plugins)
          RecordBundle(
            key: p.key,
            version: p.version,
            hash: hexEncode(p.bundle.hash),
            features: p.bundle.requiredFeatures,
          ),
      ];
      final sizes = {for (final b in m.bundles) hexEncode(b.hash): b.size};
      final missing = [
        for (final w in wanted)
          if (!store.hasObject(ObjectKind.bundles, w.hash)) w,
      ];
      var total = missing.fold<int>(0, (n, w) => n + (sizes[w.hash] ?? 0));
      final installed = {
        for (final b in active?.bundles ?? const <RecordBundle>[])
          b.key: b.hash,
      };
      var received = 0;
      void progress(int n) {
        received += n;
        bytes.n += n;
        emit(SyncDownloading(received, total));
      }

      // The app bundle first: its limits set the quota the rest must fit.
      final app = wanted.first;
      final queue = [...missing];
      if (queue.remove(app)) {
        await _obtain(
          app,
          served[app.key],
          installed[app.key],
          sizes[app.hash] ?? 0,
          progress,
        );
      }
      final quota = _quota(app.hash);
      if (store.keptUsage() + total > quota) {
        store.collectGarbage();
        throw PluxException(
          PluxErrorCode.diskQuotaExceeded,
          'the release needs ${store.keptUsage() + total} bytes, over the quota of $quota',
        );
      }
      Future<void> worker() async {
        while (queue.isNotEmpty) {
          final w = queue.removeAt(0);
          await _obtain(
            w,
            served[w.key],
            installed[w.key],
            sizes[w.hash] ?? 0,
            progress,
          );
        }
      }

      await Future.wait([
        for (var i = 0; i < downloader.parallelism && i < queue.length; i++)
          worker(),
      ]);
      final assets = await _obtainAssets(
        wanted,
        quota,
        total,
        (n) => total += n,
        progress,
      );
      final record = ReleaseRecord(
        sequence: seq,
        source: ReleaseSource.sync,
        bundles: wanted,
        assets: assets,
        signed: m.signed,
        signatures: res.signatures,
        killSwitches: control.killSwitches,
        appKillSwitch: control.appKillSwitch,
        message: control.message,
      );
      store.stage(record);
      store.collectGarbage();
      emit(SyncStaged(seq, mandatory: control.mandatory));
      return SyncResult(
        outcome: SyncOutcome.staged,
        sequence: seq,
        duration: _clock().difference(started),
        bytes: bytes.n,
        fullBytes: total,
        pluginsUpdated: missing.where((w) => w.key.isNotEmpty).length,
        settings: settings,
        configError: configError,
      );
    } on PluxException catch (e) {
      return _failed(emit, e, started, bytes.n);
    } on ApiError catch (e) {
      return _failed(emit, e.toException('sync'), started, bytes.n);
    } on DownloadFailed catch (e) {
      return _failed(
        emit,
        PluxException(PluxErrorCode.syncFailed, '$e'),
        started,
        bytes.n,
      );
    } on FileSystemException catch (e) {
      return _failed(
        emit,
        PluxException(PluxErrorCode.syncFailed, 'storage: ${e.message}'),
        started,
        bytes.n,
      );
    }
  }

  SyncResult _failed(
    void Function(SyncEvent) emit,
    PluxException e,
    DateTime started,
    int bytes,
  ) {
    emit(SyncFailed(e));
    return SyncResult(
      outcome: SyncOutcome.failed,
      duration: _clock().difference(started),
      bytes: bytes,
      error: e,
    );
  }

  /// Downloads the asset files the release's bundles index that the
  /// store lacks, one file per asset as this device prefers it (AST-001),
  /// each checked against the SHA-256 its signed bundle lists; returns the
  /// files the release uses. [bundleBytes] were already counted against
  /// [quota]; [planned] learns the bytes the assets add to the download.
  Future<List<String>> _obtainAssets(
    List<RecordBundle> bundles,
    int quota,
    int bundleBytes,
    void Function(int) planned,
    void Function(int) progress,
  ) async {
    final sizes = <String, int>{};
    for (final b in bundles) {
      final file = MappedFile.open(
        store.objectPath(ObjectKind.bundles, b.hash),
      );
      try {
        for (final a in assetsOf(BundleContainer.parse(file.bytes))) {
          final files = preferredFiles(a, config.assets);
          if (files.isEmpty) continue;
          sizes[files.first] = fileSize(a, files.first);
        }
      } finally {
        file.release();
      }
    }
    final missing = [
      for (final h in sizes.keys)
        if (!store.hasObject(ObjectKind.assets, h)) h,
    ]..sort();
    final needed = missing.fold<int>(0, (n, h) => n + sizes[h]!);
    if (store.keptUsage() + bundleBytes + needed > quota) {
      throw PluxException(
        PluxErrorCode.diskQuotaExceeded,
        'the release and its assets need ${store.keptUsage() + bundleBytes + needed} bytes, over the quota of $quota',
      );
    }
    planned(needed);
    final queue = [...missing];
    Future<void> worker() async {
      while (queue.isNotEmpty) {
        final h = queue.removeAt(0);
        final part = store.partPath('asset-$h');
        await downloader.fetch(
          Download(
            api.endpoint.resolve('v1/objects/assets/${h.substring(0, 2)}/$h'),
            part,
            expectedSize: sizes[h],
          ),
          onBytes: progress,
        );
        final got = sha256.convert(File(part).readAsBytesSync()).toString();
        if (got != h) {
          File(part).deleteSync();
          throw PluxException(
            PluxErrorCode.assetHashMismatch,
            'asset file $h arrived as $got',
          );
        }
        store.commitObject(ObjectKind.assets, h, part);
      }
    }

    await Future.wait([
      for (var i = 0; i < downloader.parallelism && i < missing.length; i++)
        worker(),
    ]);
    return sizes.keys.toList()..sort();
  }

  /// A device token for other calls between syncs, such as telemetry: the
  /// held one while it is valid for at least thirty seconds, otherwise a
  /// new one.
  Future<DeviceToken> recentToken() => _auth.token();

  /// Forgets the held token, after the server refused it.
  void forgetToken() => _auth.forgetToken();

  /// Obtains one bundle: by its delta when the plan offers one against
  /// the bundle this device holds, else — or when the rebuilt bundle is
  /// not the signed one — as the full bundle (SYN-011).
  Future<void> _obtain(
    RecordBundle w,
    ServedBundle? step,
    String? installed,
    int size,
    void Function(int) progress,
  ) async {
    if (step == null || step.hash != 'sha256:${w.hash}') {
      throw PluxException(
        PluxErrorCode.syncFailed,
        'the server sent no plan for ${w.key.isEmpty ? 'the app bundle' : w.key}',
      );
    }
    final hash = hexDecode(w.hash);
    final canPatch =
        step.action == 'delta' &&
        step.stepUrl != null &&
        installed != null &&
        step.from == 'sha256:$installed' &&
        store.hasObject(ObjectKind.bundles, installed);
    if (canPatch) {
      final part = store.partPath('delta-${w.hash}');
      try {
        await downloader.fetch(
          Download(step.stepUrl!, part, expectedSize: step.stepSize),
          onBytes: progress,
        );
        final base = MappedFile.open(
          store.objectPath(ObjectKind.bundles, installed),
        );
        final Uint8List rebuilt;
        try {
          rebuilt = applyDelta(
            base.bytes,
            File(part).readAsBytesSync(),
            config.maxBundleSize,
          );
        } finally {
          base.release();
        }
        _verify(rebuilt, hash, fromDelta: true);
        store.writeObject(ObjectKind.bundles, w.hash, rebuilt);
        return;
      } on PluxException catch (e) {
        if (e.code != PluxErrorCode.patchHashMismatch &&
            e.code != PluxErrorCode.deltaMalformed &&
            e.code != PluxErrorCode.bundleMalformed &&
            e.code != PluxErrorCode.sectionHashMismatch &&
            e.code != PluxErrorCode.sectionVerificationFailed) {
          rethrow;
        }
        // Discard the result and fall back to the full bundle, once.
      } finally {
        final f = File(part);
        if (f.existsSync()) f.deleteSync();
      }
    }
    final part = store.partPath('bundle-${w.hash}');
    await downloader.fetch(
      Download(step.url, part, expectedSize: size),
      onBytes: progress,
    );
    try {
      final file = File(part);
      if (file.lengthSync() > config.maxBundleSize) {
        throw PluxException(
          PluxErrorCode.bundleMalformed,
          'the bundle exceeds ${config.maxBundleSize} bytes',
        );
      }
      _verify(file.readAsBytesSync(), hash);
    } on PluxException {
      final f = File(part);
      if (f.existsSync()) f.deleteSync();
      rethrow;
    }
    store.commitObject(ObjectKind.bundles, w.hash, part);
  }

  /// The disk quota of the release whose app bundle is [appHash] (SYN-012,
  /// LIM-004): the `device.diskQuota` its signed app bundle carries, or the
  /// registry's default, within the host's cap. The stored bundle is
  /// verified again before its limits are read; one that fails is deleted,
  /// so the next sync downloads it again.
  int _quota(String appHash) {
    var quota = PluxLimit.deviceDiskQuota.defaultValue;
    final path = store.objectPath(ObjectKind.bundles, appHash);
    final file = MappedFile.open(path);
    try {
      final bundle = _verify(file.bytes, hexDecode(appHash));
      final meta = fbs.Meta(bundle.ofKind(SectionKind.meta).single.data);
      for (final l in meta.limits ?? const <fbs.Limit>[]) {
        if (l.key == PluxLimit.deviceDiskQuota.key) quota = l.value;
      }
    } on PluxException {
      file.release();
      File(path).deleteSync();
      rethrow;
    }
    file.release();
    return math.min(quota, config.diskQuota);
  }

  BundleContainer _verify(
    Uint8List data,
    Uint8List hash, {
    bool fromDelta = false,
  }) => verifyBundle(
    data,
    hash,
    limits: config.verifierLimits,
    supportsFeature: config.supportsFeature,
    fromDelta: fromDelta,
  );
}

final class _Counter {
  int n = 0;
}
