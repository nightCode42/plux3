// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The sync isolate (SYN-010, L-6): network I/O, patching, hashing and
/// every write to the release store happen here, never on the UI isolate.
/// The UI isolate sends commands and receives [SyncEvent]s and results.
library;

import 'dart:async';
import 'dart:io' show FileSystemException;
import 'dart:isolate';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:http/http.dart' as http;
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/security/attestation.dart';
import 'package:plux_flutter/src/security/device_keys.dart';
import 'package:plux_flutter/src/security/security_config.dart';
import 'package:plux_flutter/src/store/baseline.dart';
import 'package:plux_flutter/src/store/directory_sync.dart';
import 'package:plux_flutter/src/store/kv_store.dart' show SecretStore;
import 'package:plux_flutter/src/store/pointer.dart';
import 'package:plux_flutter/src/store/release_store.dart';
import 'package:plux_flutter/src/sync/api_client.dart';
import 'package:plux_flutter/src/sync/downloader.dart';
import 'package:plux_flutter/src/sync/sync_engine.dart';
import 'package:plux_flutter/src/sync/sync_event.dart';
import 'package:plux_flutter/src/telemetry/events.dart';
import 'package:plux_flutter/src/telemetry/outbox.dart';

/// Everything the sync isolate needs, sent to it once.
final class SyncWorkerConfig {
  /// Creates the configuration.
  const SyncWorkerConfig({
    required this.storeRoot,
    required this.endpoint,
    required this.sync,
    required this.httpClient,
    required this.credentials,
    required this.deviceKeys,
    required this.attestation,
    this.configSecrets,
    this.parallelism = 4,
    this.baseline,
    this.rootIsolateToken,
  });

  /// The release store's directory.
  final String storeRoot;

  /// The server.
  final Uri endpoint;

  /// The sync's configuration.
  final SyncConfig sync;

  /// Creates the HTTP client, inside the isolate (Cronet, `URLSession` or
  /// `dart:io`).
  final http.Client Function() httpClient;

  /// Creates the credential store, inside the isolate.
  final CredentialStore Function() credentials;

  /// Creates the device keys, inside the isolate (SEC-001).
  final DeviceKeys Function() deviceKeys;

  /// Creates the platform attestation, inside the isolate (SEC-002).
  final Attestation Function() attestation;

  /// Creates the secret store that keeps the remote security
  /// configuration, inside the isolate (SEC-182); without it the device
  /// runs on the built-in defaults.
  final SecretStore Function()? configSecrets;

  /// Downloads at once (SYN-010).
  final int parallelism;

  /// Reads a baseline on the sync isolate; a test's stand-in for the
  /// host's assets, which [SyncWorker.start] serves instead (SYN-007).
  final BaselineReader? baseline;

  /// Lets the isolate use platform channels (key storage).
  final RootIsolateToken? rootIsolateToken;
}

/// A command to the sync isolate.
sealed class _Command {
  const _Command(this.reply);
  final SendPort reply;
}

final class _Sync extends _Command {
  const _Sync(super.reply);
}

final class _Store extends _Command {
  const _Store(super.reply, this.op);
  final String op;
}

/// Imports the baseline; [assets] serves the host's asset files when set.
final class _Baseline extends _Command {
  const _Baseline(super.reply, this.assets);
  final SendPort? assets;
}

final class _Settle extends _Command {
  const _Settle(super.reply);
}

/// Reads the security settings in force.
final class _Settings extends _Command {
  const _Settings(super.reply);
}

final class _Flush extends _Command {
  const _Flush(super.reply, this.perRequest);
  final int perRequest;
}

/// Events to buffer; no reply (ADR-0034).
final class _Record {
  const _Record(this.lines, this.maxBytes);
  final List<String> lines;
  final int maxBytes;
}

/// Categories whose buffered events are deleted; no reply.
final class _Purge {
  const _Purge(this.categories);
  final Set<TelemetryCategory> categories;
}

/// The UI isolate's handle on the sync isolate.
final class SyncWorker {
  SyncWorker._(this._isolate, this._commands, this._baselineAssets);

