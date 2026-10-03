// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_module/plux_module.dart';

const _key =
    'k1:00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff';

const _settings = <String, Object?>{
  'endpoint': 'http://127.0.0.1:18094',
  'appId': 'app-1',
  'environment': 'staging',
  'rootKeys': [_key],
};

/// The host's side of the channel: it answers `config` with [settings]
/// and records the native screens the module asks for.
final class _Host {
  _Host(WidgetTester tester, {this.settings = _settings}) {
    final messenger = tester.binding.defaultBinaryMessenger
      ..setMockMethodCallHandler(hostChannel, (call) async {
        switch (call.method) {
          case 'config':
            return settings;
          case 'openNative':
            screens.add((call.arguments as Map<Object?, Object?>)['screen']!);
            return null;
        }
        return null;
      })
      ..setMockMethodCallHandler(SystemChannels.platform, (call) async {
        if (call.method == 'SystemNavigator.pop') pops++;
        return null;
      });
    addTearDown(() {
      messenger
        ..setMockMethodCallHandler(hostChannel, null)
        ..setMockMethodCallHandler(SystemChannels.platform, null);
    });
    _messenger = messenger;
  }

  final Map<String, Object?> settings;
  final screens = <Object>[];
  var pops = 0;
  late final TestDefaultBinaryMessenger _messenger;

  /// Calls the module's [method] as the host does; completes with its
  /// result or throws its error, or [MissingPluginException] when the
  /// module has no such method.
  Future<Object?> call(String method, [Object? arguments]) async {
    final reply = Completer<Object?>();
    await _messenger.handlePlatformMessage(
      hostChannel.name,
      hostChannel.codec.encodeMethodCall(MethodCall(method, arguments)),
      (data) {
        try {
          if (data == null) throw MissingPluginException(method);
          reply.complete(hostChannel.codec.decodeEnvelope(data));
        } on Object catch (e) {
          reply.completeError(e);
        }
      },
    );
    return reply.future;
  }
}

/// Opens a stand-in page for a route, which pops with 'done'.
Future<Object?> _fakePage(BuildContext context, String route) =>
    Navigator.of(context).push<Object?>(
      MaterialPageRoute(
        builder: (context) => Scaffold(
          body: TextButton(
            onPressed: () => Navigator.of(context).pop('done'),
            child: Text('page $route'),
          ),
        ),
      ),
    );

void main() {
  group('parseKey', () {
    test('reads a key ID and 32 bytes', () {
      final key = parseKey(_key);
      expect(key.keyId, 'k1');
      expect(key.algorithm, 'ed25519');
      expect(key.publicKey, hasLength(32));
      expect(key.publicKey[1], 0x11);
    });

    test('rejects a malformed key', () {
      expect(() => parseKey('k1'), throwsFormatException);
      expect(() => parseKey('k1:00'), throwsFormatException);
      expect(() => parseKey(':${'0' * 64}'), throwsFormatException);
      expect(() => parseKey('k1:${'zz' * 32}'), throwsFormatException);
    });
  });

  group('config', () {
    test('takes the host settings, with defaults [HST-033]', () {
      final module = PluxModule(storageDirectory: '/tmp/x');
      final c = module.config(_settings);
      expect(c.appId, 'app-1');
      expect(c.endpoint, Uri.parse('http://127.0.0.1:18094'));
      expect(c.environment, 'staging');
      expect(c.hostBuild, 'add-to-app');
      expect(c.storageDirectory, '/tmp/x');
      expect(c.rootKeys.single.keyId, 'k1');
      expect(c.navigatorKey, module.navigatorKey);
      expect(c.nativeRoutes.keys, ['host-settings']);
      final bare = module.config(const {'endpoint': 'http://h', 'appId': 'a'});
      expect(bare.environment, 'production');
      expect(bare.rootKeys, isEmpty);
    });

    test('rejects settings without an app or endpoint', () {
      final module = PluxModule();
      expect(
        () => module.config(const {'endpoint': 'http://h'}),
        throwsFormatException,
      );
      expect(
        () => module.config(const {'appId': 'a', 'endpoint': ''}),
        throwsFormatException,
      );
    });
  });

  testWidgets('opens the page the host asks for, then closes the host screen '
      '[HST-033]', (tester) async {
    final host = _Host(tester);
    PluxConfig? started;
    final module = PluxModule(
      initialize: (c) async {
        started = c;
        return 'started';
      },
      openPage: _fakePage,
    );
    await tester.pumpWidget(ModuleApp(module: module));
    await module.start();
    await tester.pump();
    expect(started?.appId, 'app-1');
    expect(find.text('started'), findsOneWidget);

    expect(await host.call('open', {'route': 'welcome'}), isNull);
    await tester.pumpAndSettle();
    expect(find.text('page welcome'), findsOneWidget);
    expect(host.pops, 0);

    await tester.tap(find.text('page welcome'));
    // The host's screen closes only once the page has left the tree: its
    // exit transition needs frames, which stop once the screen closes.
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 100));
    expect(find.text('page welcome'), findsOneWidget);
    expect(host.pops, 0);
    await tester.pumpAndSettle();
    expect(find.text('page welcome'), findsNothing);
    expect(find.text('started'), findsOneWidget);
    expect(host.pops, 1);
  });

  testWidgets('asks the host for its native screen', (tester) async {
    final host = _Host(tester);
    final module = PluxModule();
    await module.openNative('settings');
    expect(host.screens, ['settings']);
  });

  testWidgets('shows why the runtime did not start, and opens nothing', (
    tester,
  ) async {
    final host = _Host(tester, settings: const {'endpoint': 'http://h'});
    final module = PluxModule(
      initialize: (c) async => 'started',
      openPage: _fakePage,
    );
    await tester.pumpWidget(ModuleApp(module: module));
    await module.start();
    await tester.pump();
    expect(find.textContaining('Plux did not start'), findsOneWidget);
    await host.call('open', {'route': 'welcome'});
    await tester.pumpAndSettle();
    expect(find.text('page welcome'), findsNothing);
    expect(host.pops, 0);
  });

  testWidgets('refuses other methods and an open without a route', (
    tester,
  ) async {
    final host = _Host(tester);
    PluxModule();
    await expectLater(host.call('open'), throwsA(isA<PlatformException>()));
    await expectLater(
      host.call('close'),
      throwsA(isA<MissingPluginException>()),
    );
  });
}
