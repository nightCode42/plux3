// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// One widget per node (ADR-0031): a node reads its props from the mapped
/// section when it builds, applies its override layers, evaluates its
/// bindings, subscribes to the page-state paths they read (RT-012) and
/// calls its widget's builder. Children and slot fills are nodes built
/// only when Flutter builds them (RT-011).
library;

import 'dart:developer' as developer;

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart' show Theme, kToolbarHeight;
import 'package:flutter/widgets.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:plux_flutter/src/actions/engine.dart';
import 'package:plux_flutter/src/assets/icon_fonts.dart';
import 'package:plux_flutter/src/assets/image_providers.dart';
import 'package:plux_flutter/src/bundle/container.dart';
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/bundle/safe_read.dart';
import 'package:plux_flutter/src/core/app_state.dart';
import 'package:plux_flutter/src/data/services.dart' show DataRoot;
import 'package:plux_flutter/src/db/row_identity.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/native_catalogue/registration.dart';
import 'package:plux_flutter/src/pxl/types.dart';
import 'package:plux_flutter/src/render/decoding.dart';
import 'package:plux_flutter/src/render/generated/render.g.dart';
import 'package:plux_flutter/src/render/node_context.dart';
import 'package:plux_flutter/src/render/renderer.dart'
    show PluxRenderer, fromHost;
import 'package:plux_flutter/src/render/scope.dart';
import 'package:plux_flutter/src/render/sections.dart';
import 'package:plux_flutter/src/render/values.dart';
import 'package:plux_flutter/src/schema/registry.g.dart';
import 'package:plux_flutter/src/state/access.dart';
import 'package:plux_flutter/src/state/scope_state.dart';

/// Widgets whose output must be a direct child of their parent's render
/// object (flex and stack parent data, slivers): they are never wrapped in
/// semantics or repaint boundaries.
final Set<int> _unwrapped = {
  for (final d in widgetDescriptors)
    if (const {
      'Expanded',
      'Flexible',
      'Spacer',
      'Positioned',
      'SliverAppBar',
      'SliverFillRemaining',
      'SliverGrid',
      'SliverList',
      'SliverPadding',
      'SliverToBoxAdapter',
    }.contains(d.type))
      d.id,
};

/// The node at [index] of the section in scope.
final class PluxNode extends ConsumerWidget {
  /// Creates the node widget.
  const PluxNode(this.index, {super.key});

  /// The node's index in the section's node array.
  final int index;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final scope = RenderScope.of(context);
    final c = NodeContextImpl(context, ref, scope, index);
    try {
      final w = c.build();
      c.subscribe();
      return w;
    } on Object catch (e, stack) {
      c.subscribe();
      return c.failed(e, stack);
    }
  }
}

/// The path of a node for reports: the scope's path and the node index.
String nodePath(RenderScope scope, int index) => '${scope.path}/$index';

/// The [NodeContext] of one build of one node.
final class NodeContextImpl implements NodeContext {
  /// Creates the context of node [index] in [scope].
  NodeContextImpl(this.context, this._ref, this.scope, this.index)
    : node = scope.section.node(index);

  @override
  final BuildContext context;
  final WidgetRef _ref;

  /// The scope.
  final RenderScope scope;

  /// The node's index.
  final int index;

  /// The node.
  final fbs.Node node;

  /// The page-state paths this build's bindings read (CMP-023).
  final Set<String> reads = {};

  Map<int, fbs.Value>? _props;

  String get _path => nodePath(scope, index);

  /// Builds the node's widget, wrapped in its semantics.
  Widget build() {
    if (!_visible(node)) return const SizedBox.shrink();
    final w = _content();
    return node.widget != 0 && _unwrapped.contains(node.widget)
        ? w
        : _decorate(w);
  }

  Widget _content() {
    if (node.widget == 0) {
      if (node.component != null) return _component();
      if (node.nativeSlot != 0) return _nativeSlot();
      return _unknown('a node with neither a widget nor a component');
    }
    final builder = scope.builders[node.widget];
    if (builder == null) return _unknown('widget ${node.widget}');
    return builder(this);
  }

