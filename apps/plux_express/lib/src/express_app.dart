// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter/material.dart';
import 'package:plux_express/src/config.dart';
import 'package:plux_express/src/host.dart';
import 'package:plux_flutter/plux_flutter.dart';

/// The app: a native connectivity switch above the published catalogue.
final class ExpressApp extends StatelessWidget {
  /// Creates the app for [config] and [host], after `Plux.initialize`
  /// returned [startup].
  const ExpressApp({
    super.key,
    required this.config,
    required this.startup,
    required this.host,
  });

  /// Where the app's content comes from.
  final ExpressConfig config;

  /// What start-up found.
  final PluxStartup startup;

  /// The host's side of Plux, which the runtime's configuration also has.
  final ExpressHost host;

  @override
  Widget build(BuildContext context) => PluxScope(
    child: MaterialApp(
      title: 'Plux Express',
      navigatorKey: host.navigatorKey,
      debugShowCheckedModeBanner: false,
      theme: ThemeData(colorSchemeSeed: const Color(0xFFE4572E)),
      darkTheme: ThemeData(
        colorSchemeSeed: const Color(0xFFE4572E),
        brightness: Brightness.dark,
      ),
      home: Scaffold(
        body: SafeArea(
          child: Column(
            children: [
              ValueListenableBuilder(
                valueListenable: host.online,
                builder: (context, online, _) => SwitchListTile(
                  key: const ValueKey('online'),
                  title: const Text('Online'),
                  value: online,
                  onChanged: (v) => host.setOnline(online: v),
                ),
              ),
              Expanded(
                child: PluxView(
                  config.route,
                  sizing: PluxViewSizing.expand,
                  loadingBuilder: (_) =>
                      const Center(child: CircularProgressIndicator()),
                  fallbackBuilder: (_, e) => Center(
                    child: Padding(
                      padding: const EdgeInsets.all(24),
                      child: Text(
                        'The shop is not available yet (${e.code.id}).',
                        textAlign: TextAlign.center,
                      ),
                    ),
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    ),
  );
}
