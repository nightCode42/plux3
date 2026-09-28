// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The typed sync events of `Plux.syncEvents` (SYN-013) and the result of
/// one sync (SYN-002, SYN-015).
library;

import 'package:plux_flutter/src/errors/plux_exception.dart';

/// Something that happened during a sync.
sealed class SyncEvent {
  const SyncEvent();
}

/// The runtime is asking the server for the manifest.
final class SyncChecking extends SyncEvent {
  /// Creates the event.
  const SyncChecking();

  @override
  String toString() => 'SyncEvent.checking';
}

/// The device already has the newest release.
final class SyncUpToDate extends SyncEvent {
  /// Creates the event.
  const SyncUpToDate(this.sequence);

  /// The active release, or null when there is none.
  final int? sequence;

  @override
  String toString() => 'SyncEvent.upToDate($sequence)';
}

/// Bundles are downloading.
final class SyncDownloading extends SyncEvent {
  /// Creates the event.
  const SyncDownloading(this.received, this.total);

  /// Bytes received so far.
  final int received;

  /// Bytes expected in all.
  final int total;

  /// Progress between 0 and 1.
  double get progress =>
      total <= 0 ? 0 : (received / total).clamp(0, 1).toDouble();

  @override
  String toString() => 'SyncEvent.downloading($received/$total)';
}

/// A new release is verified and staged; it activates per the activation
/// policy (SYN-004).
final class SyncStaged extends SyncEvent {
  /// Creates the event.
  const SyncStaged(this.sequence, {this.mandatory = false});

  /// The staged release.
  final int sequence;

  /// Whether the release is a mandatory update (REL-070).
  final bool mandatory;

  @override
  String toString() =>
      'SyncEvent.staged($sequence${mandatory ? ', mandatory' : ''})';
}

/// A staged release became the active one.
final class SyncActivated extends SyncEvent {
  /// Creates the event.
  const SyncActivated(this.sequence);

  /// The active release.
  final int sequence;

  @override
  String toString() => 'SyncEvent.activated($sequence)';
}

/// The sync failed; the active release is unchanged.
final class SyncFailed extends SyncEvent {
  /// Creates the event.
  const SyncFailed(this.error);

  /// What went wrong.
  final PluxException error;

  @override
  String toString() => 'SyncEvent.failed(${error.code.id})';
}

/// The runtime went back to the last known good release (SYN-006).
final class SyncRolledBack extends SyncEvent {
  /// Creates the event.
  const SyncRolledBack(this.from, this.to);

  /// The release that failed its trial.
  final int from;

  /// The release now active.
  final int to;

  @override
  String toString() => 'SyncEvent.rolledBack($from → $to)';
}

/// How a sync ended.
enum SyncOutcome {
  /// Nothing new.
  upToDate,

  /// A new release was staged.
  staged,

  /// The sync failed.
  failed,
}

/// The result of one sync (SYN-002), with the measurements telemetry
/// reports (SYN-015).
final class SyncResult {
  /// Creates a result.
  const SyncResult({
    required this.outcome,
    required this.duration,
    this.sequence,
    this.bytes = 0,
    this.fullBytes = 0,
    this.pluginsUpdated = 0,
    this.error,
  });

  /// How it ended.
  final SyncOutcome outcome;

  /// The release staged or already active.
  final int? sequence;

  /// How long it took.
  final Duration duration;

  /// Bytes downloaded.
  final int bytes;

  /// What the changed bundles would have cost as full downloads.
  final int fullBytes;

  /// Plugins whose bundle changed.
  final int pluginsUpdated;

  /// The failure, when [outcome] is [SyncOutcome.failed].
  final PluxException? error;

  /// Bytes downloaded as a share of the full bundles (1 without deltas).
  double get deltaRatio => fullBytes == 0 ? 0 : bytes / fullBytes;

  /// The fields of the `sync_result` telemetry event (Appendix G.2,
  /// SYN-015): duration, bytes, delta ratio, plugins updated, outcome and,
  /// for a failure, its reason. Nothing identifying or sensitive.
  Map<String, Object> toTelemetry() => {
    'duration_ms': duration.inMilliseconds,
    'bytes': bytes,
    'delta_ratio': double.parse(deltaRatio.toStringAsFixed(3)),
    'plugins_updated': pluginsUpdated,
    'outcome': outcome.name,
    if (error != null) 'reason': error!.code.reason,
    if (error != null) 'code': error!.code.id,
  };
}