  /// Subscribes the node to the page-state and app-state paths its
  /// bindings read, so a change of anything else does not rebuild it
  /// (RT-012); a host's write of exposed state rebuilds every view and slot
  /// reading it in the same frame (ADR-0023).
  void subscribe() {
    List<List<String>> under(String root) => [
      for (final r in reads)
        if (r == root || r.startsWith('$root.')) r.split('.').skip(1).toList(),
    ];
    final instance = scope.state;
    final paths = under('page');
    if (instance != null && paths.isNotEmpty) {
      _ref.watch(
        pageStateProvider(instance).select((s) => _Selected(paths, s)),
      );
    }
    final app = under('app');
    if (app.isNotEmpty) {
      _ref.watch(appStateProvider.select((s) => _Selected(app, s)));
    }
    final plugin = under('plugin');
    if (plugin.isNotEmpty) {
      _ref.watch(
        pluginStateProvider(scope.pluginKey)
            .select((s) => _Selected(plugin, s)),
      );
    }
    final component = scope.componentState;
    final mine = under('component');
    if (component != null && mine.isNotEmpty) {
      _ref.watch(
        pageStateProvider(component).select((s) => _Selected(mine, s)),
      );
    }
  }

  /// Reports a failed build and renders nothing; the enclosing boundary
  /// shows its fallback (RT-020).
  Widget failed(Object e, StackTrace stack) {
    final error = e is PluxException
        ? e
        : PluxException(
            PluxErrorCode.nodeBuildFailed,
            'node $_path: $e',
            details: {'node': _path},
          );
    developer.log('$error', name: 'plux', error: e, stackTrace: stack);
    scope.report(error, path: _path);
    PluxBoundary.fail(context, error);
    return const SizedBox.shrink();
  }

  Widget _unknown(String what) {
    scope.report(
      PluxException(
        PluxErrorCode.unknownWidget,
        'node $_path: $what is not known to this runtime',
        details: {'node': _path},
      ),
      path: _path,
    );
    // A neutral placeholder, labelled in debug builds (WGT-014).
    return _placeholder(what);
  }

  // ── Values ───────────────────────────────────────────────────────────────

  /// Resolves [v] of this section, reporting a failed binding (PLX-4002)
  /// as an absent value.
  Object? resolve(fbs.Value? v) {
    try {
      return scope.resolver.resolve(v, scope.section.string, reads);
    } on BindingError catch (e) {
      _bad('a binding failed: ${e.message}');
      return null;
    }
  }

  void _bad(String message) => scope.report(
    PluxException(
      PluxErrorCode.propValueInvalid,
      'node $_path: $message',
      details: {'node': _path},
    ),
    path: _path,
  );

  /// The node's props after its override layers (BND-016).
  Map<int, fbs.Value> get _merged => _props ??= () {
    final out = <int, fbs.Value>{
      for (final p in node.props ?? const <fbs.Prop>[])
        if (p.value != null) p.id: p.value!,
    };
    for (final o in _activeOverrides()) {
      for (final p in o.props ?? const <fbs.Prop>[]) {
        if (p.value != null) out[p.id] = p.value!;
      }
    }
    return out;
  }();

  /// The active override layers, in the order they apply: size classes
  /// cascading from medium to expanded (WGT-010), then platform, locale
  /// and experiment layers.
  List<fbs.Override> _activeOverrides() {
    final layers = node.overrides;
    if (layers == null || layers.isEmpty) return const [];
    final width = MediaQuery.maybeSizeOf(context)?.width ?? 0;
    final locale = Localizations.maybeLocaleOf(context);
    final platform = defaultTargetPlatform == TargetPlatform.iOS
        ? 'ios'
        : 'android';
    int rank(fbs.Override o) {
      final key = scope.section.string(o.key);
      return switch (readEnum(() => o.kind)) {
        fbs.OverrideKind.SizeClass when key == 'medium' && width >= 600 => 1,
        fbs.OverrideKind.SizeClass when key == 'expanded' && width >= 840 => 2,
        fbs.OverrideKind.Platform when key == platform => 3,
        fbs.OverrideKind.Locale
            when locale != null &&
                (key == locale.toLanguageTag() || key == locale.languageCode) =>
          key.contains('-') ? 5 : 4,
        // Experiment assignment arrives with experiments; until then no
        // variant is active.
        _ => 0,
      };
    }

    final active = [
      for (final o in layers)
        if (rank(o) > 0) o,
    ]..sort((a, b) => rank(a).compareTo(rank(b)));
    return active;
  }

