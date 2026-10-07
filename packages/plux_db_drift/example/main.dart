// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter/widgets.dart';
import 'package:plux_db_drift/plux_db_drift.dart';
import 'package:plux_flutter/plux_flutter.dart';

/// Starts Plux with its collections in an encrypted SQLite database.
Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  await Plux.initialize(
    PluxConfig(
      appId: 'app_example',
      endpoint: Uri.parse('https://plux.example.com'),
      databaseAdapter: PluxDriftAdapter(),
    ),
  );
}
