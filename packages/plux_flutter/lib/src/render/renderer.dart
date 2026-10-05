// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The page renderer (ADR-0031): turns a page section of the active
/// release into widgets, one node per widget, with the page's parameters,
/// declared initial state, design tokens and translations as its bindings'
/// roots.
library;

import 'package:flutter/cupertino.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_riverpod/misc.dart' show ProviderListenable;
import 'package:http/http.dart' as http;
import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/actions/engine.dart';
import 'package:plux_flutter/src/actions/graph.dart';
import 'package:plux_flutter/src/actions/handlers.dart';
import 'package:plux_flutter/src/actions/run.dart';
import 'package:plux_flutter/src/assets/assets.dart';
import 'package:plux_flutter/src/assets/icon_fonts.dart';
import 'package:plux_flutter/src/assets/image_providers.dart';
import 'package:plux_flutter/src/bundle/container.dart';
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/core/active_release.dart';
import 'package:plux_flutter/src/core/app_state.dart';
import 'package:plux_flutter/src/core/config.dart';
import 'package:plux_flutter/src/core/fallback.dart';
import 'package:plux_flutter/src/data/client.dart' show DataCaller;
import 'package:plux_flutter/src/data/services.dart';
import 'package:plux_flutter/src/data/source.dart';
import 'package:plux_flutter/src/data/spec.dart';
import 'package:plux_flutter/src/device/guard.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/forms/form_state.dart';
import 'package:plux_flutter/src/native_catalogue/registration.dart';
import 'package:plux_flutter/src/navigation/guards.dart';
import 'package:plux_flutter/src/navigation/page_navigator.dart';
import 'package:plux_flutter/src/platform/platform_services.dart';
import 'package:plux_flutter/src/pxl/regex.dart';
import 'package:plux_flutter/src/pxl/types.dart';
import 'package:plux_flutter/src/pxl/vm.dart';
import 'package:plux_flutter/src/render/builders/builders.dart';
import 'package:plux_flutter/src/render/decoders.dart';
import 'package:plux_flutter/src/render/decoding.dart';
import 'package:plux_flutter/src/render/generated/render.g.dart';
import 'package:plux_flutter/src/render/node_context.dart';
import 'package:plux_flutter/src/render/page_actions.dart';
import 'package:plux_flutter/src/render/page_renderer.dart';
import 'package:plux_flutter/src/render/plux_node.dart';
import 'package:plux_flutter/src/render/scope.dart';
import 'package:plux_flutter/src/render/sections.dart';
import 'package:plux_flutter/src/render/theme.dart';
import 'package:plux_flutter/src/render/tokens.dart';
import 'package:plux_flutter/src/render/values.dart';
import 'package:plux_flutter/src/schema/limit_values.dart';
import 'package:plux_flutter/src/schema/limits.g.dart';
import 'package:plux_flutter/src/schema/registry.g.dart';
import 'package:plux_flutter/src/state/access.dart';
import 'package:plux_flutter/src/state/persistence.dart';
import 'package:plux_flutter/src/state/providers.dart';
import 'package:plux_flutter/src/state/scope_state.dart';
import 'package:plux_flutter/src/verify/manifest.dart';

/// The builders by permanent widget ID: generated ones and hand-written
/// ones (ADR-0031).
final Map<int, NodeBuilder> nodeBuilders = {
  ...generatedBuilders,
  ...manualBuilders,
};

/// Renders pages of the active release.
final class PluxRenderer implements PageRenderer, RenderServices {
  /// Creates a renderer. [report] receives every problem; [failure] those
  /// that count against a release's trial (SYN-006).
  PluxRenderer({
    required this.config,
    required this.report,
    required this.failure,
    required this.imageCacheDirectory,
    this.assets = AssetDevice.plain,
    this.actions,
    this.data,
    StatePersistence? statePersistence,
    VerifiedAssets? verified,
    int? cacheEntries,
    int? cacheBytes,
  }) : statePersistence = statePersistence ?? StatePersistence.inMemory(),
       _verified = verified ?? VerifiedAssets(),
       cache = SectionCache(
         maxEntries:
             cacheEntries ?? PluxLimit.runtimeSectionCacheEntries.defaultValue,
         maxBytes:
             cacheBytes ?? PluxLimit.runtimeSectionCacheBytes.defaultValue,
       );

  /// The configuration.
  final PluxConfig config;

  /// Reports a problem.
  final void Function(PluxException error) report;

  /// Records a failure attributed to Plux.
  final void Function(PluxException error) failure;

  /// Decoded page and component sections (RT-013).
  final SectionCache cache;

  /// Which file of an asset this device shows.
  final AssetDevice assets;

  /// Where remote images are cached on disk.
  final String imageCacheDirectory;

  /// What pages run their action graphs with (ADR-0039); without it,
  /// events report `PLX-4010` as in P3.
  final ActionServices? actions;

  /// What pages load data sources with (ADR-0048); without it, `data`
  /// reads nothing and the data actions fail.
  final DataServices? data;

  /// The data a page of [plugin] sees (ADR-0048): its own sources from
  /// [page] (a page section's sources and strings), its plugin's and the
  /// app's, shared by the plugin's pages of [release].
  DataScope? dataScope(
    ActiveRelease release,
    String plugin,
    ({List<fbs.DataSource> sources, String Function(int) string})? page,
    Map<String, Object?> Function() roots,
    String route,
  ) {
    final services = data;
    if (services == null) return null;
    services.limits = release.limits;
    final limits = _pxlLimits(release.limits);
    final pl = view(release, plugin), app = view(release, '');
    final types = typesOf(release, plugin);
    final caller = DataCaller(
      pluginKey: plugin,
      domains:
          release.meta(plugin).capabilities?.networkDomains ?? const <String>[],
      route: route,
    );
    List<DataSourceSpec> decode(
      List<fbs.DataSource> list,
      String Function(int) string,
      BundleView bundle,
    ) => [
      for (final d in list)
        DataSourceSpec.decode(
          d,
          strings: string,
          plugin: bundle,
          limits: limits,
        ),
    ];
    try {
      final shared = [
        for (final s in [
          ...decode(pl.dataSources, pl.string, pl),
          ...decode(app.dataSources, app.string, app),
        ])
          services.shared(release, s, caller, types),
      ];
      final own = [
        if (page != null)
          for (final s in decode(page.sources, page.string, pl))
            DataSourceController(
              spec: s,
              context: services,
              caller: caller,
              types: types,
            ),
      ];
      return DataScope(
        services: services,
        own: own,
        shared: shared,
        roots: roots,
      );
    } on PluxException catch (e) {
      report(e);
      return null;
    } on FormatException catch (e) {
      report(
        PluxException(PluxErrorCode.bundleMalformed, 'data: ${e.message}'),
      );
      return null;
    }
  }