  @override
  Object? prop(int id) => resolve(_merged[id]);

  @override
  T? decode<T>(int id, T? Function(Decoding d, Object? v) decoder) {
    final raw = _merged[id];
    if (raw == null) return null;
    final v = resolve(raw);
    if (v == null) return null;
    try {
      final decoded = decoder(this, v);
      if (decoded == null) _bad('prop $id is not a valid value');
      return decoded;
    } on DecodeError catch (e) {
      _bad('prop $id: ${e.message}');
      return null;
    }
  }

  @override
  Never missing(String name) =>
      throw DecodeError('the required value $name is missing');

  @override
  TextDirection get textDirection =>
      Directionality.maybeOf(context) ?? TextDirection.ltr;

  @override
  PluxIconSource? icon(String name, String set) =>
      scope.services.icon(scope, name, set);

  @override
  ImageProvider<Object>? image({String? asset, String? url}) =>
      scope.services.image(scope, asset: asset, url: url);

  @override
  PluxVectorSource? vector(String asset) => scope.services.vector(scope, asset);

  bool _visible(fbs.Node n) {
    final v = n.visible;
    if (v == null) return true;
    return resolve(v) != false;
  }

  // ── Children and slots ───────────────────────────────────────────────────

  fbs.SlotFill? _fill(int id) {
    for (final f in node.slots ?? const <fbs.SlotFill>[]) {
      if (f.id == id) return f;
    }
    return null;
  }

  Widget _child(int i) {
    final n = scope.section.node(i);
    final id = n.id;
    return PluxNode(i, key: id == null ? ValueKey(i) : ValueKey(uuidOf(id)));
  }

  @override
  bool hasSlot(int id) => (_fill(id)?.nodes ?? const []).isNotEmpty;

  @override
  Widget? slot(int id) {
    final nodes = _fill(id)?.nodes;
    if (nodes == null || nodes.isEmpty) return null;
    return _child(nodes.first);
  }

  @override
  PreferredSizeWidget? preferredSizeSlot(int id) {
    final nodes = _fill(id)?.nodes;
    if (nodes == null || nodes.isEmpty) return null;
    // Build the node once here for its preferred size; it renders as its
    // own node so its bindings stay its own.
    final probe = NodeContextImpl(context, _ref, scope, nodes.first);
    final built = probe.build();
    reads.addAll(probe.reads);
    final size = built is PreferredSizeWidget
        ? built.preferredSize
        : const Size.fromHeight(kToolbarHeight);
    return PreferredSize(preferredSize: size, child: _child(nodes.first));
  }

  @override
  List<Widget> slotList(int id) => expandAll(_fill(id)?.nodes ?? const []);

  @override
  List<Widget> children() => expandAll(node.children ?? const []);

  /// The widgets of a list of nodes: hidden nodes contribute none, `If`,
  /// `Match` and `Responsive` their chosen branch, and `ForEach` one widget
  /// per item, so list layouts see exactly the widgets shown.
  List<Widget> expandAll(List<int> indices) => [
    for (final i in indices) ...expand(i),
  ];

  /// The widgets node [i] contributes to a list of children.
  List<Widget> expand(int i) {
    final n = scope.section.node(i);
    if (!_visible(n)) return const [];
    NodeContextImpl sub() => NodeContextImpl(context, _ref, scope, i);
    switch (n.widget) {
      case WidgetIds.forEach:
        final s = sub();
        final items = s.prop(ForEachProps.items);
        reads.addAll(s.reads);
        final template = s._fill(ForEachSlots.item)?.nodes;
        if (items is! List<Object?> || template == null || template.isEmpty) {
          return const [];
        }
        return [
          for (var k = 0; k < items.length; k++)
            s.item(ForEachSlots.item, items[k], k),
        ];
      case WidgetIds.if_ || WidgetIds.match || WidgetIds.responsive:
        final s = sub();
        final branch = s.branch();
        reads.addAll(s.reads);
        return branch == null ? const [] : expand(branch);
    }
    return [_child(i)];
  }

