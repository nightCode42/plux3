// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// What the device has accepted of the update metadata (SEC-050,
/// SEC-051): the newest root it verified, the versions it will not go
/// below, and the environment identifier and key environments it learned.
/// It lives beside the release store's pointer, in the app's private
/// storage, and is only ever replaced by an atomic rename.
library;

import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:plux_flutter/src/verify/update_metadata.dart';

/// The accepted update metadata of one app, environment and channel.
final class MetadataState {
  /// Creates the state.
  const MetadataState({
    this.environmentId = '',
    this.root,
    this.keyEnvironments = const {},
    this.floor = const MetadataFloor(),
  });

  /// The environment identifier that names the metadata files; empty when
  /// not yet learned.
  final String environmentId;

  /// The newest root file the device verified, or null while it relies on
  /// the keys the app embeds.
  final Uint8List? root;

  /// Each key's environment type, as the server last reported it.
  final Map<String, String> keyEnvironments;

  /// The versions already trusted.
  final MetadataFloor floor;

  /// A copy with the given fields replaced.
  MetadataState copyWith({
    String? environmentId,
    Uint8List? root,
    Map<String, String>? keyEnvironments,
    MetadataFloor? floor,
  }) => MetadataState(
    environmentId: environmentId ?? this.environmentId,
    root: root ?? this.root,
    keyEnvironments: keyEnvironments ?? this.keyEnvironments,
    floor: floor ?? this.floor,
  );

  /// The state as it is stored.
  Uint8List encode() => Uint8List.fromList(
    utf8.encode(
      jsonEncode({
        'environmentId': environmentId,
        'root': ?(root == null ? null : base64.encode(root!)),
        'keyEnvironments': keyEnvironments,
        'floor': {
          'timestamp': floor.timestamp,
          'snapshot': floor.snapshot,
          'targets': floor.targets,
        },
      }),
    ),
  );

  /// Reads a stored state; unreadable bytes give the empty state.
  static MetadataState decode(List<int> bytes) {
    try {
      final j = jsonDecode(utf8.decode(bytes)) as Map<String, Object?>;
      final f = j['floor']! as Map<String, Object?>;
      final root = j['root'] as String?;
      return MetadataState(
        environmentId: j['environmentId']! as String,
        root: root == null ? null : base64.decode(root),
        keyEnvironments: (j['keyEnvironments']! as Map<String, Object?>)
            .cast<String, String>(),
        floor: MetadataFloor(
          timestamp: f['timestamp']! as int,
          snapshot: f['snapshot']! as int,
          targets: f['targets']! as int,
        ),
      );
    } on Object {
      return const MetadataState();
    }
  }
}

/// The file the state is kept in, in the release store's directory.
final class MetadataStore {
  /// Keeps the state in [directory] (the release store's root).
  MetadataStore(String directory) : _path = '$directory/metadata';

  final String _path;

  /// The stored state, or the empty one.
  MetadataState read() {
    final f = File(_path);
    return f.existsSync()
        ? MetadataState.decode(f.readAsBytesSync())
        : const MetadataState();
  }

  /// Replaces the state atomically: write `metadata.tmp`, `fsync`, rename.
  void write(MetadataState state) {
    final tmp = File('$_path.tmp');
    final f = tmp.openSync(mode: FileMode.write);
    try {
      f
        ..writeFromSync(state.encode())
        ..flushSync();
    } finally {
      f.closeSync();
    }
    tmp.renameSync(_path);
  }
}
