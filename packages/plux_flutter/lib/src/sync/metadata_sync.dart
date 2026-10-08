// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The update metadata part of a sync (SEC-050, SEC-051, SEC-056, B11): the
/// device follows the root chain from the root it embeds, then verifies
/// `timestamp`, `snapshot` and the manifest against it, before the manifest
/// is accepted. A failure means the new release is not activated; the
/// release that runs is untouched, and an expired document never stops it.
///
/// It runs on the sync isolate (L-6); the engine calls it between receiving
/// the manifest and verifying it (SEC-052).
library;

import 'dart:typed_data';

import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/store/metadata_state.dart';
import 'package:plux_flutter/src/sync/api_client.dart';
import 'package:plux_flutter/src/verify/manifest.dart';
import 'package:plux_flutter/src/verify/update_metadata.dart';

/// What a verified chain yields.
final class MetadataTrust {
  const MetadataTrust._(this.targetsKeys, this._state, this._floor);

  /// The keys the verified root lists for the targets role, in the form the
  /// manifest verifier takes: after a rotation they replace the embedded
  /// ones.
  final List<TrustedKey> targetsKeys;

  final MetadataState _state;
  final MetadataFloor _floor;
}

/// Verifies the update metadata of a sync against the state kept on the
/// device.
final class MetadataSync {
  /// Creates the verifier for one app and environment. [anchor] is the root
  /// the app embeds; [production] refuses development keys (SEC-056).
  MetadataSync({
    required this.api,
    required this.store,
    required this.anchor,
    required this.appId,
    required this.environment,
    required this.production,
    DateTime Function()? clock,
  }) : _clock = clock ?? DateTime.now;

  /// The API client.
  final PluxApiClient api;

  /// Where the accepted state is kept.
  final MetadataStore store;

  /// The root made from the keys the app embeds (SEC-051).
  final RootMetadata anchor;

  /// The app.
  final String appId;

  /// The environment key.
  final String environment;

  /// Whether this is a production runtime (SEC-056).
  final bool production;

  final DateTime Function() _clock;

  /// Remembers the environment identifier the server named at
  /// registration, which names the metadata files.
  void learnEnvironment() {
    final id = api.registeredEnvironmentId;
    final state = store.read();
    if (id != null && id != state.environmentId) {
      store.write(state.copyWith(environmentId: id));
    }
  }

  /// Verifies the metadata of a manifest response, or returns null when the
  /// environment has none (the manifest alone is verified, as before). Throws
  /// a [PluxException] when the chain does not hold; the caller then keeps
  /// the release that runs and activates nothing new.
  Future<MetadataTrust?> verify(DeviceToken token, ManifestResponse res) async {
    final state = store.read();
    final ref = res.metadata;
    if (ref == null) {
      if (state.floor.timestamp > 0) {
        // An environment that had metadata does not lose it (SEC-050).
        throw PluxException(
          PluxErrorCode.updateMetadataInvalid,
          'the manifest carries no update metadata, which this environment '
          'had before',
          details: const {'fault': MetadataFault.rollback},
        );
      }
      return null;
    }
    final environmentId = state.environmentId;
    if (environmentId.isEmpty) {
      throw const PluxException(
        PluxErrorCode.updateMetadataInvalid,
        'the device does not know its environment identifier; it learns it '
        'when it registers',
        details: {'fault': MetadataFault.format},
      );
    }
    final now = _clock();
    var current = _Trusted(
      state.root == null ? anchor : await loadRoot(state.root!),
      state,
    );
    var refreshed = false;
    Future<void> refresh() async {
      current = await _refreshRoots(token, current, now);
      refreshed = true;
    }

    final expires = current.root.expires;
    if (ref.rootVersion > current.root.version ||
        current.state.keyEnvironments.isEmpty ||
        (expires != null && !now.isBefore(expires))) {
      await refresh();
    }
    try {
      return await _chain(environmentId, res, current, now);
    } on PluxException catch (e) {
      // Keys may have rotated since the root held: look once more.
      if (refreshed || metadataFault(e) != MetadataFault.threshold) rethrow;
      await refresh();
      return _chain(environmentId, res, current, now);
    }
  }

  /// Records the versions of a chain whose manifest has been accepted, so
  /// that nothing older is accepted later (SEC-050).
  void commit(MetadataTrust trust) {
    store.write(
      trust._state.copyWith(floor: trust._state.floor.raisedTo(trust._floor)),
    );
  }

  /// Follows the root chain since the trusted root (SEC-051) and learns the
  /// keys' environments (SEC-056); keeps what it accepted.
  Future<_Trusted> _refreshRoots(
    DeviceToken token,
    _Trusted from,
    DateTime now,
  ) async {
    final answer = await api.rootKeys(
      token: token,
      appId: appId,
      environment: environment,
      sinceRootVersion: from.root.version,
    );
    if (production &&
        answer.keys.any((k) => k.environmentType != environmentProduction)) {
      throw const PluxException(
        PluxErrorCode.developmentKeyInProduction,
        'the server lists a key that is not a production key',
        details: {'fault': MetadataFault.developmentKey},
      );
    }
    final root = await rootChain(from.root, answer.roots, now);
    final next = from.state.copyWith(
      root: answer.roots.isEmpty ? null : answer.roots.last,
      keyEnvironments: {
        for (final k in answer.keys) k.keyId: k.environmentType,
      },
    );
    store.write(next);
    return _Trusted(root, next);
  }

  Future<MetadataTrust> _chain(
    String environmentId,
    ManifestResponse res,
    _Trusted trusted,
    DateTime now,
  ) async {
    checkRootUnexpired(trusted.root, now);
    final verifier = MetadataVerifier(
      root: trusted.root,
      now: now,
      production: production,
      keyEnvironments: trusted.state.keyEnvironments,
      floor: trusted.state.floor,
    );
    Future<Uint8List> fetch(String file) =>
        api.metadataFile(environmentId, file, maxBytes: metadataMaxBytes);
    final timestamp = await verifier.timestamp(await fetch('timestamp.json'));
    final snapshot = await verifier.snapshot(
      await fetch('${timestamp.snapshotVersion}.snapshot.json'),
      timestamp,
    );
    final targets = await verifier.targets(res.signed!, [
      for (final s in res.signatures) _signature(s),
    ], snapshot);
    return MetadataTrust._(
      _targetsKeys(trusted.root),
      trusted.state,
      MetadataFloor(
        timestamp: timestamp.version,
        snapshot: snapshot.version,
        targets: targets.version,
      ),
    );
  }

  DocumentSignature _signature(Map<String, Object?> json) {
    try {
      return DocumentSignature.fromJson(json);
    } on Object {
      throw const PluxException(
        PluxErrorCode.updateMetadataInvalid,
        'a manifest signature is malformed',
        details: {'fault': MetadataFault.format},
      );
    }
  }

  List<TrustedKey> _targetsKeys(RootMetadata root) => [
    for (final id in root.role('targets').keyIds)
      if (canonicalAlgorithm(root.keys[id]!.algorithm) == algEd25519)
        TrustedKey(
          keyId: id,
          algorithm: ed25519Algorithm,
          role: 'targets',
          publicKey: root.keys[id]!.publicKey,
        ),
  ];
}

final class _Trusted {
  const _Trusted(this.root, this.state);
  final RootMetadata root;
  final MetadataState state;
}