  /// Where session, persisted and secure state lives (STA-003).
  final StatePersistence statePersistence;

  final VerifiedAssets _verified;
  ImageDiskCache? _imageCache;
  http.Client? _client;

  final Expando<Map<String, BundleView>> _views = Expando();

  /// The bundle of plugin [key] (the app bundle for the empty key).
  BundleView view(ActiveRelease release, String key) => (_views[release] ??= {})
      .putIfAbsent(key, () => BundleView(release.bundle(key), release.gate));

  /// Releases the section cache, on memory pressure (RT-013).
  void memoryPressure() => cache.clear();

  @override
  Widget build(
    BuildContext context,
    ActiveRelease release,
    PageRef page,
    Map<String, Object?> params, {
    bool routed = false,
    void Function(Object? result)? onPop,
    void Function(String event, Object? payload)? onEvent,
  }) => PluxPageView(
    key: ValueKey((release.sequence, page.route)),
    renderer: this,
    release: release,
    page: page,
    params: params,
    routed: routed,
    onPop: onPop,
    onEvent: onEvent,
  );

  /// The state the runs of plugin [plugin] ('' for the app) reach
  /// (STA-001): the app's, the plugin's, the [page]'s or [component]'s,
  /// and each run's own variables; its change stream feeds state-watcher
  /// triggers (ACT-002).
  ScopeStateAccess stateAccessFor(
    ProviderContainer container,
    ActiveRelease release,
    String plugin, {
    PageInstance? page,
    PageInstance? component,
  }) => ScopeStateAccess(
    container: container,
    plugin: plugin,
    page: page,
    component: component,
    runModel: (entries) => scopeModel(
      release,
      plugin,
      StateScopeKind.run,
      entries,
      parents: {
        'app': appStateProvider,
        if (plugin.isNotEmpty) 'plugin': pluginStateProvider(plugin),
        'page': ?(page == null ? null : pageStateProvider(page)),
        'component': ?(component == null ? null : pageStateProvider(component)),
      },
    ),
  );

  /// An input resolver over [bundle] of [release], without tokens or
  /// translations: for the runs of the app's and the plugins' triggers.
  Resolve resolverIn(ActiveRelease release, BundleView bundle) {
    final limits = _pxlLimits(release.limits);
    return (v, roots) => toPxl(
      ValueResolver(
        plugin: bundle,
        roots: () => roots,
        token: (_) => null,
        translation: (_) => null,
        limits: limits,
      ).resolve(v, bundle.string),
    );
  }

  /// The state model of a scope instance of plugin [plugin] ('' for the
  /// app) in [release] (STA-001): its [entries], decoded from [strings];
  /// computed entries read [parents] and [extraRoots].
  ScopeModel scopeModel(
    ActiveRelease release,
    String plugin,
    StateScopeKind kind,
    List<fbs.StateEntry>? entries, {
    StringTable? strings,
    Map<String, ProviderListenable<Map<String, Object?>>> parents = const {},
    Map<String, Object?> Function()? extraRoots,
    List<fbs.Form>? forms,
    FormGraphRunner? runForm,
  }) {
    final bundle = view(release, plugin);
    final limits = _pxlLimits(release.limits);
    final literal = ValueResolver(
      plugin: bundle,
      roots: () => const {},
      token: (_) => null,
      translation: (_) => null,
      limits: limits,
    );
    final str = strings ?? bundle.string;
    final types = typesOf(release, plugin);
    Object? lit(fbs.Value? v) {
      try {
        return toPxl(literal.resolve(v, str));
      } on BindingError catch (e) {
        report(
          PluxException(
            PluxErrorCode.propValueInvalid,
            '${kind.name} state: ${e.message}',
          ),
        );
        return null;
      }
    }

    return ScopeModel(
      kind: kind,
      owner: plugin,
      decls: decodeState(
        entries,
        bundle: bundle,
        strings: str,
        types: types,
        literal: lit,
      ),
      forms: decodeForms(
        forms,
        bundle: bundle,
        strings: str,
        types: types,
        literal: lit,
        regex: limits.regex,
      ),
      runForm: runForm,
      types: types,
      evaluate: (program, roots) => ValueResolver(
        plugin: bundle,
        roots: () => roots,
        token: (_) => null,
        translation: (_) => null,
        limits: limits,
      ).evaluate(program),
      persistence: statePersistence,
      report: report,
      parents: parents,
      extraRoots: extraRoots,
    );
  }

  /// The named types the pages of plugin [plugin] may use: the app's and
  /// the plugin's (SCH-010).
  Map<String, NamedType> typesOf(ActiveRelease release, String plugin) {
    final app = view(release, '');
    final pl = view(release, plugin);
    final all = {...app.types, ...pl.types};
    app.resolveFields(all);
    pl.resolveFields(all);
    return all;
  }

  final Expando<(PluxUser?, Map<String, Object?>)> _users = Expando();

  /// PXL's `user` root (HST-011): each attribute the app's userContext
  /// declares, converted from the host's text to its declared type and
  /// null when absent, and `authenticated`, the auth delegate's answer,
  /// false without one (ADR-0040). An attribute the app does not declare,
  /// or whose text does not convert, is left out and reported once per
  /// user context with `PLX-4204`, by name only: values never reach a
  /// report (SEC-092).
  Map<String, Object?> userRoot(ActiveRelease release, PluxEnvironment? env) {
    final user = env?.user;
    final memo = _users[release];
    final Map<String, Object?> attributes;
    if (memo != null && identical(memo.$1, user)) {
      attributes = memo.$2;
    } else {
      attributes = _userAttributes(release, user);
      _users[release] = (user, attributes);
    }
    return {
      ...attributes,
      'authenticated': env?.authDelegate?.isAuthenticated ?? false,
    };
  }

