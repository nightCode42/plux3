// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:convert';
import 'dart:io';

import 'package:crypto/crypto.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:plux_flutter/src/platform/native_pinned_http.dart';
import 'package:plux_flutter/src/security/pins.dart';

/// The Dart side of the pinned client's pull loop (SEC-041, SYN-010): one
/// `httpRead` round trip over the platform channel per chunk, the codec's
/// copy of the chunk, the stream and the collection of the body. The
/// native side is a fake that answers at once, so the time is the Dart
/// side's, and the per-chunk cost shows what a chunk size saves. Not
/// gated; `make bench-pull` runs it.
String _pin(int seed) => base64.encode(sha256.convert([seed]).bytes);

void main() {
  const channel = MethodChannel('test.plux/pull_bench');
  const body = 10 * 1024 * 1024;
  const runs = 15;

  Future<List<double>> measure(int chunkSize) async {
    final chunk = Uint8List(chunkSize);
    var left = 0;
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(channel, (call) async {
          switch (call.method) {
            case 'httpOpen':
              left = body;
              return {'id': 1, 'status': 200, 'headers': <String, String>{}};
            case 'httpRead':
              if (left == 0) return null;
              final n = left < chunkSize ? left : chunkSize;
              left -= n;
              return n == chunkSize ? chunk : Uint8List(n);
          }
          return null;
        });
    final client = NativePinnedClient(
      pins: PinSet([_pin(1), _pin(2)]),
      userAgent: 'bench',
      channel: channel,
    );
    final perChunk = <double>[];
    final chunks = (body / chunkSize).ceil();
    for (var r = 0; r < runs + 3; r++) {
      final w = Stopwatch()..start();
      final res = await client.send(
        http.Request('GET', Uri.parse('https://plux.example/bench')),
      );
      var got = 0;
      await for (final c in res.stream) {
        got += c.length;
      }
      w.stop();
      expect(got, body);
      if (r >= 3) perChunk.add(w.elapsedMicroseconds / chunks);
    }
    perChunk.sort();
    return perChunk;
  }

  test(
    'measures the pull loop for a 10 MiB body [SEC-041] [SYN-010]',
    () async {
      TestWidgetsFlutterBinding.ensureInitialized();
      final out = <String, Object?>{};
      for (final size in [32 * 1024, 256 * 1024]) {
        final t = await measure(size);
        double at(double q) => t[((t.length - 1) * q).round()];
        final total = at(0.5) * (body / size).ceil() / 1000;
        out['${size ~/ 1024}KiB'] = {
          'chunks': (body / size).ceil(),
          'p50_us_per_chunk': double.parse(at(0.5).toStringAsFixed(1)),
          'p95_us_per_chunk': double.parse(at(0.95).toStringAsFixed(1)),
          'p50_ms_per_body': double.parse(total.toStringAsFixed(1)),
          'p50_mib_per_s': double.parse(
            (10 / (total / 1000)).toStringAsFixed(0),
          ),
        };
      }
      // ignore: avoid_print
      print(jsonEncode({'body_mib': 10, 'runs': runs, 'pull_loop': out}));
    },
    skip: Platform.environment['PLUX_BENCH_PULL'] == null
        ? 'set PLUX_BENCH_PULL=1, or run make bench-pull'
        : false,
  );
}
