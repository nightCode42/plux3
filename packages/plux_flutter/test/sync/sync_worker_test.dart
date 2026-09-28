// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:plux_flutter/src/sync/api_client.dart';
import 'package:plux_flutter/src/sync/sync_engine.dart';
import 'package:plux_flutter/src/sync/sync_event.dart';
import 'package:plux_flutter/src/sync/sync_worker.dart';
import 'package:plux_flutter/src/verify/bundle_verifier.dart';

import '../store/store_test_support.dart';
import 'fake_server.dart';

bool _all(String _) => true;

void main() {
  test('syncs on a background isolate that owns every store write [SYN-010] [SYN-002]', () async {
    final goldens = Goldens.load();
    final server = await FakePluxServer.start();
    final root = Directory.systemTemp.createTempSync('plux_worker').path;
    server.release = FakeRelease(
      10,
      goldens.bundles['loan-calculator/demo.pxb']!,
      {'loans': goldens.bundles['loan-calculator/loans.pxb']!},
    );
    final worker = await SyncWorker.start(
      SyncWorkerConfig(
        storeRoot: root,
        endpoint: server.endpoint,
        httpClient: http.Client.new,
        credentials: MemoryCredentialStore.new,
        sync: SyncConfig(
          appId: FakePluxServer.app,
          environment: FakePluxServer.environment,
          channel: FakePluxServer.channel,
          keys: [server.trustedKey],
          device: const DeviceInfo(
            platform: 'android',
            osVersion: '14',
            runtimeVersion: '0.1.0',
            hostBuild: '1',
          ),
          supportsFeature: _all,
          verifierLimits: const VerifierLimits(
            maxDepth: 64,
            maxVisits: 1000000,
          ),
        ),
        baseline: (_) async => null,
      ),
    );
    try {
      final events = <SyncEvent>[];
      final a = worker.sync(events.add);
      final b = worker.sync(events.add);
      expect(identical(a, b), isTrue, reason: 'a sync in progress is joined');
      final r = await a;
      expect(r.outcome, SyncOutcome.staged, reason: '${r.error}');
      expect(events.whereType<SyncStaged>().single.sequence, 10);
      expect(await worker.importBaseline(), isNull);
      expect((await worker.activate()).active, 10);
      expect(checkComplete(root), 10);
      expect((await worker.beginLaunch()).active, 10);
      await worker.markHealthy();
      await worker.recordFailure();
      await worker.collectGarbage();
      expect(
        (await worker.revert()).active,
        10,
        reason: 'nothing to go back to',
      );
      await expectLater(
        worker.activate(),
        throwsStateError,
        reason: 'nothing is staged',
      );
    } finally {
      worker.close();
      await server.close();
      Directory(root).deleteSync(recursive: true);
    }
  });

  test('the asset baseline reader returns null for a missing asset', () async {
    TestWidgetsFlutterBinding.ensureInitialized();
    expect(await assetBaseline('assets/plux')('baseline.json'), isNull);
    expect(Uint8List(0), isEmpty);
  });
}
