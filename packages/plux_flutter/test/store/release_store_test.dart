// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/store/directory_sync.dart';
import 'package:plux_flutter/src/store/pointer.dart';
import 'package:plux_flutter/src/store/release_record.dart';
import 'package:plux_flutter/src/store/release_store.dart';

import 'store_test_support.dart';

/// Thrown by a step hook to stop the process at that step.
final class _Crash implements Exception {}

void main() {
  late String root;
  setUp(() => root = Directory.systemTemp.createTempSync('plux_store').path);
  tearDown(() => Directory(root).deleteSync(recursive: true));

  ReleaseStore open({StepHook? onStep}) =>
      ReleaseStore.open(root, syncDirectory: syncDirectory, onStep: onStep);

  test(
    'encodes the pointer with a checksum and refuses a corrupt one [SYN-005]',
    () {
      const p = StorePointer(
        active: 3,
        staged: 4,
        lastKnownGood: 2,
        pinned: 2,
        rejected: 5,
        highestAccepted: 4,
        etag: '"e"',
        trial: Trial(sequence: 3, launches: 1, failures: 2, open: true),
      );
      final back = StorePointer.decode(p.encode())!;
      expect(
        [
          back.active,
          back.staged,
          back.lastKnownGood,
          back.pinned,
          back.rejected,
          back.highestAccepted,
          back.etag,
        ],
        [3, 4, 2, 2, 5, 4, '"e"'],
      );
      expect(
        [
          back.trial!.sequence,
          back.trial!.launches,
          back.trial!.failures,
          back.trial!.open,
        ],
        [3, 1, 2, true],
      );
      expect(back.kept, {2, 3, 4});
      final bytes = p.encode()..[20] ^= 1;
      expect(StorePointer.decode(bytes), isNull);
      expect(StorePointer.decode([1, 2, 3]), isNull);
      expect(
        p
            .copyWith(
              clearStaged: true,
              clearPinned: true,
              clearRejected: true,
              clearTrial: true,
              clearLastKnownGood: true,
            )
            .kept,
        {3},
      );
    },
  );

  test('stages, activates and keeps the last known good release [SYN-005] [SYN-006]', () {
    final store = open();
    writeRelease(store, 1);
    expect(store.pointer.staged, 1);
    store.activate();
    expect(store.pointer.active, 1);
    expect(
      store.pointer.trial,
      isNull,
      reason: 'the first release has nothing to go back to',
    );
    writeRelease(store, 2);
    store.activate();
    expect(store.pointer.active, 2);
    expect(store.pointer.lastKnownGood, 1);
    expect(store.pointer.trial!.sequence, 2);
    expect(checkComplete(root), 2);
    expect(store.activate, throwsStateError);
  });

  test('a crash at any durable step leaves the old or the new release fully active [SYN-005] [QA-009] [QA-002]', () {
    const steps = [
      'object.written',
      'object.renamed',
      'record.written',
      'record.renamed',
      'pointer.written',
      'pointer.renamed',
    ];
    // Establish release 1, then crash while installing 2 at the k-th
    // occurrence of each step.
    for (final step in steps) {
      for (var k = 1; k <= 4; k++) {
        final dir = Directory('$root/$step-$k')..createSync();
        final base = ReleaseStore.open(dir.path, syncDirectory: syncDirectory);
        writeRelease(base, 1);
        base.activate();
        var seen = 0;
        final crashing = ReleaseStore.open(
          dir.path,
          syncDirectory: syncDirectory,
          onStep: (s) {
            if (s == step && ++seen == k) throw _Crash();
          },
        );
        try {
          writeRelease(crashing, 2);
          crashing.activate();
        } on _Crash {
          // The process died here.
        }
        final active = checkComplete(dir.path);
        expect(active, anyOf(1, 2), reason: '$step #$k');
        // The store recovers: the next staging and activation succeed.
        final again = ReleaseStore.open(dir.path, syncDirectory: syncDirectory);
        if (again.pointer.active == 1) {
          writeRelease(again, 2);
          again.activate();
        }
        again.collectGarbage();
        expect(checkComplete(dir.path), 2, reason: '$step #$k, after recovery');
      }
    }
  });

  test('garbage collection keeps active, staged, last known good and pinned [SYN-012]', () {
    final store = open();
    for (var s = 1; s <= 4; s++) {
      writeRelease(store, s);
      store.activate();
    }
    writeRelease(store, 5);
    File(store.partPath('stale')).writeAsStringSync('partial');
    File(store.partPath('resume')).writeAsStringSync('partial');
    store.collectGarbage(keepParts: {store.partPath('resume')});
    expect(store.record(1), isNull);
    expect(store.record(2), isNull);
    expect(store.record(3), isNotNull, reason: 'last known good');
    expect(store.record(4), isNotNull, reason: 'active');
    expect(store.record(5), isNotNull, reason: 'staged');
    expect(File(store.partPath('stale')).existsSync(), isFalse);
    expect(File(store.partPath('resume')).existsSync(), isTrue);
    final objects = Directory('$root/objects/bundles').listSync().length;
    expect(
      objects,
      1 + 3 * 3,
      reason: 'one shared object and three per kept release',
    );
    expect(store.usage(), greaterThan(store.keptUsage()));
  });

  test('reverts after three failures within the first two launches and pins last known good [SYN-006]', () {
    var store = open();
    writeRelease(store, 1);
    store.activate();
    writeRelease(store, 2);
    store.activate();
    expect(store.beginLaunch(), LaunchOutcome.normal);
    expect(store.recordFailure(), isFalse);
    // The launch crashes without reaching a healthy point.
    store = open();
    expect(
      store.beginLaunch(),
      LaunchOutcome.normal,
      reason: 'two failures so far',
    );
    expect(store.pointer.trial!.failures, 2);
    expect(store.recordFailure(), isTrue);
    store = open();
    expect(store.beginLaunch(), LaunchOutcome.revertedToLastKnownGood);
    expect(store.pointer.active, 1);
    expect(store.pointer.pinned, 1);
    expect(store.pointer.rejected, 2);
    expect(store.pointer.trial, isNull);
    expect(checkComplete(root), 1);
  });

  test('a release that reaches healthy points ends its trial [SYN-006]', () {
    final store = open();
    writeRelease(store, 1);
    store.activate();
    writeRelease(store, 2);
    store.activate();
    for (var i = 0; i < 2; i++) {
      store
        ..beginLaunch()
        ..markHealthy();
    }
    expect(store.pointer.trial!.launches, 2);
    store.beginLaunch();
    expect(store.pointer.trial, isNull);
    expect(store.recordFailure(), isFalse, reason: 'no trial, no revert');
    expect(
      store.revert(),
      isTrue,
      reason: 'a host-requested revert still works',
    );
    expect(store.pointer.active, 1);
  });

  test('a revert with no last known good keeps the active release', () {
    final store = open();
    writeRelease(store, 1);
    store.activate();
    expect(store.revert(), isFalse);
    expect(store.pointer.active, 1);
  });

  test('installs a baseline without a trial and accepts manifests [SYN-007] [SEC-055]', () {
    final store = open();
    final r = ReleaseRecord(
      sequence: 7,
      source: ReleaseSource.baseline,
      bundles: [RecordBundle(key: '', version: 1, hash: _put(store, 'app'))],
    );
    store.installBaseline(r);
    expect(store.pointer.active, 7);
    expect(store.pointer.highestAccepted, 7);
    expect(store.record(7)!.source, ReleaseSource.baseline);
    store.accept(9, '"etag"');
    expect(store.pointer.highestAccepted, 9);
    store.accept(8, '"x"');
    expect(store.pointer.highestAccepted, 9, reason: 'never lowered');
    expect(store.pointer.etag, '"x"');
  });

  test('refuses to stage a record whose objects are missing', () {
    final store = open();
    expect(
      () => store.stage(
        const ReleaseRecord(
          sequence: 1,
          source: ReleaseSource.sync,
          bundles: [RecordBundle(key: '', version: 1, hash: 'ab')],
        ),
      ),
      throwsStateError,
    );
    expect(
      () => store.stage(
        ReleaseRecord(
          sequence: 1,
          source: ReleaseSource.sync,
          bundles: [RecordBundle(key: '', version: 1, hash: _put(store, 'x'))],
          assets: const ['cd'],
        ),
      ),
      throwsStateError,
    );
  });

  test('keeps control switches apart from records', () {
    final store = open();
    expect(store.control(), isNull);
    store.writeControl(
      const ControlState(
        sequence: 3,
        killSwitches: ['loans'],
        appKillSwitch: true,
        mandatory: true,
        message: 'm',
      ),
    );
    final c = open().control()!;
    expect(
      [c.sequence, c.killSwitches, c.appKillSwitch, c.mandatory, c.message],
      [
        3,
        ['loans'],
        true,
        true,
        'm',
      ],
    );
    expect(ControlState.decode([0]), isNull);
  });

  test('reports a full disk as PLX-3030 and keeps the active release [SYN-012] [QA-009]', () {
    final base = open();
    writeRelease(base, 1);
    base.activate();
    final full = open(
      onStep: (s) {
        if (s == 'record.written') {
          throw const FileSystemException(
            'write',
            'x',
            OSError('No space left on device', 28),
          );
        }
      },
    );
    expect(
      () => writeRelease(full, 2),
      throwsA(
        isA<PluxException>().having(
          (e) => e.code,
          'code',
          PluxErrorCode.diskQuotaExceeded,
        ),
      ),
    );
    expect(checkComplete(root), 1);
    final other = open(
      onStep: (s) {
        if (s == 'pointer.written') {
          throw const FileSystemException('write', 'x', OSError('denied', 13));
        }
      },
    );
    expect(() => other.accept(3, ''), throwsA(isA<FileSystemException>()));
  });

  test('records survive round trips and reject garbage', () {
    final r = ReleaseRecord(
      sequence: 4,
      source: ReleaseSource.sync,
      bundles: const [
        RecordBundle(key: '', version: 1, hash: 'aa'),
        RecordBundle(key: 'p', version: 2, hash: 'bb'),
      ],
      assets: const ['cc'],
      signed: Uint8List.fromList([1, 2]),
      signatures: const [
        {'keyid': 'k', 'alg': 'ed25519', 'sig': 'AA=='},
      ],
      killSwitches: const ['p'],
      message: 'hi',
    );
    final back = ReleaseRecord.decode(r.encode());
    expect(back.app.hash, 'aa');
    expect(back.bundles.last.isApp, isFalse);
    expect(back.signed, [1, 2]);
    expect(back.assets, ['cc']);
    expect(back.killSwitches, ['p']);
    expect(() => ReleaseRecord.decode([1]), throwsFormatException);
    expect(
      () => ReleaseRecord.decode('{"version":2}'.codeUnits),
      throwsFormatException,
    );
    final store = open();
    File('$root/releases/9.json').writeAsStringSync('garbage');
    expect(store.record(9), isNull);
  });

  test('syncs a directory and reports a missing one', () {
    syncDirectory(root);
    expect(
      () => syncDirectory('$root/missing'),
      throwsA(isA<FileSystemException>()),
    );
  });

  test('a record keeps each bundle\'s required features, so start-up opens only bundles that may declare triggers [ACT-002] [BND-008]', () {
    const r = ReleaseRecord(
      sequence: 3,
      source: ReleaseSource.sync,
      bundles: [
        RecordBundle(key: '', version: 0, hash: 'aa', features: ['pxl.v1']),
        RecordBundle(
          key: 'shop',
          version: 2,
          hash: 'bb',
          features: ['actions.triggers.v1', 'pxl.v1'],
        ),
        RecordBundle(key: 'old', version: 1, hash: 'cc'),
      ],
    );
    final back = ReleaseRecord.decode(r.encode());
    expect(
      [for (final b in back.bundles) b.features],
      [
        ['pxl.v1'],
        ['actions.triggers.v1', 'pxl.v1'],
        null,
      ],
    );
    // Unknown features (a record of runtime 0.2.0) keep the bundle in.
    expect(
      [for (final b in back.bundles) b.mayHaveTriggers],
      [false, true, true],
    );
  });
}

String _put(ReleaseStore store, String content) {
  final bytes = Uint8List.fromList(content.codeUnits);
  final hash = content.codeUnits.map((c) => c.toRadixString(16)).join();
  store.writeObject(ObjectKind.bundles, hash, bytes);
  return hash;
}