  Map<String, Object?> _userAttributes(ActiveRelease release, PluxUser? user) {
    final out = <String, Object?>{};
    final problems = <String>[];
    final List<({String name, String type, bool sensitive})> declared;
    final Map<String, NamedType> types;
    try {
      declared = view(release, '').userContext;
      types = typesOf(release, '');
    } on Object catch (e) {
      report(PluxException(PluxErrorCode.bundleMalformed, 'user context: $e'));
      return out;
    }
    for (final d in declared) {
      final text = user?.attributes[d.name];
      if (text == null) {
        out[d.name] = null;
        continue;
      }
      try {
        out[d.name] = fromText(PxlType.parse(d.type, (n) => types[n]), text);
      } on FormatException {
        out[d.name] = null;
        problems.add('${d.name} is not a ${d.type}');
      }
    }
    for (final name in user?.attributes.keys ?? const <String>[]) {
      if (!declared.any((d) => d.name == name)) {
        problems.add('$name is not declared');
      }
    }
    if (problems.isNotEmpty) {
      report(
        PluxException(
          PluxErrorCode.userContextInvalid,
          'user context: ${problems.join('; ')}',
        ),
      );
    }
    return out;
  }

  /// The app's flags with their defaults, PXL's `flags` root.
  Map<String, Object?> flagsRoot(ActiveRelease release) {
    final app = view(release, '');
    final literal = ValueResolver(
      plugin: app,
      roots: () => const {},
      token: (_) => null,
      translation: (_) => null,
      limits: _pxlLimits(release.limits),
    );
    final out = <String, Object?>{};
    for (final f in release.meta('').flags ?? const <fbs.Flag>[]) {
      final name = f.name;
      if (name == null) continue;
      try {
        out[name] = toPxl(literal.resolve(f.$default, app.string));
      } on BindingError catch (e) {
        report(
          PluxException(
            PluxErrorCode.propValueInvalid,
            'flag $name: ${e.message}',
          ),
        );
        out[name] = null;
      }
    }
    return out;
  }

  /// Runs guard graph [guard] of [page] over the page's [params] (NAV-009,
  /// ADR-0040): its roots are the page's parameters and declared initial
  /// state, `device`, `user` and `flags`; it may not navigate, and decides
  /// with the GuardResult it returns with `stop`. A guard that fails, or
  /// ends without a result, shows the fallback: guards fail closed. Pages
  /// with parameters that do not fit enter unguarded and show their error
  /// fallback, so nothing of them renders (NAV-007).
  @override
  Future<GuardOutcome> runGuard(
    ActiveRelease release,
    PageRef page,
    UuidKey guard,
    Map<String, Object?> params,
    PluxEnvironment? env,
  ) async {
    final services = actions;
    if (services == null) {
      return const GuardFallsBack('this runtime runs no action graphs');
    }
    final pluginView = view(release, page.plugin);
    final limits = _pxlLimits(release.limits);
    final ActionGraph graph;
    final Map<String, Object?>? roots;
    try {
      final g = pluginView.graph(guard);
      if (g == null) {
        return GuardFallsBack('its guard ${uuidString(guard)} is missing');
      }
      graph = decodeGraph(
        g,
        pluginView.string,
        (v, roots) => toPxl(
          ValueResolver(
            plugin: pluginView,
            roots: () => roots,
            token: (_) => null,
            translation: (_) => null,
            limits: limits,
          ).resolve(v, pluginView.string),
        ),
      );
      roots = _guardRoots(release, page, params, env, limits);
    } on PluxException catch (e) {
      report(e);
      return GuardFallsBack(e.message);
    }
    if (roots == null) return const GuardAllows();
    final host = ActionHost(
      lease: release.hold,
      context: StepContext(
        navigator: const _GuardNavigator(),
        emit: services.emit,
        nativeActions: services.nativeActions,
      ),
      limits: ActionLimits.of(release.limits),
      report: report,
      record: services.record,
      route: page.route,
      pluginKey: page.plugin,
    );
    final RunResult? result;
    try {
      result = await host.start(
        graph,
        roots: () => roots!,
        key: 'guard',
        path: '${page.plugin}/${page.pageKey}',
        trigger: RunTrigger.guard,
      );
    } finally {
      host.dispose();
    }
    if (result == null || result.outcome != RunOutcome.ok) {
      return GuardFallsBack(
        'its guard ${graph.id} failed: ${result?.error?.message ?? 'cancelled'}',
      );
    }
    return _guardOutcome(result.result);
  }

  /// The roots a guard of [page] reads, or null when [params] do not fit
  /// the page's declared parameters.
  Map<String, Object?>? _guardRoots(
    ActiveRelease release,
    PageRef page,
    Map<String, Object?> params,
    PluxEnvironment? env,
    PxlLimits limits,
  ) {
    final p = fbs.Page(page.section.data);
    final strings = p.strings ?? const <String>[];
    String str(int i) => i >= 0 && i < strings.length
        ? strings[i]
        : throw PluxException(
            PluxErrorCode.bundleMalformed,
            'string $i is outside page ${page.route}',
          );
    final types = typesOf(release, page.plugin);
    final literal = ValueResolver(
      plugin: view(release, page.plugin),
      roots: () => const {},
      token: (_) => null,
      translation: (_) => null,
      limits: limits,
    );
    final typed = <String, Object?>{};
    final declared = <String>{};
    try {
      for (final d in p.params ?? const <fbs.Param>[]) {
        final name = str(d.name);
        declared.add(name);
        if (params.containsKey(name)) {
          typed[name] = fromJson(
            PxlType.parse(str(d.type), (n) => types[n]),
            fromHost(params[name]),
          );
        } else if (d.$default != null) {
          typed[name] = toPxl(literal.resolve(d.$default, str));
        } else if (d.$required) {
          return null;
        } else {
          typed[name] = null;
        }
      }
      if (params.keys.any((k) => !declared.contains(k))) return null;
      final state = {
        for (final e in p.state ?? const <fbs.StateEntry>[])
          if (e.computed == 0)
            str(e.name): toPxl(literal.resolve(e.$default, str)),
      };
      final dispatcher = PlatformDispatcher.instance;
      final view0 = dispatcher.views.isEmpty ? null : dispatcher.views.first;
      final locale = env?.locale ?? dispatcher.locale;
      return {
        'params': typed,
        'page': state,
        'device': deviceRoot(
          width: view0 == null
              ? 0
              : view0.physicalSize.width / view0.devicePixelRatio,
          locale: locale,
          textScale: dispatcher.textScaleFactor,
          dark: switch (env?.themeMode ?? ThemeMode.system) {
            ThemeMode.light => false,
            ThemeMode.dark => true,
            ThemeMode.system =>
              dispatcher.platformBrightness == Brightness.dark,
          },
        ),
        'user': userRoot(release, env),
        'flags': flagsRoot(release),
      };
    } on FormatException {
      return null;
    } on BindingError {
      return null;
    }
  }

