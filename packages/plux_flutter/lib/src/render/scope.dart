// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// What the nodes of a page, a component instance or a template item
/// render with (ADR-0031), and the page state they subscribe to
/// (ADR-0008, RT-012).
library;

import 'package:flutter/widgets.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:plux_flutter/src/actions/engine.dart';
import 'package:plux_flutter/src/animation/registry.dart';
import 'package:plux_flutter/src/assets/icon_fonts.dart';
import 'package:plux_flutter/src/assets/image_providers.dart';
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/core/active_release.dart';
import 'package:plux_flutter/src/core/app_state.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/native_catalogue/registration.dart';
import 'package:plux_flutter/src/pxl/types.dart';
import 'package:plux_flutter/src/render/node_context.dart';
import 'package:plux_flutter/src/render/sections.dart';
import 'package:plux_flutter/src/render/values.dart';
import 'package:plux_flutter/src/state/scope_state.dart';

/// One instance of a page, component or action run on screen: the key of
/// its state (STA-001).
final class PageInstance {
  /// Creates an instance with the [initial] state, or the values [model]
  /// gives: stored, migrated or default, and computed (STA-003, STA-004).
  PageInstance(this.initial, {this.model});

  /// The declared initial state, by entry name, when there is no [model].
  final Map<String, Object?> initial;

  /// The instance's declarations and services.
  final ScopeModel? model;
}

/// The state of a page, component or run instance (STA-001). Nodes
/// subscribe with `select` on the paths their bindings read (RT-012).
final class PageStateNotifier extends Notifier<Map<String, Object?>>
    with ScopeValues {
  /// Creates the state of [instance].
  PageStateNotifier(this.instance);

  /// The instance.
  final PageInstance instance;

  @override
  ScopeModel? get model => instance.model;

  @override
  Map<String, Object?> build() =>
      instance.model == null ? instance.initial : initialValues();
}

/// The state of each page, component and run instance, disposed with it.
final pageStateProvider = NotifierProvider.autoDispose
    .family<PageStateNotifier, Map<String, Object?>, PageInstance>(
      PageStateNotifier.new,
      dependencies: [appStateProvider, pluginStateProvider],
    );

/// What the renderer provides to every node: icons (THM-005), images of
/// the release and URLs (RT-014), and the fallback of failed pages and
/// components (RT-020).
abstract interface class RenderServices {
  /// Icon [name] of [set] for a node of [scope], from the icon font of
  /// its bundle or the app's; null, reported, when there is none.
  PluxIconSource? icon(RenderScope scope, String name, String set);

  /// An image for a node of [scope]: an asset of the release by ID, or a
  /// URL on a domain the plugin declares; null, reported, when there is
  /// none it may show.
  ImageProvider<Object>? image(RenderScope scope, {String? asset, String? url});

  /// The SVG asset [asset] for a node of [scope] (CMP-031); null when the
  /// asset is not an SVG.
  PluxVectorSource? vector(RenderScope scope, String asset);

  /// Builds the fallback shown instead of a failed page or component of
  /// [plugin] (RT-020).
  Widget fallback(BuildContext context, PluxException error, String plugin);

  /// The host's native slot of [type], with the catalogue's declaration
  /// and the types its props may name; null when the host registers none
  /// or the catalogue declares none (WGT-033, ADR-0041).
  ({PluxNativeSlot slot, NativeSlotDecl decl, Map<String, NamedType> types})?
  nativeSlot(RenderScope scope, String type);
}

/// Reports a problem with a node's path (RT-020).
typedef NodeReporter = void Function(
  PluxException error, {
  required String path,
});

/// Everything the nodes of one page, component instance or template item
/// render with.
final class RenderScope {
  /// Creates a scope.
  RenderScope({
    required this.release,
    required this.plugin,
    required this.app,
    required this.pluginKey,
    required this.section,
    required this.resolver,
    required this.roots,
    required this.builders,
    required this.cache,
    required this.report,
    required this.services,
    required this.path,
    this.state,
    this.fills,
    this.parent,
    this.actions,
    this.componentState,
    this.emitEvent,
    this.animations,
  });

  /// The release rendered.
  final ActiveRelease release;

  /// The bundle whose shared sections [section] reads: the plugin's, or
  /// the app's for an app-level component.
  final BundleView plugin;

  /// The app bundle.
  final BundleView app;

  /// The plugin key.
  final String pluginKey;

  /// The page or component section whose nodes this scope renders.
  final NodeSection section;

  /// Resolves the values of [section].
  final ValueResolver resolver;

  /// The PXL roots in scope, read when a binding is evaluated.
  final Map<String, Object?> Function() roots;

  /// Builders by permanent widget ID.
  final Map<int, NodeBuilder> builders;

  /// The section cache (RT-013).
  final SectionCache cache;

  /// Reports a problem.
  final NodeReporter report;

  /// Icons, images and fallbacks.
  final RenderServices services;

  /// The node path of the scope's root, for reports.
  final String path;

  /// The page's state, which bindings under `page` read.
  final PageInstance? state;

  /// For a component instance: the instance node's slot fills, rendered in
  /// [parent].
  final ({RenderScope scope, fbs.Node node})? fills;

  /// The enclosing scope.
  final RenderScope? parent;

  /// The engine the nodes' events start runs on (ADR-0039); null where
  /// actions do not run.
  final ActionHost? actions;

  /// The state of the component instance whose nodes this scope renders,
  /// which bindings under `component` read (STA-001).
  final PageInstance? componentState;

  /// Emits an event of the component whose nodes this scope renders
  /// (`emitEvent`, SCH-030): to the instance's handler, or for a
  /// component a `PluxView` shows, to its `onEvent`. Null outside a
  /// component.
  final void Function(String event, Object? payload)? emitEvent;

  /// The timelines of the page whose nodes this scope renders (ANI-002);
  /// null where there are none, such as in a component.
  final PluxAnimations? animations;

  /// A scope with [extra] roots, for a template item.
  RenderScope withRoots(Map<String, Object?> extra, String at) {
    Map<String, Object?> all() => {...roots(), ...extra};
    return RenderScope(
      release: release,
      plugin: plugin,
      app: app,
      pluginKey: pluginKey,
      section: section,
      resolver: ValueResolver(
        plugin: plugin,
        roots: all,
        token: resolver.token,
        translation: resolver.translation,
        limits: resolver.limits,
      ),
      roots: all,
      builders: builders,
      cache: cache,
      report: report,
      services: services,
      path: at,
      state: state,
      fills: fills,
      parent: parent,
      actions: actions,
      componentState: componentState,
      emitEvent: emitEvent,
      animations: animations,
    );
  }

  /// The scope of the nearest [RenderScopeWidget].
  static RenderScope of(BuildContext context) {
    final w = context.dependOnInheritedWidgetOfExactType<RenderScopeWidget>();
    if (w == null) {
      throw StateError('a Plux node outside a Plux page');
    }
    return w.scope;
  }
}

/// Provides a [RenderScope] to the nodes below it.
final class RenderScopeWidget extends InheritedWidget {
  /// Provides [scope].
  const RenderScopeWidget({
    super.key,
    required this.scope,
    required super.child,
  });

  /// The scope.
  final RenderScope scope;

  @override
  bool updateShouldNotify(RenderScopeWidget old) =>
      !identical(old.scope, scope);
}
