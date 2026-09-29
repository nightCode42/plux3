// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:io';
import 'dart:math';
import 'dart:typed_data';

import 'package:crypto/crypto.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/bundle/container.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/schema/limits.g.dart';
import 'package:plux_flutter/src/verify/bundle_verifier.dart';

const _limits = VerifierLimits(maxDepth: 64, maxVisits: 1000000);

Uint8List _loans() =>
    File('../../schema/testdata/bundles/loan-calculator/loans.pxb')
        .readAsBytesSync();

Uint8List _hash(Uint8List data) => Uint8List.sublistView(data, 16, 48);

Matcher _code(PluxErrorCode c) =>
    throwsA(isA<PluxException>().having((e) => e.code, 'code', c));

BundleContainer _verify(
  Uint8List data, {
  Uint8List? hash,
  bool Function(String)? supports,
  bool fromDelta = false,
}) => verifyBundle(
  data,
  hash ?? _hash(data),
  limits: _limits,
  supportsFeature: supports ?? (_) => true,
  fromDelta: fromDelta,
);

void main() {
  test('the registry limits are the ones the store uses', () {
    expect(PluxLimit.bundleVerifierDepth.defaultValue, _limits.maxDepth);
    expect(PluxLimit.bundleVerifierTables.defaultValue, _limits.maxVisits);
  });

  test('accepts a signed-hash bundle and every golden [SEC-052]', () {
    for (final f in Directory(
      '../../schema/testdata/bundles',
    ).listSync(recursive: true).whereType<File>()) {
      if (!f.path.endsWith('.pxb')) continue;
      final data = f.readAsBytesSync();
      expect(_verify(data).sections, isNotEmpty, reason: f.path);
    }
  });

  test('refuses a bundle whose hash is not the signed one', () {
    final data = _loans();
    final other = Uint8List(32);
    expect(
      () => _verify(data, hash: other),
      _code(PluxErrorCode.bundleMalformed),
    );
    expect(
      () => _verify(data, hash: other, fromDelta: true),
      _code(PluxErrorCode.patchHashMismatch),
    );
  });

  test('refuses a tampered section [SEC-052] [BND-005]', () {
    final data = _loans();
    final b = BundleContainer.parse(data);
    final s = b.ofKind(SectionKind.page).first;
    data[s.data.offsetInBytes + s.data.length ~/ 2] ^= 1;
    expect(() => _verify(data), _code(PluxErrorCode.sectionHashMismatch));
  });

  test('refuses a section that fails the verifier even with matching hashes [BND-006]', () {
    final b = BundleContainer.parse(_loans());
    final page = b.ofKind(SectionKind.page).first;
    final broken = Uint8List.fromList(page.data)..[0] = 0xff; // root offset
    final rebuilt = encodeBundle(BundleKinds.plugin, [
      for (final s in b.sections)
        identical(s, page) ? Section(s.kind, s.id, s.hash, broken) : s,
    ]);
    expect(
      () => _verify(rebuilt),
      _code(PluxErrorCode.sectionVerificationFailed),
    );
  });

  test('refuses encrypted bundles and unsupported features [BND-008]', () {
    final data = _loans();
    expect(
      () => _verify(data, supports: (f) => f != 'pxl.v1'),
      _code(PluxErrorCode.unsupportedRequiredFeature),
    );
    final b = BundleContainer.parse(data);
    final enc = Uint8List.fromList(data)..[8] = 1;
    final h = bundleHash(enc, b.sections.length);
    expect(
      () => _verify(enc, hash: h),
      _code(PluxErrorCode.bundleEncryptedUnsupported),
    );
  });

  test('skips unknown section kinds unless required [BND-018]', () {
    final b = BundleContainer.parse(_loans());
    // Kind 14 is unknown to this runtime; the Dart encoder, unlike the
    // Go one, lays it out, as a future compiler would.
    final future = encodeBundle(BundleKinds.plugin, [
      ...b.sections,
      Section(14, Uint8List(16), Uint8List(0), Uint8List.fromList([1, 2, 3])),
    ]);
    expect(_verify(future).sections.length, b.sections.length + 1);
  });

  test('the encoder reproduces the Go encoder byte for byte [CMP-002]', () {
    final data = _loans();
    final b = BundleContainer.parse(data);
    expect(encodeBundle(b.kind, b.sections.reversed.toList()), data);
  });

  test('a section gate checks once per section hash [BND-006]', () {
    final b = BundleContainer.parse(_loans());
    final gate = SectionGate(_limits);
    final s = b.sections.first;
    expect(gate.passed(s), isFalse);
    gate.check(s);
    expect(gate.passed(s), isTrue);
    gate.check(s);
    final bad = Section(s.kind, s.id, s.hash, Uint8List(s.data.length));
    expect(
      () => SectionGate(_limits).check(bad),
      _code(PluxErrorCode.sectionHashMismatch),
    );
  });

  test('sections above 64 KiB are checked on a background isolate [BND-006] [SEC-052]', () async {
    final b = BundleContainer.parse(_loans());
    final small = b.sections.first;
    final gate = SectionGate(_limits);
    expect(
      gate.prepare([small]),
      isNull,
      reason: 'small ones are checked on use',
    );
    final data = Uint8List(SectionGate.uiIsolateLimit + 1);
    final hash = Uint8List.fromList(sha256.convert(data).bytes);
    // An unknown kind: hashed but not interpreted.
    final large = Section(99, Uint8List(16), hash, data);
    await gate.prepare([large, small]);
    expect(gate.passed(large), isTrue);
    expect(gate.prepare([large]), isNull, reason: 'checked once');
    final wrong = Section(99, Uint8List(16), Uint8List(32), data);
    await expectLater(
      SectionGate(_limits).prepare([wrong]),
      throwsA(
        isA<PluxException>().having(
          (e) => e.code,
          'code',
          PluxErrorCode.sectionHashMismatch,
        ),
      ),
    );
    final notAPage = Section(SectionKind.page, Uint8List(16), hash, data);
    await expectLater(
      SectionGate(_limits).prepare([notAPage]),
      throwsA(isA<PluxException>()),
    );
  });

  test('container parsing never fails untyped on damaged input [QA-004]', () {
    final good = _loans();
    final rng = Random(3040);
    for (var i = 0; i < 20000; i++) {
      final d = Uint8List.fromList(good);
      for (var k = rng.nextInt(4) + 1; k > 0; k--) {
        d[rng.nextInt(min(d.length, 48 + 72 * 12))] = rng.nextInt(256);
      }
      final cut = rng.nextInt(10) == 0
          ? Uint8List.sublistView(d, 0, rng.nextInt(d.length))
          : d;
      try {
        _verify(cut, hash: _hash(good));
      } on PluxException {
        // expected
      }
    }
  });
}