  /// The node an `If`, `Match` or `Responsive` node shows, or null.
  int? branch() {
    int? first(int slot) {
      final nodes = _fill(slot)?.nodes;
      return nodes == null || nodes.isEmpty ? null : nodes.first;
    }

    switch (node.widget) {
      case WidgetIds.if_:
        return prop(IfProps.condition) == true
            ? first(IfSlots.then)
            : first(IfSlots.else_);
      case WidgetIds.match:
        final value = prop(MatchProps.value);
        final cases = prop(MatchProps.cases);
        final branches = _fill(MatchSlots.branches)?.nodes ?? const [];
        if (cases is List<Object?>) {
          final k = cases.indexOf(value);
          if (k >= 0 && k < branches.length) return branches[k];
        }
        return first(MatchSlots.otherwise);
      case WidgetIds.responsive:
        final width = MediaQuery.maybeSizeOf(context)?.width ?? 0;
        return (width >= 840 ? first(ResponsiveSlots.expanded) : null) ??
            (width >= 600 ? first(ResponsiveSlots.medium) : null) ??
            first(ResponsiveSlots.compact);
    }
    return null;
  }

  @override
  Widget item(int slot, Object? item, int index) {
    final nodes = _fill(slot)?.nodes;
    if (nodes == null || nodes.isEmpty) return const SizedBox.shrink();
    // A record of a watched database query that did not change is the same
    // object as before: its item is the widget it was, unless something
    // its bindings read besides the row has changed (DB-006).
    final memo = item == null || dbRowKeyOf(item) == null
        ? null
        : _ItemMemo.of(item, _itemInputs(slot, index));
    final cached = memo?.widget;
    if (cached != null) return cached;
    final itemScope = scope.withRoots({
      'item': toPxl(item),
      'index': index,
    }, '$_path/$index');
    final built = RenderScopeWidget(
      key: ValueKey(index),
      scope: itemScope,
      child: PluxNode(nodes.first),
    );
    memo?.widget = built;
    return built;
  }

  /// What an item's bindings read besides its row and the state paths the
  /// item subscribes to itself: the release, the page, the theme, the
  /// device, the user, the flags and the data of the sources that are not
  /// database sources. An item is reused only while all of it is the same.
  List<Object?> _itemInputs(int slot, int index) {
    final roots = scope.roots();
    final data = roots['data'];
    final device = roots['device'];
    return [
      scope.release,
      scope.state,
      scope.actions,
      scope.section,
      Theme.of(context),
      MediaQuery.maybeOf(context)?.highContrast,
      roots['user'],
      roots['flags'],
      data is DataRoot ? data.scope.externalRevision : data,
      if (device is Map<String, Object?>) ...[
        for (final e in device.entries) e.value,
      ],
      slot,
      index,
      _path,
    ];
  }

  @override
  void imageFailed(Object error) => _bad('an image failed to load: $error');

  // ── Events and inputs ────────────────────────────────────────────────────

  @override
  bool handles(int id) =>
      (node.handlers ?? const <fbs.Handler>[]).any((h) => h.event == id);

  @override
  void fire(int id, [Object? payload]) {
    final host = scope.actions;
    if (host == null) {
      if (kDebugMode) {
        scope.report(
          PluxException(
            PluxErrorCode.actionsNotAvailable,
            'node $_path: event $id fired where actions do not run',
            details: {'node': _path},
          ),
          path: _path,
        );
      }
      return;
    }
    final s = scope;
    for (final h in node.handlers ?? const <fbs.Handler>[]) {
      if (h.event != id) continue;
      host.fire(
        handler: h,
        bundle: s.plugin,
        path: _path,
        roots: s.roots,
        payload: toPxl(payload),
        state: stateAccess(context, s),
        // Runs a component's nodes start end with its instance (ACT-004).
        owner: s.componentState,
        emitEvent: s.emitEvent,
        resolve: (v, roots) => toPxl(
          ValueResolver(
            plugin: s.plugin,
            roots: () => roots,
            token: s.resolver.token,
            translation: s.resolver.translation,
            limits: s.resolver.limits,
          ).resolve(v, s.plugin.string),
        ),
      );
    }
  }

