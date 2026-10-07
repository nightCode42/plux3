// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:convert';

import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/io_client.dart';
import 'package:plux_flutter/src/core/features.dart';
import 'package:plux_flutter/src/platform/platform_services.dart';
import 'package:plux_flutter/src/sync/api_client.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  const channel = MethodChannel('dev.plux/runtime');
  final secrets = <String, String>{};
  String? directory = '/data/plux';

  setUp(() {
    secrets.clear();
    directory = '/data/plux';
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(channel, (call) async {
          final args = (call.arguments as Map<Object?, Object?>?) ?? const {};
          final name = args['name'] as String?;
          return switch (call.method) {
            'storageDirectory' => directory,
            'secretRead' => secrets[name],
            'secretWrite' => secrets[name!] = args['value']! as String,
            'secretDelete' => secrets.remove(name),
            _ => throw MissingPluginException(call.method),
          };
        });
  });
  tearDown(
    () => TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(channel, null),
  );

  test(
    'the device credential is stored and read through the platform [SEC-092]',
    () async {
      const store = PlatformCredentialStore('device.app.production');
      expect(await store.read(), isNull);
      await store.write(const DeviceCredential('d1', 'jkt-x'));
      expect(jsonDecode(secrets['device.app.production']!), {
        'deviceId': 'd1',
        'jkt': 'jkt-x',
      });
      final back = await store.read();
      expect((back!.deviceId, back.jkt), ('d1', 'jkt-x'));
      secrets['device.app.production'] = jsonEncode({
        'deviceId': 'd1',
        'secret': 'plux_dsec_x',
      });
      expect(
        await store.read(),
        isNull,
        reason: 'a credential with a secret and no key is from before DPoP',
      );
      secrets['device.app.production'] = 'not json';
      expect(
        await store.read(),
        isNull,
        reason: 'a damaged secret reads as none',
      );
      await store.clear();
      expect(secrets, isEmpty);
    },
  );

  test('the storage directory comes from the platform', () async {
    expect(await platformStorageDirectory(), '/data/plux');
    directory = null;
    await expectLater(platformStorageDirectory(), throwsA(anything));
  });

  test('off Android and iOS the HTTP client is dart:io [SYN-010]', () {
    final client = platformHttpClient();
    expect(client, isA<IOClient>());
    client.close();
  });

  test('required features: PXL and registry revisions [BND-008]', () {
    final f = RuntimeFeatures();
    expect(f.supports('pxl.v1'), isTrue);
    expect(f.supports('pxl.v2'), isFalse);
    expect(f.supports('widget.Text.v1'), isTrue);
    expect(f.supports('widget.Text.v99'), isFalse);
    expect(f.supports('widget.NoSuch.v1'), isFalse);
    expect(f.supports('type.EdgeInsets.v1'), isTrue);
    expect(f.supports('enum.Axis.v1'), isTrue);
    expect(f.supports('section.wasm.v1'), isFalse);
    expect(f.supports('widget.Text.v0'), isFalse);
    final some = RuntimeFeatures(canBuild: (t) => t != 'Text');
    expect(some.supports('widget.Text.v1'), isFalse);
    expect(some.supports('type.EdgeInsets.v1'), isTrue);
  });
}