  /// GuardDecision's members by their permanent value IDs: a registry
  /// enum's value is its ID (ADR-0031).
  static final Map<int, String> _decisions = {
    for (final e
        in enumDescriptors
            .firstWhere((e) => e.name == 'GuardDecision')
            .values
            .entries)
      e.value: e.key,
  };

  static GuardOutcome _guardOutcome(Object? v) {
    if (v is! Map<String, Object?>) {
      return const GuardFallsBack('its guard ended without a GuardResult');
    }
    final decision = v['decision'];
    switch (decision is int ? _decisions[decision] : decision) {
      case 'allow':
        return const GuardAllows();
      case 'fallback':
        return const GuardFallsBack();
      case 'redirect':
        final route = v['route'];
        if (route is! String || route.isEmpty) {
          return const GuardFallsBack('its guard redirects without a route');
        }
        final params = v['params'];
        return GuardRedirects(route, {
          if (params is Map<String, Object?>)
            for (final e in params.entries)
              if (e.value != null) e.key: '${e.value}',
        });
    }
    return const GuardFallsBack('its guard returned no decision');
  }

  @override
  ({PluxNativeSlot slot, NativeSlotDecl decl, Map<String, NamedType> types})?
  nativeSlot(RenderScope scope, String type) {
    final slot = config.nativeSlots[type];
    final decl = view(scope.release, '').nativeSlots[type];
    if (slot == null || decl == null) return null;
    return (slot: slot, decl: decl, types: typesOf(scope.release, ''));
  }

  /// The parameter names [page] declares.
  @override
  Set<String> paramNames(ActiveRelease release, PageRef page) {
    release.gate.check(page.section);
    final p = fbs.Page(page.section.data);
    final strings = p.strings ?? const <String>[];
    return {
      for (final d in p.params ?? const <fbs.Param>[])
        if (d.name < strings.length) strings[d.name],
    };
  }

  /// A redirect's or a deep link's text [params], converted by the
  /// parameter types of [page] into their JSON form, which entering the
  /// page accepts (ADR-0040); throws [FormatException] for a name the
  /// page does not declare or text that does not convert.
  @override
  Map<String, Object?> textParams(
    ActiveRelease release,
    PageRef page,
    Map<String, String> params,
  ) {
    release.gate.check(page.section);
    final p = fbs.Page(page.section.data);
    final strings = p.strings ?? const <String>[];
    final types = typesOf(release, page.plugin);
    final declared = {
      for (final d in p.params ?? const <fbs.Param>[])
        if (d.name < strings.length && d.type < strings.length)
          strings[d.name]: strings[d.type],
    };
    return {
      for (final MapEntry(:key, :value) in params.entries)
        key: toJson(
          fromText(
            PxlType.parse(
              declared[key] ??
                  (throw FormatException(
                    '${page.route} has no parameter $key',
                  )),
              (n) => types[n],
            ),
            value,
          ),
        ),
    };
  }

  /// A shell tab's label, evaluated over the app scope (NAV-006); null,
  /// reported, when it cannot be.
  String? shellLabel(
    ActiveRelease release,
    fbs.ShellTab tab,
    PluxEnvironment env,
  ) {
    final v = _appValue(release, tab.label, env);
    return v is String ? v : null;
  }

  /// A shell tab's icon from the app's icon fonts (THM-005), or null.
  PluxIconSource? shellIcon(ActiveRelease release, fbs.ShellTab tab) {
    final v = _appValue(release, tab.icon, null);
    return decodeIconData(_AppDecoding(this, release), v);
  }

  Object? _appValue(ActiveRelease release, fbs.Value? v, PluxEnvironment? env) {
    final app = view(release, '');
    final limits = _pxlLimits(release.limits);
    final literal = ValueResolver(
      plugin: app,
      roots: () => const {},
      token: (_) => null,
      translation: (_) => null,
      limits: limits,
    );
    final flags = {
      for (final f in release.meta('').flags ?? const <fbs.Flag>[])
        if (f.name != null) f.name!: literal.resolve(f.$default, app.string),
    };
    try {
      return toPxl(
        ValueResolver(
          plugin: app,
          roots: () => {'flags': flags, 'user': userRoot(release, env)},
          token: (_) => null,
          translation: (_) => null,
          limits: limits,
        ).resolve(v, app.string),
      );
    } on BindingError catch (e) {
      report(
        PluxException(
          PluxErrorCode.propValueInvalid,
          'shell tab: ${e.message}',
        ),
      );
      return null;
    }
  }

  /// An icon of the app bundle's icon fonts (THM-005).
  PluxIconSource? appIcon(ActiveRelease release, String name, String set) {
    final key = iconFontKey(set);
    final a = view(release, '').assetByKey(key);
    final hash = a == null ? null : hexEncode(a.hash ?? const []);
    final path = hash == null ? null : release.assetPath(hash);
    if (hash == null || path == null) {
      report(
        PluxException(
          PluxErrorCode.propValueInvalid,
          a == null
              ? 'the release has no $set icon font'
              : 'the $set icon font is not stored',
        ),
      );
      return null;
    }
    return PluxIconSource(iconFonts, hash, path, name);
  }

  // ── Services ──────────────────────────────────────────────────────────────

  /// The icon fonts loaded, shared by every page (THM-005).
  late final IconFonts iconFonts = IconFonts(_verified, report: report);

