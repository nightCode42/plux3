// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter/widgets.dart';
import 'package:plux_bank/bank.dart';
import 'package:plux_flutter/plux_flutter.dart';

/// Starts Plux with the server named by `--dart-define`s, then the app.
Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  final config = BankConfig.fromEnvironment();
  final host = BankHost();
  final startup = await Plux.initialize(config.toPluxConfig(host: host));
  runApp(BankApp(config: config, startup: startup, host: host));
}