  @override
  Widget controlled<T>(
    int event,
    T initial,
    Widget Function(T value, ValueChanged<T>? onChanged) build,
  ) => _Controlled<T>(
    initial: initial,
    onChanged: handles(event) ? (v) => fire(event, v) : null,
    build: build,
  );

  @override
  Widget text(
    String initial,
    Widget Function(TextEditingController controller) build,
  ) => _TextHost(initial: initial, build: build);

  // ── Semantics and hints ──────────────────────────────────────────────────

  Widget _decorate(Widget w) {
    var out = w;
    if (node.hints & 1 != 0) out = RepaintBoundary(child: out);
    final sem = node.semantics;
    final testId = node.testId == 0 ? null : scope.section.string(node.testId);
    if (sem == null && testId == null) return out;
    String? text(fbs.Value? v) {
      final r = v == null ? null : resolve(v);
      return r is String ? r : null;
    }

    if (sem != null && sem.exclude) out = ExcludeSemantics(child: out);
    return Semantics(
      identifier: testId,
      label: text(sem?.label),
      hint: text(sem?.hint),
      value: text(sem?.value),
      header: sem != null && sem.header ? true : null,
      button: sem != null && sem.button ? true : null,
      liveRegion: sem != null && sem.liveRegion ? true : null,
      child: out,
    );
  }

  // ── Native slots ─────────────────────────────────────────────────────────

  /// Builds the host's widget for a native slot node (WGT-033, ADR-0041):
  /// props come from bindings, checked against the catalogue's types before
  /// host code sees them, and its events start the node's graphs. A slot
  /// the host does not register shows the neutral placeholder; a builder
  /// that throws is contained by the page's boundary.
  Widget _nativeSlot() {
    final type = scope.section.string(node.nativeSlot);
    final found = scope.services.nativeSlot(scope, type);
    if (found == null) {
      scope.report(
        PluxException(
          PluxErrorCode.nativeSlotNotRegistered,
          'node $_path: the host registers no native slot $type',
          details: {'node': _path},
        ),
        path: _path,
      );
      return _placeholder(type);
    }
    final (:slot, :decl, :types) = found;
    final props = <String, Object?>{};
    for (var k = 0; k < decl.props.length; k++) {
      final p = decl.props[k];
      final given = _merged[k];
      if (given == null) continue;
      final json = toJson(toPxl(resolve(given)));
      try {
        fromJson(PxlType.parse(p.type, (n) => types[n]), json);
        props[p.name] = json;
      } on FormatException catch (e) {
        _bad(
          'prop ${p.name} of native slot $type is not a ${p.type}: ${e.message}',
        );
      }
    }
    try {
      return slot.builder(context, _Slot(props, decl, types, this, type));
    } on Object catch (e, stack) {
      // Only the exception's type: its message may hold the user's data.
      return failed(
        PluxException(
          PluxErrorCode.hostCodeFailed,
          'node $_path: native slot $type failed with ${e.runtimeType}',
          details: {'node': _path},
        ),
        stack,
      );
    }
  }

  Widget _placeholder(String what) => kReleaseMode
      ? const SizedBox.shrink()
      : Text(
          '⟨$what⟩',
          textDirection: TextDirection.ltr,
          style: const TextStyle(fontSize: 10),
        );

  // ── Components ───────────────────────────────────────────────────────────

