// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter/material.dart';
import 'package:plux_devtools/plux_devtools.dart';
import 'package:plux_flutter/plux_flutter.dart';

/// Starts Plux and shows a page under the debug overlay.
Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  await Plux.initialize(
    PluxConfig(
      appId: const String.fromEnvironment('PLUX_APP_ID'),
      endpoint: Uri.parse('http://localhost:8080'),
    ),
  );
  runApp(
    PluxScope(
      child: MaterialApp(
        builder: (context, child) => PluxDevtools(child: child!),
        home: const Scaffold(body: PluxView('welcome')),
      ),
    ),
  );
}
