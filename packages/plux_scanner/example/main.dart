// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter/material.dart';
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_scanner/plux_scanner.dart';

/// Starts Plux with the scanner, on the app's root navigator.
Future<void> main() async {
  final navigatorKey = GlobalKey<NavigatorState>();
  await Plux.initialize(
    PluxConfig(
      appId: 'app_example',
      endpoint: Uri.parse('https://plux.example.com'),
      navigatorKey: navigatorKey,
      devicePackages: [PluxScanner(navigatorKey: navigatorKey)],
    ),
  );
  runApp(
    PluxScope(
      child: MaterialApp(navigatorKey: navigatorKey, home: const Scaffold()),
    ),
  );
}
