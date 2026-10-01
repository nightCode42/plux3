// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Imports the baseline release a host app embeds with `plux pull`
/// (SYN-007, CLI-004): every bundle is checked like a download — structure,
/// bundle hash, section hashes, the FlatBuffers verifier — and its publish
/// signature must verify under an embedded key (ADR-0029). It runs on the
/// sync isolate.
library;

import 'dart:convert';
import 'dart:typed_data';

import 'package:crypto/crypto.dart';

import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/store/release_record.dart';
import 'package:plux_flutter/src/store/release_store.dart';
import 'package:plux_flutter/src/verify/bundle_verifier.dart';
import 'package:plux_flutter/src/verify/manifest.dart';

/// Reads the files `plux pull` wrote, by path relative to its output
/// directory (`baseline.json`, `bundles/<key>.pxb`, `assets/<sha256>`);
/// null when absent.
typedef BaselineReader = Future<Uint8List?> Function(String path);

/// Imports the baseline when the store has no release, or only an older
/// one, and returns its sequence; null when there is no baseline or the
/// store already has one at least as new. Throws [PluxException] when the
/// baseline fails verification: an app never renders an unverified one.
Future<int?> importBaseline({
  required BaselineReader read,
  required ReleaseStore store,
  required List<TrustedKey> keys,
  required String appId,
  required String environment,
  required String channel,
  required VerifierLimits limits,
  required bool Function(String feature) supportsFeature,
}) async {
  final json = await read('baseline.json');
  if (json == null) return null;
  final Map<String, Object?> b;
  final int sequence;
  final List<Map<String, Object?>> entries;
  try {
    b = jsonDecode(utf8.decode(json)) as Map<String, Object?>;
    sequence = b['releaseSequence']! as int;
    entries = (b['bundles']! as List<Object?>).cast<Map<String, Object?>>();
  } on Object {
    throw const PluxException(
      PluxErrorCode.bundleMalformed,
      'baseline.json cannot be read',
    );
  }
  if (b['app'] != appId ||
      b['environment'] != environment ||
      b['channel'] != channel) {
    throw PluxException(
      PluxErrorCode.manifestSignatureInvalid,
      'the baseline is for ${b['app']}/${b['environment']}/${b['channel']}',
    );
  }
  final active = store.pointer.active;
  if (active != null && active >= sequence) return null;
  final bundles = <RecordBundle>[];
  for (final e in entries) {
    final hex = e['sha256'] as String? ?? '';
    final Uint8List hash;
    try {
      hash = hexDecode(hex);
    } on FormatException {
      throw PluxException(
        PluxErrorCode.bundleMalformed,
        'baseline bundle hash $hex',
      );
    }
    final data = await read(e['file']! as String);
    if (data == null || hash.length != 32) {
      throw PluxException(
        PluxErrorCode.bundleMalformed,
        'baseline bundle ${e['file']} is missing',
      );
    }
    verifyBundle(data, hash, limits: limits, supportsFeature: supportsFeature);
    final signature = DocumentSignature(
      keyId: e['keyId'] as String? ?? '',
      algorithm: e['algorithm'] as String? ?? '',
      signature: base64.decode(e['signature'] as String? ?? ''),
    );
    if (!await verifyBundleSignature(hash, signature, keys)) {
      throw PluxException(
        PluxErrorCode.manifestSignatureInvalid,
        'baseline bundle ${e['file']} is not signed by an embedded key',
      );
    }
    if (!store.hasObject(ObjectKind.bundles, hex)) {
      store.writeObject(ObjectKind.bundles, hex, data);
    }
    bundles.add(
      RecordBundle(
        key: e['plugin'] as String? ?? '',
        version: e['version'] as int? ?? 0,
        hash: hex,
      ),
    );
  }
  if (bundles.where((x) => x.isApp).length != 1) {
    throw const PluxException(
      PluxErrorCode.bundleMalformed,
      'the baseline has no app bundle',
    );
  }
  final assets = await _importAssets(b['assets'], read, store);
  store.installBaseline(
    ReleaseRecord(
      sequence: sequence,
      source: ReleaseSource.baseline,
      bundles: bundles,
      assets: assets,
    ),
  );
  store.collectGarbage();
  return sequence;
}

/// Stores the baseline's asset files, each checked against the SHA-256
/// that names it (AST-001), and returns their hashes.
Future<List<String>> _importAssets(
  Object? list,
  BaselineReader read,
  ReleaseStore store,
) async {
  final out = <String>[];
  for (final e
      in (list as List<Object?>? ?? const [])
          .whereType<Map<String, Object?>>()) {
    final hash = e['sha256'] as String? ?? '';
    final file = e['file'] as String? ?? '';
    if (!RegExp(r'^[0-9a-f]{64}$').hasMatch(hash)) {
      throw PluxException(
        PluxErrorCode.bundleMalformed,
        'baseline asset hash $hash',
      );
    }
    if (!store.hasObject(ObjectKind.assets, hash)) {
      final data = await read(file);
      if (data == null || sha256.convert(data).toString() != hash) {
        throw PluxException(
          PluxErrorCode.assetHashMismatch,
          'baseline asset $file does not match its hash',
        );
      }
      store.writeObject(ObjectKind.assets, hash, data);
    }
    out.add(hash);
  }
  return out..sort();
}
