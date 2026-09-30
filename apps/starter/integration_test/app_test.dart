// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:async';
import 'dart:developer' as developer;
import 'dart:io';

import 'package:integration_test/integration_test.dart';
import 'package:plux_starter/starter.dart';

import 'starter_flows.dart';

/// The flows on an emulator, simulator or device, against the server the
/// app was built for (`--dart-define`s, StarterConfig).
void main() {
  _announceVmService();
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  starterFlows(config: StarterConfig.fromEnvironment);
}

/// flutter test finds the app's Dart VM service by reading the simulator's
/// log for the engine's one "The Dart VM service is listening on" line.
/// It starts reading as it launches the app, and when the simulator
/// delivers the line before the reader is attached, flutter test waits for
/// it until stopped, although the app runs (ADR-0035; the app logged the
/// line and flutter never saw it in CI run 36756301098). On iOS the app
/// repeats the line during its first 40 seconds, so a late reader finds it;
/// the address is the same, and a reader that has it ignores the rest.
void _announceVmService() {
  if (!Platform.isIOS) return;
  for (final s in const [2, 5, 10, 20, 40]) {
    Timer(Duration(seconds: s), () async {
      final uri = (await developer.Service.getInfo()).serverUri;
      if (uri == null) return;
      // The line flutter's log reader matches, as the engine writes it.
      // ignore: avoid_print
      print('The Dart VM service is listening on $uri');
    });
  }
}
