// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The update metadata part of a sync (SEC-050, SEC-051, SEC-056, SEC-041,
/// B11): the device follows the root chain from the root it embeds, then
/// verifies `timestamp`, `snapshot` and the manifest against it, before the
/// manifest is accepted. The timestamp is checked on every sync, also when
/// the manifest has not changed, so that a server that stops answering with
/// fresh metadata is noticed. A failure means the new release is not
/// activated; the release that runs is untouched, and an expired document
/// never stops it.
///
/// The certificate pins a verified root carries replace the Plux host's
/// pins (SEC-041).
///
/// It runs on the sync isolate (L-6); the engine calls it between receiving
/// the manifest and verifying it (SEC-052).
library;

import 'dart:typed_data';

import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/security/pins.dart';
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
  /// Creates the verifier for one app and environment. The trust anchor is
  /// the root document the app embeds ([embeddedRoot], `root.json`), or
  /// else the root made from its [keys] (SEC-051). [production] refuses
  /// development keys (SEC-056). The pins of a verified root for [host]
  /// replace those of [pins].
  MetadataSync({
    required this.api,
    required this.store,
    required this.keys,
    required this.appId,
    required this.environment,
    required this.production,
    this.embeddedRoot,
    this.host = '',
    this.pins,
    DateTime Function()? clock,
  }) : _clock = clock ?? DateTime.now;

  /// The API client.
  final PluxApiClient api;

  /// Where the accepted state is kept.
  final MetadataStore store;

  /// The keys the app embeds (`keys.json`).
  final List<TrustedKey> keys;

  /// The root file the app embeds, or null.
  final Uint8List? embeddedRoot;

  /// The app.
  final String appId;

  /// The environment key.
  final String environment;

  /// Whether this is a production runtime (SEC-056).
  final bool production;

  /// The Plux server's host name, whose pins a root may carry.
  final String host;

  /// The pins in force for the Plux host, replaced from verified roots.
  final PinSet? pins;

  final DateTime Function() _clock;

  /// Applies the pins a previous sync stored to [set], at launch, so that
  /// the first connection already uses them (SEC-041).
  static void applyStoredPins(PinSet set, MetadataState state) {
    if (state.pins.length >= minPins && state.pinsVersion > set.version) {
      set.replace(state.pins, version: state.pinsVersion);
    }
  }

  /// Verifies the metadata of a manifest response, or returns null when the
  /// environment has none (the manifest alone is verified, as before). Throws
  /// a [PluxException] when the chain does not hold; the caller then keeps
  /// the release that runs and activates nothing new.
  Future<MetadataTrust?> verify(DeviceToken token, ManifestResponse res) async {
    var state = store.read();
    final ref = res.metadata;
    if (ref == null) {
      if (state.floor.timestamp > 0) {
        // An environment that had metadata does not lose it (SEC-050).
        throw const PluxException(
          PluxErrorCode.updateMetadataInvalid,
          'the manifest carries no update metadata, which this environment '
          'had before',
          details: {'fault': MetadataFault.rollback},
        );
      }
      return null;
    }
    if (ref.environmentId.isNotEmpty &&
        ref.environmentId != state.environmentId) {
      state = state.copyWith(environmentId: ref.environmentId);
      store.write(state);
    }
    if (state.environmentId.isEmpty) {
      throw const PluxException(
        PluxErrorCode.updateMetadataInvalid,
        'the manifest names no environment for its update metadata',
        details: {'fault': MetadataFault.format},
      );
    }
    final environmentId = state.environmentId;
    return _underRoot(token, state, ref.rootVersion, (trusted, now) async {
      final floor = await _chain(environmentId, trusted, now, (
        timestamp,
        snapshot,
        verifier,
      ) async {
        final targets = await verifier.targets(res.signed!, [
          for (final s in res.signatures) _signature(s),
        ], snapshot);
        return MetadataFloor(
          timestamp: timestamp.version,
          snapshot: snapshot.version,
          targets: targets.version,
        );
      });
      return MetadataTrust._(_targetsKeys(trusted.root), trusted.state, floor);
    });
  }

  /// Checks the newest timestamp when the manifest has not changed: it must
  /// verify under the root, be unexpired and not older than the one seen
  /// before. Nothing is checked for an environment that has no metadata.
  Future<void> checkTimestamp(DeviceToken token) async {
    final state = store.read();
    if (state.floor.timestamp == 0 || state.environmentId.isEmpty) return;
    final version = await _underRoot(token, state, 0, (trusted, now) async {
      final verifier = MetadataVerifier(
        root: trusted.root,
        now: now,
        production: production,
        keyEnvironments: trusted.state.keyEnvironments,
        floor: trusted.state.floor,
      );
      final bytes = await api.metadataFile(
        state.environmentId,
        'timestamp.json',
        maxBytes: metadataMaxBytes,
      );
      return (await verifier.timestamp(bytes)).version;
    });
    final latest = store.read();
    store.write(
      latest.copyWith(
        floor: latest.floor.raisedTo(MetadataFloor(timestamp: version)),
      ),
    );
  }

  /// Records the versions of a chain whose manifest has been accepted, so
  /// that nothing older is accepted later (SEC-050).
  void commit(MetadataTrust trust) {
    store.write(
      trust._state.copyWith(floor: trust._state.floor.raisedTo(trust._floor)),
    );
  }

  /// Runs [action] under the trusted root, first following the root chain
  /// when the manifest names a newer root, when the key environments are
  /// not yet known or the root has expired, and once more when the action
  /// fails the signature threshold, since the keys may have rotated.
  Future<T> _underRoot<T>(
    DeviceToken token,
    MetadataState state,
    int rootHint,
    Future<T> Function(_Trusted trusted, DateTime now) action,
  ) async {
    final now = _clock();
    var current = await _start(state);
    var refreshed = false;
    Future<void> refresh() async {
      current = await _refreshRoots(token, current, now);
      refreshed = true;
    }

    final expires = current.root.expires;
    if (rootHint > current.root.version ||
        current.state.keyEnvironments.isEmpty ||
        (expires != null && !now.isBefore(expires))) {
      await refresh();
    }
    current = _withPins(current);
    try {
      return await action(current, now);
    } on PluxException catch (e) {
      if (refreshed || metadataFault(e) != MetadataFault.threshold) rethrow;
      await refresh();
      current = _withPins(current);
      return action(current, now);
    }
  }

  /// The root to start from: the one stored, unless the app embeds a newer
  /// one, then the embedded one.
  Future<_Trusted> _start(MetadataState state) async {
    final embedded = embeddedRoot == null
        ? rootFromKeys(keys)
        : await loadRoot(embeddedRoot!);
    final stored = state.root == null ? null : await loadRoot(state.root!);
    final root = stored != null && stored.version >= embedded.version
        ? stored
        : embedded;
    return _Trusted(root, state);
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

  /// Puts the pins of the root in use into force when it carries pins for
  /// the Plux host and is newer than the root they came from (SEC-041). A
  /// root without pins leaves the pins as they are; there are never fewer
  /// than [minPins].
  _Trusted _withPins(_Trusted t) {
    final list = t.root.pins[host];
    if (list == null || t.root.version <= t.state.pinsVersion) return t;
    final set = pins;
    if (set != null && t.root.version > set.version) {
      set.replace(list, version: t.root.version);
    }
    final next = t.state.copyWith(pins: list, pinsVersion: t.root.version);
    store.write(next);
    return _Trusted(t.root, next);
  }

  /// Fetches and verifies the timestamp and the snapshot it names, then
  /// calls [last] with them to verify what the snapshot pins.
  Future<MetadataFloor> _chain(
    String environmentId,
    _Trusted trusted,
    DateTime now,
    Future<MetadataFloor> Function(
      TimestampMetadata timestamp,
      SnapshotMetadata snapshot,
      MetadataVerifier verifier,
    )
    last,
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
    return last(timestamp, snapshot, verifier);
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
