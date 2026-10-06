// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_bank/bank.dart';
import 'package:plux_flutter/plux_flutter.dart';
// The host run needs the runtime's test hook for the device credential
// store: the platform's key store has no platform side under flutter test.
// ignore: implementation_imports
import 'package:plux_flutter/src/core/runtime.dart' show RuntimeOverrides;
// ignore: implementation_imports
import 'package:plux_flutter/src/sync/sync_engine.dart'
    show MemoryCredentialStore;

const _key =
    'k1:00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff';

void main() {
  group('BankConfig.parse', () {
    test('takes the defaults for empty defines', () {
      final c = BankConfig.parse(const {
        'PLUX_APP_ID': 'a1',
        'PLUX_ENDPOINT': '',
      });
      expect(c.appId, 'a1');
      expect(c.endpoint, Uri.parse('http://localhost:8080'));
      expect(c.environment, 'staging');
      expect(c.route, 'login');
      expect(c.hostBuild, 'dev');
      expect(c.rootKeys, isEmpty);
      expect(c.trustReferenceCa, isFalse);
    });

    test('reads every define and the root keys', () {
      final c = BankConfig.parse(const {
        'PLUX_APP_ID': 'a1',
        'PLUX_ENDPOINT': 'https://plux.example',
        'PLUX_ENVIRONMENT': 'production',
        'PLUX_ROUTE': 'dashboard',
        'PLUX_HOST_BUILD': '7',
        'PLUX_ROOT_KEYS': '$_key, ',
        'PLUX_REFAPI_CA': 'Zm9v',
      });
      expect(c.endpoint.host, 'plux.example');
      expect(
        (c.environment, c.route, c.hostBuild),
        ('production', 'dashboard', '7'),
      );
      expect(c.trustReferenceCa, isTrue);
      final k = c.rootKeys.single;
      expect((k.keyId, k.algorithm, k.role), ('k1', 'ed25519', 'targets'));
      expect(k.publicKey.first, 0x00);
      expect(k.publicKey[1], 0x11);
      expect(k.publicKey.last, 0xff);
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
        expect(() => BankConfig.parse(defines), throwsFormatException);
      });
    }

    test('fromEnvironment needs the app ID define', () {
      expect(BankConfig.fromEnvironment, throwsFormatException);
    });
  });

  group('BankConfig.toPluxConfig', () {
    const defines = {'PLUX_APP_ID': 'a1', 'PLUX_ENVIRONMENT': 'production'};

    test('wires the host and keeps the platform clients in production', () {
      final host = BankHost();
      final c = BankConfig.parse(defines);
      final p = c.toPluxConfig(host: host, storageDirectory: '/s');
      expect(p.appId, 'a1');
      expect(p.environment, 'production');
      expect(p.storageDirectory, '/s');
      expect(p.baseline, 'assets/plux');
      expect(p.navigatorKey, host.navigatorKey);
      expect(p.authDelegate, host);
      expect(p.nativeActions.keys, ['bankSignIn']);
      expect(p.devicePackages.map((d) => d.name), ['plux_media']);
      expect(p.httpClient, isNull);
      expect(p.webSocketClient, isNull);
      expect(c.toPluxConfig(host: host, baseline: null).baseline, isNull);
    });

    test('an end-to-end build trusts the reference CA for HTTP and '
        'WebSockets', () {
      final p = BankConfig.parse({...defines, 'PLUX_REFAPI_CA': 'Zm9v'})
          .toPluxConfig(host: BankHost());
      expect(p.httpClient, same(referenceHttpClient));
      expect(p.webSocketClient, same(referenceWebSocketClient));
    });
  });

  group('BankHost', () {
    test('holds the token in memory and hands it to the runtime', () async {
      final host = BankHost();
      expect(host.isAuthenticated, isFalse);
      expect(await host.accessToken(), isNull);

      expect(host.signIn('t0ken'), isTrue);
      expect(host.isAuthenticated, isTrue);
      expect(await host.accessToken(), 't0ken');
    });

    test('refuses a blank token and keeps the session it had', () async {
      final host = BankHost()..signIn('t0ken');
      expect(host.signIn(''), isFalse);
      expect(await host.accessToken(), 't0ken');
    });

    test('a refused token is not refreshed: the user signs in again', () async {
      final host = BankHost()..signIn('t0ken');
      expect(await host.refresh(), isNull);
      expect(host.isAuthenticated, isFalse);
    });

    test('logout drops the token', () async {
      final host = BankHost()..signIn('t0ken');
      host.onLogout();
      expect(host.isAuthenticated, isFalse);
      expect(await host.accessToken(), isNull);
    });

    test(
      'the native action bankSignIn reads the token a page passes',
      () async {
        final host = BankHost();
        final action = host.nativeActions['bankSignIn']!;
        expect(await action.callWith({'token': 'from-the-page'}), isTrue);
        expect(await host.accessToken(), 'from-the-page');
        expect(await action.callWith({'token': ''}), isFalse);
        expect(await host.accessToken(), 'from-the-page');
      },
    );
  });

  group('the reference CA clients', () {
    test('refuse text that is no base64', () {
      expect(() => trustingHttpClient('not base64!'), throwsFormatException);
    });

    test('refuse bytes that are no certificate', () {
      expect(
        () => trustingHttpClient(base64Encode(utf8.encode('not a PEM'))),
        throwsA(isA<TlsException>()),
      );
    });

    test('are built from the compile-time define, which is empty here', () {
      expect(referenceCa, isEmpty);
      expect(referenceWebSocketClient, throwsA(isA<TlsException>()));
    });
  });

  testWidgets('without a server the home screen waits for a release '
      '[HST-001]', (tester) async {
    final dir = Directory.systemTemp.createTempSync('plux_bank_host');
    addTearDown(() => dir.deleteSync(recursive: true));
    final config = BankConfig.parse(const {
      'PLUX_APP_ID': '01d0c450-6c00-7000-8000-000000000200',
      // Nothing listens on port 9 (discard) here: the first sync fails.
      'PLUX_ENDPOINT': 'http://127.0.0.1:9',
    });
    final host = BankHost();
    final startup = await tester.runAsync(
      () => Plux.initializeWith(
        config.toPluxConfig(host: host, storageDirectory: dir.path),
        const RuntimeOverrides(credentials: MemoryCredentialStore.new),
      ),
    );
    addTearDown(() => tester.runAsync(Plux.dispose));
    expect(startup!.ready, isFalse);
    await tester.pumpWidget(
      BankApp(config: config, startup: startup, host: host),
    );
    await tester.pump();
    // No release yet: the login page waits for one.
    expect(find.byType(CircularProgressIndicator), findsOne);
    expect(find.byType(PluxView), findsOne);
    await tester.pumpWidget(const SizedBox());
  });
}