  /// Starts the sync isolate. [baselineAssets] is the directory of the
  /// host's assets holding the baseline `plux pull` wrote, if any.
  static Future<SyncWorker> start(
    SyncWorkerConfig config, {
    String? baselineAssets,
  }) async {
    final ready = ReceivePort();
    final isolate = await Isolate.spawn(_main, (
      config,
      ready.sendPort,
    ), debugName: 'plux-sync');
    final commands = await ready.first as SendPort;
    ready.close();
    return SyncWorker._(isolate, commands, baselineAssets);
  }

  final Isolate _isolate;
  final SendPort _commands;
  final String? _baselineAssets;
  Future<SyncResult>? _running;

  /// Syncs once, reporting events to [onEvent]; a sync already running is
  /// joined rather than started twice (ADR-0021).
  Future<SyncResult> sync(void Function(SyncEvent) onEvent) {
    final running = _running;
    if (running != null) return running;
    final port = ReceivePort();
    final done = Completer<SyncResult>();
    port.listen((m) {
      if (m is SyncEvent) onEvent(m);
      if (m is SyncResult) {
        done.complete(m);
        port.close();
      }
    });
    _commands.send(_Sync(port.sendPort));
    return _running = done.future.whenComplete(() => _running = null);
  }

  /// Imports the embedded baseline; the sequence, or null.
  ///
  /// The host's assets are readable only on this isolate: `rootBundle`
  /// needs the services binding, which a background isolate does not
  /// have. So this isolate serves each file the sync isolate asks for,
  /// moving its bytes across rather than copying them into a message; the
  /// sync isolate verifies and stores them (L-6).
  Future<int?> importBaseline() async {
    final dir = _baselineAssets;
    if (dir == null) {
      return await _ask((p) => _Baseline(p, null)) as int?;
    }
    final assets = ReceivePort();
    assets.listen((m) async {
      final (path, reply) = m as (String, SendPort);
      reply.send(await _loadAsset('$dir/$path'));
    });
    try {
      return await _ask((p) => _Baseline(p, assets.sendPort)) as int?;
    } finally {
      assets.close();
    }
  }

  /// The security settings in force: the stored configuration once it has
  /// verified against its hash again, else the built-in defaults; null
  /// when the worker keeps no configuration (SEC-182).
  Future<SecuritySettings?> loadSettings() async =>
      await _ask(_Settings.new) as SecuritySettings?;

  /// Activates the staged release (SYN-004).
  Future<StorePointer> activate() => _store('activate');

  /// Starts a launch (SYN-006); the pointer afterwards.
  Future<StorePointer> beginLaunch() => _store('beginLaunch');

  /// Records that the launch reached a healthy point.
  Future<StorePointer> markHealthy() => _store('markHealthy');

  /// Records a failure attributed to Plux (SYN-006).
  Future<StorePointer> recordFailure() => _store('recordFailure');

  /// Reverts to the last known good release.
  Future<StorePointer> revert() => _store('revert');

  /// Collects garbage.
  Future<StorePointer> collectGarbage() => _store('collectGarbage');

  Future<StorePointer> _store(String op) async =>
      await _ask((p) => _Store(p, op)) as StorePointer;

  Future<Object?> _ask(_Command Function(SendPort) command) async {
    final port = ReceivePort();
    _commands.send(command(port.sendPort));
    final reply = await port.first;
    port.close();
    if (reply is PluxException) throw reply;
    if (reply is _Failure) throw StateError(reply.message);
    return reply;
  }

  /// Buffers telemetry events, as [TelemetryLine]s, within [maxBytes]
  /// (ADR-0034). Messages to the isolate keep their order, so events
  /// recorded before a [flushTelemetry] are in it.
  void recordTelemetry(List<String> lines, int maxBytes) =>
      _commands.send(_Record(lines, maxBytes));

  /// Deletes the buffered events of [categories] (SEC-161).
  void purgeTelemetry(Set<TelemetryCategory> categories) =>
      _commands.send(_Purge(categories));

  /// Sends the buffered events in batches of at most [perRequest].
  Future<TelemetryFlush> flushTelemetry(int perRequest) async =>
      await _ask((p) => _Flush(p, perRequest)) as TelemetryFlush;

  /// Completes once the isolate has handled every message sent before.
  Future<void> settle() => _ask(_Settle.new);

