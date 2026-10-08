// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The update metadata a fake server serves (SEC-050, SEC-051): a root of
/// three offline keys with a threshold of two, rotated on request, and the
/// snapshot and timestamp the worker would sign over the server's targets
/// key. All Ed25519; the Go-signed vectors cover ES256.
library;

import 'dart:convert';
import 'dart:typed_data';

import 'package:crypto/crypto.dart';
import 'package:cryptography/cryptography.dart' show SimpleKeyPair;
import 'package:cryptography/dart.dart';
import 'package:plux_flutter/src/verify/jcs.dart';
import 'package:plux_flutter/src/verify/manifest.dart';
import 'package:plux_flutter/src/verify/update_metadata.dart';

final _ed = DartEd25519(sha512: const DartSha512());

/// A test key pair with its public key and identifier.
final class MetaKey {
  MetaKey._(this.pair, this.publicKey) : id = metadataKeyId(publicKey);

  /// Makes the key of [seed].
  static Future<MetaKey> of(int seed) async {
    final pair = await _ed.newKeyPairFromSeed(List.filled(32, seed));
    return MetaKey._(
      pair,
      Uint8List.fromList((await pair.extractPublicKey()).bytes),
    );
  }

  /// The pair.
  final SimpleKeyPair pair;

  /// The raw public key.
  final Uint8List publicKey;

  /// The key identifier.
  final String id;

  /// The key as a root lists it.
  Map<String, Object?> get listed => {
    'alg': 'Ed25519',
    'public': base64.encode(publicKey),
  };
}

String _time(DateTime t) =>
    t.toUtc().toIso8601String().replaceFirst('.000', '');

/// The signed and served update metadata of one environment.
final class FakeMetadata {
  FakeMetadata._(this.targets, this._offline, this._online);

  /// Starts at root 1, timestamp 9, snapshot 3 and targets 5, all valid
  /// until [expires], over the server's targets key (the one its manifest
  /// signature verifies under).
  static Future<FakeMetadata> create(MetaKey targets) async {
    final offline = [
      await MetaKey.of(21),
      await MetaKey.of(22),
      await MetaKey.of(23),
    ];
    final m = FakeMetadata._(targets, offline, (
      snapshot: await MetaKey.of(31),
      timestamp: await MetaKey.of(32),
    ));
    await m.publish();
    return m;
  }

  /// The server's targets key.
  final MetaKey targets;

  List<MetaKey> _offline;
  ({MetaKey snapshot, MetaKey timestamp}) _online;

  /// The identifier that names the environment's files.
  String environmentId = 'env_0001';

  /// The environment type `GetRootKeys` reports.
  String environmentType = 'production';

  /// Versions of the current files.
  int rootVersion = 1, timestampVersion = 9, snapshotVersion = 3;

  /// The version the snapshot pins; the manifest carries it.
  int targetsVersion = 5;

  /// The targets version the snapshot pins when it is not [targetsVersion]:
  /// a snapshot that does not belong to the manifest.
  int? pinnedTargets;

  /// The root version the manifest's reference claims, when it is not
  /// [rootVersion].
  int? hintedRoot;

  /// Expiries of the current files.
  DateTime rootExpires = DateTime.utc(2027),
      snapshotExpires = DateTime.utc(2026, 11),
      timestampExpires = DateTime.utc(2026, 10, 9);

  /// Roots by version - 1.
  final List<Uint8List> roots = [];

  /// Served files by name, such as `timestamp.json`.
  final Map<String, Uint8List> files = {};

  late List<MetaKey> _firstOffline;
  late ({MetaKey snapshot, MetaKey timestamp}) _firstOnline;

  /// The keys of root 1, as the app embeds them (`keys.json`).
  List<TrustedKey> get embedded => [
    for (final (role, k) in [
      for (final k in _firstOffline) ('root', k),
      ('targets', targets),
      ('snapshot', _firstOnline.snapshot),
      ('timestamp', _firstOnline.timestamp),
    ])
      TrustedKey(
        keyId: k.id,
        algorithm: 'ed25519',
        role: role,
        publicKey: k.publicKey,
      ),
  ];

