// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:convert';
import 'dart:io';
import 'dart:math' as math;
import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_flutter/src/core/active_release.dart';
import 'package:plux_flutter/src/core/runtime.dart';
import 'package:plux_flutter/src/render/page_renderer.dart';
import 'package:plux_flutter/src/state/providers.dart';
import 'package:plux_flutter/src/sync/sync_engine.dart';

import '../sync/fake_server.dart';

final class _Renderer implements PageRenderer {
  @override
  Widget build(
    BuildContext context,
    ActiveRelease release,
    PageRef page,
    Map<String, Object?> params, {
    bool routed = false,
  }) => Text('page ${page.route}', textDirection: TextDirection.ltr);
}

/// Always draws 0, so every sampled event is kept.
final class _Keep implements math.Random {
  @override
  double nextDouble() => 0;

  @override
  bool nextBool() => true;

  @override
  int nextInt(int max) => 0;
}

http.Client _client() => http.Client();

Future<Uint8List?> Function(String) _reader(Map<String, Uint8List> files) =>
    (p) async => files[p];

Map<String, Object?> fieldsOf(Map<String, Object?> e) => e['fields'] == null
    ? const {}
    : jsonDecode(utf8.decode(base64.decode(e['fields']! as String)))
          as Map<String, Object?>;

