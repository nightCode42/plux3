// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:convert';
import 'dart:isolate';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/platform/platform_services.dart';
import 'package:plux_flutter/src/security/pins.dart';

import '../support/test_certificate.dart';

PluxErrorCode _codeOf(void Function() f) {
  try {
    f();
  } on PluxException catch (e) {
    return e.code;
  }
  throw StateError('no PluxException');
}

void main() {
  final a = unrelatedPin(1);
  final b = unrelatedPin(2);
  final https = Uri.parse('https://plux.example.com');

  // Verifies: SEC-041.
  test('a pin is the base64 of a SHA-256 digest in canonical form '
      '[SEC-041]', () {
    expect(isWellFormedPin(a), isTrue);
    expect(a, hasLength(44));
    expect(isWellFormedPin(''), isFalse);
    expect(isWellFormedPin(a.substring(1)), isFalse);
    expect(isWellFormedPin('${a.substring(0, 43)}!'), isFalse);
    // Not a SHA-256 digest: 20 bytes.
    expect(isWellFormedPin(base64.encode(Uint8List(20))), isFalse);
    // Right length, but not the canonical encoding of its bytes.
    expect(isWellFormedPin('${a.substring(0, 42)}B='), isFalse);
  });

  // Verifies: SEC-041.
  test('a set needs two distinct well-formed pins [SEC-041]', () {
    expect(validatedPins([a, b]), {a, b});
    expect(_codeOf(() => validatedPins([a])), PluxErrorCode.missingProperty);
    expect(
      _codeOf(() => validatedPins([a, a])),
      PluxErrorCode.missingProperty,
      reason: 'the same pin twice is one pin',
    );
    expect(_codeOf(() => validatedPins([a, 'x'])), PluxErrorCode.invalidFormat);
    expect(
      _codeOf(() => validatedPins(const [])),
      PluxErrorCode.missingProperty,
    );
  });

  // Verifies: SEC-041.
  test('the config without pins is refused only in a release build with an '
      'https endpoint [SEC-041]', () {
    expect(
      _codeOf(() => pinSetFor(https, const [], releaseMode: true)),
      PluxErrorCode.missingProperty,
    );
    expect(pinSetFor(https, const []), isNull, reason: 'debug build');
    expect(
      pinSetFor(
        Uri.parse('http://localhost:8080'),
        const [],
        releaseMode: true,
      ),
      isNull,
      reason: 'a local development server',
    );
    expect(pinSetFor(https, [a, b], releaseMode: true)!.pins, {a, b});
    // Pins are checked whatever the build.
    expect(_codeOf(() => pinSetFor(https, [a])), PluxErrorCode.missingProperty);
  });

  // Verifies: SEC-041, SEC-050.
  test('a replacement keeps two pins and only moves forward [SEC-041] '
      '[SEC-050]', () {
    final set = PinSet([a, b]);
    final c = unrelatedPin(3);
    expect(set.version, 0);
    set.replace([b, c], version: 4);
    expect(set.pins, {b, c});
    expect(set.version, 4);
    expect(set.matches([a]), isFalse);
    expect(set.matches([a, c]), isTrue);
    expect(
      _codeOf(() => set.replace([a, b], version: 4)),
      PluxErrorCode.outOfRange,
      reason: 'the same version',
    );
    expect(
      _codeOf(() => set.replace([a], version: 5)),
      PluxErrorCode.missingProperty,
      reason: 'never fewer than two pins',
    );
    expect(set.pins, {b, c}, reason: 'a refused replacement changes nothing');
    expect(set.version, 4);
  });

  // Verifies: SEC-041.
  test('the pin of a certificate is the hash of its SPKI [SEC-041]', () async {
    final cert = await TestCertificate.generate();
    expect(spkiPin(cert.der), cert.pin);
    final other = await TestCertificate.generate();
    expect(spkiPin(other.der), isNot(cert.pin));
    expect(subjectPublicKeyInfo(cert.der), cert.spki);
  });

  // Verifies: SEC-041.
  test('what is not a certificate is refused [SEC-041]', () async {
    final cert = await TestCertificate.generate();
    expect(() => spkiPin(Uint8List(0)), throwsFormatException);
    expect(
      () => spkiPin(Uint8List.fromList([1, 2, 3, 4])),
      throwsFormatException,
    );
    expect(
      () => spkiPin(Uint8List.sublistView(cert.der, 0, cert.der.length - 40)),
      throwsFormatException,
    );
  });

  // Verifies: SEC-041.
  test('the client factory can be sent to the isolate that uses it '
      '[SEC-041]', () async {
    final factory = PlatformHttpClients(https, PinSet([a, b]));
    final ok = await Isolate.run(() {
      factory.create().close();
      return true;
    });
    expect(ok, isTrue);
  });
}
