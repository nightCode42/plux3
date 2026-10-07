// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/plux_flutter.dart';
// The host run needs the runtime's test hook for the device credential
// store: the platform's key store has no platform side under flutter test.
// ignore: implementation_imports
import 'package:plux_flutter/src/core/runtime.dart' show RuntimeOverrides;
// ignore: implementation_imports
import 'package:plux_flutter/src/security/software_keys.dart'
    show DevelopmentAttestation, SoftwareDeviceKeys;
// ignore: implementation_imports
import 'package:plux_flutter/src/sync/sync_engine.dart'
    show MemoryCredentialStore;
import 'package:plux_starter/starter.dart';

import '../integration_test/starter_flows.dart';

/// The flows on the development machine, against the server the Go driver
/// starts (`make e2e-starter`); skipped when none is configured.
void main() {
  const appId = String.fromEnvironment('PLUX_APP_ID');
  final dirs = <Directory>[];
  tearDownAll(() {
    for (final d in dirs) {
      d.deleteSync(recursive: true);
    }
  });
  starterFlows(
    config: () => appId.isEmpty ? null : StarterConfig.fromEnvironment(),
    storage: () async {
      final d = await Directory.systemTemp.createTemp('plux_starter');
      dirs.add(d);
      return d.path;
    },
    initialize: (c) => Plux.initializeWith(
      c,
      RuntimeOverrides(
        credentials: MemoryCredentialStore.new,
        deviceKeys: SoftwareDeviceKeys.new,
        attestation: () => const DevelopmentAttestation('e2e'),
      ),
    ),
  );
}
