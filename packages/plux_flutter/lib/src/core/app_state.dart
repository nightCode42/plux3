// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// App state (ADR-0023): the app document's state entries as live values
/// that plugin bindings read through `app.<name>`, and that the host reads
/// and writes when an entry is `exposed`, through [PluxState] handles.
/// Plugin-side writes, computed entries and persistence are P5's.
library;

import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/pxl/types.dart';
import 'package:plux_flutter/src/render/renderer.dart';
import 'package:plux_flutter/src/render/sections.dart';
import 'package:plux_flutter/src/state/providers.dart';

/// The app state of the active release, by entry name, in PXL form.
final class AppStateNotifier extends Notifier<Map<String, Object?>> {
  Map<String, AppStateDecl> _decls = const {};

  /// The persisted exposed entries reported already, once each.
  final Set<String> _warned = {};

  /// The entries' declarations, by name.
  Map<String, AppStateDecl> get decls => _decls;

  @override
  Map<String, Object?> build() {
    final release = ref.watch(activeReleaseProvider);
    final rt = ref.watch(pluxRuntimeProvider);
    final renderer = rt?.renderer;
    if (release == null || renderer is! PluxRenderer) {
      _decls = const {};
      return const {};
    }
    final previous = stateOrNull ?? const <String, Object?>{};
    final old = _decls;
    final (:values, :decls) = renderer.appState(release);
    _decls = decls;
    for (final d in decls.values) {
      // A value the host wrote outlives a new release while its entry
      // keeps its name and type.
      final was = old[d.name];
      if (was != null && was.type == d.type && previous.containsKey(d.name)) {
        values[d.name] = previous[d.name];
      }
      if (kDebugMode && d.exposed && d.persisted && _warned.add(d.name)) {
        rt!.reportProblem(
          PluxException(
            PluxErrorCode.actionsNotAvailable,
            'app state ${d.name} declares a persistence, which arrives in P5; it is kept in memory',
            details: {'state': d.name},
          ),
        );
      }
    }
    return values;
  }

  /// Replaces entry [name] with [value], in PXL form.
  void set(String name, Object? value) => state = {...state, name: value};
}

/// The app state of the active release.
final appStateProvider =
    NotifierProvider<AppStateNotifier, Map<String, Object?>>(
      AppStateNotifier.new,
      dependencies: [activeReleaseProvider, pluxRuntimeProvider],
    );

/// A handle on an exposed app state entry (HST-021, ADR-0023), from
/// `Plux.state<T>(name)`. Values cross in their JSON form: strings,
/// numbers, booleans, lists and maps; decimals and dates as strings.
/// `plux codegen` writes typed accessors over it (HST-030).
final class PluxState<T> {
  /// A handle on entry [name] of the app state in [container].
  PluxState(this.name, this._container);

  /// The entry's name in the app document.
  final String name;

  final ProviderContainer _container;

  AppStateNotifier get _notifier => _container.read(appStateProvider.notifier);

  /// The entry's declaration when it is exposed, else null, reported with
  /// PLX-4203.
  AppStateDecl? _exposed(String what) {
    final decl = _notifier.decls[name];
    if (decl != null && decl.exposed) return decl;
    _report(
      decl == null
          ? 'the app has no state entry $name to $what'
          : 'app state $name is not exposed, so the host cannot $what it',
    );
    return null;
  }

  void _report(String message) {
    _container
        .read(pluxRuntimeProvider)
        ?.reportProblem(
          PluxException(
            PluxErrorCode.exposedStateTypeMismatch,
            message,
            details: {'state': name},
          ),
        );
  }

  bool get _released => _container.read(activeReleaseProvider) != null;

  /// The current value; null before a release is active, and null,
  /// reported, when the entry is not exposed.
  T? get value {
    if (!_released || _exposed('read') == null) return null;
    return toJson(_container.read(appStateProvider)[name]) as T?;
  }

  /// Writes [value], completing with true; with false, reported with
  /// PLX-4203, when no release is active yet, the entry is not exposed or
  /// [value] does not have its declared type, and the entry keeps its
  /// value. The write takes effect
  /// before this returns, so every view and slot reading the entry
  /// rebuilds in the same frame; the future leaves room for persistence
  /// (P5).
  Future<bool> set(T value) async {
    if (!_released) {
      _report('no release is active yet, so app state $name cannot be written');
      return false;
    }
    final decl = _exposed('write');
    if (decl == null) return false;
    final rt = _container.read(pluxRuntimeProvider);
    final release = _container.read(activeReleaseProvider);
    final renderer = rt?.renderer;
    final types = release != null && renderer is PluxRenderer
        ? renderer.typesOf(release, '')
        : const <String, NamedType>{};
    try {
      final pxl = fromJson(
        PxlType.parse(decl.type, (n) => types[n]),
        fromHost(value),
      );
      _notifier.set(name, pxl);
      return true;
    } on FormatException catch (e) {
      _report('app state $name is a ${decl.type}: ${e.message}');
      return false;
    }
  }

  /// The values the entry takes from now on, starting with none: one per
  /// change. A watch started before a release is active emits once one
  /// is, while the entry is exposed.
  Stream<T?> watch() {
    ProviderSubscription<Object?>? sub;
    late final StreamController<T?> out;
    out = StreamController<T?>(
      onListen: () {
        if (_released && _exposed('watch') == null) return;
        sub = _container.listen<Object?>(
          appStateProvider.select((s) => s[name]),
          (_, next) {
            if (_notifier.decls[name]?.exposed ?? false) {
              out.add(toJson(next) as T?);
            }
          },
        );
      },
      onCancel: () {
        sub?.close();
        // Not returned: the cancel would wait for the done event the
        // cancelled listener never receives.
        unawaited(out.close());
      },
    );
    return out.stream;
  }
}
