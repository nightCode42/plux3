// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:io';
import 'dart:math';
import 'dart:typed_data';

import 'package:crypto/crypto.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:plux_flutter/src/bundle/container.dart';
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/schema/limits.g.dart';
import 'package:plux_flutter/src/store/directory_sync.dart';
import 'package:plux_flutter/src/store/release_store.dart';
import 'package:plux_flutter/src/sync/api_client.dart';
import 'package:plux_flutter/src/sync/downloader.dart';
import 'package:plux_flutter/src/sync/sync_engine.dart';
import 'package:plux_flutter/src/sync/sync_event.dart';
import 'package:plux_flutter/src/verify/bundle_verifier.dart';

import '../store/store_test_support.dart';
import 'fake_server.dart';

void main() {
  final goldens = Goldens.load();
  final demo = goldens.bundles['loan-calculator/demo.pxb']!;
  final loans = goldens.bundles['loan-calculator/loans.pxb']!;
  final features = goldens.bundles['features/features.pxb']!;
  final tasks = goldens.bundles['features/tasks.pxb']!;
  final appDelta = goldens
      .deltas['compiled-loan-calculator/demo.pxb-to-features/features.pxb']!;
  final loansDelta = goldens
      .deltas['compiled-loan-calculator/loans.pxb-to-features/tasks.pxb']!;

  final logo = File(
    '../../schema/testdata/documents/loan-calculator/assets/images/logo.png',
  ).readAsBytesSync();
  final logoHash = sha256.convert(logo).toString();
  final logoPath = '/v1/objects/assets/${logoHash.substring(0, 2)}/$logoHash';
  late FakePluxServer server;
  late String root;
  late http.Client client;
  late List<Duration> sleeps;
  late MemoryCredentialStore credentials;
  var now = DateTime.utc(2026, 9, 28, 12);

  setUp(() async {
    server = await FakePluxServer.start();
    root = Directory.systemTemp.createTempSync('plux_sync').path;
    client = http.Client();
    sleeps = [];
    credentials = MemoryCredentialStore();
    now = DateTime.utc(2026, 9, 28, 12);
  });
  tearDown(() async {
    client.close();
    await server.close();
    Directory(root).deleteSync(recursive: true);
  });

  SyncEngine engine({int quota = 200 << 20, bool Function(String)? supports}) =>
      SyncEngine(
        config: SyncConfig(
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
          supportsFeature: supports ?? (_) => true,
          verifierLimits: const VerifierLimits(
            maxDepth: 64,
            maxVisits: 1000000,
          ),
          diskQuota: quota,
        ),
        store: ReleaseStore.open(root, syncDirectory: syncDirectory),
        api: PluxApiClient(client, server.endpoint),
        downloader: Downloader(
          client,
          sleep: (d) async => sleeps.add(d),
          random: Random(1),
        ),
        credentials: credentials,
        clock: () => now,
      );

  Future<(SyncResult, List<SyncEvent>)> sync({
    int quota = 200 << 20,
    bool Function(String)? supports,
  }) async {
    final events = <SyncEvent>[];
    final r = await engine(quota: quota, supports: supports).run(events.add);
    return (r, events);
  }

  Future<void> firstRelease() async {
    server.release = FakeRelease(10, demo, {'loans': loans});
    final (r, _) = await sync();
    expect(r.outcome, SyncOutcome.staged, reason: '${r.error}');
    ReleaseStore.open(root).activate();
  }

  test('syncs every bundle of a first release, verifies and stages it [SYN-001] [SYN-013]', () async {
    server.release = FakeRelease(10, demo, {'loans': loans});
    final (r, events) = await sync();
    expect(r.outcome, SyncOutcome.staged, reason: '${r.error}');
    expect(r.sequence, 10);
    expect(r.pluginsUpdated, 1);
    expect(r.bytes, demo.length + loans.length + logo.length);
    expect(r.deltaRatio, 1);
    expect(events.first, isA<SyncChecking>());
    expect(events.whereType<SyncDownloading>(), isNotEmpty);
    expect((events.last as SyncStaged).sequence, 10);
    final store = ReleaseStore.open(root);
    expect(store.pointer.staged, 10);
    expect(store.pointer.highestAccepted, 10);
    expect(store.record(10)!.bundles.map((b) => b.key), ['', 'loans']);
    expect(store.record(10)!.assets, [logoHash]);
    expect(store.hasObject(ObjectKind.assets, logoHash), isTrue);
    store.activate();
    expect(checkComplete(root), 10);
    expect(server.registrations, 1);
    expect(await credentials.read(), isNotNull);
  });

  test('an asset file that does not match its signed hash fails the sync, '
      'then the next sync fetches it again [AST-001]', () async {
    server.release = FakeRelease(10, demo, {'loans': loans});
    server.faults[logoPath] = [const CorruptFault()];
    final (r, _) = await sync();
    expect(r.outcome, SyncOutcome.failed);
    expect(r.error?.code, PluxErrorCode.assetHashMismatch);
    final store = ReleaseStore.open(root);
    expect(store.pointer.staged, isNull);
    expect(store.hasObject(ObjectKind.assets, logoHash), isFalse);
    final (again, _) = await sync();
    expect(again.outcome, SyncOutcome.staged, reason: '${again.error}');
  });

  test(
    'asset files the store holds are not downloaded again, and each is '
    'fetched once whatever the number of bundles using it [AST-001]',
    () async {
      await firstRelease();
      server.release = FakeRelease(11, demo, {'loans': loans, 'tasks': tasks});
      final before = server.requests.length;
      final (r, _) = await sync();
      expect(r.outcome, SyncOutcome.staged, reason: '${r.error}');
      expect(
        server.requests.sublist(before).where((p) => p.contains('/assets/')),
        isEmpty,
      );
      expect(ReleaseStore.open(root).record(11)!.assets, contains(logoHash));
    },
  );

  test(
    'a release whose assets exceed the quota is refused [SYN-012]',
    () async {
      server.release = FakeRelease(10, demo, {'loans': loans});
      final (r, _) = await sync(quota: demo.length + loans.length + 1);
      expect(r.error?.code, PluxErrorCode.diskQuotaExceeded);
      expect(r.error?.message, contains('assets'));
    },
  );

  test(
    'an unchanged release costs one small request [NFR-006] [SYN-013]',
    () async {
      await firstRelease();
      final before = server.requests.length;
      final (r, events) = await sync();
      expect(r.outcome, SyncOutcome.upToDate);
      expect(events.last, isA<SyncUpToDate>());
      final calls = server.requests.sublist(before);
      expect(calls.where((p) => p.endsWith('GetManifest')).length, 1);
      expect(calls.where((p) => p.startsWith('/v1/objects')), isEmpty);
      // One manifest request whose bodies stay under 1 KiB: the device
      // sends the digest of its bundles, not the list.
      expect(
        server.manifestRequestSizes.last + server.manifestResponseSizes.last,
        lessThan(1024),
      );
    },
  );

  test('updates by deltas, rebuilding and verifying each bundle [SYN-011] [QA-002]', () async {
    await firstRelease();
    server
      ..addDelta(demo, features, appDelta)
      ..addDelta(loans, tasks, loansDelta)
      ..release = FakeRelease(11, features, {'loans': tasks});
    final (r, _) = await sync();
    expect(r.outcome, SyncOutcome.staged, reason: '${r.error}');
    expect(r.bytes, appDelta.length + loansDelta.length);
    expect(r.deltaRatio, lessThan(1));
    expect(
      server.requests.where((p) => p.startsWith('/v1/objects/bundles')),
      hasLength(2),
      reason: 'only the first sync downloaded bundles',
    );
    ReleaseStore.open(root).activate();
    expect(checkComplete(root), 11);
  });

  test(
    'a corrupted delta falls back to the full bundle once [SYN-011] [QA-009]',
    () async {
      await firstRelease();
      server
        ..addDelta(loans, tasks, loansDelta)
        ..release = FakeRelease(11, demo, {'loans': tasks});
      final deltaPath = server.serveObject('deltas', loansDelta);
      server.faults[deltaPath] = [const CorruptFault()];
      final (r, _) = await sync();
      expect(r.outcome, SyncOutcome.staged, reason: '${r.error}');
      expect(
        server.requests.where((p) => p.contains(Goldens.hashOf(tasks))),
        isNotEmpty,
      );
    },
  );

  test(
    'a truncated delta also falls back to the full bundle [QA-009]',
    () async {
      await firstRelease();
      server
        ..addDelta(loans, tasks, loansDelta.sublist(0, loansDelta.length - 10))
        ..release = FakeRelease(11, demo, {'loans': tasks});
      final (r, _) = await sync();
      expect(r.outcome, SyncOutcome.staged, reason: '${r.error}');
    },
  );

  test('a corrupt full bundle after a bad delta fails the sync and keeps the release [SYN-011] [QA-009]', () async {
    await firstRelease();
    server
      ..addDelta(loans, tasks, loansDelta)
      ..release = FakeRelease(11, demo, {'loans': tasks});
    final deltaPath = server.serveObject('deltas', loansDelta);
    final h = Goldens.hashOf(tasks);
    final fullPath = '/v1/objects/bundles/${h.substring(0, 2)}/$h';
    server.faults[deltaPath] = [const CorruptFault()];
    server.faults[fullPath] = [const CorruptFault()];
    final (r, events) = await sync();
    expect(r.outcome, SyncOutcome.failed);
    expect(events.last, isA<SyncFailed>());
    expect(
      r.error!.code,
      isIn([PluxErrorCode.bundleMalformed, PluxErrorCode.sectionHashMismatch]),
    );
    expect(checkComplete(root), 10);
    expect(ReleaseStore.open(root).pointer.staged, isNull);
  });

  test(
    'resumes a download after the network drops mid-body [SYN-010] [QA-009]',
    () async {
      server.release = FakeRelease(10, demo, {'loans': loans});
      final h = Goldens.hashOf(loans);
      server.faults['/v1/objects/bundles/${h.substring(0, 2)}/$h'] = [
        const DropFault(1000),
      ];
      final (r, _) = await sync();
      expect(r.outcome, SyncOutcome.staged, reason: '${r.error}');
      expect(server.ranges, ['bytes=1000-']);
      expect(sleeps, hasLength(1));
    },
  );

  test('retries server errors with backoff and honours Retry-After [SYN-010] [QA-009]', () async {
    server.release = FakeRelease(10, demo, {'loans': loans});
    final h = Goldens.hashOf(demo);
    server.faults['/v1/objects/bundles/${h.substring(0, 2)}/$h'] = [
      const StatusFault(503, retryAfter: '7'),
      const StatusFault(500),
    ];
    final (r, _) = await sync();
    expect(r.outcome, SyncOutcome.staged, reason: '${r.error}');
    expect(sleeps.first, const Duration(seconds: 7));
    expect(
      sleeps[1],
      lessThanOrEqualTo(const Duration(seconds: 1)),
      reason: 'full jitter over 2 × 500 ms',
    );
  });

  test('fails after the retries when the server keeps failing, keeping the release [QA-009]', () async {
    await firstRelease();
    server.release = FakeRelease(11, demo, {'loans': tasks});
    final h = Goldens.hashOf(tasks);
    server.faults['/v1/objects/bundles/${h.substring(0, 2)}/$h'] = List.filled(
      5,
      const StatusFault(503),
      growable: true,
    );
    final (r, _) = await sync();
    expect(r.outcome, SyncOutcome.failed);
    expect(r.error!.code, PluxErrorCode.syncFailed);
    expect(sleeps, hasLength(4));
    expect(checkComplete(root), 10);
  });

  test(
    'a manifest call that fails is reported and changes nothing [QA-009]',
    () async {
      await firstRelease();
      server.faults['/plux.v1.ManifestService/GetManifest'] = [
        const StatusFault(503),
      ];
      final (r, _) = await sync();
      expect(r.error!.code, PluxErrorCode.syncFailed);
      expect(r.error!.details['status'], '503');
      expect(checkComplete(root), 10);
    },
  );

  test('refuses an expired manifest when the device clock runs ahead [SEC-052] [QA-009]', () async {
    server
      ..release = FakeRelease(10, demo, {'loans': loans})
      ..expires = DateTime.utc(2026, 10, 5);
    now = DateTime.utc(2026, 10, 6);
    final (ahead, _) = await sync();
    expect(ahead.error!.code, PluxErrorCode.manifestExpired);
    now = DateTime.utc(2026, 1, 1);
    final (behind, _) = await sync();
    expect(
      behind.outcome,
      SyncOutcome.staged,
      reason: 'a clock behind still accepts an unexpired manifest',
    );
  });

  test('refuses a lower release sequence [SEC-055]', () async {
    await firstRelease();
    server.release = FakeRelease(9, demo, {'loans': loans});
    final (r, _) = await sync();
    expect(r.error!.code, PluxErrorCode.rollbackRejected);
  });

  test('refuses a release this runtime cannot use [BND-008]', () async {
    server.release = FakeRelease(
      10,
      demo,
      {'loans': loans},
      requiredFeatures: ['pxl.v1', 'widget.Hologram.v1'],
    );
    final (r, _) = await sync(supports: (f) => f != 'widget.Hologram.v1');
    expect(r.error!.code, PluxErrorCode.unsupportedRequiredFeature);
    expect(
      ReleaseStore.open(root).pointer.highestAccepted,
      0,
      reason: 'nothing was accepted',
    );
  });

  test('refuses a release above the disk quota [SYN-012] [LIM-004]', () async {
    server.release = FakeRelease(10, demo, {'loans': loans});
    final (r, _) = await sync(quota: 1000);
    expect(r.error!.code, PluxErrorCode.diskQuotaExceeded);
    expect(Directory('$root/objects/bundles').listSync(), isEmpty);
  });

  test('the quota is the one the release\'s signed app bundle declares [SYN-012] [LIM-004]', () async {
    server.release = FakeRelease(10, withDiskQuota(demo, 1000), {
      'loans': loans,
    });
    final (r, _) = await sync();
    expect(r.error?.code, PluxErrorCode.diskQuotaExceeded, reason: '$r');
    expect(r.error!.message, contains('over the quota of 1000'));
    expect(
      Directory('$root/objects/bundles').listSync(),
      isEmpty,
      reason: 'the app bundle read for its limits is not kept',
    );

    server.release = FakeRelease(11, withDiskQuota(demo, 100 << 20), {
      'loans': loans,
    });
    final (ok, _) = await sync();
    expect(ok.outcome, SyncOutcome.staged, reason: '$ok');
  });

  test('ignores the sequence it reverted from and records control switches [SYN-006] [RT-022]', () async {
    await firstRelease();
    final store = ReleaseStore.open(root);
    store.writePointer(store.pointer.copyWith(rejected: 11));
    server.release = FakeRelease(
      11,
      demo,
      {'loans': tasks},
      killSwitches: ['loans'],
    );
    final (r, _) = await sync();
    expect(r.outcome, SyncOutcome.upToDate);
    final c = ReleaseStore.open(root).control()!;
    expect(c.sequence, 11);
    expect(c.killSwitches, ['loans']);
  });

  test('registers again when the server no longer knows the device', () async {
    await firstRelease();
    server.forgetDevices = true;
    final (r, _) = await sync();
    expect(r.outcome, SyncOutcome.upToDate);
    expect(server.registrations, 2);
  });

  test(
    'reports a mandatory release as staged with the flag [SYN-004]',
    () async {
      server.release = FakeRelease(10, demo, {'loans': loans}, mandatory: true);
      final (_, events) = await sync();
      expect((events.last as SyncStaged).mandatory, isTrue);
      expect(events.last.toString(), contains('mandatory'));
    },
  );
}

