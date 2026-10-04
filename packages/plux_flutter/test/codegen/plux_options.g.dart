// Written by plux for the app "demo" in its "production" environment. Do not edit:
// run plux init again to update it, for example after the keys rotate.
//
// ignore_for_file: type=lint

import 'dart:typed_data';

import 'package:flutter/widgets.dart';
import 'package:plux_flutter/plux_flutter.dart';

/// Where this app's Plux content comes from, and the root keys its
/// releases are verified with, embedded at build time (SEC-051, HST-032).
abstract final class PluxOptions {
  /// The Plux app.
  static const appId = '01e0c450-6c00-7000-8000-000000000001';

  /// The Plux server.
  static const endpoint = 'https://plux.example.com';

  /// The environment the app syncs from.
  static const environment = 'production';

  /// The environment's channel.
  static const channel = 'production';

  /// The environment's root public keys.
  static final rootKeys = <PluxPublicKey>[
    PluxPublicKey(
      keyId: 'k1',
      algorithm: 'ed25519',
      role: 'targets',
      publicKey: Uint8List.fromList([0xab, 0x01]),
    ),
  ];

  /// The runtime's configuration with these values; [navigatorKey] is the
  /// app's root navigator, where deep links open their pages. Pass other
  /// options to PluxConfig yourself where you need them.
  static PluxConfig config({GlobalKey<NavigatorState>? navigatorKey}) =>
      PluxConfig(
        appId: appId,
        endpoint: Uri.parse(endpoint),
        environment: environment,
        channel: channel,
        rootKeys: rootKeys,
        navigatorKey: navigatorKey,
      );
}
