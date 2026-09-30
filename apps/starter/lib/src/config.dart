// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter/foundation.dart';
import 'package:plux_flutter/plux_flutter.dart';

/// Where the starter app finds its Plux server and app, from
/// `--dart-define`s (`make dev` writes them from the seeded stack):
///
/// | Define | Meaning | Default |
/// |---|---|---|
/// | `PLUX_ENDPOINT` | the server's base URL | `http://localhost:8080` |
/// | `PLUX_APP_ID` | the app's ID | none: required |
/// | `PLUX_ENVIRONMENT` | the environment key | `staging` |
/// | `PLUX_ROUTE` | the page the home screen shows | `welcome` |
/// | `PLUX_HOST_BUILD` | the host build telemetry reports | `dev` |
/// | `PLUX_ROOT_KEYS` | `keyId:hex` public keys, comma-separated; without them the baseline's `keys.json` | none |
@immutable
final class StarterConfig {
  /// Creates a configuration.
  const StarterConfig({
    required this.endpoint,
    required this.appId,
    this.environment = 'staging',
    this.route = 'welcome',
    this.hostBuild = 'dev',
    this.rootKeys = const [],
  });

  /// Reads the configuration the app was built with.
  factory StarterConfig.fromEnvironment() => StarterConfig.parse(const {
    'PLUX_ENDPOINT': String.fromEnvironment('PLUX_ENDPOINT'),
    'PLUX_APP_ID': String.fromEnvironment('PLUX_APP_ID'),
    'PLUX_ENVIRONMENT': String.fromEnvironment('PLUX_ENVIRONMENT'),
    'PLUX_ROUTE': String.fromEnvironment('PLUX_ROUTE'),
    'PLUX_HOST_BUILD': String.fromEnvironment('PLUX_HOST_BUILD'),
    'PLUX_ROOT_KEYS': String.fromEnvironment('PLUX_ROOT_KEYS'),
  });

  /// Reads a configuration from [defines]; an empty value takes the
  /// default. Throws [FormatException] for a missing app ID, an endpoint
  /// that is not an absolute HTTP URL or a malformed key.
  factory StarterConfig.parse(Map<String, String> defines) {
    String get(String k, String fallback) =>
        (defines[k] ?? '').isEmpty ? fallback : defines[k]!;
    final appId = get('PLUX_APP_ID', '');
    if (appId.isEmpty) {
      throw const FormatException(
        'PLUX_APP_ID is required: build with --dart-define=PLUX_APP_ID=<id>',
      );
    }
    final endpoint = Uri.tryParse(
      get('PLUX_ENDPOINT', 'http://localhost:8080'),
    );
    if (endpoint == null ||
        !endpoint.hasAuthority ||
        !(endpoint.isScheme('http') || endpoint.isScheme('https'))) {
      throw FormatException(
        'PLUX_ENDPOINT is not an HTTP URL',
        defines['PLUX_ENDPOINT'],
      );
    }
    return StarterConfig(
      endpoint: endpoint,
      appId: appId,
      environment: get('PLUX_ENVIRONMENT', 'staging'),
      route: get('PLUX_ROUTE', 'welcome'),
      hostBuild: get('PLUX_HOST_BUILD', 'dev'),
      rootKeys: [
        for (final k in get('PLUX_ROOT_KEYS', '').split(','))
          if (k.trim().isNotEmpty) _key(k.trim()),
      ],
    );
  }

  static PluxPublicKey _key(String s) {
    final (id, hex) = switch (s.split(':')) {
      [final id, final hex] when id.isNotEmpty && hex.length == 64 => (id, hex),
      _ => throw FormatException(
        'PLUX_ROOT_KEYS: expected keyId:<64 hex digits>',
        s,
      ),
    };
    final bytes = Uint8List(32);
    for (var i = 0; i < 32; i++) {
      final b = int.tryParse(hex.substring(2 * i, 2 * i + 2), radix: 16);
      if (b == null) throw FormatException('PLUX_ROOT_KEYS: not hex', s);
      bytes[i] = b;
    }
    return PluxPublicKey(
      keyId: id,
      algorithm: 'ed25519',
      role: 'targets',
      publicKey: bytes,
    );
  }

  /// The server's base URL.
  final Uri endpoint;

  /// The Plux app's ID.
  final String appId;

  /// The environment key.
  final String environment;

  /// The route of the page the home screen shows.
  final String route;

  /// The host build reported at registration and in telemetry.
  final String hostBuild;

  /// Embedded root keys; empty to read the baseline's `keys.json`.
  final List<PluxPublicKey> rootKeys;

  /// The runtime's configuration: this app, its environment and channel,
  /// the host's theme under Plux pages (HST-012), and the [baseline]
  /// directory of the app's assets, where `plux pull` writes it; null
  /// starts without one.
  PluxConfig toPluxConfig({
    String? storageDirectory,
    PluxErrorHandler? onError,
    String? baseline = 'assets/plux',
  }) => PluxConfig(
    appId: appId,
    endpoint: endpoint,
    environment: environment,
    rootKeys: rootKeys,
    hostBuild: hostBuild,
    storageDirectory: storageDirectory,
    onError: onError,
    baseline: baseline,
  );
}
