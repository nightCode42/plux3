// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/security/device_keys.dart';
import 'package:plux_flutter/src/security/dpop.dart';
import 'package:plux_flutter/src/security/software_keys.dart';

/// The cost of creating a DPoP proof (NFR-012, SEC-021): header and payload
/// JSON, base64url, the `ath` hash and the key's signature, end to end
/// through [DpopProofs.proof] with an access token and a nonce.
///
/// Two runs. With a key store whose `sign` answers at once the time is the
/// Dart side's alone. With [SoftwareDeviceKeys], pure-Dart P-256, it is an
/// upper bound for a platform without hardware speed-up; a phone's
/// Keystore or Secure Enclave is measured by the device benchmark
/// (`apps/starter/integration_test/dpop_bench_test.dart`). Not gated;
/// `make bench-dpop` runs it.
final class _InstantKeys implements DeviceKeys {
  final _signature = Uint8List(64);

  @override
  Future<Uint8List> sign(String alias, Uint8List data) async => _signature;

  @override
  Future<DeviceKey> create(
    String alias,
    KeyPurpose purpose, {
    Uint8List? challenge,
    bool strongBox = true,
  }) => throw UnimplementedError();

  @override
  Future<DeviceKey?> load(String alias, KeyPurpose purpose) =>
      throw UnimplementedError();

  @override
  Future<void> delete(String alias) => throw UnimplementedError();
}

void main() {
  const warmUp = 100;
  const iterations = 1000;
  final uri = Uri.parse('https://plux.example/plux.v1.SyncService/Pull?x=1');
  // About the size of a signed access token.
  final token = base64Url.encode(List<int>.generate(384, (i) => i % 251));

  Future<Map<String, Object?>> measure(
    DeviceKeys keys,
    String alias,
    EcPublicKey publicKey,
  ) async {
    final proofs = DpopProofs(keys: keys, alias: alias, publicKey: publicKey);
    Future<String> one() => proofs.proof(
      method: 'POST',
      uri: uri,
      accessToken: token,
      nonce: 'server-nonce-0123456789',
    );
    for (var i = 0; i < warmUp; i++) {
      await one();
    }
    final micros = <int>[];
    final w = Stopwatch();
    for (var i = 0; i < iterations; i++) {
      w.reset();
      w.start();
      await one();
      w.stop();
      micros.add(w.elapsedMicroseconds);
    }
    micros.sort();
    double ms(double q) => double.parse(
      (micros[((micros.length - 1) * q).round()] / 1000).toStringAsFixed(3),
    );
    return {
      'warm_up': warmUp,
      'iterations': iterations,
      'p50_ms': ms(0.5),
      'p95_ms': ms(0.95),
      'p99_ms': ms(0.99),
    };
  }

  test(
    'measures DPoP proof creation [NFR-012] [SEC-021]',
    () async {
      final instantKey = EcPublicKey(Uint8List(32), Uint8List(32));
      final instant = await measure(_InstantKeys(), 'bench', instantKey);

      final software = SoftwareDeviceKeys();
      final key = await software.create('bench', KeyPurpose.dpop);
      final signed = await measure(software, 'bench', key.publicKey);

      // ignore: avoid_print
      print(
        jsonEncode({
          'dpop_proof': {'instant_sign': instant, 'software_keys': signed},
        }),
      );
    },
    timeout: const Timeout(Duration(minutes: 5)),
    skip: Platform.environment['PLUX_BENCH_DPOP'] == null
        ? 'set PLUX_BENCH_DPOP=1, or run make bench-dpop'
        : false,
  );
}
