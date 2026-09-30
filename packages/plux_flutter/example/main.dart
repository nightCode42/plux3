// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter/material.dart';
import 'package:plux_flutter/plux_flutter.dart';

/// Starts Plux, then shows a native screen with a published page inside it
/// and a button that opens the page full screen.
Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  final startup = await Plux.initialize(
    PluxConfig(
      appId: const String.fromEnvironment('PLUX_APP_ID'),
      endpoint: Uri.parse(
        const String.fromEnvironment(
          'PLUX_ENDPOINT',
          defaultValue: 'http://localhost:8080',
        ),
      ),
    ),
  );
  runApp(ExampleApp(ready: startup.ready));
}

/// The example host app.
class ExampleApp extends StatelessWidget {
  /// Creates the app; [ready] says whether a release is on the device.
  const ExampleApp({super.key, required this.ready});

  /// Whether Plux pages can render yet.
  final bool ready;

  @override
  Widget build(BuildContext context) => PluxScope(
    child: MaterialApp(
      home: Scaffold(
        appBar: AppBar(title: const Text('Plux example')),
        body: Column(
          children: [
            const PluxSyncTile(),
            Builder(
              builder: (context) => ListTile(
                title: const Text('Open the welcome page'),
                onTap: () => Plux.open<void>(context, 'welcome'),
              ),
            ),
            const Expanded(
              child: PluxView(
                'welcome',
                loadingBuilder: _loading,
                fallbackBuilder: _fallback,
              ),
            ),
          ],
        ),
      ),
    ),
  );
}

Widget _loading(BuildContext context) =>
    const Center(child: CircularProgressIndicator());

Widget _fallback(BuildContext context, PluxException error) =>
    Center(child: Text('Not available yet (${error.code.id})'));