  Widget _component() {
    final id = uuidOf(node.component!);
    // A plugin's own components, then the app's.
    var bundle = scope.plugin;
    var section = bundle.section(SectionKind.component, id);
    if (section == null) {
      bundle = scope.app;
      section = bundle.section(SectionKind.component, id);
    }
    if (section == null) {
      throw PluxException(
        PluxErrorCode.bundleMalformed,
        'node $_path: component ${uuidString(id)} is not in the bundle',
      );
    }
    final found = section;
    scope.release.gate.check(found);
    final cs = scope.cache.get(found, () => NodeSection.component(found));
    final component = cs.component!;
    // Defaults are values of the component's own section and bundle.
    final defaults = ValueResolver(
      plugin: bundle,
      roots: scope.roots,
      token: scope.resolver.token,
      translation: scope.resolver.translation,
      limits: scope.resolver.limits,
    );
    final declared = component.props ?? const <fbs.Param>[];
    final props = <String, Object?>{};
    for (var k = 0; k < declared.length; k++) {
      final name = cs.string(declared[k].name);
      final given = _merged[k];
      props[name] = given != null
          ? toPxl(resolve(given))
          : toPxl(_resolveIn(defaults, cs, declared[k].$default));
    }
    final path = '$_path/${uuidString(id)}';
    final parentRoots = scope.roots;
    // The instance's handlers receive the component's events (SCH-030):
    // a declared event's index is the instance node's event ID.
    final events = [
      for (final e in component.events ?? const <fbs.ComponentEvent>[])
        cs.string(e.name),
    ];
    void emit(String event, Object? payload) {
      final i = events.indexOf(event);
      if (i < 0) {
        _bad('component ${uuidString(id)} declares no event $event');
        return;
      }
      if (handles(i)) fire(i, payload);
    }

    final services = scope.services;
    final plugin = scope.pluginKey;
    final outer = scope;
    return PluxBoundary(
      path: path,
      fallback: (c, e) => services.fallback(c, e, plugin),
      child: _ComponentInstance(
        key: ValueKey(path),
        actions: scope.actions,
        create: () {
          final renderer = services;
          if (renderer is! PluxRenderer) {
            return PageInstance({
              for (final e in component.state ?? const <fbs.StateEntry>[])
                if (e.computed == 0)
                  cs.string(e.name): toPxl(
                    _resolveIn(defaults, cs, e.$default),
                  ),
            });
          }
          return PageInstance(
            const {},
            model: renderer.scopeModel(
              outer.release,
              identical(bundle, outer.app) ? '' : plugin,
              StateScopeKind.component,
              component.state,
              strings: cs.string,
              parents: {
                'app': appStateProvider,
                'plugin': pluginStateProvider(plugin),
              },
              extraRoots: () => {'props': props},
              forms: component.forms,
              runForm: (graph, value, key, debounce) async {
                final host = outer.actions;
                if (host == null || host.disposed) return null;
                Map<String, Object?> runRoots() => {
                  ...parentRoots(),
                  'props': props,
                };
                final g = host.graph(
                  graph,
                  bundle,
                  path,
                  (v, roots) => toPxl(
                    ValueResolver(
                      plugin: bundle,
                      roots: () => roots,
                      token: outer.resolver.token,
                      translation: outer.resolver.translation,
                      limits: outer.resolver.limits,
                    ).resolve(v, bundle.string),
                  ),
                );
                if (g == null) return null;
                return host.start(
                  g,
                  roots: runRoots,
                  key: '$path#$key',
                  path: path,
                  event: value,
                  policy: RunPolicy(ConcurrencyPolicy.debounce, debounce),
                );
              },
            ),
          );
        },
        build: (instance, ref) {
          Map<String, Object?> roots() => {
            ...parentRoots(),
            'props': props,
            'component': ref.read(pageStateProvider(instance)),
          };
          final inner = RenderScope(
            release: outer.release,
            plugin: bundle,
            app: outer.app,
            pluginKey: outer.pluginKey,
            section: cs,
            resolver: ValueResolver(
              plugin: bundle,
              roots: roots,
              token: outer.resolver.token,
              translation: outer.resolver.translation,
              limits: outer.resolver.limits,
            ),
            roots: roots,
            builders: outer.builders,
            cache: outer.cache,
            report: outer.report,
            services: outer.services,
            path: path,
            state: outer.state,
            fills: (scope: outer, node: node),
            parent: outer,
            actions: outer.actions,
            componentState: instance,
            emitEvent: emit,
          );
          return RenderScopeWidget(scope: inner, child: const PluxNode(0));
        },
      ),
    );
  }

  Object? _resolveIn(ValueResolver r, NodeSection s, fbs.Value? v) {
    try {
      return r.resolve(v, s.string, reads);
    } on BindingError catch (e) {
      _bad('a default failed: ${e.message}');
      return null;
    }
  }
}

/// The page-state values at some paths, compared deeply.
final class _Selected {
  _Selected(List<List<String>> paths, Map<String, Object?> state)
    : values = [for (final p in paths) _at(state, p)];

