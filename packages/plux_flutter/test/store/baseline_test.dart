// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:cryptography/dart.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/store/baseline.dart';
import 'package:plux_flutter/src/store/release_store.dart';
import 'package:plux_flutter/src/verify/bundle_verifier.dart';
import 'package:plux_flutter/src/verify/manifest.dart';

import 'store_test_support.dart';

const _limits = VerifierLimits(maxDepth: 64, maxVisits: 1000000);

void main() {
  late String root;
  late Map<String, Uint8List> files;
  late TrustedKey key;

  Uint8List golden(String n) =>
      File('../../schema/testdata/bundles/$n').readAsBytesSync();

  Future<Map<String, Object?>> entry(
    String plugin,
    Uint8List bundle,
    String file, {
    bool badSignature = false,
  }) async {
    final ed = DartEd25519(sha512: const DartSha512());
    final kp = await ed.newKeyPairFromSeed(List.filled(32, 4));
    final hash = bundle.sublist(16, 48);
    final sig = await ed.sign(badSignature ? Uint8List(32) : hash, keyPair: kp);
    files[file] = bundle;
    return {
      'plugin': plugin,
      'version': 3,
      'sha256': hexEncode(hash),
      'file': file,
      'keyId': 'k1',
      'algorithm': 'ed25519',
      'signature': base64.encode(sig.bytes),
    };
  }

  Future<void> write({
    int sequence = 5,
    bool badSignature = false,
    String app = 'app',
  }) async {
    files['baseline.json'] = Uint8List.fromList(
      utf8.encode(
        jsonEncode({
          'app': app,
          'environment': 'production',
          'channel': 'production',
          'releaseSequence': sequence,
          'bundles': [
            await entry(
              '',
              golden('loan-calculator/demo.pxb'),
              'bundles/_app.pxb',
            ),
            await entry(
              'loans',
              golden('loan-calculator/loans.pxb'),
              'bundles/loans.pxb',
              badSignature: badSignature,
            ),
          ],
        }),
      ),
    );
  }

  Future<int?> run() => importBaseline(
    read: (p) async => files[p],
    store: ReleaseStore.open(root),
    keys: [key],
    appId: 'app',
    environment: 'production',
    channel: 'production',
    limits: _limits,
    supportsFeature: (_) => true,
  );

  setUp(() async {
    root = Directory.systemTemp.createTempSync('plux_baseline').path;
    files = {};
    final kp = await DartEd25519(sha512: const DartSha512())
        .newKeyPairFromSeed(List.filled(32, 4));
    key = TrustedKey(
      keyId: 'k1',
      algorithm: 'ed25519',
      role: 'targets',
      publicKey: Uint8List.fromList((await kp.extractPublicKey()).bytes),
    );
  });
  tearDown(() => Directory(root).deleteSync(recursive: true));

  test('imports and activates a verified baseline for an offline first launch [SYN-007] [SEC-052]', () async {
    await write();
    expect(await run(), 5);
    final store = ReleaseStore.open(root);
    expect(store.pointer.active, 5);
    expect(store.pointer.highestAccepted, 5);
    expect(store.pointer.trial, isNull);
    expect(checkComplete(root), 5);
    expect(await run(), isNull, reason: 'already imported');
  });

  test('replaces an older release with a newer embedded baseline', () async {
    await write(sequence: 5);
    await run();
    await write(sequence: 6);
    expect(await run(), 6);
    expect(ReleaseStore.open(root).pointer.active, 6);
  });

  test('does nothing without a baseline', () async {
    expect(await run(), isNull);
  });

  test('refuses a baseline with a bad signature, a tampered bundle or another app [SEC-052]', () async {
    await write(badSignature: true);
    await expectLater(
      run(),
      throwsA(
        isA<PluxException>().having(
          (e) => e.code,
          'code',
          PluxErrorCode.manifestSignatureInvalid,
        ),
      ),
    );
    await write();
    files['bundles/loans.pxb'] = Uint8List.fromList(files['bundles/loans.pxb']!)
      ..[3000] ^= 1;
    await expectLater(run(), throwsA(isA<PluxException>()));
    await write(app: 'other');
    await expectLater(run(), throwsA(isA<PluxException>()));
    files['baseline.json'] = Uint8List.fromList(utf8.encode('{}'));
    await expectLater(run(), throwsA(isA<PluxException>()));
    expect(
      ReleaseStore.open(root).pointer.active,
      isNull,
      reason: 'nothing unverified was installed',
    );
  });
}
