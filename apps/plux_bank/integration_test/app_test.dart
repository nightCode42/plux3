// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter/semantics.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:plux_bank/bank.dart';

import 'bank_flows.dart';

/// The flows on an emulator, simulator or device, against the server and the
/// reference backend the app was built for (`--dart-define`s, BankConfig).
void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  _holdPlatformSemantics();
  bankFlows(config: BankConfig.fromEnvironment);
}

/// Keeps the platform from turning semantics on or off while the flows
/// run. When it does, the framework takes or releases a semantics handle
/// for it, and the test framework, which checks that a test ends with no
/// more handles than it started with, fails the flow in which that
/// happens (the starter's flows hold it the same way, test/e2e/ios.sh).
void _holdPlatformSemantics() {
  final dispatcher = SemanticsBinding.instance.platformDispatcher;
  VoidCallback? framework;
  setUpAll(() {
    framework = dispatcher.onSemanticsEnabledChanged;
    dispatcher.onSemanticsEnabledChanged = () {};
  });
  tearDownAll(() => dispatcher.onSemanticsEnabledChanged = framework);
}