  final List<Object?> values;

  static Object? _at(Object? v, List<String> path) {
    var cur = v;
    for (final k in path) {
      if (cur is! Map<String, Object?>) return null;
      cur = cur[k];
    }
    return cur;
  }

  @override
  bool operator ==(Object other) =>
      other is _Selected && _deepEquals(values, other.values);

  @override
  int get hashCode => values.length;
}

bool _deepEquals(Object? a, Object? b) {
  if (a is List<Object?> && b is List<Object?>) {
    if (a.length != b.length) return false;
    for (var i = 0; i < a.length; i++) {
      if (!_deepEquals(a[i], b[i])) return false;
    }
    return true;
  }
  if (a is Set<Object?> && b is Set<Object?>) {
    return a.length == b.length && a.containsAll(b);
  }
  if (a is Map<String, Object?> && b is Map<String, Object?>) {
    if (a.length != b.length) return false;
    for (final e in a.entries) {
      if (!b.containsKey(e.key) || !_deepEquals(e.value, b[e.key])) {
        return false;
      }
    }
    return true;
  }
  return a == b;
}

/// An input keeping its value locally (ADR-0031).
final class _Controlled<T> extends StatefulWidget {
  const _Controlled({
    required this.initial,
    required this.onChanged,
    required this.build,
  });

  final T initial;
  final ValueChanged<T>? onChanged;
  final Widget Function(T value, ValueChanged<T>? onChanged) build;

  @override
  State<_Controlled<T>> createState() => _ControlledState<T>();
}

final class _ControlledState<T> extends State<_Controlled<T>> {
  late T _value = widget.initial;

  @override
  void didUpdateWidget(_Controlled<T> old) {
    super.didUpdateWidget(old);
    if (!_deepEquals(widget.initial, old.initial)) _value = widget.initial;
  }

  @override
  Widget build(BuildContext context) {
    final fire = widget.onChanged;
    return widget.build(
      _value,
      fire == null
          ? null
          : (v) {
              setState(() => _value = v);
              fire(v);
            },
    );
  }
}

/// A text input's controller, kept for the life of the node.
final class _TextHost extends StatefulWidget {
  const _TextHost({required this.initial, required this.build});

  final String initial;
  final Widget Function(TextEditingController controller) build;

  @override
  State<_TextHost> createState() => _TextHostState();
}

final class _TextHostState extends State<_TextHost> {
  late final TextEditingController _controller = TextEditingController(
    text: widget.initial,
  );

  @override
  void didUpdateWidget(_TextHost old) {
    super.didUpdateWidget(old);
    if (widget.initial != old.initial) _controller.text = widget.initial;
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => widget.build(_controller);
}

/// Contains the failures of a page or component instance (RT-020): when a
/// node below fails to build, the boundary shows [fallback] instead of its
/// child, and nothing outside it is affected.
final class PluxBoundary extends StatefulWidget {
  /// Creates a boundary around [child].
  const PluxBoundary({
    super.key,
    required this.path,
    required this.child,
    this.fallback,
  });

  /// The node path of what the boundary contains.
  final String path;

  /// What the boundary contains.
  final Widget child;

  /// Builds the fallback; an empty box when null.
  final Widget Function(BuildContext context, PluxException error)? fallback;

  /// Tells the nearest boundary above [context] that a node failed.
  static void fail(BuildContext context, PluxException error) {
    final state = context.findAncestorStateOfType<_PluxBoundaryState>();
    state?._fail(error);
  }

  @override
  State<PluxBoundary> createState() => _PluxBoundaryState();
}

final class _PluxBoundaryState extends State<PluxBoundary> {
  PluxException? _error;

  void _fail(PluxException error) {
    if (_error != null) return;
    _error = error;
    // The failing node is building now; switch after the frame.
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) setState(() {});
    });
  }

  @override
  Widget build(BuildContext context) {
    final e = _error;
    if (e == null) return widget.child;
    return widget.fallback?.call(context, e) ?? const SizedBox.shrink();
  }
}

/// What a native slot's builder reads and emits through.
final class _Slot implements PluxSlot {
  _Slot(this._props, this._decl, this._types, this._node, this._type);