void main() {
  final goldens = Goldens.load();
  late FakePluxServer server;
  late String dir;
  var now = DateTime.utc(2026, 9, 29, 9);

  setUp(() async {
    server = await FakePluxServer.start();
    dir = Directory.systemTemp.createTempSync('plux_telrt').path;
    now = DateTime.utc(2026, 9, 29, 9);
  });
  tearDown(() async {
    await Plux.dispose();
    await server.close();
    Directory(dir).deleteSync(recursive: true);
  });

  Future<void> start(
    WidgetTester tester, {
    PluxConsent consent = PluxConsent.necessaryOnly,
    Map<String, double> sampling = const {},
    String app = 'loan-calculator/demo.pxb',
    Map<String, String> plugins = const {'loans': 'loan-calculator/loans.pxb'},
  }) async {
    await tester.runAsync(() async {
      server.release = null;
      await Plux.initializeWith(
        PluxConfig(
          appId: FakePluxServer.app,
          endpoint: server.endpoint,
          rootKeys: [
            PluxPublicKey(
              keyId: server.keyId,
              algorithm: 'ed25519',
              role: 'targets',
              publicKey: server.publicKey,
            ),
          ],
          storageDirectory: dir,
          httpClient: _client,
          hostBuild: '41',
          consent: consent,
          telemetrySampling: sampling,
        ),
        RuntimeOverrides(
          credentials: MemoryCredentialStore.new,
          baseline: _reader(
            await server.baseline(5, goldens.bundles[app]!, {
              for (final MapEntry(:key, :value) in plugins.entries)
                key: goldens.bundles[value]!,
            }),
          ),
          healthyAfter: const Duration(hours: 1),
          clock: () => now,
          random: _Keep(),
        ),
      );
      Plux.container.read(pluxRuntimeProvider)!.renderer = _Renderer();
      // With a release on disk the first sync runs in the background;
      // joining it lets its result reach the buffer.
      await Plux.sync();
    });
  }

  PluxRuntime rt() => Plux.container.read(pluxRuntimeProvider)!;

  Future<void> flush(WidgetTester tester) =>
      tester.runAsync(() => rt().flushTelemetry()).then((_) {});

  List<String> names() => [for (final e in server.events) e['name']! as String];

  Map<String, Object?> only(String name) =>
      server.events.firstWhere((e) => e['name'] == name);

  testWidgets('without consent, the start of a session and each sync are '
      'sent, nothing about what the user looks at [ANL-001] [SEC-161] '
      '[SYN-015] [REL-080]', (tester) async {
    await start(tester);
    await flush(tester);
    expect(names(), containsAllInOrder(['session_start', 'sync_result']));
    final start0 = only('session_start');
    expect(start0['releaseSequence'], '5');
    final f = fieldsOf(start0);
    expect(f['runtime_version'], PluxRuntimeInfo.version);
    expect(f['host_build'], '41');
    expect(f['platform'], Platform.operatingSystem);
    expect(f.containsKey('locale'), isFalse, reason: 'needs analytics consent');
    expect(fieldsOf(only('sync_result'))['outcome'], 'failed');
    expect(
      server.compressedRequests.where((p) => p.endsWith('IngestEvents')),
      isNotEmpty,
    );
    await tester.pumpWidget(
      const PluxScope(child: PluxView('loan-calculator')),
    );
    await tester.pumpWidget(const SizedBox());
    rt().didChangeAppLifecycleState(AppLifecycleState.paused);
    await flush(tester);
    expect(names(), isNot(contains('screen_view')));
    expect(names(), isNot(contains('session_end')));
  });

  testWidgets('with analytics consent, pages report their views and '
      'rendering, and leaving ends the stretch of the session '
      '[ANL-001] [RT-015]', (tester) async {
    await start(tester, consent: const PluxConsent(analytics: true));
    await tester.pumpWidget(
      const PluxScope(child: PluxView('loan-calculator')),
    );
    await tester.pump();
    now = now.add(const Duration(seconds: 3));
    await tester.pumpWidget(const SizedBox());
    rt().didChangeAppLifecycleState(AppLifecycleState.paused);
    await flush(tester);
    final view = only('screen_view');
    expect(view['route'], 'loan-calculator');
    expect(view['pluginKey'], 'loans');
    expect(fieldsOf(view).keys, containsAll(['duration_ms', 'source_route']));
    final perf = fieldsOf(only('render_perf'));
    expect(perf.keys, containsAll(['first_frame_ms', 'frames', 'node_count']));
    expect(perf['node_count'], greaterThan(0));
    expect(fieldsOf(only('session_end'))['duration_ms'], 3000);
    expect(fieldsOf(only('session_start'))['locale'], isNotNull);
    // Back within half an hour the session goes on; after it, a new one.
    now = now.add(const Duration(minutes: 5));
    rt().didChangeAppLifecycleState(AppLifecycleState.resumed);
    now = now.add(const Duration(seconds: 1));
    rt().didChangeAppLifecycleState(AppLifecycleState.paused);
    now = now.add(PluxRuntime.sessionTimeout);
    rt().didChangeAppLifecycleState(AppLifecycleState.resumed);
    await flush(tester);
    expect(names().where((n) => n == 'session_start'), hasLength(2));
    expect(
      [
        for (final e in server.events)
          if (e['name'] == 'session_end') fieldsOf(e)['duration_ms'],
      ],
      [3000, 1000],
    );
  });

  testWidgets('withdrawing consent deletes the analytics events not yet '
      'sent [SEC-161] [ANL-003]', (tester) async {
    await start(tester, consent: const PluxConsent(analytics: true));
    await tester.pumpWidget(
      const PluxScope(child: PluxView('loan-calculator')),
    );
    await tester.pumpWidget(const SizedBox());
    Plux.setConsent(PluxConsent.necessaryOnly);
    rt().telemetry.error(
      const PluxException(PluxErrorCode.bundleMalformed, 'x'),
    );
    await flush(tester);
    expect(names(), isNot(contains('screen_view')));
    expect(names(), isNot(contains('render_perf')));
    expect(names(), contains('error'));
  });

  testWidgets('a reported problem is sent as an error event [ANL-001]', (
    tester,
  ) async {
    await start(tester);
    await flush(tester);
    // The sync that found no release, reported as a problem too.
    expect([
      for (final e in server.events)
        if (e['name'] == 'error') fieldsOf(e)['code'],
    ], contains(PluxErrorCode.syncFailed.id));
  });

  testWidgets('the app bundle sets the sampling rates; the host lowers '
      'them [ANL-003]', (tester) async {
    await start(
      tester,
      sampling: const {'screen_view': 0.1, 'render_perf': 0.9},
      app: 'widgets/widgets.pxb',
      plugins: const {'gallery': 'widgets/gallery.pxb'},
    );
    final t = rt().telemetry;
    expect(t.appSampling, {'render_perf': 0.25, 'screen_view': 0.5});
    expect(t.rate('screen_view'), 0.1);
    expect(t.rate('render_perf'), 0.25);
    expect(t.rate('sync_result'), 1);
  });

  testWidgets('events recorded before dispose are kept for the next launch '
      '[ANL-002]', (tester) async {
    await start(tester);
    rt().telemetry.error(
      const PluxException(PluxErrorCode.bundleMalformed, 'late'),
    );
    await tester.runAsync(Plux.dispose);
    final buffered = Directory(dir)
        .listSync(recursive: true)
        .whereType<File>()
        .singleWhere((f) => f.path.endsWith('telemetry/events.jsonl'));
    expect(buffered.readAsStringSync(), contains('"name":"error"'));
  });
}
