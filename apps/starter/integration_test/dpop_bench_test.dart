// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:plux_flutter/src/security/device_keys.dart';
import 'package:plux_flutter/src/security/dpop.dart';

/// Whether the run is on the reference device, where the target binds
/// (`--dart-define=PLUX_BENCH_ASSERT=1`). Elsewhere the benchmark reports.
const _assertTarget = String.fromEnvironment('PLUX_BENCH_ASSERT') == '1';

/// NFR-012's target for the 95th percentile, in milliseconds.
const _targetP95Ms = 15.0;

/// The cost of creating a DPoP proof on a device (NFR-012, SEC-021): 200
/// proofs through [DpopProofs.proof] with the platform's own key store
/// ([PlatformDeviceKeys]) — StrongBox or the TEE on Android, the Secure
/// Enclave on iOS, and the platform's software keystore on an emulator or
/// simulator. It prints p50, p95 and p99 as JSON, and fails when p95 is over
/// the target only with `PLUX_BENCH_ASSERT=1`, the reference-device run:
///
///     flutter test integration_test/dpop_bench_test.dart -d <device> \
///         --dart-define=PLUX_BENCH_ASSERT=1
///
/// The host counterpart is `make bench-dpop`.
void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  const warmUp = 20;
  const iterations = 200;
  const alias = 'dev.plux.bench.dpop';
  const keys = PlatformDeviceKeys();

  tearDownAll(() => keys.delete(alias));

  testWidgets(
    'creates a DPoP proof within the target at p95 [NFR-012] [SEC-021]',
    (tester) async {
      final key = await keys.create(alias, KeyPurpose.dpop);
      final proofs = DpopProofs(
        keys: keys,
        alias: alias,
        publicKey: key.publicKey,
      );
      final uri = Uri.parse('https://plux.example/plux.v1.SyncService/Pull');
      final token = base64Url.encode(List<int>.generate(384, (i) => i % 251));
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
      final p95 = ms(0.95);

      // ignore: avoid_print
      print(
        jsonEncode({
          'dpop_proof_device': {
            'key_storage': key.storage.wireName,
            'warm_up': warmUp,
            'iterations': iterations,
            'p50_ms': ms(0.5),
            'p95_ms': p95,
            'p99_ms': ms(0.99),
            'target_p95_ms': _targetP95Ms,
            'asserted': _assertTarget,
          },
        }),
      );

      if (_assertTarget) {
        expect(
          p95,
          lessThanOrEqualTo(_targetP95Ms),
          reason: 'NFR-012: p95 of ${key.storage.wireName} proofs',
        );
      }
    },
    timeout: const Timeout(Duration(minutes: 5)),
  );
}
