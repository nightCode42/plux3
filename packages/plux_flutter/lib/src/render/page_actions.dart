// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The triggers of a page, a plugin or the app (ACT-002): a page's
/// lifecycle handlers, the owner's timers, watchers, app lifecycle, push,
/// host and data-source triggers, and its error handler (ACT-020), run on
/// the owner's engine. An error the owner's handler does not handle goes
/// on to the plugin's, then the app's.
library;

import 'dart:async';

import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/actions/clock.dart';
import 'package:plux_flutter/src/actions/engine.dart';
import 'package:plux_flutter/src/actions/graph.dart';
import 'package:plux_flutter/src/actions/handlers.dart';
import 'package:plux_flutter/src/actions/run.dart';
import 'package:plux_flutter/src/actions/triggers.dart';
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/render/sections.dart';
import 'package:plux_flutter/src/render/values.dart';

/// Evaluates a step input against a run's roots.
typedef Resolve = Object? Function(
  fbs.Value? value,
  Map<String, Object?> roots,
);

/// The triggers of one page instance, or of a plugin or the app.
final class PageActions {
  /// Creates the triggers of [page], run on [host] over [bundle]; [roots]
  /// and [resolve] read the page's latest roots. A plugin or the app has
  /// no page and gives its [specs]. [plugin] is the owner's plugin, null
  /// for the app; [sourceOf] names the data source a name means in the
  /// owner's scope; [errorsAfter] runs the next owners' error handlers.
  PageActions({
    required this.host,
    required this.page,
    required this.bundle,
    required this.path,
    required this.roots,
    required this.resolve,
    required this.hub,
    ActionClock clock = const ActionClock(),
    StateWatchSource watches = const NoStateWatches(),
    List<TriggerSpec>? specs,
    String? plugin,
    String? Function(String name)? sourceOf,
    this.errorsAfter,
  }) {
    triggers = OwnerTriggers(
      specs: specs ?? decodeTriggers(page?.triggers),
      fire: _trigger,
      clock: clock,
      watches: watches,
      plugin: plugin,
      sourceOf: sourceOf,
    );
    host.errors = routeError;
  }

  /// Runs the error handlers after the owner's own: the plugin's, then
  /// the app's (ACT-020); null for the app.
  final ErrorRouter? errorsAfter;

  /// The page's engine.
  final ActionHost host;

  /// The page section, or null for a component view.
  final fbs.Page? page;

  /// The plugin bundle the graphs are in.
  final BundleView bundle;

  /// The page's path, for reports.
  final String path;

  /// The page's roots now.
  final Map<String, Object?> Function() roots;

  /// Evaluates an input value.
  final Resolve resolve;

  /// The runtime's trigger hub.
  final TriggerHub hub;

  /// The page's own triggers.
  late final OwnerTriggers triggers;

  void Function()? _unregister;
  bool? _current;

  fbs.Handler? _lifecycle(fbs.LifecycleEvent e) {
    for (final h in page?.lifecycle ?? const <fbs.Handler>[]) {
      if (h.event == e.value) return h;
    }
    return null;
  }

  /// Starts the triggers and runs `onInit` and `onEnter`.
  void start() {
    triggers.start();
    _unregister = hub.register(triggers);
    _fireLifecycle(fbs.LifecycleEvent.Init);
    _fireLifecycle(fbs.LifecycleEvent.Enter);
    _current = true;
  }

  /// The page's route became the top route, or stopped being it: runs
  /// `onResume` or `onLeave`.
  void routeCurrent(bool current) {
    final was = _current;
    _current = current;
    if (was == null || was == current) return;
    _fireLifecycle(
      current ? fbs.LifecycleEvent.Resume : fbs.LifecycleEvent.Leave,
    );
  }

  /// Runs `onDispose` as a detached run, so the page's disposal does not
  /// cancel it, then stops the triggers and cancels the page's runs.
  void dispose() {
    final h = _lifecycle(fbs.LifecycleEvent.Dispose);
    if (h != null) {
      final graph = host.graph(h.graph, bundle, path, resolve);
      if (graph != null) {
        unawaited(
          host.start(
            graph,
            roots: roots,
            key: '$path#onDispose',
            path: path,
            trigger: RunTrigger.lifecycle,
            policy: const RunPolicy(ConcurrencyPolicy.parallel),
            detached: true,
          ),
        );
      }
    }
    triggers.dispose();
    _unregister?.call();
    host.dispose();
  }