  /// Icons come from the font the bundle of the node indexes for the set,
  /// else the app bundle's (THM-005).
  @override
  PluxIconSource? icon(RenderScope scope, String name, String set) {
    final key = iconFontKey(set);
    final a = scope.plugin.assetByKey(key) ?? scope.app.assetByKey(key);
    final hash = a == null ? null : hexEncode(a.hash ?? const []);
    final path = hash == null ? null : scope.release.assetPath(hash);
    if (hash == null || path == null) {
      scope.report(
        PluxException(
          PluxErrorCode.propValueInvalid,
          a == null
              ? 'the release has no $set icon font'
              : 'the $set icon font is not stored',
        ),
        path: scope.path,
      );
      return null;
    }
    return PluxIconSource(iconFonts, hash, path, name);
  }

  @override
  ImageProvider<Object>? image(
    RenderScope scope, {
    String? asset,
    String? url,
  }) => asset != null
      ? _asset(scope, asset)
      : url != null
      ? _remote(scope, url)
      : null;

  @override
  PluxVectorSource? vector(RenderScope scope, String asset) {
    final a = scope.plugin.asset(asset) ?? scope.app.asset(asset);
    if (a?.mediaType != svgType) return null;
    for (final hash in preferredFiles(a!, assets)) {
      final path = scope.release.assetPath(hash);
      if (path != null) {
        return PluxVectorSource(PluxVectorLoader(path, hash, _verified));
      }
    }
    scope.report(
      PluxException(
        PluxErrorCode.propValueInvalid,
        'the SVG asset $asset has no vector_graphics file in the release',
      ),
      path: scope.path,
    );
    return const PluxVectorSource(null);
  }

  /// The stored file of an asset, the best this device can show that the
  /// release holds (AST-001). An SVG is no image: only `Image` shows one,
  /// from its `vector_graphics` form (CMP-031).
  ImageProvider<Object>? _asset(RenderScope scope, String id) {
    final a = scope.plugin.asset(id) ?? scope.app.asset(id);
    if (a?.mediaType == svgType) {
      scope.report(
        PluxException(
          PluxErrorCode.propValueInvalid,
          'the SVG asset $id can be shown by Image only',
        ),
        path: scope.path,
      );
      return null;
    }
    if (a != null) {
      for (final hash in preferredFiles(a, assets)) {
        final path = scope.release.assetPath(hash);
        if (path != null) return PluxAssetImage(path, hash, _verified);
      }
    }
    scope.report(
      PluxException(
        PluxErrorCode.propValueInvalid,
        a == null
            ? 'asset $id is not in the release'
            : 'asset $id has no file this device can show',
      ),
      path: scope.path,
    );
    return null;
  }

  /// A remote image: HTTPS, on a domain the plugin declares (SEC-080,
  /// AST-002), else blocked and reported (PLX-6030).
  ImageProvider<Object>? _remote(RenderScope scope, String url) {
    final uri = Uri.tryParse(url);
    final domains =
        scope.release.meta(scope.pluginKey).capabilities?.networkDomains ??
        const <String>[];
    if (uri == null ||
        uri.scheme != 'https' ||
        !domainAllowed(uri.host, domains)) {
      scope.report(
        PluxException(
          PluxErrorCode.outboundRequestBlocked,
          'the image $url is not on an HTTPS domain the plugin declares',
        ),
        path: scope.path,
      );
      return null;
    }
    final limits = scope.release.limits;
    return PluxNetworkImage(
      uri,
      cache: _imageCache ??= ImageDiskCache(
        imageCacheDirectory,
        maxBytes: limits.valueOf(PluxLimit.runtimeImageDiskCacheBytes),
      ),
      client: _client ??= (config.httpClient ?? platformHttpClient)(),
      maxBytes: limits.valueOf(PluxLimit.runtimeImageSize),
    );
  }

  @override
  Widget fallback(BuildContext context, PluxException error, String plugin) =>
      buildFallback(context, config, error, plugin: plugin);
}

/// One page on screen.
final class PluxPageView extends ConsumerStatefulWidget {
  /// Creates the page.
  const PluxPageView({
    super.key,
    required this.renderer,
    required this.release,
    required this.page,
    required this.params,
    this.routed = false,
    this.onPop,
    this.onEvent,
  });

  /// The renderer.
  final PluxRenderer renderer;

  /// The release rendered from.
  final ActiveRelease release;

  /// The page.
  final PageRef page;

  /// The route parameters, in the host's or the JSON form of their types.
  final Map<String, Object?> params;

  /// Whether the page owns its route, so that `pop` may pop it.
  final bool routed;

  /// Receives an embedded page's `pop` result, or null (ADR-0023).
  final void Function(Object? result)? onPop;

  /// Receives the events a shown component emits (SCH-030), with the
  /// payload in its JSON form, or null.
  final void Function(String event, Object? payload)? onEvent;

  @override
  ConsumerState<PluxPageView> createState() => _PluxPageViewState();
}

final class _PluxPageViewState extends ConsumerState<PluxPageView> {
  late final BundleView _plugin = widget.renderer.view(
    widget.release,
    widget.page.plugin,
  );
  late final BundleView _app = widget.renderer.view(widget.release, '');

  /// Whether the view shows an exported component rather than a page
  /// (NAV-004, ADR-0023): its inputs are props, its state is `component`.
  late final bool _component =
      widget.page.section.kind == SectionKind.component;
  late final NodeSection _section = widget.renderer.cache.get(
    widget.page.section,
    () => _component
        ? NodeSection.component(widget.page.section)
        : NodeSection.page(widget.page.section),
  );
  late final PxlLimits _limits = _pxlLimits(widget.release.limits);
  late final Map<String, Object?> _params;
  late final PageInstance _instance;
  late final Map<String, Object?> _flags;

  /// Why the page's parameters cannot be used (NAV-007), or null.
  PluxException? _paramError;

  /// The container of the page's providers.
  late final ProviderContainer _container;

  /// The state the page's runs reach and its watchers listen to (STA-001,
  /// ACT-002).
  late final ScopeStateAccess _state = widget.renderer.stateAccessFor(
    _container,
    widget.release,
    widget.page.plugin,
    page: _component ? null : _instance,
    component: _component ? _instance : null,
  );

