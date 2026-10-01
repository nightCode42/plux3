// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:convert';
import 'dart:io';
import 'dart:math';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/verify/manifest.dart';

/// The vectors written by backend/internal/release (go test -update),
/// signed by the P2 signer.
final Map<String, Object?> _vectors = jsonDecode(
  File('../../schema/testdata/manifest/vectors.json').readAsStringSync(),
) as Map<String, Object?>;

List<Map<String, Object?>> _list(String key) =>
    (_vectors[key]! as List<Object?>).cast<Map<String, Object?>>();

void main() {
  final keys = [for (final k in _list('keys')) TrustedKey.fromJson(k)];
  final unsupported = (_vectors['unsupportedFeatures']! as List<Object?>)
      .toSet();

  for (final c in _list('manifests')) {
    test(
      'verifies the ${c['name']} manifest as ADR-0029 requires [SEC-052] [SEC-055] [BND-008]',
      () async {
        final context = VerificationContext(
          keys: keys,
          app: c['app']! as String,
          environment: c['environment']! as String,
          channel: c['channel']! as String,
          now: DateTime.parse(c['now']! as String),
          highestAccepted: c['highestAccepted']! as int,
          runtimeVersion: c['runtime']! as String,
          supportsFeature: (f) => !unsupported.contains(f),
        );
        final signed = base64.decode(c['signed']! as String);
        final sigs = [
          for (final s
              in (c['signatures']! as List<Object?>)
                  .cast<Map<String, Object?>>())
            DocumentSignature.fromJson(s),
        ];
        final error = c['error'] as String?;
        if (error != null) {
          await expectLater(
            verifyManifest(signed, sigs, context),
            throwsA(
              isA<PluxException>().having((e) => e.code.id, 'code', error),
            ),
          );
          return;
        }
        final m = await verifyManifest(signed, sigs, context);
        expect(m.app, context.app);
        expect(m.releaseSequence, 231);
        expect(m.plugins.single.key, 'loans');
        expect(m.plugins.single.version, 14);
        expect(m.bundles.length, 2);
        expect(m.appBundle.hashRef, startsWith('sha256:'));
        expect(m.expires.isAfter(m.issuedAt), isTrue);
        if (c['name'] == 'kill-switch') {
          expect(m.control.killSwitches, ['loans']);
          expect(m.control.message, 'Maintenance');
        }
      },
    );
  }

  for (final c in _list('bundles')) {
    test(
      'checks the ${c['name']} baseline bundle signature [SEC-052]',
      () async {
        final ok = await verifyBundleSignature(
          hexDecode(c['hash']! as String),
          DocumentSignature(
            keyId: c['keyId']! as String,
            algorithm: c['algorithm']! as String,
            signature: base64.decode(c['signature']! as String),
          ),
          keys,
        );
        expect(ok, c['valid']);
      },
    );
  }

  test('refuses an empty, oversized or malformed signed document', () async {
    final context = VerificationContext(
      keys: keys,
      app: 'a',
      environment: 'e',
      channel: 'c',
      now: DateTime.utc(2026),
      highestAccepted: 0,
      runtimeVersion: '0.1.0',
      supportsFeature: (_) => true,
      maxSize: 8,
    );
    for (final doc in ['', '{"a":123456789}', '[1]']) {
      await expectLater(
        verifyManifest(Uint8List.fromList(utf8.encode(doc)), const [], context),
        throwsA(isA<PluxException>()),
      );
    }
  });

  test('the manifest parser fails only with typed errors [QA-004]', () {
    final valid = base64.decode(_list('manifests').first['signed']! as String);
    final text = utf8.decode(valid);
    expect(parseManifest(valid).releaseSequence, 231);
    final rng = Random(3001);
    const pieces = ['null', '1', '"x"', '[]', '{}', 'true', '-1', '1e999'];
    for (var i = 0; i < 20000; i++) {
      final at = rng.nextInt(text.length);
      final end = min(text.length, at + rng.nextInt(12));
      final mutated = rng.nextBool()
          ? text.replaceRange(at, end, pieces[rng.nextInt(pieces.length)])
          : text.replaceRange(
              at,
              at + 1,
              String.fromCharCode(rng.nextInt(128)),
            );
      try {
        parseManifest(Uint8List.fromList(utf8.encode(mutated)));
      } on PluxException catch (e) {
        expect(e.code, PluxErrorCode.manifestSignatureInvalid);
      }
    }
  });

  test('compares semantic versions', () {
    expect(compareVersions('0.1.0', '0.1.0'), 0);
    expect(compareVersions('0.1.0', '0.2.0'), lessThan(0));
    expect(compareVersions('1.0.0', '0.9.9'), greaterThan(0));
    expect(compareVersions('1.0.0-beta', '1.0.0'), lessThan(0));
    expect(compareVersions('1.0.0', '1.0.0-rc.1'), greaterThan(0));
    expect(compareVersions('1.0.0-rc.2', '1.0.0-rc.10'), lessThan(0));
    expect(compareVersions('1.0.0-rc', '1.0.0-rc.1'), lessThan(0));
    expect(compareVersions('1.0.0-alpha', '1.0.0-1'), greaterThan(0));
    expect(compareVersions('1.0.0+build', '1.0.0'), 0);
    expect(compareVersions('garbage', '1.0.0'), greaterThan(0));
    expect(compareVersions('1.0.0', 'garbage'), lessThan(0));
    expect(compareVersions('x', 'y'), 0);
  });

  test('decodes and encodes hexadecimal', () {
    expect(hexEncode(hexDecode('00ff10')), '00ff10');
    expect(() => hexDecode('0'), throwsFormatException);
    expect(() => hexDecode('+1'), throwsFormatException);
  });
}
