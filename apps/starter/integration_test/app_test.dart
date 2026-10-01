// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:io';

import 'package:flutter/semantics.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:plux_starter/starter.dart';

import 'starter_flows.dart';

/// The flows on an emulator, simulator or device, against the server the
/// app was built for (`--dart-define`s, StarterConfig).
void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  setUpAll(_simulatorSemantics);
  starterFlows(config: StarterConfig.fromEnvironment);
}

/// On an iOS simulator the engine turns the platform's semantics on once
/// the app's view appears, which holds a semantics handle. Under XCTest
/// (test/e2e/ios.sh) the flows start with the app, so that handle would
/// appear during the first flow, and the test framework, which checks that
/// a test leaves no handle behind, would fail it. The flows wait for it.
Future<void> _simulatorSemantics() async {
  if (!Platform.isIOS || !Platform.environment.containsKey('SIMULATOR_UDID')) {
    return;
  }
  final dispatcher = SemanticsBinding.instance.platformDispatcher;
  final end = DateTime.now().add(const Duration(seconds: 20));
  while (!dispatcher.semanticsEnabled && DateTime.now().isBefore(end)) {
    await Future<void>.delayed(const Duration(milliseconds: 100));
  }
}