  /// Emits an event of the component the view shows to the host's
  /// `PluxView.onEvent` (SCH-030), in its JSON form; an event the
  /// component does not declare is reported.
  void _emitToHost(String event, Object? payload) {
    final declared = [
      for (final e
          in _section.component?.events ?? const <fbs.ComponentEvent>[])
        _section.string(e.name),
    ];
    if (!declared.contains(event)) {
      widget.renderer.report(
        PluxException(
          PluxErrorCode.propValueInvalid,
          'component ${widget.page.route} declares no event $event',
          details: {'route': widget.page.route, 'plugin': widget.page.plugin},
        ),
      );
      return;
    }
    widget.onEvent?.call(event, toJson(payload));
  }

  /// The engine of the page's runs (ADR-0039).
  late final ActionHost? _actions = () {
    final services = widget.renderer.actions;
    if (services == null) return null;
    final result = _section.page?.result ?? 0;
    return ActionHost(
      lease: widget.release.hold,
      context: StepContext(
        services: services.services,
        navigator: PageNavigator(
          router: services.router,
          context: () => context,
          route: widget.page.route,
          routed: widget.routed,
          resultType: result == 0 ? null : _section.string(result),
          types: () => _types,
          onPop: widget.onPop,
        ),
        emit: services.emit,
        nativeActions: services.nativeActions,
        state: _state,
        flows: BundleFlows(
          own: widget.page.plugin,
          bundles: (key) {
            try {
              return widget.renderer.view(widget.release, key);
            } on PluxException {
              return null;
            }
          },
          roots: () => _roots(),
          resolve: _resolveIn,
        ),
        clock: services.clock,
        track: (name, props) => services.record(
          'custom',
          route: widget.page.route,
          pluginKey: widget.page.plugin,
          fields: {'name': name, 'props': props},
        ),
        sync: services.sync,
        data: _data,
        logout: services.logout,
        device: services.deviceGuard == null
            ? null
            : DeviceScope(
                plugin: widget.page.plugin,
                guard: services.deviceGuard!,
                secure: _section.page?.secure ?? false,
                context: () => mounted ? context : null,
                overlay: () => mounted
                    ? Overlay.maybeOf(context, rootOverlay: true)
                    : null,
              ),
      ),
      limits: ActionLimits.of(widget.release.limits),
      report: widget.renderer.report,
      record: services.record,
      route: widget.page.route,
      pluginKey: widget.page.plugin,
      traces: services.traces,
      honoursParallel:
          widget.release
              .meta(widget.page.plugin)
              .requiredFeatures
              ?.contains('actions.concurrency.v1') ??
          false,
      presenter: (error) {
        if (!mounted) return;
        ScaffoldMessenger.maybeOf(context)
            ?.showSnackBar(SnackBar(content: Text(fallbackMessage(error))));
      },
    );
  }();

  /// The page's triggers and lifecycle (ACT-002), on [_actions].
  late final PageActions? _pageActions = () {
    final host = _actions;
    final services = widget.renderer.actions;
    if (host == null || services == null) return null;
    return PageActions(
      host: host,
      page: _section.page,
      bundle: _plugin,
      path: _path,
      roots: () => _roots(),
      resolve: _resolveIn(_plugin),
      hub: services.triggers,
      clock: services.clock,
      watches: StateChangeWatches(_state),
      plugin: widget.page.plugin,
      sourceOf: (name) => _data?[name]?.spec.id,
      errorsAfter: (e) => services.ownerErrors(widget.page.plugin, e),
    );
  }();

  /// The resolver of the latest build, for runs that triggers start
  /// outside a build.
  ValueResolver? _resolver;

  /// An input resolver over [bundle], with the latest build's tokens and
  /// translations.
  Resolve _resolveIn(BundleView bundle) => (v, roots) {
    final r = _resolver;
    return toPxl(
      ValueResolver(
        plugin: bundle,
        roots: () => roots,
        token: r?.token ?? (_) => null,
        translation: r?.translation ?? (_) => null,
        limits: _limits,
      ).resolve(v, bundle.string),
    );
  };

  String get _path => '${widget.page.plugin}/${widget.page.pageKey}';

  /// Runs a form's asynchronous validator graph on the page's engine,
  /// debounced with restart semantics (STA-020, ADR-0047).
  Future<RunResult?> _runForm(
    fbs.Uuid graph,
    Object? value,
    String key,
    Duration debounce,
  ) async {
    final host = _actions;
    if (host == null || host.disposed) return null;
    final g = host.graph(graph, _plugin, _path, _resolveIn(_plugin));
    if (g == null) return null;
    return host.start(
      g,
      roots: () => _roots(),
      key: key,
      path: _path,
      event: value,
      policy: RunPolicy(ConcurrencyPolicy.debounce, debounce),
    );
  }

  /// The roots of the latest build, for data parameters and transforms and
  /// for runs that triggers start outside a build.
  Map<String, Object?> Function() _roots = () => const {};

  /// The page's data (ADR-0048).
  late final DataScope? _data = () {
    final page = _section.page;
    final scope = widget.renderer.dataScope(
      widget.release,
      widget.page.plugin,
      page == null
          ? null
          : (
              sources: page.dataSources ?? const <fbs.DataSource>[],
              string: _section.string,
            ),
      () => _roots(),
      widget.page.route,
    );
    scope?.addListener(_dataChanged);
    return scope;
  }();

  void _dataChanged() {
    if (mounted) setState(() {});
  }

  void _report(PluxException e, {required String path}) {
    // A failed node counts against the release's trial, which reports it.
    if (e.code == PluxErrorCode.nodeBuildFailed) {
      widget.renderer.failure(e);
    } else {
      widget.renderer.report(e);
    }
  }

