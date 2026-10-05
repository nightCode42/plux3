// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The triggers and error handlers of the app and its plugins (ACT-002,
/// ACT-020): the app document's and each plugin document's timers,
/// watchers, app lifecycle, push, host and data-source triggers run with
/// the release they came in, from its activation until another release
/// replaces it or the runtime stops. Their runs read the app's and the
/// plugin's state, `user`, `flags`, `device` and the owner's data, and
/// navigate on the host's root navigator. Errors a page does not handle
/// go to its plugin's handler, then the app's.
library;

import 'dart:ui' show PlatformDispatcher;

import 'package:flutter/foundation.dart' show ValueListenable;
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/actions/engine.dart';
import 'package:plux_flutter/src/actions/handlers.dart';
import 'package:plux_flutter/src/actions/run.dart';
import 'package:plux_flutter/src/actions/triggers.dart';
import 'package:plux_flutter/src/core/active_release.dart';
import 'package:plux_flutter/src/core/app_state.dart';
import 'package:plux_flutter/src/data/services.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/navigation/page_navigator.dart';
import 'package:plux_flutter/src/render/page_actions.dart';
import 'package:plux_flutter/src/render/renderer.dart';
import 'package:plux_flutter/src/state/access.dart';
import 'package:plux_flutter/src/state/providers.dart' show PluxEnvironment;

/// The app's and the plugins' triggers of one release.
final class ReleaseOwners {
  /// Creates the owners of [release], whose runs reach state through
  /// [container]; [environment] gives `user`, and [navigatorKey] the
  /// navigator their navigation steps and fallback messages use.
  ReleaseOwners({
    required this.renderer,
    required this.release,
    required this.container,
    required this.environment,
    required this.navigatorKey,
  });

  /// The renderer.
  final PluxRenderer renderer;

  /// The release.
  final ActiveRelease release;

  /// The runtime's container.
  final ProviderContainer container;

  /// The host's environment.
  final PluxEnvironment Function() environment;

  /// The host's root navigator, or null.
  final GlobalKey<NavigatorState>? Function() navigatorKey;

  final Map<String, ({PageActions actions, DataScope? data})> _owners = {};

  /// Starts the triggers of the app and of every plugin that declares
  /// some; an owner whose bundle cannot be read is reported and skipped.
  void start() {
    final services = renderer.actions;
    if (services == null) return;
    // Only bundles that may declare triggers are opened: a plugin's
    // bundle is otherwise mapped and verified when one of its pages is
    // first shown, not at start-up.
    final keys = {
      for (final b in release.record.bundles)
        if (b.mayHaveTriggers) b.key,
    };
    for (final key in keys) {
      // A plugin, or the app, turned off by a kill switch runs nothing
      // (RT-022).
      if (release.disabled(key)) continue;
      try {
        final specs = decodeTriggers(release.meta(key).triggers);
        if (specs.isEmpty) continue;
        _owners[key] = _start(services, key, specs);
      } on PluxException {
        // A bundle that cannot be read runs nothing; the page host
        // reports it with its fallback when one of its pages is shown.
        continue;
      }
    }
    for (final o in _owners.values) {
      o.actions.start();
    }
  }

  /// Runs plugin [plugin]'s error handler, then the app's, for [error]
  /// (ACT-020): true when one handled it.
  Future<bool> errors(String plugin, ActionError error) async {
    final owner = _owners[plugin] ?? _owners[''];
    return await owner?.actions.routeError(error) ?? false;
  }

  /// Stops every trigger and cancels the runs that are not detached.
  void dispose() {
    for (final o in _owners.values) {
      o.actions.dispose();
      o.data?.dispose();
    }
    _owners.clear();
  }

