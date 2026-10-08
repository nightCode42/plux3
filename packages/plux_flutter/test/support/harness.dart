// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:io';
import 'dart:typed_data';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_flutter/src/core/runtime.dart';
import 'package:plux_flutter/src/render/renderer.dart';
import 'package:plux_flutter/src/state/providers.dart';
import 'package:plux_flutter/src/sync/sync_engine.dart';

import '../sync/fake_device.dart';
import '../sync/fake_server.dart';

http.Client _client() => http.Client();

/// A baseline reader whose closure holds only the files, so it can be sent
/// to the sync isolate.
Future<Uint8List?> Function(String) _reader(Map<String, Uint8List> files) =>
    (p) async => files[p];

/// A runtime started against a fake server, for widget tests.
final class Harness {
  Harness._(this.server, this.dir);

  /// Starts the fake server and a temporary store directory.
  static Future<Harness> create() async => Harness._(
    await FakePluxServer.start(),
    Directory.systemTemp.createTempSync('plux_h').path,
  );

  /// The fake server.
  final FakePluxServer server;

  /// The store directory.
  final String dir;

  /// Problems the runtime reported.
  final List<PluxException> errors = [];

  /// The secure storage of the state stores' keys, kept across restarts.
  final MemorySecretStore secrets = MemorySecretStore();

  /// The golden bundles.
  static final Goldens goldens = Goldens.load();

  /// Starts Plux from an embedded baseline of [app] and [plugins] at
  /// release 5, offline.
  Future<PluxStartup> startFrom(
    Uint8List app,
    Map<String, Uint8List> plugins, {
    PluxThemeSource themeSource = PluxThemeSource.host,
    PluxNavigationDelegate? navigationDelegate,
    Widget Function(BuildContext, String)? notFoundBuilder,
    PluxAuthDelegate? authDelegate,
    GlobalKey<NavigatorState>? navigatorKey,
    PluxConsent consent = PluxConsent.necessaryOnly,
    Map<String, PluxNativeRoute<Object?, Object?>> nativeRoutes = const {},
    Map<String, PluxNativeSlot> nativeSlots = const {},
    Map<String, PluxNativeAction<Object?, Object?>> nativeActions = const {},
    PluxRouterAdapter? router,
    PluxDatabaseAdapter? databaseAdapter,
  }) async {
    server.release = null;
    final baseline = await server.baseline(5, app, plugins);
    return Plux.initializeWith(
      PluxConfig(
        appId: FakePluxServer.app,
        endpoint: server.endpoint,
        rootKeys: [
          PluxPublicKey(
            keyId: server.keyId,
            algorithm: 'ed25519',
            role: 'targets',
            publicKey: server.publicKey,
          ),
        ],
        storageDirectory: dir,
        httpClient: _client,
        themeSource: themeSource,
        onError: (e, _) => errors.add(e),
        fallbackBuilder: (_, e) =>
            Text('fallback ${e.code.id}', textDirection: TextDirection.ltr),
        navigationDelegate: navigationDelegate,
        notFoundBuilder: notFoundBuilder,
        authDelegate: authDelegate,
        navigatorKey: navigatorKey,
        consent: consent,
        nativeRoutes: nativeRoutes,
        nativeSlots: nativeSlots,
        nativeActions: nativeActions,
        router: router,
        databaseAdapter: databaseAdapter,
      ),
      RuntimeOverrides(
        credentials: MemoryCredentialStore.new,
        deviceKeys: FakeDeviceKeys.new,
        attestation: FakeAttestation.new,
        baseline: _reader(baseline),
        healthyAfter: const Duration(hours: 1),
        secrets: () => secrets,
        configSecrets: MemorySecretStore.new,
      ),
    );
  }

  /// The running runtime.
  PluxRuntime get runtime => Plux.container.read(pluxRuntimeProvider)!;

  /// Loads the golden icon fonts the active release holds, as a page would
  /// on first use; a widget test calls it inside `runAsync`, since the
  /// fonts are read from the store.
  Future<void> loadIconFonts() async {
    final release = runtime.active.value!;
    final fonts = (runtime.renderer! as PluxRenderer).iconFonts;
    for (final f in Directory(
      '../../schema/testdata/bundles',
    ).listSync(recursive: true)) {
      if (f is! File || !f.path.contains('/icons/')) continue;
      final hash = f.uri.pathSegments.last.split('.').first;
      final path = release.assetPath(hash);
      if (path != null) await fonts.load(hash, path);
    }
  }

  /// Stops Plux and the server and removes the store.
  Future<void> close() async {
    await Plux.dispose();
    await server.close();
    Directory(dir).deleteSync(recursive: true);
  }
}

/// Lets asynchronous work outside the fake-async zone finish, then pumps.
Future<void> settle(WidgetTester tester, [int rounds = 3]) async {
  for (var i = 0; i < rounds; i++) {
    await tester.runAsync(
      () => Future<void>.delayed(const Duration(milliseconds: 10)),
    );
    await tester.pump();
  }
}

/// Secure storage in memory, for tests.
final class MemorySecretStore implements SecretStore {
  /// The secrets, by name.
  final Map<String, String> values = {};

  @override
  Future<String?> read(String name) async => values[name];

  @override
  Future<void> write(String name, String value) async => values[name] = value;

  @override
  Future<void> delete(String name) async => values.remove(name);
}
