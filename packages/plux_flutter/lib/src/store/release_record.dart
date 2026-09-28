// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// One release as the store keeps it (ADR-0021): the signed manifest it
/// was accepted with — or, for a baseline, the per-bundle signatures —
/// and the bundles and assets it needs. A record is written once and
/// never changed.
library;

import 'dart:convert';
import 'dart:typed_data';

/// How a release reached the device.
enum ReleaseSource {
  /// Embedded in the host app by `plux pull` (SYN-007).
  baseline,

  /// Downloaded by a sync (SYN-001).
  sync,
}

/// A bundle of a release: the plugin key (empty for the app bundle), its
/// version and its bundle hash.
final class RecordBundle {
  /// Creates an entry.
  const RecordBundle({
    required this.key,
    required this.version,
    required this.hash,
  });

  /// The plugin key; empty for the app bundle.
  final String key;

  /// The plugin version.
  final int version;

  /// The bundle hash, lower-case hexadecimal.
  final String hash;

  /// Whether this is the app bundle.
  bool get isApp => key.isEmpty;

  Map<String, Object?> _toJson() => {
    'key': key,
    'version': version,
    'hash': hash,
  };

  static RecordBundle _fromJson(Map<String, Object?> j) => RecordBundle(
    key: j['key']! as String,
    version: j['version']! as int,
    hash: j['hash']! as String,
  );
}

/// A release record.
final class ReleaseRecord {
  /// Creates a record.
  const ReleaseRecord({
    required this.sequence,
    required this.source,
    required this.bundles,
    this.assets = const [],
    this.signed,
    this.signatures = const [],
    this.killSwitches = const [],
    this.appKillSwitch = false,
    this.message = '',
  });

  /// The release sequence.
  final int sequence;

  /// Where it came from.
  final ReleaseSource source;

  /// The app bundle and every plugin bundle.
  final List<RecordBundle> bundles;

  /// The SHA-256 of every asset file the release uses, hexadecimal.
  final List<String> assets;

  /// The signed manifest bytes, for a synced release.
  final Uint8List? signed;

  /// The manifest's signatures, as `{keyid, alg, sig}` objects.
  final List<Map<String, Object?>> signatures;

  /// Plugins switched off by the manifest (RT-022).
  final List<String> killSwitches;

  /// Whether all Plux content is switched off.
  final bool appKillSwitch;

  /// The message shown while a switch is on.
  final String message;

  /// The app bundle.
  RecordBundle get app => bundles.firstWhere((b) => b.isApp);

  /// Encodes the record.
  List<int> encode() => utf8.encode(
    jsonEncode({
      'version': 1,
      'sequence': sequence,
      'source': source.name,
      'bundles': [for (final b in bundles) b._toJson()],
      'assets': assets,
      'signed': signed == null ? null : base64.encode(signed!),
      'signatures': signatures,
      'killSwitches': killSwitches,
      'appKillSwitch': appKillSwitch,
      'message': message,
    }),
  );

  /// Decodes a record; throws [FormatException] when it is not one.
  static ReleaseRecord decode(List<int> bytes) {
    try {
      final j = jsonDecode(utf8.decode(bytes)) as Map<String, Object?>;
      if (j['version'] != 1) throw const FormatException('record version');
      final signed = j['signed'] as String?;
      return ReleaseRecord(
        sequence: j['sequence']! as int,
        source: ReleaseSource.values.byName(j['source']! as String),
        bundles: [
          for (final b
              in (j['bundles']! as List<Object?>).cast<Map<String, Object?>>())
            RecordBundle._fromJson(b),
        ],
        assets: (j['assets']! as List<Object?>).cast<String>().toList(),
        signed: signed == null ? null : base64.decode(signed),
        signatures: (j['signatures']! as List<Object?>)
            .cast<Map<String, Object?>>()
            .toList(),
        killSwitches: (j['killSwitches']! as List<Object?>)
            .cast<String>()
            .toList(),
        appKillSwitch: j['appKillSwitch']! as bool,
        message: j['message']! as String,
      );
    } on FormatException {
      rethrow;
    } on Object catch (e) {
      throw FormatException('not a release record: $e');
    }
  }
}

/// The latest verified control switches of the channel (REL-030): they can
/// change without a new release sequence, so they are kept apart from the
/// immutable records, in a file replaced atomically.
final class ControlState {
  /// Creates the state.
  const ControlState({
    required this.sequence,
    this.killSwitches = const [],
    this.appKillSwitch = false,
    this.mandatory = false,
    this.message = '',
  });

  /// The release sequence of the manifest they came with.
  final int sequence;

  /// Plugins that must not render (RT-022).
  final List<String> killSwitches;

  /// Whether all Plux content is switched off.
  final bool appKillSwitch;

  /// Whether the release is a mandatory update (REL-070).
  final bool mandatory;

  /// The message shown while a switch is on.
  final String message;

  /// Encodes the state.
  List<int> encode() => utf8.encode(
    jsonEncode({
      'sequence': sequence,
      'killSwitches': killSwitches,
      'appKillSwitch': appKillSwitch,
      'mandatory': mandatory,
      'message': message,
    }),
  );

  /// Decodes a state; null when it cannot be read.
  static ControlState? decode(List<int> bytes) {
    try {
      final j = jsonDecode(utf8.decode(bytes)) as Map<String, Object?>;
      return ControlState(
        sequence: j['sequence']! as int,
        killSwitches: (j['killSwitches']! as List<Object?>)
            .cast<String>()
            .toList(),
        appKillSwitch: j['appKillSwitch']! as bool,
        mandatory: j['mandatory']! as bool,
        message: j['message']! as String,
      );
    } on Object {
      return null;
    }
  }
}