  void _fireLifecycle(fbs.LifecycleEvent e) {
    final h = _lifecycle(e);
    if (h == null) return;
    host.fire(
      handler: h,
      bundle: bundle,
      path: path,
      roots: roots,
      resolve: resolve,
      trigger: RunTrigger.lifecycle,
      absent: ConcurrencyPolicy.queue,
      key: '$path#lifecycle${e.value}',
    );
  }

  void _trigger(TriggerSpec spec, Object? payload) {
    if (spec.kind == TriggerKind.error) return;
    host.fire(
      handler: spec.handler,
      bundle: bundle,
      path: path,
      roots: roots,
      resolve: resolve,
      payload: toPxl(payload),
      trigger: switch (spec.kind) {
        TriggerKind.timer => RunTrigger.timer,
        TriggerKind.stateChange => RunTrigger.watch,
        TriggerKind.appResume ||
        TriggerKind.appPause => RunTrigger.appLifecycle,
        TriggerKind.pushOpened => RunTrigger.push,
        TriggerKind.hostEvent => RunTrigger.hostEvent,
        TriggerKind.dataLoaded ||
        TriggerKind.dataFailed ||
        TriggerKind.dataMessage ||
        TriggerKind.dataProgress ||
        TriggerKind.outboxSynced ||
        TriggerKind.outboxFailed ||
        TriggerKind.outboxConflict => RunTrigger.data,
        TriggerKind.error => RunTrigger.error,
      },
      absent: switch (spec.kind) {
        TriggerKind.timer => ConcurrencyPolicy.drop,
        TriggerKind.stateChange ||
        TriggerKind.dataProgress => ConcurrencyPolicy.restart,
        _ => ConcurrencyPolicy.queue,
      },
      key: '$path#${spec.kind.name}:${spec.name}',
    );
  }

  /// Runs the owner's error handler for an error no `onError` handled,
  /// then the next owners' (ACT-020): true when one ran to its end.
  Future<bool> routeError(ActionError error) async {
    if (await _ownHandler(error)) return true;
    return await errorsAfter?.call(error) ?? false;
  }

  Future<bool> _ownHandler(ActionError error) async {
    final spec = triggers.errorHandler;
    if (spec == null) return false;
    final r = await host.fireFor(
      handler: spec.handler,
      bundle: bundle,
      path: path,
      roots: roots,
      resolve: resolve,
      payload: error.toPxl(),
      trigger: RunTrigger.error,
      absent: ConcurrencyPolicy.queue,
      key: '$path#onError',
      errorHandler: true,
    );
    return r?.outcome == RunOutcome.ok;
  }
}

/// Finds flows in the active release (ACT-061): a flow of the caller's
/// plugin by key, or an exported flow of another as `<plugin>/<flow>`.
final class BundleFlows implements FlowResolver {
  /// Creates the resolver for the plugin [own] of [bundles].
  BundleFlows({
    required this.own,
    required this.bundles,
    required this.roots,
    required this.resolve,
  });

  /// The caller's plugin.
  final String own;

  /// The view of a plugin's bundle, or null when the release has none.
  final BundleView? Function(String plugin) bundles;

  /// The roots a flow reads.
  final Map<String, Object?> Function() roots;

  /// An input resolver over a plugin's bundle.
  final Resolve Function(BundleView bundle) resolve;

  final Map<String, ActionGraph?> _cache = {};

  @override
  FlowTarget? flow(String name) {
    final graph = _cache.putIfAbsent(name, () {
      final slash = name.indexOf('/');
      final plugin = slash < 0 ? own : name.substring(0, slash);
      final key = slash < 0 ? name : name.substring(slash + 1);
      final bundle = bundles(plugin);
      final g = bundle?.flow(key);
      if (bundle == null || g == null || (plugin != own && !g.exported)) {
        return null;
      }
      return decodeGraph(g, bundle.string, resolve(bundle));
    });
    return graph == null ? null : FlowTarget(graph, roots);
  }
}
