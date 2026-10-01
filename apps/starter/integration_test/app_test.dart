// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter/semantics.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:plux_starter/starter.dart';

import 'starter_flows.dart';

/// The flows on an emulator, simulator or device, against the server the
/// app was built for (`--dart-define`s, StarterConfig).
void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  _holdPlatformSemantics();
  starterFlows(config: StarterConfig.fromEnvironment);
}

/// Keeps the platform from turning semantics on or off while the flows
/// run. When it does, the framework takes or releases a semantics handle
/// for it, and the test framework, which checks that a test ends with no
/// more handles than it started with, fails the flow in which that
/// happens. Under XCTest (test/e2e/ios.sh) the flows start with the app,
/// and XCTest turns the app's accessibility on when it attaches, seconds
/// into the first flow (CI run 36823086351). The flows build their own
/// semantics through the tester, which `find.bySemanticsLabel` reads.
void _holdPlatformSemantics() {
  final dispatcher = SemanticsBinding.instance.platformDispatcher;
  VoidCallback? framework;
  setUpAll(() {
    framework = dispatcher.onSemanticsEnabledChanged;
    dispatcher.onSemanticsEnabledChanged = () {};
  });
  tearDownAll(() => dispatcher.onSemanticsEnabledChanged = framework);
}
