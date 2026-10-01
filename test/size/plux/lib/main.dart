// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter/material.dart';
import 'package:plux_flutter/plux_flutter.dart';

/// The size job's blank host with the Plux runtime (RT-061): starting
/// Plux and showing a page keeps every part of the runtime a host app
/// uses in the build — sync, verification, the renderer and every widget
/// builder.
Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  await Plux.initialize(
    PluxConfig(
      appId: '01f0c450-6c00-7000-8000-000000000001',
      endpoint: Uri.parse('https://plux.example'),
    ),
  );
  runApp(
    const MaterialApp(
      home: Scaffold(body: PluxScope(child: PluxView('home'))),
    ),
  );
}
