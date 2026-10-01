// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// The device half of the sync benchmark (QA-007, NFR-006, NFR-007): the
// real runtime syncs through the simulated slow network of
// backend/internal/netsim while the Go half,
// backend/internal/server/sync_bench_integration_test.go, publishes new
// revisions of the benchmark project and records what crossed the
// network. It runs only when that test starts it (make bench-sync).

import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/plux_flutter.dart';
// The host run needs the runtime's test hook for the device credential:
// the platform's key store has no platform side under flutter test.
// ignore: implementation_imports
import 'package:plux_flutter/src/core/runtime.dart' show RuntimeOverrides;
// ignore: implementation_imports
import 'package:plux_flutter/src/sync/sync_engine.dart'
    show MemoryCredentialStore;

const _control = String.fromEnvironment('PLUX_BENCH_SYNC_CONTROL');
const _endpoint = String.fromEnvironment('PLUX_ENDPOINT');
const _appId = String.fromEnvironment('PLUX_APP_ID');
const _rootKeys = String.fromEnvironment('PLUX_ROOT_KEYS');
const _checks = int.fromEnvironment('PLUX_BENCH_SYNC_CHECKS', defaultValue: 5);
const _updates = int.fromEnvironment(
  'PLUX_BENCH_SYNC_UPDATES',
  defaultValue: 10,
);

/// Asks the Go half to do [what]; it answers once done.
Future<void> _ask(String what, [Map<String, String> query = const {}]) async {
  final client = HttpClient();
  try {
    final uri = Uri.parse('$_control/$what').replace(queryParameters: query);
    final req = await client.postUrl(uri);
    final res = await req.close();
    final body = await utf8.decodeStream(res);
    if (res.statusCode != HttpStatus.ok) {
      throw StateError('$what: ${res.statusCode} $body');
    }
  } finally {
    client.close();
  }
}

List<PluxPublicKey> _keys() => [
  for (final k in _rootKeys.split(','))
    if (k.contains(':'))
      PluxPublicKey(
        keyId: k.split(':').first,
        algorithm: 'ed25519',
        role: 'targets',
        publicKey: Uint8List.fromList([
          for (var i = 0; i < 32; i++)
            int.parse(k.split(':').last.substring(2 * i, 2 * i + 2), radix: 16),
        ]),
      ),
];

void main() {
  testWidgets(
    'syncs on the slow network: first launch, up-to-date checks and '
    'updates of three plugins [NFR-006] [NFR-007]',
    (tester) async {
      // The test binding answers every HttpClient request with 400; the
      // control calls to the Go half need the real network. (The runtime
      // syncs on its own isolate, which the binding does not touch.)
      HttpOverrides.global = null;
      final dir = Directory.systemTemp.createTempSync('plux_bench_sync');
      addTearDown(() => dir.deleteSync(recursive: true));
      final config = PluxConfig(
        appId: _appId,
        endpoint: Uri.parse(_endpoint),
        environment: 'staging',
        rootKeys: _keys(),
        baseline: null,
        activation: ActivationPolicy.immediate,
        storageDirectory: dir.path,
        hostBuild: 'bench',
      );
      await tester.runAsync(() async {
        // A first launch with nothing on the device waits for the sync.
        await _ask('phase', {'name': 'first'});
        final startup = await Plux.initializeWith(
          config,
          const RuntimeOverrides(credentials: MemoryCredentialStore.new),
        );
        expect(startup.sequence, 1, reason: '$startup');

        for (var i = 1; i <= _checks; i++) {
          await _ask('phase', {'name': 'check-$i'});
          final r = await Plux.sync();
          expect(r.outcome, SyncOutcome.upToDate, reason: '$r');
        }

        for (var i = 1; i <= _updates; i++) {
          // Revision i + 1 changes three plugins; the Go half publishes
          // and promotes it before answering.
          await _ask('publish', {'revision': '${i + 1}'});
          await _ask('phase', {'name': 'update-$i'});
          final watch = Stopwatch()..start();
          final r = await Plux.sync();
          watch.stop();
          expect(r.sequence, i + 1, reason: '$r');
          await _ask('result', {
            'name': 'update',
            'ms': '${watch.elapsedMicroseconds / 1000}',
          });
        }
        await _ask('phase', {'name': 'done'});
        await Plux.dispose();
      });
    },
    skip: _control.isEmpty,
    timeout: const Timeout(Duration(minutes: 20)),
  );
}
