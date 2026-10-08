// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Verifies: SEC-050, SEC-051, SEC-056, SEC-122 (the device side, B11).

import 'dart:convert';
import 'dart:io';
import 'dart:math';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/security/pins.dart';
import 'package:plux_flutter/src/store/metadata_state.dart';
import 'package:plux_flutter/src/store/release_store.dart';
import 'package:plux_flutter/src/sync/api_client.dart';
import 'package:plux_flutter/src/sync/downloader.dart';
import 'package:plux_flutter/src/sync/metadata_sync.dart';
import 'package:plux_flutter/src/sync/sync_engine.dart';
import 'package:plux_flutter/src/sync/sync_event.dart';
import 'package:plux_flutter/src/verify/bundle_verifier.dart';
import 'package:plux_flutter/src/verify/update_metadata.dart';

import 'fake_device.dart';
import 'fake_metadata.dart';
import 'fake_server.dart';

void main() {
  final goldens = Goldens.load();
  final demo = goldens.bundles['loan-calculator/demo.pxb']!;
  final loans = goldens.bundles['loan-calculator/loans.pxb']!;

  late FakePluxServer server;
  late FakeMetadata metadata;
  late String root;
  late http.Client client;
  late MemoryCredentialStore credentials;
  late FakeDeviceKeys keys;
  var now = DateTime.utc(2026, 10, 8, 12);

  setUp(() async {
    server = await FakePluxServer.start();
    metadata = await FakeMetadata.create(await MetaKey.of(9));
    server.metadata = metadata;
    root = Directory.systemTemp.createTempSync('plux_meta').path;
    client = http.Client();
    credentials = MemoryCredentialStore();
    keys = FakeDeviceKeys();
    now = DateTime.utc(2026, 10, 8, 12);
  });
  tearDown(() async {
    client.close();
    await server.close();
    Directory(root).deleteSync(recursive: true);
  });

  Future<SyncResult> sync({
    bool production = false,
    Uint8List? rootDocument,
    PinSet? pins,
  }) => SyncEngine(
    config: SyncConfig(
      appId: FakePluxServer.app,
      environment: FakePluxServer.environment,
      channel: FakePluxServer.channel,
      keys: metadata.embedded,
      device: const DeviceInfo(
        platform: 'android',
        osVersion: '14',
        runtimeVersion: '0.1.0',
        hostBuild: '1',
      ),
      supportsFeature: (_) => true,
      verifierLimits: const VerifierLimits(maxDepth: 64, maxVisits: 1000000),
      production: production,
      rootDocument: rootDocument,
      pins: pins,
    ),
    store: ReleaseStore.open(root),
    api: PluxApiClient(client, server.endpoint),
    downloader: Downloader(client, sleep: (d) async {}, random: Random(1)),
    credentials: credentials,
    keys: keys,
    attestation: FakeAttestation(),
    clock: () => now,
  ).run((_) {});

  MetadataState state() => MetadataStore(root).read();

  Future<void> firstRelease() async {
    server.release = FakeRelease(10, demo, {'loans': loans});
    final r = await sync();
    expect(r.outcome, SyncOutcome.staged, reason: '${r.error}');
    ReleaseStore.open(root).activate();
  }

  void expectRefused(SyncResult r, PluxErrorCode code, String fault) {
    expect(r.outcome, SyncOutcome.failed);
    expect(r.error!.code, code);
    expect(r.error!.details['fault'], fault);
    final store = ReleaseStore.open(root);
    expect(store.pointer.staged, isNull, reason: 'nothing new is staged');
  }

  test('accepts a release whose manifest the metadata chain pins, and keeps '
      'the versions it saw [SEC-050]', () async {
    server.release = FakeRelease(10, demo, {'loans': loans});
    final r = await sync();
    expect(r.outcome, SyncOutcome.staged, reason: '${r.error}');
    final s = state();
    expect(s.environmentId, metadata.environmentId);
    expect((s.floor.timestamp, s.floor.snapshot, s.floor.targets), (9, 3, 5));
    expect(s.keyEnvironments.values.toSet(), {environmentProduction});
    expect(server.requests, contains('/v1/metadata/env_0001/timestamp.json'));
    expect(server.requests, contains('/v1/metadata/env_0001/3.snapshot.json'));
    expect(
      server.requests.where((p) => p.endsWith('GetRootKeys')),
      hasLength(1),
      reason: 'once, to learn the key environments',
    );
    // The next release under newer metadata is accepted as well.
    ReleaseStore.open(root).activate();
    metadata
      ..timestampVersion = 10
      ..snapshotVersion = 4
      ..targetsVersion = 6;
    await metadata.publish();
    server.release = FakeRelease(11, demo, {'loans': loans});
    final again = await sync();
    expect(again.outcome, SyncOutcome.staged, reason: '${again.error}');
    expect(state().floor.targets, 6);
    expect(
      server.requests.where((p) => p.endsWith('GetRootKeys')),
      hasLength(1),
      reason: 'the root has not changed',
    );
  });

  test('an expired timestamp at sync refuses the new release and leaves the '
      'last good one running [SEC-050] [B11]', () async {
    await firstRelease();
    server.release = FakeRelease(11, demo, {'loans': loans});
    now = DateTime.utc(2026, 10, 10);
    final r = await sync();
    expectRefused(
      r,
      PluxErrorCode.updateMetadataInvalid,
      MetadataFault.expired,
    );
    final store = ReleaseStore.open(root);
    expect(store.pointer.active, 10);
    expect(store.pointer.highestAccepted, 10);
    // Fresh metadata is accepted again.
    metadata
      ..timestampExpires = DateTime.utc(2026, 10, 12)
      ..timestampVersion = 10;
    await metadata.publish();
    expect((await sync()).outcome, SyncOutcome.staged);
  });

  test(
    'metadata older than what the device saw is a rollback [SEC-050]',
    () async {
      await firstRelease();
      server.release = FakeRelease(11, demo, {'loans': loans});
      metadata.timestampVersion = 8;
      await metadata.publish();
      final r = await sync();
      expectRefused(r, PluxErrorCode.rollbackRejected, MetadataFault.rollback);
      expect(state().floor.timestamp, 9, reason: 'the floor does not move');
    },
  );

  test(
    'a snapshot that pins another manifest version is refused [SEC-050]',
    () async {
      metadata.pinnedTargets = 4;
      await metadata.publish();
      server.release = FakeRelease(10, demo, {'loans': loans});
      final r = await sync();
      expectRefused(
        r,
        PluxErrorCode.updateMetadataInvalid,
        MetadataFault.mismatch,
      );
      expect(state().floor.timestamp, 0, reason: 'nothing was accepted');
    },
  );

  test('an environment that had metadata cannot drop it [SEC-050]', () async {
    await firstRelease();
    server.release = FakeRelease(11, demo, {'loans': loans});
    server.metadata = null;
    final r = await sync();
    expectRefused(
      r,
      PluxErrorCode.updateMetadataInvalid,
      MetadataFault.rollback,
    );
  });

  test('without metadata the manifest alone is verified, as before '
      '[SEC-050]', () async {
    server.metadata = null;
    server.release = FakeRelease(10, demo, {'loans': loans});
    final r = await sync();
    expect(r.outcome, SyncOutcome.staged, reason: '${r.error}');
    expect(state().floor.timestamp, 0);
    expect(server.requests.where((p) => p.startsWith('/v1/metadata')), isEmpty);
  });

  test('follows a root rotation, persists the new root and then verifies '
      'the chain under its keys [SEC-051]', () async {
    await firstRelease();
    await metadata.rotate();
    metadata
      ..timestampVersion = 10
      ..snapshotVersion = 4
      ..targetsVersion = 6;
    await metadata.publish();
    server.release = FakeRelease(11, demo, {'loans': loans});
    final r = await sync();
    expect(r.outcome, SyncOutcome.staged, reason: '${r.error}');
    final stored = await loadRoot(state().root!);
    expect(stored.version, 2);
    expect(state().floor.targets, 6);
  });

  test('refuses a rotation signed by the new keys alone, and keeps the '
      'root it has [SEC-051]', () async {
    await firstRelease();
    await metadata.rotate(forged: true);
    metadata.timestampVersion = 10;
    await metadata.publish();
    server.release = FakeRelease(11, demo, {'loans': loans});
    final r = await sync();
    expectRefused(
      r,
      PluxErrorCode.updateMetadataInvalid,
      MetadataFault.threshold,
    );
    expect(state().root, isNull);
    expect(ReleaseStore.open(root).pointer.active, 10);
  });

  test(
    'looks for a rotation once when the chain does not verify under the '
    'root it holds, even if the manifest names an older root [SEC-051]',
    () async {
      await firstRelease();
      await metadata.rotate();
      metadata
        ..hintedRoot = 1
        ..timestampVersion = 10;
      await metadata.publish();
      server.release = FakeRelease(11, demo, {'loans': loans});
      final r = await sync();
      expect(r.outcome, SyncOutcome.staged, reason: '${r.error}');
      expect((await loadRoot(state().root!)).version, 2);
    },
  );

  test('a production runtime refuses development keys, a development one '
      'accepts them [SEC-056]', () async {
    metadata.environmentType = environmentDevelopment;
    server.release = FakeRelease(10, demo, {'loans': loans});
    final refused = await sync(production: true);
    expectRefused(
      refused,
      PluxErrorCode.developmentKeyInProduction,
      MetadataFault.developmentKey,
    );
    expect((await sync()).outcome, SyncOutcome.staged);
  });

  test('keys that are not marked production are refused in production '
      '[SEC-056]', () async {
    metadata.environmentType = '';
    server.release = FakeRelease(10, demo, {'loans': loans});
    final r = await sync(production: true);
    expect(r.error!.code, PluxErrorCode.developmentKeyInProduction);
  });

  test('the timestamp is checked on every sync, also when the manifest has '
      'not changed, so a frozen server cannot hold the device [SEC-050] '
      '[B11]', () async {
    await firstRelease();
    final before = server.requests.length;
    expect((await sync()).outcome, SyncOutcome.upToDate);
    expect(
      server.requests.sublist(before),
      contains('/v1/metadata/env_0001/timestamp.json'),
    );
    // The same manifest, but the timestamp has run out: refused, and the
    // release that runs is untouched.
    now = DateTime.utc(2026, 10, 10);
    final r = await sync();
    expect(r.outcome, SyncOutcome.failed);
    expect(r.error!.code, PluxErrorCode.updateMetadataInvalid);
    expect(r.error!.details['fault'], MetadataFault.expired);
    expect(ReleaseStore.open(root).pointer.active, 10);
    // An older timestamp than the one seen is a rollback.
    now = DateTime.utc(2026, 10, 8, 13);
    metadata.timestampVersion = 8;
    await metadata.publish();
    final old = await sync();
    expect(old.error!.code, PluxErrorCode.rollbackRejected);
    // A newer one raises the floor.
    metadata.timestampVersion = 12;
    await metadata.publish();
    expect((await sync()).outcome, SyncOutcome.upToDate);
    expect(state().floor.timestamp, 12);
  });

  test('an environment without metadata is not asked for a timestamp on an '
      'unchanged manifest [SEC-050]', () async {
    server.metadata = null;
    server.release = FakeRelease(10, demo, {'loans': loans});
    expect((await sync()).outcome, SyncOutcome.staged);
    ReleaseStore.open(root).activate();
    expect((await sync()).outcome, SyncOutcome.upToDate);
    expect(server.requests.where((p) => p.startsWith('/v1/metadata')), isEmpty);
  });

  test('starts from the root the app embeds, in preference to the keys, and '
      'refuses one that does not verify [SEC-051]', () async {
    await metadata.rotate();
    metadata
      ..timestampVersion = 10
      ..snapshotVersion = 4
      ..targetsVersion = 6;
    await metadata.publish();
    server.release = FakeRelease(11, demo, {'loans': loans});
    final bad = Uint8List.fromList(metadata.roots.last)
      ..[metadata.roots.last.length - 20] ^= 1;
    final refused = await sync(rootDocument: bad);
    expect(refused.outcome, SyncOutcome.failed);
    expect(refused.error!.code, PluxErrorCode.updateMetadataInvalid);
    final r = await sync(rootDocument: metadata.roots.last);
    expect(r.outcome, SyncOutcome.staged, reason: '${r.error}');
    expect(state().root, isNull, reason: 'no rotation to follow');
    expect(state().floor.targets, 6);
  });

  test('the pins of a verified root replace the Plux host pins, and survive '
      'a restart; a root without pins leaves them [SEC-041]', () async {
    String pin(int n) => base64.encode(List.filled(32, n));
    final set = PinSet([pin(1), pin(2)]);
    await firstRelease();
    metadata.pins = {
      '127.0.0.1': [pin(3), pin(4)],
      'other.example.com': [pin(5), pin(6)],
    };
    await metadata.rotate();
    metadata.timestampVersion = 10;
    await metadata.publish();
    server.release = FakeRelease(11, demo, {'loans': loans});
    final r = await sync(pins: set);
    expect(r.outcome, SyncOutcome.staged, reason: '${r.error}');
    expect(set.pins, {pin(3), pin(4)});
    expect(set.version, 2);
    expect(state().pins, [pin(3), pin(4)]);
    final restarted = PinSet([pin(1), pin(2)]);
    MetadataSync.applyStoredPins(restarted, state());
    expect(restarted.pins, {pin(3), pin(4)});
    // The next root pins nothing for the host: the pins stay.
    metadata.pins = null;
    await metadata.rotate();
    metadata.timestampVersion = 11;
    await metadata.publish();
    ReleaseStore.open(root).activate();
    server.release = FakeRelease(12, demo, {'loans': loans});
    expect((await sync(pins: set)).outcome, SyncOutcome.staged);
    expect(set.pins, {pin(3), pin(4)});
    expect(set.version, 2);
  });

  test('the pins of an older root cannot bring back a retired pin '
      '[SEC-041]', () async {
    String pin(int n) => base64.encode(List.filled(32, n));
    final set = PinSet([pin(1), pin(2)], version: 5);
    metadata.pins = {
      '127.0.0.1': [pin(3), pin(4)],
    };
    await metadata.rotate();
    metadata.timestampVersion = 10;
    await metadata.publish();
    server.release = FakeRelease(10, demo, {'loans': loans});
    expect((await sync(pins: set)).outcome, SyncOutcome.staged);
    expect(set.pins, {pin(1), pin(2)}, reason: 'version 2 is not above 5');
  });

  test('the accepted state survives a restart, and unreadable bytes give '
      'the empty state [SEC-050]', () {
    final s = MetadataState(
      environmentId: 'env_1',
      root: Uint8List.fromList([1, 2, 3]),
      keyEnvironments: const {'k': environmentProduction},
      floor: const MetadataFloor(timestamp: 3, snapshot: 2, targets: 1),
    );
    final store = MetadataStore(root)..write(s);
    final back = MetadataStore(root).read();
    expect(back.environmentId, 'env_1');
    expect(back.root, [1, 2, 3]);
    expect(back.keyEnvironments, {'k': environmentProduction});
    expect(back.floor.targets, 1);
    File('$root/metadata').writeAsStringSync('{not json');
    expect(store.read().floor.timestamp, 0);
    expect(store.read().root, isNull);
  });
}