  @override
  void initState() {
    super.initState();
    _container = ProviderScope.containerOf(context, listen: false);
    final limits = widget.release.limits;
    widget.renderer.cache.resize(
      maxEntries: limits.valueOf(PluxLimit.runtimeSectionCacheEntries),
      maxBytes: limits.valueOf(PluxLimit.runtimeSectionCacheBytes),
    );
    final literal = ValueResolver(
      plugin: _plugin,
      roots: () => const {},
      token: (_) => null,
      translation: (_) => null,
      limits: _limits,
    );
    Object? read(ValueResolver r, fbs.Value? v, StringTable strings) {
      try {
        return toPxl(r.resolve(v, strings));
      } on BindingError catch (e) {
        _report(
          PluxException(PluxErrorCode.propValueInvalid, '$_path: ${e.message}'),
          path: _path,
        );
        return null;
      }
    }

    final page = _section.page;
    final component = _section.component;
    _params = {};
    // Parameters are checked on entry (NAV-007), and a component view's
    // props like them (ADR-0023): one missing, unknown or of the wrong type
    // shows the error fallback instead of the page.
    final problems = <String>[];
    final declared = <String>{};
    final params = page?.params ?? component?.props ?? const <fbs.Param>[];
    for (final p in params) {
      final name = _section.string(p.name);
      declared.add(name);
      if (widget.params.containsKey(name)) {
        try {
          _params[name] = _hostParam(
            _section.string(p.type),
            widget.params[name],
          );
        } on FormatException catch (e) {
          problems.add('$name: ${e.message}');
        }
      } else if (p.$default != null) {
        _params[name] = read(literal, p.$default, _section.string);
      } else if (p.$required) {
        problems.add('$name is missing');
      } else {
        _params[name] = null;
      }
    }
    for (final name in widget.params.keys) {
      if (!declared.contains(name)) {
        problems.add('$name is not a ${_component ? 'prop' : 'parameter'}');
      }
    }
    if (problems.isNotEmpty) {
      final what = _component ? 'component' : 'route';
      _paramError = PluxException(
        PluxErrorCode.routeParametersInvalid,
        '$what ${widget.page.route}: ${problems.join('; ')}',
        details: {'route': widget.page.route, 'plugin': widget.page.plugin},
      );
      widget.renderer.report(_paramError!);
    }
    _instance = PageInstance(
      const {},
      model: widget.renderer.scopeModel(
        widget.release,
        widget.page.plugin,
        _component ? StateScopeKind.component : StateScopeKind.page,
        page?.state ?? component?.state,
        strings: _section.string,
        parents: {
          'app': appStateProvider,
          'plugin': pluginStateProvider(widget.page.plugin),
        },
        extraRoots: () => {_component ? 'props' : 'params': _params},
        forms: page?.forms ?? component?.forms,
        runForm: _runForm,
      ),
    );
    final appLiteral = ValueResolver(
      plugin: _app,
      roots: () => const {},
      token: (_) => null,
      translation: (_) => null,
      limits: _limits,
    );
    _flags = {
      for (final f in widget.release.meta('').flags ?? const <fbs.Flag>[])
        if (f.name != null) f.name!: read(appLiteral, f.$default, _app.string),
    };
  }

  /// The declared types the page's values may have: the app's and the
  /// plugin's.
  late final Map<String, NamedType> _types = () {
    final all = {..._app.types, ..._plugin.types};
    _app.resolveFields(all);
    _plugin.resolveFields(all);
    return all;
  }();

  /// A route parameter in the host's or the literal form of its declared
  /// type (document-model.md §3), converted to its PXL value; throws
  /// [FormatException] when it does not fit the type.
  Object? _hostParam(String type, Object? value) =>
      fromJson(PxlType.parse(type, (n) => _types[n]), fromHost(value));

  bool _started = false;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final current = ModalRoute.of(context)?.isCurrent ?? true;
    if (!_started) {
      _started = true;
      if (_paramError == null) {
        // After the first frame, so the first runs read built roots.
        WidgetsBinding.instance.addPostFrameCallback((_) {
          if (mounted) _pageActions?.start();
        });
      }
      return;
    }
    _pageActions?.routeCurrent(current);
  }

  @override
  void dispose() {
    final page = _pageActions;
    if (page != null) {
      page.dispose();
    } else {
      _actions?.dispose();
    }
    _data?.removeListener(_dataChanged);
    _data?.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final failed = _paramError;
    if (failed != null) {
      return widget.renderer.fallback(context, failed, widget.page.plugin);
    }
    // Keep the page's state alive while the page is shown.
    ref.listen(pageStateProvider(_instance), (_, _) {});
    final env = ref.watch(environmentProvider);
    final media = MediaQuery.maybeOf(context);
    final locale =
        env.locale ??
        Localizations.maybeLocaleOf(context) ??
        const Locale('en');
    final source = widget.renderer.config.themeSource;
    final host = Theme.of(context);
    // The mode the host sets with Plux.setThemeMode; otherwise the host
    // theme's under a host-based source, the platform's under the Plux
    // theme (THM-002, ADR-0032).
    final dark = switch (env.themeMode) {
      ThemeMode.light => false,
      ThemeMode.dark => true,
      ThemeMode.system when source == PluxThemeSource.plux =>
        media?.platformBrightness == Brightness.dark,
      ThemeMode.system => host.brightness == Brightness.dark,
    };
    final device = deviceRoot(
      width: media?.size.width ?? 0,
      locale: locale,
      textScale: media?.textScaler.scale(1) ?? 1.0,
      dark: dark,
    );
    Map<String, Object?> roots() => {
      _component ? 'props' : 'params': _params,
      _component ? 'component' : 'page': ref.read(pageStateProvider(_instance)),
      'app': ref.read(appStateProvider),
      'plugin': ref.read(pluginStateProvider(widget.page.plugin)),
      'device': device,
      // Read on every evaluation: user.authenticated is the host's answer
      // at that moment (ADR-0040).
      'user': widget.renderer.userRoot(widget.release, env),
      'flags': _flags,
      'data': _data?.root ?? const <String, Object?>{},
    };
    _roots = roots;
    final tokens = TokenReader(
      app: _app,
      dark: dark,
      brand: env.brand,
      highContrast: media?.highContrast ?? false,
    );
    final appResolver = ValueResolver(
      plugin: _app,
      roots: () => const {},
      token: (_) => null,
      translation: (_) => null,
      limits: _limits,
    );
    final theme = _themeFor(
      (
        host: host,
        source: source,
        dark: dark,
        brand: env.brand,
        highContrast: tokens.highContrast,
      ),
      tokens,
      appResolver,
    );
    final tags = [
      locale.toLanguageTag(),
      locale.languageCode,
      widget.release.meta('').defaultLocale ?? '',
    ];
    String? translation(UuidKey key) {
      for (final tag in tags) {
        if (tag.isEmpty) continue;
        // Plugin translations first, then the app's.
        final m = _plugin.message(tag, key) ?? _app.message(tag, key);
        if (m != null) return m;
      }
      return null;
    }

    final resolver = ValueResolver(
      plugin: _plugin,
      roots: roots,
      token: (path) => theme.role(path) ?? tokens.read(path, appResolver),
      translation: translation,
      limits: _limits,
    );
    _resolver = resolver;
    final scope = RenderScope(
      release: widget.release,
      plugin: _plugin,
      app: _app,
      pluginKey: widget.page.plugin,
      section: _section,
      resolver: resolver,
      roots: roots,
      builders: nodeBuilders,
      cache: widget.renderer.cache,
      report: _report,
      services: widget.renderer,
      path: _path,
      state: _instance,
      actions: _actions,
      componentState: _component ? _instance : null,
      emitEvent: _component ? _emitToHost : null,
    );
    final plugin = widget.page.plugin;
    Widget page = PluxBoundary(
      path: _path,
      fallback: (c, e) => widget.renderer.fallback(c, e, plugin),
      child: RenderScopeWidget(scope: scope, child: const PluxNode(0)),
    );
    if (theme.cupertino case final cupertino?) {
      page = CupertinoTheme(data: cupertino, child: page);
    }
    return Theme(data: theme.material, child: page);
  }

  ({
    ThemeData host,
    PluxThemeSource source,
    bool dark,
    String? brand,
    bool highContrast,
  })?
  _themeKey;
  PluxTheme? _theme;

  /// The page's themes, rebuilt only when what they depend on changes.
  PluxTheme _themeFor(
    ({
      ThemeData host,
      PluxThemeSource source,
      bool dark,
      String? brand,
      bool highContrast,
    })
    key,
    TokenReader tokens,
    ValueResolver resolver,
  ) {
    final have = _theme;
    final old = _themeKey;
    if (have != null &&
        old != null &&
        identical(old.host, key.host) &&
        old.source == key.source &&
        old.dark == key.dark &&
        old.brand == key.brand &&
        old.highContrast == key.highContrast) {
      return have;
    }
    _themeKey = key;
    return _theme = PluxTheme.resolve(
      host: key.host,
      source: key.source,
      dark: key.dark,
      tokens: (
        token: (path) => tokens.read(path, resolver),
        raw: (path) => tokens.raw(path, resolver),
        paths: _app.tokenPaths,
      ),
    );
  }
}

