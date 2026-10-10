// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Verifies: SEC-122.

import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/security/device_keys.dart';
import 'package:plux_flutter/src/security/p256.dart';
import 'package:plux_flutter/src/security/software_keys.dart';

void main() {
  late Uint8List publicKey;
  late Uint8List signature;
  final message = utf8.encode('the canonical signed part');

  setUp(() async {
    final keys = SoftwareDeviceKeys();
    final key = await keys.create('k', KeyPurpose.dpop);
    final q = key.publicKey;
    publicKey = Uint8List.fromList([4, ...q.x, ...q.y]);
    signature = await keys.sign('k', Uint8List.fromList(message));
  });

  test('the base point is on the curve and has the published order', () {
    expect(
      isP256PublicKey(
        Uint8List.fromList([
          4,
          ...p256Bytes32(p256Base.x),
          ...p256Bytes32(p256Base.y),
        ]),
      ),
      isTrue,
    );
    expect(p256Multiply(p256Order, p256Base), isNull);
  });

  test('verifies what the software key signed [SEC-122]', () {
    expect(p256Verify(publicKey, message, signature), isTrue);
  });

  test('refuses a changed message, signature or key [SEC-122]', () {
    expect(p256Verify(publicKey, utf8.encode('another'), signature), isFalse);
    final flipped = Uint8List.fromList(signature)..[40] ^= 1;
    expect(p256Verify(publicKey, message, flipped), isFalse);
    final other = Uint8List.fromList(publicKey)..[64] ^= 1;
    expect(p256Verify(other, message, signature), isFalse);
  });

  test('refuses malformed input without throwing [SEC-122]', () {
    expect(p256Verify(publicKey.sublist(1), message, signature), isFalse);
    expect(p256Verify(publicKey, message, signature.sublist(1)), isFalse);
    expect(p256Verify(publicKey, message, Uint8List(64)), isFalse);
    final compressed = Uint8List.fromList(publicKey)..[0] = 2;
    expect(p256Verify(compressed, message, signature), isFalse);
    final order = p256Bytes32(p256Order);
    expect(
      p256Verify(publicKey, message, Uint8List.fromList([...order, ...order])),
      isFalse,
    );
  });
}
