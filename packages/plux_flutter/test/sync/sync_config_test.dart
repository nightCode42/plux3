// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Verifies: SEC-182.

import 'dart:convert';
import 'dart:io';
import 'dart:math';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/security/security_config.dart';
import 'package:plux_flutter/src/store/directory_sync.dart';
import 'package:plux_flutter/src/store/kv_store.dart';
import 'package:plux_flutter/src/store/release_store.dart';
import 'package:plux_flutter/src/sync/api_client.dart';
import 'package:plux_flutter/src/sync/downloader.dart';
import 'package:plux_flutter/src/sync/sync_engine.dart';
import 'package:plux_flutter/src/sync/sync_event.dart';
import 'package:plux_flutter/src/sync/sync_worker.dart';
import 'package:plux_flutter/src/verify/bundle_verifier.dart';

import 'fake_device.dart';
import 'fake_server.dart';

final class _Secrets implements SecretStore {
  final Map<String, String> values = {};

  @override
  Future<String?> read(String name) async => values[name];

  @override
  Future<void> write(String name, String value) async => values[name] = value;

  @override
  Future<void> delete(String name) async => values.remove(name);
}

const _v1 = {
  'profile': 'strict',
  'overrides': {'inactivityLockTimeout': 120, 'allowDirectDataSources': false},
};
const _v2 = {
  'profile': 'strict',
  'overrides': {'inactivityLockTimeout': 90, 'screenshotBlockingDefault': true},
};
// RFC 7396 patch from _v1 to _v2.
const _v1ToV2 = {
  'overrides': {
    'inactivityLockTimeout': 90,
    'allowDirectDataSources': null,
    'screenshotBlockingDefault': true,
  },
};
const _wrong = {'profile': 'maximum'};

