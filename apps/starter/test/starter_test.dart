// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/plux_flutter.dart';
// The host run needs the runtime's test hooks: the platform's key store
// has no platform side under flutter test.
// ignore: implementation_imports
import 'package:plux_flutter/src/core/runtime.dart' show RuntimeOverrides;
// ignore: implementation_imports
import 'package:plux_flutter/src/state/providers.dart' show environmentProvider;
// ignore: implementation_imports
import 'package:plux_flutter/src/sync/sync_engine.dart'
    show MemoryCredentialStore;
import 'package:plux_starter/starter.dart';

const _key =
    'k1:00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff';

void main() {
  group('StarterConfig.parse', () {
    test('takes the defaults for empty defines', () {
      final c = StarterConfig.parse(const {
        'PLUX_APP_ID': 'a1',
        'PLUX_ENDPOINT': '',
      });
      expect(c.appId, 'a1');
      expect(c.endpoint, Uri.parse('http://localhost:8080'));
      expect(c.environment, 'staging');
      expect(c.route, 'welcome');
      expect(c.hostBuild, 'dev');
      expect(c.rootKeys, isEmpty);
    });

    test('reads every define and the root keys', () {
      final c = StarterConfig.parse(const {
        'PLUX_APP_ID': 'a1',
        'PLUX_ENDPOINT': 'https://plux.example',
        'PLUX_ENVIRONMENT': 'production',
        'PLUX_ROUTE': 'home',
        'PLUX_HOST_BUILD': '7',
        'PLUX_ROOT_KEYS': '$_key, ',
      });
      expect(c.endpoint.host, 'plux.example');
      expect(
        (c.environment, c.route, c.hostBuild),
        ('production', 'home', '7'),
      );
      final k = c.rootKeys.single;
      expect((k.keyId, k.algorithm, k.role), ('k1', 'ed25519', 'targets'));
      expect(k.publicKey.first, 0x00);
      expect(k.publicKey[1], 0x11);
      expect(k.publicKey.last, 0xff);
      final p = c.toPluxConfig(storageDirectory: '/s');
      expect(p.appId, 'a1');
      expect(p.environment, 'production');
      expect(p.hostBuild, '7');
      expect(p.storageDirectory, '/s');
      expect(p.rootKeys, c.rootKeys);
      expect(p.baseline, 'assets/plux');
      expect(c.toPluxConfig(baseline: null).baseline, isNull);
    });

    for (final (name, defines) in [
      ('no app ID', const <String, String>{}),
      (
        'an endpoint without a host',
        const {'PLUX_APP_ID': 'a', 'PLUX_ENDPOINT': 'localhost'},
      ),
      (
        'an endpoint that is not HTTP',
        const {'PLUX_APP_ID': 'a', 'PLUX_ENDPOINT': 'ftp://h'},
      ),
      (
        'a key without its ID',
        const {'PLUX_APP_ID': 'a', 'PLUX_ROOT_KEYS': 'abcd'},
      ),
      (
        'a key of the wrong length',
        const {'PLUX_APP_ID': 'a', 'PLUX_ROOT_KEYS': 'k:abcd'},
      ),
      (
        'a key that is not hex',
        {'PLUX_APP_ID': 'a', 'PLUX_ROOT_KEYS': 'k:${'zz' * 32}'},
      ),
    ]) {
      test('refuses $name', () {
        expect(() => StarterConfig.parse(defines), throwsFormatException);
      });
    }

    test('fromEnvironment needs the app ID define', () {
      expect(StarterConfig.fromEnvironment, throwsFormatException);
    });
  });

  testWidgets('without a server the home screen waits for a release, and '
      'its controls reach the runtime [HST-001]', (tester) async {
    final dir = Directory.systemTemp.createTempSync('plux_starter_host');
    addTearDown(() => dir.deleteSync(recursive: true));
    final config = StarterConfig.parse(const {
      'PLUX_APP_ID': '01e0c450-6c00-7000-8000-000000000001',
      // Nothing listens on port 9 (discard) here: the first sync fails.
      'PLUX_ENDPOINT': 'http://127.0.0.1:9',
    });
    final startup = await tester.runAsync(
      () => Plux.initializeWith(
        config.toPluxConfig(storageDirectory: dir.path),
        const RuntimeOverrides(credentials: MemoryCredentialStore.new),
      ),
    );
    addTearDown(() => tester.runAsync(Plux.dispose));
    expect(startup!.ready, isFalse);
    await tester.pumpWidget(StarterApp(config: config, startup: startup));
    await tester.pump();
    // No release yet: the page waits for one, and the sync tile says why
    // none came.
    expect(find.byType(CircularProgressIndicator), findsOne);
    expect(find.textContaining('PLX-3050'), findsOne);

    await tester.tap(find.text('Dark theme'));
    await tester.pump();
    // Past the theme's cross-fade.
    await tester.pump(const Duration(seconds: 1));
    expect(Plux.container.read(environmentProvider).themeMode, ThemeMode.dark);
    expect(
      Theme.of(tester.element(find.text('Dark theme'))).brightness,
      Brightness.dark,
    );
    await tester.tap(find.text('Share usage analytics'));
    await tester.pump();
    expect(Plux.container.read(environmentProvider).consent.analytics, isTrue);

    await tester.tap(find.byKey(const ValueKey('open-page')));
    await tester.pump();
    // Past the route's transition.
    await tester.pump(const Duration(seconds: 1));
    // The page full screen, over the home screen's.
    expect(find.byType(PluxView, skipOffstage: false), findsNWidgets(2));
    expect(find.text('Dark theme'), findsNothing);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('without a release the mixed screen waits: its views load, '
      'the counter reads nothing and a write is refused [HST-021] [NAV-004]', (
    tester,
  ) async {
    final dir = Directory.systemTemp.createTempSync('plux_starter_mixed');
    addTearDown(() => dir.deleteSync(recursive: true));
    final problems = <PluxException>[];
    final config = StarterConfig.parse(const {
      'PLUX_APP_ID': '01e0c450-6c00-7000-8000-000000000001',
      'PLUX_ENDPOINT': 'http://127.0.0.1:9',
    });
    final startup = await tester.runAsync(
      () => Plux.initializeWith(
        config.toPluxConfig(
          storageDirectory: dir.path,
          onError: (e, _) => problems.add(e),
        ),
        const RuntimeOverrides(credentials: MemoryCredentialStore.new),
      ),
    );
    addTearDown(() => tester.runAsync(Plux.dispose));
    await tester.pumpWidget(StarterApp(config: config, startup: startup!));
    await tester.pump();
    await tester.tap(find.byKey(const ValueKey('open-mixed')));
    await tester.pump();
    await tester.pump(const Duration(seconds: 1));
    expect(find.byType(PluxView), findsNWidgets(2));
    expect(find.text('Native view of the counter: null'), findsOne);
    expect(Plux.state<int>('counter').value, isNull);
    expect(
      problems.where((e) => e.code == PluxErrorCode.exposedStateTypeMismatch),
      isEmpty,
    );
    await tester.tap(find.byKey(const ValueKey('add-one')));
    await tester.pump();
    expect(
      problems
          .where((e) => e.code == PluxErrorCode.exposedStateTypeMismatch)
          .map((e) => e.details['state']),
      ['counter'],
    );
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('the map card reports the place picked, and the app registers '
      'it as the MapCard native slot [WGT-033]', (tester) async {
    final picked = <String>[];
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: MapCard(title: 'Nearby', onPlace: picked.add),
        ),
      ),
    );
    expect(find.text('Nearby'), findsOne);
    await tester.tap(find.text('Old town'));
    expect(picked, ['Old town']);
    final config = StarterConfig.parse(const {'PLUX_APP_ID': 'a1'});
    expect(config.toPluxConfig().nativeSlots.keys, ['MapCard']);
  });
}
