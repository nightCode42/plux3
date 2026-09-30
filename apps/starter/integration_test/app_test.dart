// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:integration_test/integration_test.dart';
import 'package:plux_starter/starter.dart';

import 'starter_flows.dart';

/// The flows on an emulator, simulator or device, against the server the
/// app was built for (`--dart-define`s, StarterConfig).
void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  starterFlows(config: StarterConfig.fromEnvironment);
}
