// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The runtime's Riverpod providers (ADR-0008, RT-003). They are top-level
/// declarations; all state lives in the container Plux uses — its own, the
/// host's, or one nested under the host's.
library;

import 'package:flutter/material.dart' show ThemeMode;
import 'package:flutter/widgets.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_riverpod/misc.dart' show Override;
import 'package:plux_flutter/src/core/active_release.dart';
import 'package:plux_flutter/src/core/config.dart';
import 'package:plux_flutter/src/core/runtime.dart';
import 'package:plux_flutter/src/sync/sync_event.dart';

/// Holds the runtime of the container.
final class RuntimeHolder extends Notifier<PluxRuntime?> {
  @override
  PluxRuntime? build() => null;

  /// Sets or clears the runtime.
  // A notifier's state setter, not a mutable global.
  // ignore: use_setters_to_change_properties
  void set(PluxRuntime? runtime) => state = runtime;
}

/// The runtime, once `Plux.initialize` has run.
final pluxRuntimeProvider = NotifierProvider<RuntimeHolder, PluxRuntime?>(
  RuntimeHolder.new,
  dependencies: const [],
);

/// Follows the runtime's active release (SYN-004).
final class ActiveReleaseNotifier extends Notifier<ActiveRelease?> {
  @override
  ActiveRelease? build() {
    final rt = ref.watch(pluxRuntimeProvider);
    if (rt == null) return null;
    void follow() => state = rt.active.value;
    rt.active.addListener(follow);
    ref.onDispose(() => rt.active.removeListener(follow));
    return rt.active.value;
  }
}

/// The active release.
final activeReleaseProvider =
    NotifierProvider<ActiveReleaseNotifier, ActiveRelease?>(
      ActiveReleaseNotifier.new,
      dependencies: [pluxRuntimeProvider],
    );

/// Follows the latest sync event (SYN-013).
final class SyncStatusNotifier extends Notifier<SyncEvent?> {
  @override
  SyncEvent? build() {
    final rt = ref.watch(pluxRuntimeProvider);
    if (rt == null) return null;
    void follow() => state = rt.lastEvent.value;
    rt.lastEvent.addListener(follow);
    ref.onDispose(() => rt.lastEvent.removeListener(follow));
    return rt.lastEvent.value;
  }
}

/// The latest sync event.
final syncStatusProvider = NotifierProvider<SyncStatusNotifier, SyncEvent?>(
  SyncStatusNotifier.new,
  dependencies: [pluxRuntimeProvider],
);

/// What Plux pages render with besides their own data: locale, theme mode,
/// brand, consent and user context (HST-001).
@immutable
final class PluxEnvironment {
  /// Creates an environment.
  const PluxEnvironment({
    this.locale,
    this.themeMode = ThemeMode.system,
    this.brand,
    this.consent = PluxConsent.necessaryOnly,
    this.user,
    this.authDelegate,
  });

  /// The locale; the host's when null.
  final Locale? locale;

  /// Light, dark or the system setting.
  final ThemeMode themeMode;

  /// The brand overlay.
  final String? brand;

  /// The user's consent.
  final PluxConsent consent;

  /// The user context.
  final PluxUser? user;

  /// The auth delegate (from P4).
  final PluxAuthDelegate? authDelegate;
}

/// Holds the environment; the `Plux` setters change it.
final class EnvironmentNotifier extends Notifier<PluxEnvironment> {
  @override
  PluxEnvironment build() => const PluxEnvironment();

  /// Replaces the environment.
  void replace(PluxEnvironment e) => state = e;
}

/// The environment of Plux pages.
final environmentProvider =
    NotifierProvider<EnvironmentNotifier, PluxEnvironment>(
      EnvironmentNotifier.new,
      dependencies: const [],
    );

/// The limits of the active app bundle (LIM-001, LIM-004).
final limitsProvider = Provider<Map<String, int>>((ref) {
  final r = ref.watch(activeReleaseProvider);
  return r == null ? const {} : r.limits;
}, dependencies: [activeReleaseProvider]);

/// The providers whose state is Plux's own. In a container nested under the
/// host's they are scoped to Plux's child container, so the host's
/// container never holds Plux state (ADR-0008).
final pluxScopedProviders = <Override>[
  pluxRuntimeProvider,
  environmentProvider,
];