PxlLimits _pxlLimits(Map<String, int> app) => PxlLimits(
  budget: app.valueOf(PluxLimit.pxlOperationBudget),
  stringLength: app.valueOf(PluxLimit.pxlStringLength),
  collectionSize: app.valueOf(PluxLimit.pxlCollectionSize),
  decimalDigits: app.valueOf(PluxLimit.pxlDecimalDigits),
  regex: RegexLimits(
    patternLength: app.valueOf(PluxLimit.pxlRegexPatternLength),
    programSize: app.valueOf(PluxLimit.pxlRegexProgramSize),
    repeat: app.valueOf(PluxLimit.pxlRegexRepeat),
  ),
);

/// PXL's `device` root (Appendix E.2) for a window [width] logical pixels
/// wide.
Map<String, Object?> deviceRoot({
  required double width,
  required Locale locale,
  required double textScale,
  required bool dark,
}) => {
  'platform': defaultTargetPlatform == TargetPlatform.iOS ? 'ios' : 'android',
  'osVersion': '',
  'locale': locale.toLanguageTag(),
  'textScale': textScale,
  'darkMode': dark,
  'sizeClass': width >= 840
      ? 'expanded'
      : width >= 600
      ? 'medium'
      : 'compact',
  // Attestation arrives in P6; until then no assurance is claimed.
  'assuranceLevel': 'AL0',
};

/// What a guard may navigate: nothing. A guard decides with the result it
/// returns; a redirect is one of its results (ADR-0040).
final class _GuardNavigator implements RunNavigator {
  const _GuardNavigator();

  static ActionError get _refused => const ActionError(
    ActionErrorKind.custom,
    PluxErrorCode.navigationRefused,
    'a guard decides with its result and cannot navigate',
  );

  @override
  Future<void> navigate(
    String route,
    Map<String, Object?> params,
    String mode,
    String? until,
  ) => Future.error(_refused);

  @override
  Future<Object?> present(
    String route,
    Map<String, Object?> params, {
    required bool sheet,
    required bool dismissible,
  }) => Future.error(_refused);

  @override
  void pop(Object? result) => throw _refused;

  @override
  void switchTab(String tab) => throw _refused;
}

/// Converts a value the host passed as a route parameter into the JSON
/// form of the document model's literals: [DateTime] as an ISO 8601
/// string, [Duration] as milliseconds and [Color] as `#RRGGBBAA`; strings,
/// numbers, booleans, lists and string-keyed maps as they are.
Object? fromHost(Object? v) => switch (v) {
  DateTime() => v.toIso8601String(),
  Duration() => v.inMilliseconds,
  Color() =>
    '#${(((v.toARGB32() & 0xffffff) << 8) | ((v.toARGB32() >> 24) & 0xff)).toRadixString(16).padLeft(8, '0').toUpperCase()}',
  List<Object?>() => [for (final x in v) fromHost(x)],
  Map<String, Object?>() => {
    for (final e in v.entries) e.key: fromHost(e.value),
  },
  _ => v,
};

/// Resolves the icons of shell tabs from the app bundle's fonts.
final class _AppDecoding implements Decoding {
  const _AppDecoding(this._renderer, this._release);

  final PluxRenderer _renderer;
  final ActiveRelease _release;

  @override
  TextDirection get textDirection => TextDirection.ltr;

  @override
  PluxIconSource? icon(String name, String set) =>
      _renderer.appIcon(_release, name, set);

  @override
  ImageProvider<Object>? image({String? asset, String? url}) => null;

  @override
  PluxVectorSource? vector(String asset) => null;
}
