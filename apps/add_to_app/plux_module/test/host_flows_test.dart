// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/plux_flutter.dart';
// The host run needs the runtime's test hook for the device credential
// store: the platform's key store has no platform side under flutter test.
// ignore: implementation_imports
import 'package:plux_flutter/src/core/runtime.dart' show RuntimeOverrides;
// ignore: implementation_imports
import 'package:plux_flutter/src/security/software_keys.dart'
    show DevelopmentAttestation, SoftwareDeviceKeys;
// ignore: implementation_imports
import 'package:plux_flutter/src/sync/sync_engine.dart'
    show MemoryCredentialStore;
import 'package:plux_module/plux_module.dart';

const _appId = String.fromEnvironment('PLUX_APP_ID');
const _flow = String.fromEnvironment('PLUX_FLOW');

/// The module's own screen, with the runtime's status, when no page is
/// open.
final _home = find.textContaining('PluxStartup(ready');

/// Longer than any route transition, in the test's fake time.
const _transition = Duration(seconds: 1);

/// The add-to-app flows on the development machine, the native hosts'
/// UI tests in Dart (HST-033): TestAddToAppAgainstTheServer runs one flow
/// per process, as the hosts' tests do, against the server it starts —
/// `offline` with an endpoint nothing listens on, then `online`. Skipped
/// when no server is configured.
void main() {
  testWidgets(
    'a native screen opens a page, rendered offline from the baseline',
    (tester) async {
      final host = await _Host.start(tester);
      await host.open('welcome');
      await host.pumpUntil(find.text('Welcome to Plux'));
      await host.back();
      await host.pumpUntil(_home, pops: 1);
      await host.stop();
    },
    skip: _appId.isEmpty || _flow != 'offline',
  );

  testWidgets(
    'sync applies an update at a safe point, and a plugin page opens a '
    'native screen',
    (tester) async {
      final host = await _Host.start(tester);
      // The sync the runtime started with, joined: a failed sync shows its
      // error, not a timeout.
      final synced = await tester.runAsync(Plux.sync);
      expect(
        synced?.outcome,
        isNot(SyncOutcome.failed),
        reason: 'the sync failed: ${synced?.error}',
      );
      // The newer release activates once no page is open: close and
      // reopen until it shows.
      final end = DateTime.now().add(const Duration(seconds: 60));
      for (;;) {
        await host.open('welcome');
        await host.pumpUntil(find.textContaining('Welcome to Plux'));
        if (find.text('Welcome to Plux, again').evaluate().isNotEmpty) break;
        if (DateTime.now().isAfter(end)) fail('the update never applied');
        await host.back();
        await host.pumpUntil(_home);
        await tester.runAsync(
          () => Future<void>.delayed(const Duration(milliseconds: 500)),
        );
      }
      await host.back();
      await host.pumpUntil(_home);

      await host.open('host-link');
      await host.pumpUntil(find.text('Open host settings'));
      await tester.tap(find.text('Open host settings'));
      await host.pumpUntil(find.text('Open host settings'), screens: 1);
      expect(host.screens, ['settings']);
      await host.stop();
    },
    skip: _appId.isEmpty || _flow != 'online',
    timeout: const Timeout(Duration(minutes: 3)),
  );
}

/// The native host's side: the channel's handler with the server's
/// settings, the native screens asked for, and the screen closes.
final class _Host {
  _Host._(this.tester, this.module, this.dir);

  static Future<_Host> start(WidgetTester tester) async {
    final dir = Directory.systemTemp.createTempSync('plux_module');
    final module = PluxModule(
      initialize: (c) => Plux.initializeWith(
        c,
        RuntimeOverrides(
          credentials: MemoryCredentialStore.new,
          deviceKeys: SoftwareDeviceKeys.new,
          attestation: () => const DevelopmentAttestation('e2e'),
        ),
      ),
      storageDirectory: dir.path,
    );
    final host = _Host._(tester, module, dir);
    tester.binding.defaultBinaryMessenger
      ..setMockMethodCallHandler(hostChannel, (call) async {
        switch (call.method) {
          case 'config':
            return {
              'endpoint': const String.fromEnvironment('PLUX_ENDPOINT'),
              'appId': _appId,
              'environment': const String.fromEnvironment('PLUX_ENVIRONMENT'),
              'hostBuild': 'add-to-app',
              'rootKeys': const String.fromEnvironment('PLUX_ROOT_KEYS')
                  .split(','),
            };
          case 'openNative':
            host.screens.add(
              (call.arguments as Map<Object?, Object?>)['screen']!,
            );
            return null;
        }
        return null;
      })
      ..setMockMethodCallHandler(SystemChannels.platform, (call) async {
        if (call.method == 'SystemNavigator.pop') host.pops++;
        return null;
      });
    await tester.runAsync(module.start);
    await tester.pumpWidget(ModuleApp(module: module));
    expect(_home, findsOneWidget);
    return host;
  }

  final WidgetTester tester;
  final PluxModule module;
  final Directory dir;
  final screens = <Object>[];
  var pops = 0;

  /// Asks the module to open [route], as a native screen does.
  Future<void> open(String route) =>
      tester.binding.defaultBinaryMessenger.handlePlatformMessage(
        hostChannel.name,
        hostChannel.codec.encodeMethodCall(
          MethodCall('open', {'route': route}),
        ),
        (_) {},
      );

  /// Closes the open page with its back button once the page's route
  /// transition has ended: a tap during the transition misses the button
  /// and leaves the page open, so no safe point comes (CI run 37623043856).
  Future<void> back() async {
    await tester.pump(_transition);
    await tester.pageBack();
    await tester.pump(_transition);
  }

  /// Pumps until [finder] finds something, the screen has closed [pops]
  /// times and the module has opened [screens] native screens, with real
  /// time passing for the runtime's I/O.
  Future<void> pumpUntil(Finder finder, {int? pops, int? screens}) async {
    final end = DateTime.now().add(const Duration(seconds: 60));
    while (finder.evaluate().isEmpty ||
        (pops != null && this.pops < pops) ||
        (screens != null && this.screens.length < screens)) {
      if (DateTime.now().isAfter(end)) {
        fail('waiting for $finder (pops $pops, screens $screens)');
      }
      await tester.runAsync(
        () => Future<void>.delayed(const Duration(milliseconds: 50)),
      );
      await tester.pump(const Duration(milliseconds: 50));
    }
  }

  /// Stops the runtime and removes its state.
  Future<void> stop() async {
    await tester.pumpWidget(const SizedBox());
    await tester.runAsync(Plux.dispose);
    tester.binding.defaultBinaryMessenger
      ..setMockMethodCallHandler(hostChannel, null)
      ..setMockMethodCallHandler(SystemChannels.platform, null);
    dir.deleteSync(recursive: true);
  }
}
