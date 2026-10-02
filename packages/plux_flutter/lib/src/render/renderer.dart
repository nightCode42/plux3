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
import 'package:http/http.dart' as http;
import 'package:plux_flutter/src/actions/engine.dart';
import 'package:plux_flutter/src/actions/handlers.dart';
import 'package:plux_flutter/src/actions/run.dart';
import 'package:plux_flutter/src/assets/assets.dart';
import 'package:plux_flutter/src/assets/icon_fonts.dart';
import 'package:plux_flutter/src/assets/image_providers.dart';
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/core/active_release.dart';
import 'package:plux_flutter/src/core/config.dart';
import 'package:plux_flutter/src/core/fallback.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/navigation/page_navigator.dart';
import 'package:plux_flutter/src/platform/platform_services.dart';
import 'package:plux_flutter/src/pxl/types.dart';
import 'package:plux_flutter/src/pxl/vm.dart';
import 'package:plux_flutter/src/render/builders/builders.dart';
import 'package:plux_flutter/src/render/decoders.dart';
import 'package:plux_flutter/src/render/decoding.dart';
import 'package:plux_flutter/src/render/generated/render.g.dart';
import 'package:plux_flutter/src/render/node_context.dart';
import 'package:plux_flutter/src/render/page_renderer.dart';
import 'package:plux_flutter/src/render/plux_node.dart';
import 'package:plux_flutter/src/render/scope.dart';
import 'package:plux_flutter/src/render/sections.dart';
import 'package:plux_flutter/src/render/theme.dart';
import 'package:plux_flutter/src/render/tokens.dart';
import 'package:plux_flutter/src/render/values.dart';
import 'package:plux_flutter/src/schema/limits.g.dart';
import 'package:plux_flutter/src/state/providers.dart';
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
    VerifiedAssets? verified,
    int? cacheEntries,
    int? cacheBytes,
  }) : _verified = verified ?? VerifiedAssets(),
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
  }) => PluxPageView(
    key: ValueKey((release.sequence, page.route)),
    renderer: this,
    release: release,
    page: page,
    params: params,
    routed: routed,
  );

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
    final user = env?.user;
    try {
      return toPxl(
        ValueResolver(
          plugin: app,
          roots: () => {
            'flags': flags,
            'user': {
              if (user != null) ...user.attributes,
              if (user != null) 'id': user.id,
              'authenticated': env?.authDelegate?.isAuthenticated ?? false,
            },
          },
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
        maxBytes:
            limits[PluxLimit.runtimeImageDiskCacheBytes.key] ??
            PluxLimit.runtimeImageDiskCacheBytes.defaultValue,
      ),
      client: _client ??= (config.httpClient ?? platformHttpClient)(),
      maxBytes:
          limits[PluxLimit.runtimeImageSize.key] ??
          PluxLimit.runtimeImageSize.defaultValue,
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

  @override
  ConsumerState<PluxPageView> createState() => _PluxPageViewState();
}

final class _PluxPageViewState extends ConsumerState<PluxPageView> {
  late final BundleView _plugin = widget.renderer.view(
    widget.release,
    widget.page.plugin,
  );
  late final BundleView _app = widget.renderer.view(widget.release, '');
  late final NodeSection _section = widget.renderer.cache.get(
    widget.page.section,
    () => NodeSection.page(widget.page.section),
  );
  late final PxlLimits _limits = _pxlLimits(widget.release.limits);
  late final Map<String, Object?> _params;
  late final PageInstance _instance;
  late final Map<String, Object?> _flags;

  /// Why the page's parameters cannot be used (NAV-007), or null.
  PluxException? _paramError;

  /// The engine of the page's runs (ADR-0039).
  late final ActionHost? _actions = () {
    final services = widget.renderer.actions;
    if (services == null) return null;
    final result = _section.page?.result ?? 0;
    return ActionHost(
      context: StepContext(
        navigator: PageNavigator(
          router: services.router,
          context: () => context,
          route: widget.page.route,
          routed: widget.routed,
          resultType: result == 0 ? null : _section.string(result),
          types: () => _types,
        ),
        emit: services.emit,
        nativeActions: services.nativeActions,
      ),
      limits: ActionLimits.of(widget.release.limits),
      report: widget.renderer.report,
      record: services.record,
      route: widget.page.route,
      pluginKey: widget.page.plugin,
    );
  }();

  String get _path => '${widget.page.plugin}/${widget.page.pageKey}';

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
    final limits = widget.release.limits;
    widget.renderer.cache.resize(
      maxEntries:
          limits['runtime.sectionCacheEntries'] ??
          PluxLimit.runtimeSectionCacheEntries.defaultValue,
      maxBytes:
          limits['runtime.sectionCacheBytes'] ??
          PluxLimit.runtimeSectionCacheBytes.defaultValue,
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

    final page = _section.page!;
    _params = {};
    // Parameters are checked on entry (NAV-007): one missing, unknown or
    // of the wrong type shows the error fallback instead of the page.
    final problems = <String>[];
    final declared = <String>{};
    for (final p in page.params ?? const <fbs.Param>[]) {
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
      if (!declared.contains(name)) problems.add('$name is not a parameter');
    }
    if (problems.isNotEmpty) {
      _paramError = PluxException(
        PluxErrorCode.routeParametersInvalid,
        'route ${widget.page.route}: ${problems.join('; ')}',
        details: {'route': widget.page.route, 'plugin': widget.page.plugin},
      );
      widget.renderer.report(_paramError!);
    }
    _instance = PageInstance({
      for (final e in page.state ?? const <fbs.StateEntry>[])
        if (e.computed == 0)
          _section.string(e.name): read(literal, e.$default, _section.string),
    });
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

  @override
  void dispose() {
    _actions?.dispose();
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
    final width = media?.size.width ?? 0;
    final device = {
      'platform': defaultTargetPlatform == TargetPlatform.iOS
          ? 'ios'
          : 'android',
      'osVersion': '',
      'locale': locale.toLanguageTag(),
      'textScale': media?.textScaler.scale(1) ?? 1.0,
      'darkMode': dark,
      'sizeClass': width >= 840
          ? 'expanded'
          : width >= 600
          ? 'medium'
          : 'compact',
      // Attestation arrives in P6; until then no assurance is claimed.
      'assuranceLevel': 'AL0',
    };
    final user = env.user;
    final userRoot = <String, Object?>{
      if (user != null) ...user.attributes,
      if (user != null) 'id': user.id,
    };
    Map<String, Object?> roots() => {
      'params': _params,
      'page': ref.read(pageStateProvider(_instance)),
      'device': device,
      // Read on every evaluation: the host's answer, signed out without a
      // delegate (ADR-0040).
      'user': {
        ...userRoot,
        'authenticated': env.authDelegate?.isAuthenticated ?? false,
      },
      'flags': _flags,
    };
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

    final scope = RenderScope(
      release: widget.release,
      plugin: _plugin,
      app: _app,
      pluginKey: widget.page.plugin,
      section: _section,
      resolver: ValueResolver(
        plugin: _plugin,
        roots: roots,
        token: (path) => theme.role(path) ?? tokens.read(path, appResolver),
        translation: translation,
        limits: _limits,
      ),
      roots: roots,
      builders: nodeBuilders,
      cache: widget.renderer.cache,
      report: _report,
      services: widget.renderer,
      path: _path,
      state: _instance,
      actions: _actions,
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
  budget:
      app['pxl.operationBudget'] ?? PluxLimit.pxlOperationBudget.defaultValue,
  stringLength:
      app['pxl.stringLength'] ?? PluxLimit.pxlStringLength.defaultValue,
  collectionSize:
      app['pxl.collectionSize'] ?? PluxLimit.pxlCollectionSize.defaultValue,
  decimalDigits:
      app['pxl.decimalDigits'] ?? PluxLimit.pxlDecimalDigits.defaultValue,
);

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
