// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Helpers shared by the store tests and the kill harness; pure Dart, so
/// the harness can run them in a plain `dart` subprocess.
library;

import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:crypto/crypto.dart';
import 'package:plux_flutter/src/store/release_record.dart';
import 'package:plux_flutter/src/store/release_store.dart';

/// A release of [n] plugin "bundles" whose contents derive from [sequence];
/// objects are named by their SHA-256, as assets are.
ReleaseRecord writeRelease(ReleaseStore store, int sequence, {int n = 3}) {
  final bundles = <RecordBundle>[];
  for (var i = 0; i <= n; i++) {
    // Plugin 0 never changes, so releases share an object.
    final content = Uint8List.fromList(
      utf8.encode(
        i == 0
            ? 'shared bundle'
            : 'release $sequence plugin $i ${'x' * (sequence % 7)}',
      ),
    );
    final hash = sha256.convert(content).toString();
    if (!store.hasObject(ObjectKind.bundles, hash)) {
      store.writeObject(ObjectKind.bundles, hash, content);
    }
    bundles.add(
      RecordBundle(
        key: i == 0 ? '' : 'plugin$i',
        version: sequence,
        hash: hash,
      ),
    );
  }
  final record = ReleaseRecord(
    sequence: sequence,
    source: ReleaseSource.sync,
    bundles: bundles,
  );
  store.stage(record);
  return record;
}

/// Checks that the store's active release is complete: its record reads
/// and every object it names is stored with the content its name hashes.
/// Returns the active sequence.
int? checkComplete(String root) {
  final store = ReleaseStore.open(root);
  final active = store.pointer.active;
  if (active == null) return null;
  final r = store.record(active);
  if (r == null) throw StateError('the active record $active is missing');
  for (final b in r.bundles) {
    final f = File(store.objectPath(ObjectKind.bundles, b.hash));
    if (!f.existsSync()) {
      throw StateError('object ${b.hash} of $active is missing');
    }
    if (_name(f.readAsBytesSync()) != b.hash) {
      throw StateError('object ${b.hash} of $active is corrupt');
    }
  }
  return active;
}

/// What an object is named by: a container's bundle hash (its header
/// hash field), else the SHA-256 of the content.
String _name(Uint8List data) {
  if (data.length >= 48 && String.fromCharCodes(data, 0, 4) == 'PLUX') {
    return data
        .sublist(16, 48)
        .map((b) => b.toRadixString(16).padLeft(2, '0'))
        .join();
  }
  return sha256.convert(data).toString();
}
