// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_express/express.dart';
import 'package:plux_flutter/plux_flutter.dart';
// The host run needs the runtime's test hook for the device credential
// store: the platform's key store has no platform side under flutter test.
// ignore: implementation_imports
import 'package:plux_flutter/src/core/runtime.dart' show RuntimeOverrides;
// ignore: implementation_imports
import 'package:plux_flutter/src/sync/sync_engine.dart'
    show MemoryCredentialStore;

import '../integration_test/express_flows.dart';

/// Secure storage in memory: the platform's has no platform side under
/// `flutter test`, and the outbox and the encrypted cache keep their keys in
/// it.
final class _MemorySecrets implements SecretStore {
  final _values = <String, String>{};

  @override
  Future<String?> read(String name) async => _values[name];

  @override
  Future<void> write(String name, String value) async => _values[name] = value;

  @override
  Future<void> delete(String name) async => _values.remove(name);
}

/// The flows on the development machine, against the server and the
/// reference backend the Go driver starts (`make e2e-starter`); skipped when
/// none is configured.
void main() {
  const appId = String.fromEnvironment('PLUX_APP_ID');
  final dirs = <Directory>[];
  tearDownAll(() {
    for (final d in dirs) {
      d.deleteSync(recursive: true);
    }
  });
  expressFlows(
    config: () => appId.isEmpty ? null : ExpressConfig.fromEnvironment(),
    storage: () async {
      final d = await Directory.systemTemp.createTemp('plux_express');
      dirs.add(d);
      return d.path;
    },
    initialize: (c) => Plux.initializeWith(
      c,
      RuntimeOverrides(
        credentials: MemoryCredentialStore.new,
        secrets: _MemorySecrets.new,
      ),
    ),
  );
}
