// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:convert';
import 'dart:io';
import 'dart:math' as math;

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_flutter/src/sync/api_client.dart';
import 'package:plux_flutter/src/telemetry/events.dart';
import 'package:plux_flutter/src/telemetry/outbox.dart';
import 'package:plux_flutter/src/telemetry/recorder.dart';

import '../sync/fake_server.dart';

/// A generator whose draws are given.
final class _Draws implements math.Random {
  _Draws(this.values);
  final List<double> values;
  var _i = 0;

  @override
  double nextDouble() => values[_i++ % values.length];

  @override
  bool nextBool() => nextDouble() < 0.5;

  @override
  int nextInt(int max) => (nextDouble() * max).floor();
}

Map<String, Object?> fieldsOf(Map<String, Object?> event) =>
    jsonDecode(utf8.decode(base64.decode(event['fields']! as String)))
        as Map<String, Object?>;

void main() {
  group('events', () {
    test('only flat, well-named, non-sensitive scalars are kept '
        '[ANL-003] [SCH-012]', () {
      final long = 'x' * (maxTelemetryText + 1);
      final out = redactFields({
        'route': '/a',
        'duration_ms': 12,
        'ratio': 0.5,
        'ok': true,
        'nan': double.nan,
        'password_hint': 'x',
        'auth_token': 'y',
        'user_email': 'a@b.c',
        'Bad-Name': 1,
        'nested': {'a': 1},
        'list': [1],
        'long': long,
        'none': null,
      });
      expect(out, {'duration_ms': 12, 'ok': true, 'ratio': 0.5, 'route': '/a'});
      expect(out.keys.toList(), ['duration_ms', 'ok', 'ratio', 'route']);
      final many = redactFields({for (var i = 0; i < 40; i++) 'f$i': i});
      expect(many, hasLength(maxTelemetryFields));
    });

    test('text is cut at a character boundary within the byte bound', () {
      final s = clipText('é' * 200); // 400 bytes
      expect(utf8.encode(s).length, lessThanOrEqualTo(maxTelemetryText));
      expect(s, 'é' * 128);
      expect(clipText('short'), 'short');
    });

    test('every catalogued event has a category; operational ones need no '
        'consent [SEC-161]', () {
      expect(
        telemetryCategories.keys,
        containsAll([
          'session_start',
          'session_end',
          'screen_view',
          'render_perf',
          'action_run',
          'api_call',
          'function_call',
          'sync_result',
          'error',
          'experiment_exposure',
          'rasp_detection',
          'custom',
        ]),
      );
      for (final n in ['session_start', 'sync_result', 'error']) {
        expect(telemetryCategories[n], TelemetryCategory.necessary);
      }
      for (final n in ['session_end', 'screen_view', 'render_perf']) {
        expect(telemetryCategories[n], TelemetryCategory.analytics);
      }
    });

    test('an event is Connect JSON with its fields as canonical JSON bytes, '
        'and a buffered line round-trips', () {
      final e = eventJson(
        name: 'screen_view',
        time: DateTime.utc(2026, 9, 29, 12),
        fields: {'b': 1, 'a': 'x'},
        releaseSequence: 7,
        route: '/r',
      );
      expect(e['time'], '2026-09-29T12:00:00.000Z');
      expect(e['releaseSequence'], '7');
      expect(e.containsKey('pluginKey'), isFalse);
      expect(
        utf8.decode(base64.decode(e['fields']! as String)),
        '{"b":1,"a":"x"}',
      );
      final line = TelemetryLine(TelemetryCategory.analytics, e);
      final back = TelemetryLine.parse(line.encode())!;
      expect(back.category, TelemetryCategory.analytics);
      expect(back.event, e);
      expect(TelemetryLine.parse('not json'), isNull);
      expect(TelemetryLine.parse('{"c":"nope","e":{}}'), isNull);
      expect(TelemetryLine.parse('[1]'), isNull);
    });
  });

  group('recorder', () {
    late List<TelemetryLine> posted;
    late List<Set<TelemetryCategory>> withdrawn;

    TelemetryRecorder recorder({
      PluxConsent consent = PluxConsent.necessaryOnly,
      Map<String, double> host = const {},
      List<double> draws = const [0.0],
    }) {
      posted = [];
      withdrawn = [];
      return TelemetryRecorder(
        post: (lines) =>
            posted.addAll(lines.map((l) => TelemetryLine.parse(l)!)),
        withdraw: withdrawn.add,
        consent: consent,
        hostSampling: host,
        random: _Draws(draws),
        clock: () => DateTime.utc(2026),
      );
    }

    test('without consent only operational events are recorded; withdrawing '
        'consent deletes the buffered ones [SEC-161] [ANL-003]', () {
      final r = recorder();
      expect(r.record('session_start'), isTrue);
      expect(r.record('screen_view', route: '/a'), isFalse);
      expect(r.record('experiment_exposure'), isFalse);
      expect(r.record('not_an_event'), isFalse);
      r.consent = const PluxConsent(analytics: true, experiments: true);
      expect(r.record('screen_view', route: '/a'), isTrue);
      expect(r.record('experiment_exposure'), isTrue);
      expect(withdrawn, isEmpty);
      r.consent = const PluxConsent(experiments: true);
      expect(withdrawn, [
        {TelemetryCategory.analytics},
      ]);
      r.consent = PluxConsent.necessaryOnly;
      expect(withdrawn.last, {TelemetryCategory.experiments});
      expect(posted.map((l) => l.event['name']), [
        'session_start',
        'screen_view',
        'experiment_exposure',
      ]);
    });

    test('sampling keeps the lower of the app and host rates, and never '
        'samples operational events [ANL-003]', () {
      final r = recorder(
        consent: const PluxConsent(analytics: true),
        host: {'screen_view': 0.25, 'error': 0},
        draws: [0.3, 0.2, 0.9, 0.99],
      )..appSampling = {'screen_view': 0.5, 'render_perf': 0.1};
      expect(r.rate('screen_view'), 0.25);
      expect(r.rate('render_perf'), 0.1);
      expect(r.rate('session_end'), 1);
      expect(r.rate('error'), 1);
      expect(r.record('screen_view'), isFalse); // 0.3 ≥ 0.25
      expect(r.record('screen_view'), isTrue); // 0.2 < 0.25
      expect(r.record('error'), isTrue); // never sampled
      expect(r.record('session_end'), isTrue); // rate 1
    });

    test('an error event carries its code, node path, route and fingerprint, '
        'never its message [ANL-001] [SCH-012]', () {
      final r = recorder()..releaseSequence = 9;
      const e = PluxException(
        PluxErrorCode.nodeBuildFailed,
        'node 3: the user typed secret@example.com',
        details: {'node': 'page/3', 'route': '/loans', 'plugin': 'loans'},
      );
      expect(r.error(e), isTrue);
      final ev = posted.single.event;
      expect(ev['name'], 'error');
      expect(ev['route'], '/loans');
      expect(ev['pluginKey'], 'loans');
      expect(ev['releaseSequence'], '9');
      final f = fieldsOf(ev);
      expect(f['code'], PluxErrorCode.nodeBuildFailed.id);
      expect(f['reason'], PluxErrorCode.nodeBuildFailed.reason);
      expect(f['node_path'], 'page/3');
      expect(
        f['fingerprint'],
        TelemetryRecorder.fingerprint(
          PluxErrorCode.nodeBuildFailed.id,
          'page/3',
          '/loans',
        ),
      );
      expect(f['fingerprint'], hasLength(16));
      expect(jsonEncode(ev), isNot(contains('secret@example.com')));
    });
  });

  group('outbox', () {
    late FakePluxServer server;
    late Directory dir;
    late PluxApiClient api;
    late http.Client client;

    setUp(() async {
      server = await FakePluxServer.start();
      dir = Directory.systemTemp.createTempSync('plux_tel');
      client = http.Client();
      api = PluxApiClient(client, server.endpoint);
    });
    tearDown(() async {
      client.close();
      await server.close();
      dir.deleteSync(recursive: true);
    });

    String line(int i, [TelemetryCategory c = TelemetryCategory.necessary]) =>
        TelemetryLine(
          c,
          eventJson(name: 'error', time: DateTime.utc(2026), fields: {'n': i}),
        ).encode();

    Future<TelemetryFlush> flush(TelemetryOutbox o, {int perRequest = 2}) =>
        o.flush(
          api: api,
          token: () async => 'plux_dat_dev',
          appId: FakePluxServer.app,
          environment: 'production',
          perRequest: perRequest,
        );

    test('the buffer keeps the newest events within its byte bound '
        '[ANL-002] [LIM-001]', () {
      final o = TelemetryOutbox(dir.path);
      final one = utf8.encode(line(0)).length + 1;
      o.append([line(0), line(1)], one * 3);
      expect(o.length, 2);
      o.append([line(2), line(3)], one * 3);
      expect(o.length, 3);
      final kept = File('${dir.path}/telemetry/events.jsonl')
          .readAsLinesSync()
          .map((l) => fieldsOf(TelemetryLine.parse(l)!.event)['n']);
      expect(kept, [1, 2, 3]);
      o.append([], one);
      expect(o.length, 3);
    });

    test('withdrawn categories are deleted from the buffer [SEC-161]', () {
      final o = TelemetryOutbox(dir.path)
        ..append([
          line(0),
          line(1, TelemetryCategory.analytics),
          line(2, TelemetryCategory.experiments),
        ], 1 << 20)
        ..purge({TelemetryCategory.analytics});
      expect(o.length, 2);
      o.purge({TelemetryCategory.experiments, TelemetryCategory.analytics});
      expect(o.length, 1);
      TelemetryOutbox('${dir.path}/none').purge({TelemetryCategory.analytics});
    });

    test('events are sent in gzip-compressed batches and removed once the '
        'server answers [ANL-002]', () async {
      final o = TelemetryOutbox(dir.path)
        ..append([for (var i = 0; i < 5; i++) line(i)], 1 << 20);
      final r = await flush(o);
      expect((r.sent, r.dropped, r.left, r.error), (5, 0, 0, null));
      expect(server.events.map((e) => fieldsOf(e)['n']), [0, 1, 2, 3, 4]);
      expect(
        server.compressedRequests.where((p) => p.endsWith('IngestEvents')),
        hasLength(3),
      );
      expect((await flush(o)).sent, 0);
    });

    test('a retryable failure or a lost token keeps the events; a batch '
        'refused for good is dropped [ANL-002]', () async {
      final o = TelemetryOutbox(dir.path)
        ..append([for (var i = 0; i < 4; i++) line(i)], 1 << 20);
      const path = '/plux.v1.TelemetryService/IngestEvents';
      server.faults[path] = [const StatusFault(503)];
      var r = await flush(o);
      expect((r.sent, r.left, r.error?.status), (0, 4, 503));
      server.faults[path] = [const StatusFault(400, code: 'invalid_argument')];
      r = await flush(o);
      expect((r.sent, r.dropped, r.left), (2, 2, 0));
      expect(server.events.map((e) => fieldsOf(e)['n']), [2, 3]);
      o.append([line(9)], 1 << 20);
      r = await o.flush(
        api: api,
        token: () async => 'wrong',
        appId: FakePluxServer.app,
        environment: 'production',
        perRequest: 10,
      );
      expect((r.left, r.error?.code), (1, 'unauthenticated'));
    });

    test('at most a few batches go per flush, so a long-offline device '
        'catches up over several', () async {
      final o = TelemetryOutbox(dir.path)
        ..append([
          for (var i = 0; i < TelemetryOutbox.maxBatches + 3; i++) line(i),
        ], 1 << 20);
      final r = await flush(o, perRequest: 1);
      expect((r.sent, r.left), (TelemetryOutbox.maxBatches, 3));
    });
  });
}