  final Map<String, Object?> _props;
  final NativeSlotDecl _decl;
  final Map<String, NamedType> _types;
  final NodeContextImpl _node;
  final String _type;

  @override
  Object? operator [](String name) => _props[name];

  @override
  void emit(String name, [Object? payload]) {
    final i = _decl.events.indexWhere((e) => e.name == name);
    if (i < 0) {
      _node._bad('native slot $_type has no event $name');
      return;
    }
    final type = _decl.events[i].payload;
    Object? value;
    if (type != null && payload != null) {
      try {
        value = fromJson(
          PxlType.parse(type, (n) => _types[n]),
          fromHost(payload),
        );
      } on FormatException catch (e) {
        _node._bad(
          'event $name of native slot $_type carries no $type: ${e.message}',
        );
        return;
      }
    }
    _node.fire(i, value);
  }
}

/// The state a run started by a node of [scope] reads and writes
/// (STA-001): the app's, the plugin's, the page's or component's, and its
/// own variables.
StateAccess? stateAccess(BuildContext context, RenderScope scope) {
  final renderer = scope.services;
  if (renderer is! PluxRenderer) return null;
  final page = scope.state;
  final component = scope.componentState;
  return ScopeStateAccess(
    container: ProviderScope.containerOf(context, listen: false),
    plugin: scope.pluginKey,
    page: identical(page, component) ? null : page,
    component: component,
    runModel: (entries) => renderer.scopeModel(
      scope.release,
      identical(scope.plugin, scope.app) ? '' : scope.pluginKey,
      StateScopeKind.run,
      entries,
      parents: {
        'app': appStateProvider,
        'plugin': pluginStateProvider(scope.pluginKey),
        if (page != null && !identical(page, component))
          'page': pageStateProvider(page),
        if (component != null) 'component': pageStateProvider(component),
      },
    ),
  );
}

/// Holds a component instance's state for as long as the instance is
/// shown (STA-001).
final class _ComponentInstance extends ConsumerStatefulWidget {
  const _ComponentInstance({
    super.key,
    required this.create,
    required this.build,
    this.actions,
  });

  final PageInstance Function() create;

  /// The engine the instance's runs start on: they end with it (ACT-004).
  final ActionHost? actions;
  final Widget Function(PageInstance instance, WidgetRef ref) build;

  @override
  ConsumerState<_ComponentInstance> createState() => _ComponentInstanceState();
}

final class _ComponentInstanceState extends ConsumerState<_ComponentInstance> {
  late final PageInstance _instance = widget.create();

  @override
  void dispose() {
    widget.actions?.cancelOwned(_instance);
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    // Keeps the instance's state alive while it is shown.
    ref.listen(pageStateProvider(_instance), (_, _) {});
    return widget.build(_instance, ref);
  }
}

/// The widget built for one record of a watched query, with what it was
/// built from.
final class _ItemMemo {
  _ItemMemo._(this.inputs);

  static final Expando<_ItemMemo> _memos = Expando('plux item memos');

  /// The memo of [row] for [inputs]: the earlier one if its inputs are
  /// equal, else a new one that replaces it.
  static _ItemMemo of(Object row, List<Object?> inputs) {
    final old = _memos[row];
    if (old != null && _same(old.inputs, inputs)) return old;
    return _memos[row] = _ItemMemo._(inputs);
  }

  static bool _same(List<Object?> a, List<Object?> b) {
    if (a.length != b.length) return false;
    for (var i = 0; i < a.length; i++) {
      if (!_equal(a[i], b[i])) return false;
    }
    return true;
  }

  /// Equal as the item's bindings see them: roots such as `user` are new
  /// small maps on every read, so maps are compared by their entries.
  static bool _equal(Object? a, Object? b) {
    if (identical(a, b) || a == b) return true;
    if (a is! Map<Object?, Object?> || b is! Map<Object?, Object?>) {
      return false;
    }
    if (a.length != b.length) return false;
    for (final e in a.entries) {
      if (!b.containsKey(e.key) || b[e.key] != e.value) return false;
    }
    return true;
  }

  final List<Object?> inputs;

  /// The item's widget, once built.
  Widget? widget;
}
