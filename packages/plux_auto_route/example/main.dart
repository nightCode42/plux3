// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:auto_route/auto_route.dart';
import 'package:flutter/material.dart';
import 'package:plux_auto_route/plux_auto_route.dart';
import 'package:plux_flutter/plux_flutter.dart';

/// Starts Plux on a RootStackRouter that holds the host's screen, Plux
/// pages and the app document's `main` shell.
Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  final plux = PluxAutoRoutes();
  final router = RootStackRouter.build(
    routes: [
      NamedRouteDef(
        name: 'profile',
        path: '/profile/:userId',
        builder: (context, data) =>
            Text('Profile ${data.params.getString('userId')}'),
      ),
      ...plux.routes,
      plux.shell('main', tabs: ['home', 'second']),
    ],
  );
  await Plux.initialize(
    PluxConfig(
      appId: const String.fromEnvironment('PLUX_APP_ID'),
      endpoint: Uri.parse('http://localhost:8080'),
      router: PluxAutoRoute(router),
    ),
  );
  runApp(
    PluxScope(
      child: MaterialApp.router(
        routerConfig: router.config(
          deepLinkBuilder: (_) => const DeepLink.path('/main'),
        ),
      ),
    ),
  );
}