void main() {
  final goldens = Goldens.load();
  final demo = goldens.bundles['loan-calculator/demo.pxb']!;
  final loans = goldens.bundles['loan-calculator/loans.pxb']!;
  late FakePluxServer server;
  late String root;
  late http.Client client;
  late MemoryCredentialStore credentials;
  late FakeDeviceKeys keys;
  late _Secrets secrets;

  setUp(() async {
    server = await FakePluxServer.start();
    root = Directory.systemTemp.createTempSync('plux_config').path;
    client = http.Client();
    credentials = MemoryCredentialStore();
    keys = FakeDeviceKeys();
    secrets = _Secrets();
    server
      ..release = FakeRelease(10, demo, {'loans': loans})
      ..pinsConfig = true;
  });
  tearDown(() async {
    client.close();
    await server.close();
    Directory(root).deleteSync(recursive: true);
  });

  SyncConfig syncConfig() => SyncConfig(
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
    supportsFeature: (_) => true,
    verifierLimits: const VerifierLimits(maxDepth: 64, maxVisits: 1000000),
  );

  SyncEngine engine({bool keepConfig = true}) => SyncEngine(
    config: syncConfig(),
    store: ReleaseStore.open(root, syncDirectory: syncDirectory),
    api: PluxApiClient(client, server.endpoint),
    downloader: Downloader(client, sleep: (d) async {}, random: Random(1)),
    credentials: credentials,
    keys: keys,
    attestation: FakeAttestation(),
    configSecrets: keepConfig ? secrets : null,
    clock: () => DateTime.utc(2026, 9, 28, 12),
  );

  Future<SyncResult> sync({bool keepConfig = true}) =>
      engine(keepConfig: keepConfig).run((_) {});

  /// Stages the first release and activates it, with no configuration
  /// pinned yet.
  Future<void> firstRelease() async {
    final r = await sync();
    expect(r.outcome, SyncOutcome.staged, reason: '${r.error}');
    ReleaseStore.open(root).activate();
  }

  void serve(int version, Map<String, Object?> doc, Map<int, Object?> patches) {
    server
      ..configVersion = version
      ..configDocument = doc
      ..configPatches
      ..configPatches.addAll(patches);
  }

  /// The versions the requests carried, a repeat for the same sync (the
  /// installed list the server asks for) counted once.
  List<Object?> sent() => [
    for (final (i, v) in server.configVersionsSeen.indexed)
      if (i == 0 || server.configVersionsSeen[i - 1] != v) v,
  ];

  Future<StoredSecurityConfig?> stored() => SecurityConfigStore(
    secrets,
    FakePluxServer.app,
    FakePluxServer.environment,
  ).read();

  group('remote security configuration', () {
    test('the first request carries version 0 and a patch from 0 builds the configuration [SEC-182]', () async {
      serve(1, _v1, {0: _v1});
      final r = await sync();
      expect(r.configError, isNull, reason: '${r.configError}');
      expect(sent(), ['0']);
      final s = r.settings!;
      expect(s.version, 1);
      expect(s.profile, SecurityProfile.strict);
      expect(s.number(SecuritySetting.inactivityLockTimeout), 120);
      expect(s.flag(SecuritySetting.allowDirectDataSources), isFalse);
      // Strict defaults stay where nothing is overridden.
      expect(s.flag(SecuritySetting.encryptLocalStores), isTrue);
      final kept = await stored();
      expect(kept!.version, 1);
      expect(kept.document, _v1);
    });

    test(
      'an incremental patch from the held version updates it [SEC-182]',
      () async {
        serve(1, _v1, {0: _v1});
        await firstRelease();
        serve(2, _v2, {1: _v1ToV2, 0: _v2});
        final r = await sync();
        expect(r.configError, isNull, reason: '${r.configError}');
        expect(sent(), ['0', '1']);
        expect(r.settings!.version, 2);
        expect(r.settings!.number(SecuritySetting.inactivityLockTimeout), 90);
        expect(
          r.settings!.flag(SecuritySetting.screenshotBlockingDefault),
          isTrue,
        );
        // The deleted override is back at the strict default.
        expect(
          r.settings!.flag(SecuritySetting.allowDirectDataSources),
          isTrue,
        );
        expect((await stored())!.document, _v2);
      },
    );

    test(
      'the same version changes nothing and sends no patch [SEC-182]',
      () async {
        serve(1, _v1, {0: _v1});
        await firstRelease();
        final before = await secrets.read(
          'security-config.${FakePluxServer.app}.${FakePluxServer.environment}',
        );
        final r = await sync();
        expect(sent(), ['0', '1']);
        expect(r.settings!.version, 1);
        expect(r.configError, isNull);
        expect(
          secrets.values.values.single,
          before,
          reason: 'not written again',
        );
      },
    );

    test('a mismatch asks again from version 0 and applies the full configuration [SEC-182]', () async {
      serve(1, _v1, {0: _v1});
      await firstRelease();
      serve(2, _v2, {1: _wrong, 0: _v2});
      final r = await sync();
      expect(sent(), ['0', '1', '0']);
      expect(r.configError, isNull, reason: '${r.configError}');
      expect(r.settings!.version, 2);
      expect((await stored())!.document, _v2);
    });

    test(
      'a server that cannot patch asks for the whole configuration [SEC-182]',
      () async {
        serve(1, _v1, {0: _v1});
        await firstRelease();
        serve(2, _v2, {1: _v2});
        server.configFullRequiredFor.add(1);
        final r = await sync();
        expect(r.configError, isNull, reason: '${r.configError}');
        expect(r.settings!.version, 2);
        expect((await stored())!.document, _v2);
      },
    );

    test('a second mismatch keeps the last good configuration and reports it [SEC-182]', () async {
      serve(1, _v1, {0: _v1});
      await firstRelease();
      serve(2, _v2, {1: _wrong, 0: _wrong});
      final r = await sync();
      expect(sent(), ['0', '1', '0']);
      expect(r.configError?.code, PluxErrorCode.securityConfigHashMismatch);
      expect(r.settings!.version, 1);
      expect(r.settings!.number(SecuritySetting.inactivityLockTimeout), 120);
      expect((await stored())!.document, _v1);
      expect(r.outcome, isNot(SyncOutcome.failed));
    });

    test(
      'a wrong signed hash is never applied, even with a right patch [SEC-182]',
      () async {
        serve(1, _v1, {0: _v1});
        server.wrongConfigHashes.addAll(['00' * 32, '00' * 32]);
        final r = await sync();
        expect(r.configError?.code, PluxErrorCode.securityConfigHashMismatch);
        expect(r.settings, SecuritySettings.builtIn);
        expect(await stored(), isNull);
      },
    );

    test(
      'the stored configuration survives a restart and is read again [SEC-182]',
      () async {
        serve(1, _v1, {0: _v1});
        await firstRelease();
        final s = await engine().loadSettings();
        expect(s!.version, 1);
        expect(s.number(SecuritySetting.inactivityLockTimeout), 120);
        // The next sync sends the version it holds.
        await sync();
        expect(server.configVersionsSeen.last, '1');
      },
    );

    test('a stored configuration that no longer hashes is discarded at launch [SEC-182]', () async {
      serve(1, _v1, {0: _v1});
      await firstRelease();
      final name = secrets.values.keys.single;
      final j = jsonDecode(secrets.values[name]!) as Map<String, Object?>;
      ((j['document']! as Map)['overrides']! as Map)['inactivityLockTimeout'] =
          1;
      secrets.values[name] = jsonEncode(j);
      final s = await engine().loadSettings();
      expect(s, SecuritySettings.builtIn);
      expect(secrets.values, isEmpty);
      // The device is back on the defaults and asks for everything.
      final r = await sync();
      expect(server.configVersionsSeen.last, '0');
      expect(r.settings!.version, 1);
    });

    test('without configuration storage the defaults stay and nothing is applied [SEC-182]', () async {
      serve(1, _v1, {0: _v1});
      final r = await sync(keepConfig: false);
      expect(r.outcome, SyncOutcome.staged);
      expect(r.settings, isNull);
      expect(r.configError, isNull);
      expect(secrets.values, isEmpty);
      expect(await engine(keepConfig: false).loadSettings(), isNull);
    });

    test('a manifest that pins no configuration leaves the stored one alone [SEC-182]', () async {
      serve(1, _v1, {0: _v1});
      await firstRelease();
      server.pinsConfig = false;
      final r = await sync();
      expect(r.configError, isNull);
      expect(r.settings!.version, 1);
    });

    test(
      'the settings reach the caller across the sync isolate [SEC-182]',
      () async {
        serve(1, _v1, {0: _v1});
        final worker = await SyncWorker.start(
          SyncWorkerConfig(
            storeRoot: root,
            endpoint: server.endpoint,
            httpClient: http.Client.new,
            credentials: MemoryCredentialStore.new,
            configSecrets: _Secrets.new,
            deviceKeys: FakeDeviceKeys.new,
            attestation: FakeAttestation.new,
            sync: syncConfig(),
            baseline: (_) async => null,
          ),
        );
        try {
          expect(await worker.loadSettings(), SecuritySettings.builtIn);
          final r = await worker.sync((_) {});
          expect(r.configError, isNull, reason: '${r.configError}');
          expect(r.settings!.version, 1);
          expect(
            r.settings!.number(SecuritySetting.inactivityLockTimeout),
            120,
          );
          final again = await worker.loadSettings();
          expect(again, r.settings);
        } finally {
          worker.close();
        }
      },
    );
  });
}
