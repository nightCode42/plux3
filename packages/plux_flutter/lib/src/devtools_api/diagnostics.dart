// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Hooks consumed by `plux_devtools` (spec §6.4, `devtools_api/`): what the
/// runtime shows to debugging tools. Nothing is recorded in release builds.
library;

import 'package:flutter/foundation.dart';
import 'package:plux_flutter/src/core/active_release.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/sync/sync_event.dart';

/// A problem the runtime reported.
@immutable
final class PluxDiagnostic {
  /// Creates a diagnostic.
  const PluxDiagnostic({required this.at, required this.error});

  /// When it was reported, on the device clock.
  final DateTime at;

  /// The problem.
  final PluxException error;
}

/// A plugin of the active release.
@immutable
final class PluxPluginInfo {
  /// Creates the description.
  const PluxPluginInfo({
    required this.key,
    required this.version,
    required this.bundleHash,
    required this.switchedOff,
  });

  /// The plugin key; empty for the app bundle.
  final String key;

  /// The plugin version.
  final int version;

  /// The signed bundle hash, lower-case hexadecimal.
  final String bundleHash;

  /// Whether a kill switch keeps it from rendering (RT-022).
  final bool switchedOff;
}

/// The active release as debugging tools show it.
@immutable
final class PluxReleaseInfo {
  /// Creates the description.
  const PluxReleaseInfo({required this.sequence, required this.plugins});

  /// The release sequence.
  final int sequence;

  /// The app bundle and every plugin, in the release's order.
  final List<PluxPluginInfo> plugins;
}

/// What the runtime shows to debugging tools, read through
/// `Plux.diagnostics`. In release builds [log] stays empty and [release]
/// null.
abstract interface class PluxDiagnostics {
  /// The latest problems the runtime reported, oldest first.
  ValueListenable<List<PluxDiagnostic>> get log;

  /// The active release.
  ValueListenable<PluxReleaseInfo?> get release;

  /// The latest sync event.
  ValueListenable<SyncEvent?> get syncStatus;
}

/// The runtime's diagnostics.
final class RuntimeDiagnostics implements PluxDiagnostics {
  /// Follows [active] and [syncStatus]; keeps the last [capacity] problems.
  RuntimeDiagnostics(
    ValueListenable<ActiveRelease?> active,
    this.syncStatus, {
    this.capacity = 200,
  }) : _active = active {
    if (kReleaseMode) return;
    _active.addListener(_follow);
    _follow();
  }

  final ValueListenable<ActiveRelease?> _active;

  /// The most problems [log] keeps.
  final int capacity;

  final ValueNotifier<List<PluxDiagnostic>> _log = ValueNotifier(const []);
  final ValueNotifier<PluxReleaseInfo?> _release = ValueNotifier(null);

  @override
  ValueListenable<List<PluxDiagnostic>> get log => _log;

  @override
  ValueListenable<PluxReleaseInfo?> get release => _release;

  @override
  final ValueListenable<SyncEvent?> syncStatus;

  /// Records a problem the runtime reported.
  void record(PluxException error) {
    if (kReleaseMode) return;
    final next = [
      ..._log.value,
      PluxDiagnostic(at: DateTime.now(), error: error),
    ];
    _log.value = List.unmodifiable(
      next.length > capacity ? next.sublist(next.length - capacity) : next,
    );
  }

  void _follow() {
    final r = _active.value;
    _release.value = r == null
        ? null
        : PluxReleaseInfo(
            sequence: r.sequence,
            plugins: List.unmodifiable([
              for (final b in r.record.bundles)
                PluxPluginInfo(
                  key: b.key,
                  version: b.version,
                  bundleHash: b.hash,
                  switchedOff:
                      r.control.appKillSwitch ||
                      (!b.isApp && r.disabled(b.key)),
                ),
            ]),
          );
  }

  /// Stops following the runtime.
  void dispose() {
    _active.removeListener(_follow);
  }
}
