// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_go_router/plux_go_router.dart';

/// Starts Plux on a GoRouter that holds the host's screen, Plux pages and
/// the app document's `main` shell.
Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  final plux = PluxGoRoutes();
  final router = GoRouter(
    initialLocation: '/main/home',
    routes: [
      GoRoute(
        name: 'profile',
        path: '/profile/:userId',
        builder: (context, state) =>
            Text('Profile ${state.pathParameters['userId']}'),
      ),
      ...plux.routes,
      plux.shell('main', tabs: ['home', 'second']),
    ],
  );
  await Plux.initialize(
    PluxConfig(
      appId: const String.fromEnvironment('PLUX_APP_ID'),
      endpoint: Uri.parse('http://localhost:8080'),
      router: PluxGoRouter(router),
    ),
  );
  runApp(PluxScope(child: MaterialApp.router(routerConfig: router)));
}
