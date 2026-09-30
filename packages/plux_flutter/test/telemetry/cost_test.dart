// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/telemetry/events.dart';

/// Request headers of one upload beyond the body, a generous estimate: the
/// runtime sends the method line, host, content type and encoding,
/// protocol version, a bearer token and the length, about 300 bytes;
/// TLS framing is inside the margin (docs/benchmarks/p3-telemetry-cost.md).
const requestOverhead = 600;

/// The response of one upload, headers included, estimated likewise.
const responseOverhead = 250;

void main() {
  test('a typical day of telemetry costs well under 100 KiB [NFR-041] '
      '[ANL-002]', () {
    // The typical day of the benchmark: 5 sessions, each with 8 pages
    // viewed, a sync, and a flush after the sync and on leaving; one error
    // and full analytics consent (the worst case: every event is sent).
    const sessions = 5;
    const pages = 8;
    var time = DateTime.utc(2026, 9, 29, 8);
    DateTime tick() => time = time.add(const Duration(seconds: 37));
    final requests = <List<Map<String, Object?>>>[];
    for (var s = 0; s < sessions; s++) {
      final afterSync = <Map<String, Object?>>[
        eventJson(
          name: 'session_start',
          time: tick(),
          releaseSequence: 42,
          fields: redactFields({
            'runtime_version': '0.1.0',
            'host_build': '2026.9.29+118',
            'platform': 'android',
            'os_version': 'Linux 6.1.75-android14-11-g2b6d2c3 #1 SMP PREEMPT',
            'device_class': 'phone',
            'locale': 'de-CH',
          }),
        ),
        eventJson(
          name: 'sync_result',
          time: tick(),
          releaseSequence: 42,
          fields: {
            'duration_ms': 412,
            'bytes': 18233,
            'delta_ratio': 0.087,
            'plugins_updated': 1,
            'outcome': 'staged',
          },
        ),
      ];
      final onLeaving = <Map<String, Object?>>[
        for (var p = 0; p < pages; p++) ...[
          eventJson(
            name: 'screen_view',
            time: tick(),
            releaseSequence: 42,
            pluginKey: 'loans',
            route: 'loans/calculator-$p',
            fields: {
              'source_route': 'loans/overview-$p',
              'duration_ms': 23000 + p * 131,
            },
          ),
          eventJson(
            name: 'render_perf',
            time: tick(),
            releaseSequence: 42,
            pluginKey: 'loans',
            route: 'loans/calculator-$p',
            fields: {
              'build_ms': 6 + p,
              'first_frame_ms': 41 + p,
              'frames': 1380 + p,
              'janky_frame_pct': 1.4,
              'node_count': 212 + p,
            },
          ),
        ],
        if (s == 0)
          eventJson(
            name: 'error',
            time: tick(),
            releaseSequence: 42,
            pluginKey: 'loans',
            route: 'loans/calculator-0',
            fields: {
              'code': 'PLX-4001',
              'reason': 'NODE_BUILD_FAILED',
              'node_path': 'loans/calculator/root/1/4',
              'fingerprint': '5f0c2a9e41b37d18',
            },
          ),
        eventJson(
          name: 'session_end',
          time: tick(),
          releaseSequence: 42,
          fields: {'duration_ms': 196000},
        ),
      ];
      requests
        ..add(afterSync)
        ..add(onLeaving);
    }
    final events = requests.fold(0, (n, r) => n + r.length);
    var body = 0;
    for (final r in requests) {
      body += gzip
          .encode(
            utf8.encode(
              jsonEncode({
                'appId': '01c0c450-6c00-7000-8000-000000000001',
                'environment': 'production',
                'events': r,
              }),
            ),
          )
          .length;
    }
    final total = body + requests.length * (requestOverhead + responseOverhead);
    // The figures docs/benchmarks/p3-telemetry-cost.md reports.
    // ignore: avoid_print
    print(
      'NFR-041 typical day: $events events in ${requests.length} requests, '
      '$body bytes compressed, $total bytes with overheads',
    );
    expect(events, 5 * (2 + 2 * pages + 1) + 1);
    expect(total, lessThan(100 * 1024));
    // A quarter of the budget: the margin for heavier days and TLS.
    expect(total, lessThan(25 * 1024));
  });
}
