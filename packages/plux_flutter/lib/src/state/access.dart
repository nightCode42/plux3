// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// How an action run reaches state (STA-001, STA-002): writes by path
/// (`page.count`, `app.user`, `run.step`), the run's own variables, and a
/// per-path change stream that state-watcher triggers listen to.
library;

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_riverpod/misc.dart' show ProviderListenable;
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/core/app_state.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/render/scope.dart';
import 'package:plux_flutter/src/state/scope_state.dart';

/// Called after the value at a watched path changed.
typedef StateListener = void Function(Object? previous, Object? next);

/// The change stream of state: R1's state-watcher triggers listen here.
abstract interface class StateChangeSource {
  /// Calls [listener] after each change of the value at [path], a root and
  /// a path under it (`page.count`, `app.user.name`); a write that leaves
  /// the value equal does not call it. Returns the function that stops
  /// listening.
  void Function() listen(String path, StateListener listener);
}

/// The variables of one action run (scope `run`).
abstract interface class RunVariables {
  /// The current values, which `run.<name>` reads.
  Map<String, Object?> get values;

  /// State access that also reaches the run's variables.
  StateAccess get access;

  /// Ends the run's variables.
  void close();
}

/// State as an action run sees it; writes throw [StateWriteException].
abstract interface class StateAccess implements StateChangeSource {
  /// Writes [value] to the entry at [path] (setState).
  void write(String path, Object? value);

  /// Merges [patch] into the object entry at [path] (patchState).
  void patch(String path, Map<String, Object?> patch);

  /// Resets the entry at [path] to its default (resetState).
  void reset(String path);

  /// Opens the variables of a run whose graph declares [entries]; null
  /// when it declares none.
  RunVariables? openRun(List<fbs.StateEntry> entries);
}

/// [StateAccess] over the scope instances one node sees.
final class ScopeStateAccess implements StateAccess {
  /// Creates access for plugin [plugin] with the page and component
  /// instances in scope; [runModel] builds the model of a run's variables.
  ScopeStateAccess({
    required this.container,
    required this.plugin,
    this.page,
    this.component,
    this.run,
    this.runModel,
  });

  /// The runtime's container.
  final ProviderContainer container;

  /// The plugin.
  final String plugin;

  /// The page instance, if any.
  final PageInstance? page;

  /// The component instance, if any.
  final PageInstance? component;

  /// The current run's variables, if any.
  final PageInstance? run;

  /// Builds the model of a run's variables.
  final ScopeModel Function(List<fbs.StateEntry> entries)? runModel;

  /// The provider of [root]'s values, or null.
  ProviderListenable<Map<String, Object?>>? providerOf(String root) =>
      switch (root) {
        'app' => appStateProvider,
        'plugin' => pluginStateProvider(plugin),
        'page' => page == null ? null : pageStateProvider(page!),
        'component' => component == null ? null : pageStateProvider(component!),
        'run' => run == null ? null : pageStateProvider(run!),
        _ => null,
      };

  ScopeValues _scope(String root) {
    final values = switch (root) {
      'app' => container.read(appStateProvider.notifier),
      'plugin' => container.read(pluginStateProvider(plugin).notifier),
      'page' when page != null => container.read(
        pageStateProvider(page!).notifier,
      ),
      'component' when component != null => container.read(
        pageStateProvider(component!).notifier,
      ),
      'run' when run != null => container.read(
        pageStateProvider(run!).notifier,
      ),
      _ => null,
    };
    return values ??
        (throw StateWriteException(
          PluxErrorCode.stateWriteRefused,
          'no $root state is in scope here',
        ));
  }

  (ScopeValues, String) _entry(String path) {
    final dot = path.indexOf('.');
    if (dot <= 0) {
      throw StateWriteException(
        PluxErrorCode.stateWriteRefused,
        '$path is not a state path',
      );
    }
    return (_scope(path.substring(0, dot)), path.substring(dot + 1));
  }

  @override
  void write(String path, Object? value) {
    final (s, name) = _entry(path);
    s.writeEntry(name, value);
  }

  @override
  void patch(String path, Map<String, Object?> patch) {
    final (s, name) = _entry(path);
    s.patchEntry(name, patch);
  }

  @override
  void reset(String path) {
    final (s, name) = _entry(path);
    s.resetEntry(name);
  }

  @override
  void Function() listen(String path, StateListener listener) {
    final parts = path.split('.');
    final provider = providerOf(parts.first);
    if (provider == null) return () {};
    final sub = container.listen<Object?>(
      provider.select((s) => valueAt(s, parts.skip(1))),
      (prev, next) {
        if (!sameValue(prev, next)) listener(prev, next);
      },
    );
    return sub.close;
  }

  @override
  RunVariables? openRun(List<fbs.StateEntry> entries) {
    final build = runModel;
    if (entries.isEmpty || build == null) return null;
    return _Run(this, PageInstance(const {}, model: build(entries)));
  }
}

final class _Run implements RunVariables {
  _Run(ScopeStateAccess outer, PageInstance instance)
    : access = ScopeStateAccess(
        container: outer.container,
        plugin: outer.plugin,
        page: outer.page,
        component: outer.component,
        run: instance,
        runModel: outer.runModel,
      ),
      _instance = instance {
    // Keeps the run's variables alive until the run ends.
    _keep = outer.container.listen(pageStateProvider(instance), (_, _) {});
  }

  @override
  final ScopeStateAccess access;
  final PageInstance _instance;
  late final ProviderSubscription<Map<String, Object?>> _keep;

  @override
  Map<String, Object?> get values =>
      access.container.read(pageStateProvider(_instance));

  @override
  void close() => _keep.close();
}
