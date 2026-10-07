// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The runtime's platform channel (ADR-0029): the release store's
/// directory, and secrets encrypted under a platform-held key — Android
/// Keystore, iOS Keychain — so that the device secret is never written in
/// the clear. Bundle data never crosses it.
library;

import 'dart:convert';
import 'dart:io';

import 'package:cronet_http/cronet_http.dart';
import 'package:cupertino_http/cupertino_http.dart';
import 'package:flutter/services.dart';
import 'package:http/http.dart' as http;
import 'package:http/io_client.dart';
import 'package:plux_flutter/src/runtime_info.dart';
import 'package:plux_flutter/src/store/kv_store.dart';
import 'package:plux_flutter/src/sync/api_client.dart';
import 'package:plux_flutter/src/sync/sync_engine.dart';

const _channel = MethodChannel('dev.plux/runtime');

/// The non-backed-up directory the platform gives the runtime.
Future<String> platformStorageDirectory() async {
  final dir = await _channel.invokeMethod<String>('storageDirectory');
  if (dir == null) {
    throw const FileSystemException('the platform gave no storage directory');
  }
  return dir;
}

/// The device credential, kept by the platform encrypted (ADR-0029); one
/// per app and environment.
final class PlatformCredentialStore implements CredentialStore {
  /// Creates the store for the credential [name].
  const PlatformCredentialStore(this.name);

  /// The secret's name, lower case letters, digits, `.`, `_` and `-`.
  final String name;

  @override
  Future<DeviceCredential?> read() async {
    final v = await _channel.invokeMethod<String>('secretRead', {'name': name});
    if (v == null) return null;
    try {
      final j = jsonDecode(v) as Map<String, Object?>;
      // A credential from before DPoP holds a secret and no key
      // thumbprint: the device registers again.
      final jkt = j['jkt'];
      if (jkt is! String) return null;
      return DeviceCredential(j['deviceId']! as String, jkt);
    } on Object {
      return null;
    }
  }

  @override
  Future<void> write(DeviceCredential c) =>
      _channel.invokeMethod<void>('secretWrite', {
        'name': name,
        'value': jsonEncode({'deviceId': c.deviceId, 'jkt': c.jkt}),
      });

  @override
  Future<void> clear() =>
      _channel.invokeMethod<void>('secretDelete', {'name': name});
}

/// Secrets the platform keeps encrypted under its own key (ADR-0029):
/// the built-in store's key (plan p5 D6). Names are lower case letters,
/// digits, `.`, `_` and `-`.
final class PlatformSecretStore implements SecretStore {
  /// Creates the store.
  const PlatformSecretStore();

  @override
  Future<String?> read(String name) =>
      _channel.invokeMethod<String>('secretRead', {'name': name});

  @override
  Future<void> write(String name, String value) => _channel.invokeMethod<void>(
    'secretWrite',
    {'name': name, 'value': value},
  );

  @override
  Future<void> delete(String name) =>
      _channel.invokeMethod<void>('secretDelete', {'name': name});
}

/// The platform's HTTP/2 client (SYN-010): Cronet on Android, `URLSession`
/// on iOS, `dart:io` elsewhere (tests and desktop development).
http.Client platformHttpClient() {
  final agent = PluxRuntimeInfo.userAgent;
  try {
    if (Platform.isAndroid) {
      return CronetClient.fromCronetEngine(
        CronetEngine.build(
          userAgent: agent,
          enableHttp2: true,
          cacheMode: CacheMode.disabled,
        ),
        closeEngine: true,
      );
    }
    if (Platform.isIOS) {
      final config = URLSessionConfiguration.ephemeralSessionConfiguration()
        ..httpAdditionalHeaders = {'User-Agent': agent};
      return CupertinoClient.fromSessionConfiguration(config);
    }
  } on Object {
    // Fall through to dart:io when the platform client is unavailable.
  }
  return IOClient(HttpClient()..userAgent = agent);
}
