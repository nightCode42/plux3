// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// App and plugin state (ADR-0023, STA-001): the app document's state
/// entries as live values that plugin bindings read through `app.<name>`
/// and actions write; each plugin's own through `plugin.<name>`. The host
/// reads, writes and watches app entries that are `exposed`, through
/// [PluxState] handles, and sees the writes plugins make (STA-030,
/// HST-021). Writes are typed, computed entries memoised and stored
/// entries persisted by the scope's [ScopeValues].
library;

import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:plux_flutter/src/core/active_release.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/pxl/types.dart';
import 'package:plux_flutter/src/render/renderer.dart';
import 'package:plux_flutter/src/state/providers.dart';
import 'package:plux_flutter/src/state/scope_state.dart';

/// The app state of the active release, by entry name, in PXL form.
final class AppStateNotifier extends Notifier<Map<String, Object?>>
    with ScopeValues {
  ScopeModel? _model;

  @override
  ScopeModel? get model => _model;

  /// The entries' declarations, by name.
  Map<String, StateDecl> get decls => _model?.byName ?? const {};

  @override
  Map<String, Object?> build() {
    final release = ref.watch(activeReleaseProvider);
    final rt = ref.watch(pluxRuntimeProvider);
    final renderer = rt?.renderer;
    if (release == null || renderer is! PluxRenderer) {
      _model = null;
      return const {};
    }
    final m = renderer.scopeModel(
      release,
      '',
      StateScopeKind.app,
      renderer.view(release, '').stateEntries,
    );
    _model = m;
    return initialValues(keep: _kept(m));
  }

  /// The values a new release keeps: those of entries that keep their
  /// name and type.
  Map<String, Object?> _kept(ScopeModel next) {
    final previous = stateOrNull ?? const <String, Object?>{};
    return {
      for (final d in next.decls)
        if (!d.isComputed &&
            previous.containsKey(d.name) &&
            decls[d.name]?.typeExpr == d.typeExpr)
          d.name: previous[d.name],
    };
  }
}

/// The app state of the active release.
final appStateProvider =
    NotifierProvider<AppStateNotifier, Map<String, Object?>>(
      AppStateNotifier.new,
      dependencies: [activeReleaseProvider, pluxRuntimeProvider],
    );

/// The state of one plugin in the active release (STA-001); it lives as
/// long as the runtime and keeps its values across releases while an
/// entry keeps its name and type.
final class PluginStateNotifier extends Notifier<Map<String, Object?>>
    with ScopeValues {
  /// Creates the state of plugin [plugin].
  PluginStateNotifier(this.plugin);

  /// The plugin's key.
  final String plugin;

  ScopeModel? _model;

  @override
  ScopeModel? get model => _model;

  @override
  Map<String, Object?> build() {
    final release = ref.watch(activeReleaseProvider);
    final rt = ref.watch(pluxRuntimeProvider);
    final renderer = rt?.renderer;
    if (release == null || renderer is! PluxRenderer || !_has(release)) {
      _model = null;
      return const {};
    }
    final old = _model?.byName ?? const <String, StateDecl>{};
    final previous = stateOrNull ?? const <String, Object?>{};
    final m = renderer.scopeModel(
      release,
      plugin,
      StateScopeKind.plugin,
      renderer.view(release, plugin).stateEntries,
      parents: {'app': appStateProvider},
    );
    _model = m;
    return initialValues(
      keep: {
        for (final d in m.decls)
          if (!d.isComputed &&
              previous.containsKey(d.name) &&
              old[d.name]?.typeExpr == d.typeExpr)
            d.name: previous[d.name],
      },
    );
  }

  bool _has(ActiveRelease release) =>
      release.record.bundles.any((b) => b.key == plugin);
}

/// The state of each plugin.
final pluginStateProvider =
    NotifierProvider.family<PluginStateNotifier, Map<String, Object?>, String>(
      PluginStateNotifier.new,
      dependencies: [
        activeReleaseProvider,
        pluxRuntimeProvider,
        appStateProvider,
      ],
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
  StateDecl? _exposed(String what) {
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
  /// value; a computed entry cannot be written. The write takes effect
  /// before this returns, so every view and slot reading the entry
  /// rebuilds in the same frame, and a session, persisted or secure entry
  /// is stored behind it (STA-003).
  Future<bool> set(T value) async {
    if (!_released) {
      _report('no release is active yet, so app state $name cannot be written');
      return false;
    }
    final decl = _exposed('write');
    if (decl == null) return false;
    final type = decl.type;
    if (type == null) {
      _report('app state $name has a type this runtime cannot read');
      return false;
    }
    try {
      _notifier.writeEntry(name, fromJson(type, fromHost(value)));
      return true;
    } on FormatException catch (e) {
      _report(
        decl.sensitive
            ? 'app state $name is a ${decl.typeExpr}; the value is not'
            : 'app state $name is a ${decl.typeExpr}: ${e.message}',
      );
      return false;
    } on StateWriteException catch (e) {
      _report(e.message);
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