  Future<Uint8List> _document(
    Map<String, Object?> signed,
    List<MetaKey> signers,
  ) async {
    final message = utf8.encode(canonicalJson(signed));
    final sigs = [
      for (final k in signers)
        {
          'keyid': k.id,
          'alg': 'Ed25519',
          'sig': base64.encode(
            (await _ed.sign(message, keyPair: k.pair)).bytes,
          ),
        },
    ]..sort((a, b) => a['keyid']!.compareTo(b['keyid']!));
    return Uint8List.fromList(
      utf8.encode(canonicalJson({'signed': signed, 'signatures': sigs})),
    );
  }

  Map<String, Object?> _rootSigned(int version) {
    final keys = {
      for (final k in [
        ..._offline,
        targets,
        _online.snapshot,
        _online.timestamp,
      ])
        k.id: k.listed,
    };
    Map<String, Object?> role(List<MetaKey> ks, int threshold) => {
      'keyids': [for (final k in ks) k.id],
      'threshold': threshold,
    };
    return {
      '_type': 'root',
      'version': version,
      'expires': _time(rootExpires),
      'specVersion': '1.0.0',
      'keys': keys,
      'roles': {
        'root': role(_offline, 2),
        'targets': role([targets], 1),
        'snapshot': role([_online.snapshot], 1),
        'timestamp': role([_online.timestamp], 1),
      },
    };
  }

  /// Signs the snapshot and timestamp for the current versions.
  Future<void> publish() async {
    if (roots.isEmpty) {
      _firstOffline = _offline;
      _firstOnline = _online;
      roots.add(await _document(_rootSigned(1), _offline.take(2).toList()));
      files['1.root.json'] = roots.last;
    }
    final snapshot = await _document(
      {
        '_type': 'snapshot',
        'version': snapshotVersion,
        'expires': _time(snapshotExpires),
        'specVersion': '1.0.0',
        'meta': {
          'targets.json': {'version': pinnedTargets ?? targetsVersion},
        },
      },
      [_online.snapshot],
    );
    files['$snapshotVersion.snapshot.json'] = snapshot;
    files['timestamp.json'] = await _document(
      {
        '_type': 'timestamp',
        'version': timestampVersion,
        'expires': _time(timestampExpires),
        'specVersion': '1.0.0',
        'meta': {
          'snapshot.json': {
            'version': snapshotVersion,
            'sha256': sha256.convert(snapshot).toString(),
          },
        },
      },
      [_online.timestamp],
    );
  }

  /// Publishes the next root: one root key and both online keys replaced,
  /// signed by two keys of the root it replaces and two of its own.
  ///
  /// With [forged], the new root is signed by its own keys alone, as an
  /// attacker without the old keys would.
  Future<void> rotate({bool forged = false}) async {
    final previous = _offline;
    _offline = [previous[0], await MetaKey.of(24), await MetaKey.of(25)];
    _online = (snapshot: await MetaKey.of(33), timestamp: await MetaKey.of(34));
    rootVersion++;
    final signed = _rootSigned(rootVersion);
    final root = await _document(
      signed,
      forged
          ? [_offline[1], _offline[2]]
          : [previous[0], previous[1], _offline[1]],
    );
    roots.add(root);
    files['$rootVersion.root.json'] = root;
    await publish();
  }

  /// The offline keys of the current root.
  List<MetaKey> get offline => _offline;

  /// The reference a manifest response carries.
  Map<String, Object?> get reference => {
    'rootVersion': '${hintedRoot ?? rootVersion}',
    'snapshotVersion': '$snapshotVersion',
    'timestampVersion': '$timestampVersion',
  };

  /// The answer of `GetRootKeys` for a device holding root [since].
  Map<String, Object?> rootKeys(int since) => {
    'keys': [
      for (final (role, ks) in [
        ('root', _offline),
        ('targets', [targets]),
        ('snapshot', [_online.snapshot]),
        ('timestamp', [_online.timestamp]),
      ])
        for (final k in ks)
          {
            'keyId': k.id,
            'algorithm': 'Ed25519',
            'publicKey': base64.encode(k.publicKey),
            'role': role,
            'environmentType': environmentType,
          },
    ],
    'roots': [for (final r in roots.skip(since)) base64.encode(r)],
  };
}
