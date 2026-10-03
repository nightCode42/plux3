// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The Flutter module the add-to-app hosts embed (HST-033): native screens
/// open Plux pages in it by route, and its plugin pages open the hosts'
/// native screens.
library;

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:plux_flutter/plux_flutter.dart';

/// The channel between the module and its native host.
///
/// | Method | Called by | Arguments | Result |
/// |---|---|---|---|
/// | `config` | module | none | the runtime's settings: `endpoint`, `appId`, `environment`, `hostBuild` and `rootKeys` (`keyId:hex` strings) |
/// | `open` | host | `route` | none; the page opens, and when it pops the module asks the system to close its screen |
/// | `openNative` | module | `screen` | none, once the native screen has closed |
const hostChannel = MethodChannel('dev.plux/host');

/// The host's native screens behind the module's native routes, by route.
const nativeScreens = {'host-settings': 'settings'};

/// Opens a Plux page and completes when it pops.
typedef PageOpener = Future<Object?> Function(
  BuildContext context,
  String route,
);

Future<Object?> _openPage(BuildContext context, String route) =>
    Plux.open<Object?>(context, route);

/// The module: it asks the host for the runtime's settings, starts Plux,
/// and opens the pages the host asks for.
final class PluxModule {
  /// Creates the module on [channel]. [initialize] starts the runtime,
  /// [openPage] opens a page and [storageDirectory] holds the runtime's
  /// state (null for its default); tests pass their own.
  PluxModule({
    this.channel = hostChannel,
    this.initialize = Plux.initialize,
    this.openPage = _openPage,
    this.storageDirectory,
  }) {
    channel.setMethodCallHandler(_handle);
  }

  /// The channel to the host.
  final MethodChannel channel;

  /// Starts the runtime with a configuration.
  final Future<Object> Function(PluxConfig config) initialize;

  /// Opens a page.
  final PageOpener openPage;

  /// Where the runtime keeps its state; null for its default.
  final String? storageDirectory;

  /// The navigator Plux pages open on.
  final navigatorKey = GlobalKey<NavigatorState>();

  /// What the module's own screen shows: starting, started, or why the
  /// runtime could not start.
  final status = ValueNotifier<String>('Starting Plux');

  final _ready = Completer<void>();

  /// Asks the host for the settings and starts the runtime.
  Future<void> start() async {
    try {
      final settings = await channel.invokeMapMethod<String, Object?>('config');
      final startup = await initialize(config(settings ?? const {}));
      status.value = '$startup';
      _ready.complete();
    } on Object catch (e) {
      status.value = 'Plux did not start: $e';
      _ready.complete(Future.error(e));
      // Observed here, so a module whose runtime never starts does not
      // also report an unhandled error.
      unawaited(_ready.future.catchError((_) {}));
    }
  }

  /// The runtime's configuration from the host's [settings]. Throws
  /// [FormatException] when one is missing or malformed.
  PluxConfig config(Map<String, Object?> settings) {
    String text(String key, [String? fallback]) => switch (settings[key]) {
      final String v when v.isNotEmpty => v,
      _ when fallback != null => fallback,
      _ => throw FormatException('the host gave no $key'),
    };
    return PluxConfig(
      appId: text('appId'),
      endpoint: Uri.parse(text('endpoint')),
      environment: text('environment', 'production'),
      hostBuild: text('hostBuild', 'add-to-app'),
      rootKeys: [
        for (final k in (settings['rootKeys'] as List<Object?>?) ?? const [])
          parseKey(k! as String),
      ],
      storageDirectory: storageDirectory,
      navigatorKey: navigatorKey,
      // What the runtime reports goes to the device log, where the
      // hosts' UI tests collect it when a flow fails.
      onError: (error, _) => debugPrint('plux_module: $error'),
      nativeRoutes: {
        for (final MapEntry(key: route, value: screen) in nativeScreens.entries)
          route: PluxNativeRoute<Object?, Object?>.opened(
            open: (context, params) => openNative(screen),
          ),
      },
    );
  }

  /// Asks the host to show its native [screen]; completes once it closes.
  Future<Object?> openNative(String screen) =>
      channel.invokeMethod<Object?>('openNative', {'screen': screen});

  Future<Object?> _handle(MethodCall call) async {
    if (call.method != 'open') {
      throw MissingPluginException('the module has no method ${call.method}');
    }
    final route = switch (call.arguments) {
      {'route': final String route} => route,
      _ => throw PlatformException(
        code: 'arguments',
        message: 'open needs a route',
      ),
    };
    unawaited(_open(route));
    return null;
  }

  // Opens route on the module's navigator once the runtime is ready; when
  // the page pops, the host's screen closes: a FlutterActivity finishes, a
  // FlutterFragment's activity goes back, a FlutterViewController is
  // popped or dismissed.
  Future<void> _open(String route) async {
    try {
      await _ready.future;
    } on Object {
      return; // the status says why
    }
    var context = navigatorKey.currentContext;
    while (context == null) {
      await Future<void>.delayed(const Duration(milliseconds: 16));
      context = navigatorKey.currentContext;
    }
    if (!context.mounted) return;
    await openPage(context, route);
    await SystemNavigator.pop();
  }
}

/// Reads a `keyId:<64 hex digits>` Ed25519 public key.
PluxPublicKey parseKey(String s) {
  final (id, hex) = switch (s.split(':')) {
    [final id, final hex] when id.isNotEmpty && hex.length == 64 => (id, hex),
    _ => throw FormatException('expected keyId:<64 hex digits>', s),
  };
  final bytes = Uint8List(32);
  for (var i = 0; i < 32; i++) {
    final b = int.tryParse(hex.substring(2 * i, 2 * i + 2), radix: 16);
    if (b == null) throw FormatException('not hex', s);
    bytes[i] = b;
  }
  return PluxPublicKey(
    keyId: id,
    algorithm: 'ed25519',
    role: 'targets',
    publicKey: bytes,
  );
}

/// The module's widget tree: the navigator Plux pages open on, over a
/// screen that shows the runtime's status while no page is open. The
/// runtime's scope wraps it once the runtime has started.
final class ModuleApp extends StatelessWidget {
  /// Creates the app for [module].
  const ModuleApp({super.key, required this.module});

  /// The module.
  final PluxModule module;

  @override
  Widget build(BuildContext context) => ValueListenableBuilder(
    valueListenable: module.status,
    builder: (context, status, _) {
      final app = MaterialApp(
        title: 'Plux module',
        debugShowCheckedModeBanner: false,
        theme: ThemeData(colorSchemeSeed: const Color(0xFF5B3DF5)),
        navigatorKey: module.navigatorKey,
        home: Scaffold(body: Center(child: Text(status))),
      );
      return Plux.isInitialized ? PluxScope(child: app) : app;
    },
  );
}