  /// Stops the isolate.
  void close() => _isolate.kill(priority: Isolate.immediate);
}

final class _Failure {
  const _Failure(this.message);
  final String message;
}

Future<void> _main((SyncWorkerConfig, SendPort) args) async {
  final (config, ready) = args;
  final token = config.rootIsolateToken;
  if (token != null) BackgroundIsolateBinaryMessenger.ensureInitialized(token);
  final store = ReleaseStore.open(
    config.storeRoot,
    syncDirectory: syncDirectory,
  );
  final client = config.httpClient();
  final api = PluxApiClient(client, config.endpoint);
  final engine = SyncEngine(
    config: config.sync,
    store: store,
    api: api,
    downloader: Downloader(client, parallelism: config.parallelism),
    credentials: config.credentials(),
    keys: config.deviceKeys(),
    attestation: config.attestation(),
    configSecrets: config.configSecrets?.call(),
  );
  final outbox = TelemetryOutbox(config.storeRoot);
  final commands = ReceivePort();
  ready.send(commands.sendPort);
  await for (final c in commands) {
    switch (c) {
      case _Settle(:final reply):
        reply.send(null);
      case _Settings(:final reply):
        try {
          reply.send(await engine.loadSettings());
        } on Object catch (e) {
          reply.send(_Failure('$e'));
        }
      case _Record(:final lines, :final maxBytes):
        try {
          outbox.append(lines, maxBytes);
        } on FileSystemException {
          // A full or unwritable disk loses events, never the sync.
        }
      case _Purge(:final categories):
        try {
          outbox.purge(categories);
        } on FileSystemException {
          // Retried at the next change of consent.
        }
      case _Flush(:final reply, :final perRequest):
        try {
          final r = await outbox.flush(
            api: api,
            token: engine.recentToken,
            appId: config.sync.appId,
            environment: config.sync.environment,
            perRequest: perRequest,
          );
          if (r.error?.code == 'unauthenticated') engine.forgetToken();
          reply.send(r);
        } on Object catch (e) {
          reply.send(_Failure('$e'));
        }
      case _Sync(:final reply):
        reply.send(await engine.run(reply.send));
      case _Baseline(:final reply, :final assets):
        final read = assets == null ? config.baseline : _servedBy(assets);
        try {
          reply.send(
            read == null
                ? null
                : await importBaseline(
                    read: read,
                    store: store,
                    keys: config.sync.keys,
                    appId: config.sync.appId,
                    environment: config.sync.environment,
                    channel: config.sync.channel,
                    limits: config.sync.verifierLimits,
                    supportsFeature: config.sync.supportsFeature,
                  ),
          );
        } on PluxException catch (e) {
          reply.send(e);
        }
      case _Store(:final reply, :final op):
        try {
          switch (op) {
            case 'activate':
              store.activate();
            case 'beginLaunch':
              store.beginLaunch();
            case 'markHealthy':
              store.markHealthy();
            case 'recordFailure':
              store.recordFailure();
            case 'revert':
              store.revert();
            case 'collectGarbage':
              store.collectGarbage();
          }
          reply.send(store.pointer);
        } on PluxException catch (e) {
          reply.send(e);
        } on Object catch (e) {
          reply.send(_Failure('$e'));
        }
    }
  }
}

/// The asset [key] of the host app, ready to send to another isolate;
/// null when the app has no such asset, and a [_Failure] when it cannot
/// be read, so the sync isolate never waits for an answer that will not
/// come.
Future<Object?> _loadAsset(String key) async {
  try {
    final data = await rootBundle.load(key);
    return TransferableTypedData.fromList([data]);
  } on FlutterError {
    return null;
  } on Object catch (e) {
    return _Failure('$e');
  }
}

/// Reads baseline files through [assets], which the UI isolate serves.
BaselineReader _servedBy(SendPort assets) => (path) async {
  final reply = ReceivePort();
  assets.send((path, reply.sendPort));
  final answer = await reply.first;
  reply.close();
  if (answer is _Failure) {
    throw PluxException(
      PluxErrorCode.bundleMalformed,
      'baseline file $path cannot be read: ${answer.message}',
    );
  }
  return (answer as TransferableTypedData?)?.materialize().asUint8List();
};