  ({PageActions actions, DataScope? data}) _start(
    ActionServices services,
    String key,
    List<TriggerSpec> specs,
  ) {
    final bundle = renderer.view(release, key);
    final state = renderer.stateAccessFor(container, release, key);
    DataScope? data;
    Map<String, Object?> roots() => {
      'app': container.read(appStateProvider),
      if (key.isNotEmpty) 'plugin': container.read(pluginStateProvider(key)),
      'user': renderer.userRoot(release, environment()),
      'flags': renderer.flagsRoot(release),
      'device': _device(),
      'data': data?.root ?? const <String, Object?>{},
    };
    data = renderer.dataScope(release, key, null, roots, '');
    final navigator = navigatorKey;
    final host = ActionHost(
      context: StepContext(
        navigator: PageNavigator(
          router: services.router,
          context: () =>
              navigator()?.currentContext ??
              (throw navigationRefused(
                'no navigator for the app\'s and plugins\' triggers: set PluxConfig.navigatorKey',
              )),
          route: '',
          routed: false,
          resultType: null,
          types: () => renderer.typesOf(release, key),
        ),
        emit: services.emit,
        nativeActions: services.nativeActions,
        state: state,
        flows: BundleFlows(
          own: key,
          bundles: (k) {
            try {
              return renderer.view(release, k);
            } on PluxException {
              return null;
            }
          },
          roots: roots,
          resolve: (b) => renderer.resolverIn(release, b),
        ),
        clock: services.clock,
        track: (name, props) => services.record(
          'custom',
          pluginKey: key,
          fields: {'name': name, 'props': props},
        ),
        sync: services.sync,
        data: data,
        logout: services.logout,
      ),
      limits: ActionLimits.of(release.limits),
      report: renderer.report,
      record: services.record,
      route: '',
      pluginKey: key,
      traces: services.traces,
      honoursParallel:
          release
              .meta(key)
              .requiredFeatures
              ?.contains('actions.concurrency.v1') ??
          false,
      presenter: (error) {
        final c = navigator()?.currentContext;
        if (c == null || !c.mounted) return;
        ScaffoldMessenger.maybeOf(c)
            ?.showSnackBar(SnackBar(content: Text(fallbackMessage(error))));
      },
    );
    final actions = PageActions(
      host: host,
      page: null,
      bundle: bundle,
      path: key.isEmpty ? 'app' : key,
      roots: roots,
      resolve: renderer.resolverIn(release, bundle),
      hub: services.triggers,
      clock: services.clock,
      watches: StateChangeWatches(state),
      specs: specs,
      plugin: key.isEmpty ? null : key,
      errorsAfter: key.isEmpty
          ? null
          : (e) async => await _owners['']?.actions.routeError(e) ?? false,
    );
    return (actions: actions, data: data);
  }

  Map<String, Object?> _device() {
    final d = PlatformDispatcher.instance;
    final view = d.views.isEmpty ? null : d.views.first;
    final env = environment();
    return deviceRoot(
      width: view == null ? 0 : view.physicalSize.width / view.devicePixelRatio,
      locale: env.locale ?? d.locale,
      textScale: d.textScaleFactor,
      dark: switch (env.themeMode) {
        ThemeMode.light => false,
        ThemeMode.dark => true,
        ThemeMode.system => d.platformBrightness == Brightness.dark,
      },
    );
  }
}

/// Keeps the [ReleaseOwners] of the active release running: started when
/// a release activates, replaced with it, stopped with the runtime.
final class OwnerLifetime {
  /// Creates the lifetime over [active]; [create] makes a release's
  /// owners, or null when none run.
  OwnerLifetime(this.active, this.create) {
    active.addListener(_changed);
    _changed();
  }

  /// The active release.
  final ValueListenable<ActiveRelease?> active;

  /// Makes the owners of a release.
  final ReleaseOwners? Function(ActiveRelease release) create;

  ReleaseOwners? _current;
  ActiveRelease? _release;

  /// The owners running now, or null.
  ReleaseOwners? get current => _current;

  void _changed() {
    final r = active.value;
    if (identical(r, _release)) return;
    _release = r;
    _current?.dispose();
    _current = r == null ? null : create(r)
      ?..start();
  }

  /// Stops the owners.
  void dispose() {
    active.removeListener(_changed);
    _current?.dispose();
    _current = null;
    _release = null;
  }
}
