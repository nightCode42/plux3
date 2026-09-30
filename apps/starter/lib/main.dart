// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter/widgets.dart';
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_starter/starter.dart';

/// Starts Plux with the server named by `--dart-define`s, then the app.
Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  final config = StarterConfig.fromEnvironment();
  final startup = await Plux.initialize(config.toPluxConfig());
  runApp(StarterApp(config: config, startup: startup));
}