/// [app] with a `device.diskQuota` limit of [quota] in its meta section,
/// the bundle re-encoded (its hash changes; the fake server signs what it
/// serves). The compiler writes no limit at its default, so the section is
/// rebuilt with the fields a sync reads plus the override.
Uint8List withDiskQuota(Uint8List app, int quota) {
  final b = BundleContainer.parse(app);
  fbs.UuidObjectBuilder? uuid(fbs.Uuid? u) =>
      u == null ? null : fbs.UuidObjectBuilder(hi: u.hi, lo: u.lo);
  final sections = [
    for (final s in b.sections)
      if (s.kind != SectionKind.meta)
        s
      else
        () {
          final m = fbs.Meta(s.data);
          expect(m.limits ?? const <fbs.Limit>[], isEmpty);
          final data = fbs.MetaObjectBuilder(
            kind: m.kind,
            id: uuid(m.id),
            key: m.key,
            name: m.name,
            version: m.version,
            compilerVersion: m.compilerVersion,
            schemaVersion: m.schemaVersion,
            requiredFeatures: m.requiredFeatures,
            minRuntime: m.minRuntime,
            limits: [
              fbs.LimitObjectBuilder(
                key: PluxLimit.deviceDiskQuota.key,
                value: quota,
              ),
            ],
            plugins: [
              for (final u in m.plugins ?? const <fbs.Uuid>[]) uuid(u)!,
            ],
            defaultLocale: m.defaultLocale,
            supportedLocales: m.supportedLocales,
            entryRoute: m.entryRoute,
          ).toBytes(String.fromCharCodes(s.data, 4, 8));
          return Section(s.kind, s.id, s.hash, data);
        }(),
  ];
  return encodeBundle(b.kind, sections);
}
