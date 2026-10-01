// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The release store's pointer file (ADR-0021): which release is active,
/// staged, last known good and pinned, the highest sequence accepted for
/// the channel (SEC-055), the manifest ETag, and the trial counters of a
/// newly activated release (SYN-006). It is small JSON carrying a SHA-256
/// of its own content, and it is only ever replaced by an atomic rename.
library;

import 'dart:convert';

import 'package:crypto/crypto.dart';

/// The trial of a newly activated release (SYN-006).
final class Trial {
  /// Creates a trial.
  const Trial({
    required this.sequence,
    this.launches = 0,
    this.failures = 0,
    this.open = false,
  });

  /// The release on trial.
  final int sequence;

  /// Launches started with it active.
  final int launches;

  /// Failures attributed to Plux during the trial.
  final int failures;

  /// Whether the current launch has not yet reached a healthy point; still
  /// true at the next start means the last launch crashed.
  final bool open;

  /// A copy with the given fields replaced.
  Trial copyWith({int? launches, int? failures, bool? open}) => Trial(
    sequence: sequence,
    launches: launches ?? this.launches,
    failures: failures ?? this.failures,
    open: open ?? this.open,
  );

  Map<String, Object?> _toJson() => {
    'sequence': sequence,
    'launches': launches,
    'failures': failures,
    'open': open,
  };

  static Trial? _fromJson(Object? j) {
    if (j == null) return null;
    final m = j as Map<String, Object?>;
    return Trial(
      sequence: m['sequence']! as int,
      launches: m['launches']! as int,
      failures: m['failures']! as int,
      open: m['open']! as bool,
    );
  }
}

/// The content of the pointer file. Sequences are release sequences; null
/// means none.
final class StorePointer {
  /// Creates a pointer.
  const StorePointer({
    this.active,
    this.staged,
    this.lastKnownGood,
    this.pinned,
    this.rejected,
    this.highestAccepted = 0,
    this.etag = '',
    this.trial,
  });

  /// The empty store.
  static const empty = StorePointer();

  /// The release pages render from.
  final int? active;

  /// A verified release waiting for activation (SYN-004).
  final int? staged;

  /// The release before the active one (SYN-006).
  final int? lastKnownGood;

  /// A release kept after an automatic revert, until a higher sequence
  /// arrives (SYN-006).
  final int? pinned;

  /// The release reverted from after a failed trial; a manifest with this
  /// sequence is ignored until a higher one arrives (SYN-006).
  final int? rejected;

  /// The highest sequence accepted for the channel (SEC-055).
  final int highestAccepted;

  /// The ETag of the last manifest (NFR-006).
  final String etag;

  /// The trial of the active release, while it lasts.
  final Trial? trial;

  /// The releases whose records and objects must be kept.
  Set<int> get kept => {?active, ?staged, ?lastKnownGood, ?pinned};

  /// Encodes the pointer with its checksum.
  List<int> encode() {
    final body = <String, Object?>{
      'version': 1,
      'active': active,
      'staged': staged,
      'lastKnownGood': lastKnownGood,
      'pinned': pinned,
      'rejected': rejected,
      'highestAccepted': highestAccepted,
      'etag': etag,
      'trial': trial?._toJson(),
    };
    final text = jsonEncode(body);
    return utf8.encode(
      jsonEncode({
        'sum': sha256.convert(utf8.encode(text)).toString(),
        'body': text,
      }),
    );
  }

  /// Decodes a pointer; null when the file is corrupt or of another
  /// version, which the store treats as no store (ADR-0021).
  static StorePointer? decode(List<int> bytes) {
    try {
      final outer = jsonDecode(utf8.decode(bytes)) as Map<String, Object?>;
      final text = outer['body']! as String;
      if (sha256.convert(utf8.encode(text)).toString() != outer['sum']) {
        return null;
      }
      final m = jsonDecode(text) as Map<String, Object?>;
      if (m['version'] != 1) return null;
      return StorePointer(
        active: m['active'] as int?,
        staged: m['staged'] as int?,
        lastKnownGood: m['lastKnownGood'] as int?,
        pinned: m['pinned'] as int?,
        rejected: m['rejected'] as int?,
        highestAccepted: m['highestAccepted']! as int,
        etag: m['etag']! as String,
        trial: Trial._fromJson(m['trial']),
      );
    } on Object {
      return null;
    }
  }

  /// A copy with the given fields replaced; `clear*` sets one to null.
  StorePointer copyWith({
    int? active,
    int? staged,
    int? lastKnownGood,
    int? pinned,
    int? rejected,
    int? highestAccepted,
    String? etag,
    Trial? trial,
    bool clearStaged = false,
    bool clearLastKnownGood = false,
    bool clearPinned = false,
    bool clearRejected = false,
    bool clearTrial = false,
  }) => StorePointer(
    active: active ?? this.active,
    staged: clearStaged ? null : staged ?? this.staged,
    lastKnownGood: clearLastKnownGood
        ? null
        : lastKnownGood ?? this.lastKnownGood,
    pinned: clearPinned ? null : pinned ?? this.pinned,
    rejected: clearRejected ? null : rejected ?? this.rejected,
    highestAccepted: highestAccepted ?? this.highestAccepted,
    etag: etag ?? this.etag,
    trial: clearTrial ? null : trial ?? this.trial,
  );
}
