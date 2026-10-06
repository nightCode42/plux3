// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter/material.dart';
import 'package:plux_bank/src/config.dart';
import 'package:plux_bank/src/host.dart';
import 'package:plux_flutter/plux_flutter.dart';

/// The app: a native shell around the published login page, which signs in,
/// hands the token to [BankHost] and navigates on to the dashboard.
final class BankApp extends StatelessWidget {
  /// Creates the app for [config] and [host], after `Plux.initialize`
  /// returned [startup].
  const BankApp({
    super.key,
    required this.config,
    required this.startup,
    required this.host,
  });

  /// Where the app's content comes from.
  final BankConfig config;

  /// What start-up found.
  final PluxStartup startup;

  /// The host's side of Plux, which the runtime's configuration also has.
  final BankHost host;

  @override
  Widget build(BuildContext context) => PluxScope(
    child: MaterialApp(
      title: 'Plux Bank',
      navigatorKey: host.navigatorKey,
      debugShowCheckedModeBanner: false,
      theme: ThemeData(colorSchemeSeed: const Color(0xFF0B3D91)),
      darkTheme: ThemeData(
        colorSchemeSeed: const Color(0xFF0B3D91),
        brightness: Brightness.dark,
      ),
      home: Scaffold(
        body: PluxView(
          config.route,
          sizing: PluxViewSizing.expand,
          loadingBuilder: (_) =>
              const Center(child: CircularProgressIndicator()),
          fallbackBuilder: (_, e) => Center(
            child: Padding(
              padding: const EdgeInsets.all(24),
              child: Text(
                'The bank is not available yet (${e.code.id}).',
                textAlign: TextAlign.center,
              ),
            ),
          ),
        ),
      ),
    ),
  );
}
