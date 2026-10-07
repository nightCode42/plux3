// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/actions/handlers.dart';
import 'package:plux_flutter/src/data/client.dart';
import 'package:plux_flutter/src/data/failure.dart';
import 'package:plux_flutter/src/data/services.dart';
import 'package:plux_flutter/src/data/source.dart';
import 'package:plux_flutter/src/data/spec.dart';
import 'package:plux_flutter/src/data/sse.dart';
import 'package:plux_flutter/src/data/stream_session.dart';
import 'package:plux_flutter/src/data/stream_transport.dart';
import 'package:plux_flutter/src/data/transport.dart';
import 'package:plux_flutter/src/pxl/types.dart';
import 'package:plux_flutter/src/schema/registry.g.dart';

import 'io_support.dart';

/// A local WebSocket server that records what it receives.
final class _WsServer {
  late HttpServer server;
  final List<WebSocket> sockets = [];
  final List<String> received = [];
  final List<Uri> uris = [];
  final List<String?> protocols = [];
  int refuse = 0;
  int requests = 0;
  int closed = 0;
  void Function(WebSocket ws, String message)? onMessage;

  Future<void> start() async {
    server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    server.listen((r) async {
      requests++;
      if (refuse > 0) {
        refuse--;
        r.response.statusCode = 503;
        await r.response.close();
        return;
      }
      uris.add(r.uri);
      protocols.add(r.headers.value('sec-websocket-protocol'));
      final offered = r.headers['sec-websocket-protocol'];
      final ws = await WebSocketTransformer.upgrade(
        r,
        protocolSelector: offered == null ? null : (list) => list.first,
      );
      sockets.add(ws);
      ws.listen((m) {
        received.add(m as String);
        onMessage?.call(ws, m);
      }, onDone: () => closed++);
    });
  }

  Uri get url => Uri.parse('ws://127.0.0.1:${server.port}/live');
  Future<void> stop() => server.close(force: true);
}

/// A local SSE server that plays one script per connection.
final class _SseServer {
  late HttpServer server;
  final List<String?> lastEventIds = [];
  final List<String> scripts = [];
  int status = 200;

  Future<void> start() async {
    server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    server.listen((r) async {
      lastEventIds.add(r.headers.value('last-event-id'));
      if (status != 200) {
        r.response.statusCode = status;
        await r.response.close();
        return;
      }
      r.response.headers.contentType = ContentType('text', 'event-stream');
      if (scripts.isNotEmpty) r.response.write(scripts.removeAt(0));
      await r.response.close();
    });
  }

  Uri get url => Uri.parse('http://127.0.0.1:${server.port}/feed');
  Future<void> stop() => server.close(force: true);
}

final class _Collected {
  final List<Object?> messages = [];
  final List<StreamStatus> statuses = [];
  final List<DataFailure?> ends = [];
  final List<DataFailure> problems = [];
}

final class _Events implements DataSourceEvents, DataIoEvents {
  final List<String> seen = [];
  final List<Object?> values = [];

  @override
  void loaded(DataSourceEvent e) => seen.add('loaded ${e.source}');

  @override
  void failed(DataSourceEvent e) => seen.add('failed ${e.source}');

  @override
  void message(DataSourceEvent e) {
    seen.add('message ${e.source}');
    values.add(e.value);
  }

  @override
  void progress(DataSourceEvent e) => seen.add('progress ${e.source}');

  @override
  void outbox(OutboxOutcome o, DataSourceEvent e) =>
      seen.add('${o.name} ${e.source}');
}

final types = <String, NamedType>{
  'Task': NamedType.object('Task')
    ..fields!.addAll({
      'id': const PxlType(PxlKind.string),
      'title': const PxlType(PxlKind.string),
      'done': const PxlType(PxlKind.bool),
    }),
};

void main() {
  late FakeScheduler sched;
  late _Collected got;
  late http.Client client;
  final sessions = <StreamSession>[];

  setUp(() {
    sched = FakeScheduler();
    got = _Collected();
    client = http.Client();
  });

  tearDown(() async {
    for (final s in sessions) {
      await s.stop();
    }
    sessions.clear();
    client.close();
  });

  StreamSession make({
    required StreamKind kind,
    required Uri url,
    StreamWire wire = StreamWire.plain,
    Object? subscribe,
    int max = 10000,
    bool Function()? network,
  }) {
    final s = StreamSession(
      transport: SocketStreamTransport(client),
      open: (lastId) async => StreamRequest(
        kind: kind,
        url: url,
        protocols: wire == StreamWire.graphql
            ? const [graphqlWsProtocol]
            : const [],
        lastEventId: lastId,
        maxMessageBytes: max,
        connectTimeout: const Duration(seconds: 5),
      ),
      wire: wire,
      subscribe: subscribe,
      minBackoff: const Duration(milliseconds: 100),
      maxBackoff: const Duration(milliseconds: 400),
      onMessage: got.messages.add,
      onStatus: got.statuses.add,
      onEnd: got.ends.add,
      onProblem: got.problems.add,
      random: TopRandom(),
      scheduler: sched.call,
      networkAvailable: network,
    );
    sessions.add(s);
    return s;
  }

  test('the SSE parser reads chunks of any size, CR, LF and CRLF, comments, multi-line data, ids and retry [DAT-012]', () {
    final p = SseParser(maxDataBytes: 1000);
    expect(p.add('\u{FEFF}: a comment\r\nda'), isEmpty);
    final first = p.add('ta: one\r\ndata:  two\rid: 7\r\n\r\n');
    expect(first, hasLength(1));
    expect(first.single.data, 'one\n two');
    expect(first.single.event, 'message');
    expect(first.single.id, '7');
    // A CRLF split between chunks is one line end.
    expect(p.add('event: tick\ndata: x\r'), isEmpty);
    final second = p.add('\n\r\nretry: 2500\n\n');
    expect(second.single.event, 'tick');
    expect(second.single.data, 'x');
    expect(second.single.id, '7', reason: 'the last event ID persists');
    expect(p.retryMs, 2500);
    // An event without data, and a bad retry, dispatch nothing.
    expect(p.add('event: lonely\n\nretry: soon\n\n'), isEmpty);
    expect(p.retryMs, 2500);
    expect(p.add('data\n\n').single.data, '');
  });

  test(
    'the SSE parser refuses an event over its bound [DAT-012] [LIM-004]',
    () {
      final p = SseParser(maxDataBytes: 10);
      expect(
        () => p.add('data: 0123456789ab\n\n'),
        throwsA(isA<SseTooLarge>()),
      );
    },
  );

  test('SSE: multi-line data is one message, retry sets the wait, and the last event ID is sent again [DAT-012]', () async {
    final srv = _SseServer();
    await srv.start();
    addTearDown(srv.stop);
    srv.scripts
      ..add('retry: 1500\n\nid: 41\ndata: {"a":\ndata: 1}\n\n')
      ..add('id: 42\ndata: "next"\n\n');
    final s = make(kind: StreamKind.sse, url: srv.url);
    s.start();
    await s.ready;
    await eventually(() => got.messages.isNotEmpty, 'the first message');
    expect(got.messages.first, {'a': 1});
    // The server ended the response: the session waits `retry` and
    // connects again with the last event ID.
    await eventually(() => sched.timers.isNotEmpty, 'a reconnection timer');
    expect(sched.delays.single, const Duration(milliseconds: 400));
    expect(
      got.statuses,
      containsAllInOrder([StreamStatus.open, StreamStatus.reconnecting]),
    );
    sched.timers.single.fire();
    await eventually(() => got.messages.length == 2, 'the second message');
    expect(got.messages.last, 'next');
    expect(srv.lastEventIds, [null, '41']);
  });

  test(
    'SSE: a client error ends the stream, a server error is retried [DAT-012]',
    () async {
      final srv = _SseServer();
      await srv.start();
      addTearDown(srv.stop);
      srv.status = 404;
      final gone = make(kind: StreamKind.sse, url: srv.url);
      gone.start();
      await expectLater(
        gone.ready,
        throwsA(
          isA<DataFailure>()
              .having((f) => f.code, 'code', PluxErrorCode.dataStreamFailed)
              .having((f) => f.status, 'status', 404),
        ),
      );
      expect(got.ends.single!.code, PluxErrorCode.dataStreamFailed);
      expect(gone.status, StreamStatus.closed);
      expect(sched.timers, isEmpty);

      srv.status = 503;
      final flaky = make(kind: StreamKind.sse, url: srv.url);
      flaky.start();
      await flaky.ready;
      await eventually(() => sched.active.isNotEmpty, 'a reconnection timer');
      expect(flaky.status, StreamStatus.reconnecting);
      expect(sched.delays.single, const Duration(milliseconds: 100));
    },
  );

  test('WebSocket: a lost connection reconnects with doubling, jittered waits up to the maximum, and subscribes again [DAT-012]', () async {
    final srv = _WsServer();
    await srv.start();
    addTearDown(srv.stop);
    final s = make(
      kind: StreamKind.webSocket,
      url: srv.url,
      subscribe: {'op': 'watch'},
    );
    s.start();
    await s.ready;
    await eventually(() => srv.received.length == 1, 'the subscribe message');
    expect(jsonDecode(srv.received.single), {'op': 'watch'});
    srv.sockets[0].add('{"n":1}');
    await eventually(() => got.messages.length == 1, 'a message');
    expect(got.messages.single, {'n': 1});

    // Connection one delivered a message, so the wait starts again at the
    // minimum; connections that deliver nothing double it, up to the maximum.
    final waits = <Duration>[];
    for (var i = 0; i < 4; i++) {
      await srv.sockets[i].close();
      await eventually(() => sched.active.isNotEmpty, 'a timer, round $i');
      waits.add(sched.active.first.delay);
      sched.active.first.fire();
      await eventually(
        () => srv.sockets.length == i + 2,
        'connection ${i + 2}',
      );
    }
    expect(waits, const [
      Duration(milliseconds: 100),
      Duration(milliseconds: 200),
      Duration(milliseconds: 400),
      Duration(milliseconds: 400),
    ]);
    await eventually(() => srv.received.length == 5, 'five subscriptions');
    expect(srv.received.toSet(), {
      jsonEncode({'op': 'watch'}),
    });
    expect(got.ends, isEmpty);
  });

  test('WebSocket: a refused handshake backs off, and the first message resets the wait [DAT-012]', () async {
    final srv = _WsServer()..refuse = 3;
    await srv.start();
    addTearDown(srv.stop);
    final s = make(kind: StreamKind.webSocket, url: srv.url);
    s.start();
    await s.ready;
    for (var i = 0; i < 3; i++) {
      await sched.fireNext();
      await eventually(() => srv.requests == i + 2, 'attempt ${i + 2}');
      if (i < 2) {
        await eventually(
          () => sched.timers.length == i + 2,
          'a timer after attempt ${i + 2}',
        );
      }
    }
    await eventually(() => srv.sockets.length == 1, 'the connection');
    expect(sched.delays.take(3), const [
      Duration(milliseconds: 100),
      Duration(milliseconds: 200),
      Duration(milliseconds: 400),
    ]);
    await eventually(() => s.status == StreamStatus.open, 'open');
    srv.sockets.single.add('1');
    await eventually(() => got.messages.isNotEmpty, 'a message');
    await srv.sockets.single.close();
    await eventually(() => sched.timers.length == 4, 'the next timer');
    expect(sched.delays.last, const Duration(milliseconds: 100));
  });

  test('a message over data.streamMessageSize closes the stream with PLX-5111 and does not reconnect [DAT-012] [LIM-004]', () async {
    final srv = _WsServer();
    await srv.start();
    addTearDown(srv.stop);
    final s = make(kind: StreamKind.webSocket, url: srv.url, max: 50);
    s.start();
    await s.ready;
    await eventually(() => srv.sockets.isNotEmpty, 'the connection');
    srv.sockets.single.add(jsonEncode({'a': 'x' * 200}));
    await eventually(() => got.ends.isNotEmpty, 'the end');
    expect(got.ends.single!.code, PluxErrorCode.dataStreamMessageTooLarge);
    expect(s.status, StreamStatus.closed);
    expect(sched.timers, isEmpty);
    expect(got.messages, isEmpty);

    final sse = _SseServer();
    await sse.start();
    addTearDown(sse.stop);
    sse.scripts.add('data: ${'y' * 200}\n\n');
    got = _Collected();
    final e = make(kind: StreamKind.sse, url: sse.url, max: 50);
    e.start();
    await eventually(() => got.ends.isNotEmpty, 'the SSE end');
    expect(got.ends.single!.code, PluxErrorCode.dataStreamMessageTooLarge);
  });

  test('graphql-transport-ws: init, ack, subscribe, ping, next; and the same handshake again after a reconnect [DAT-012]', () async {
    final srv = _WsServer();
    srv.onMessage = (ws, m) {
      final j = jsonDecode(m) as Map<String, Object?>;
      switch (j['type']) {
        case 'connection_init':
          ws.add(jsonEncode({'type': 'connection_ack'}));
        case 'subscribe':
          ws
            ..add(jsonEncode({'type': 'ping'}))
            ..add(
              jsonEncode({
                'id': '1',
                'type': 'next',
                'payload': {
                  'data': {'tick': 1},
                },
              }),
            );
      }
    };
    await srv.start();
    addTearDown(srv.stop);
    final s = make(
      kind: StreamKind.webSocket,
      url: srv.url,
      wire: StreamWire.graphql,
      subscribe: {
        'query': 'subscription T { tick }',
        'variables': {'room': 'a'},
      },
    );
    s.start();
    await s.ready;
    await eventually(() => got.messages.length == 1, 'a message');
    expect(got.messages.single, {'tick': 1});
    expect(srv.protocols.single, graphqlWsProtocol);
    await eventually(() => srv.received.length == 3, 'init, subscribe, pong');
    final types = [
      for (final m in srv.received)
        (jsonDecode(m) as Map<String, Object?>)['type'],
    ];
    expect(types, ['connection_init', 'subscribe', 'pong']);
    final sub = jsonDecode(srv.received[1]) as Map<String, Object?>;
    expect(sub['id'], '1');
    expect(sub['payload'], {
      'query': 'subscription T { tick }',
      'variables': {'room': 'a'},
    });

    await srv.sockets[0].close();
    await sched.fireNext();
    await eventually(
      () => got.messages.length == 2,
      'a message after reconnect',
    );
    expect(
      [
        for (final m in srv.received)
          (jsonDecode(m) as Map<String, Object?>)['type'],
      ].where((t) => t == 'subscribe').length,
      2,
      reason: 'the subscription is made again',
    );
  });

  test('graphql-transport-ws: an error message ends the subscription with PLX-5106, a complete message ends it quietly; a 4401 close is final [DAT-012]', () async {
    final srv = _WsServer();
    var mode = 'error';
    srv.onMessage = (ws, m) {
      final j = jsonDecode(m) as Map<String, Object?>;
      if (j['type'] == 'connection_init') {
        ws.add(jsonEncode({'type': 'connection_ack'}));
      } else if (j['type'] == 'subscribe') {
        switch (mode) {
          case 'error':
            ws.add(
              jsonEncode({
                'id': '1',
                'type': 'error',
                'payload': [
                  {'message': 'secret detail'},
                ],
              }),
            );
          case 'complete':
            ws.add(jsonEncode({'id': '1', 'type': 'complete'}));
          default:
            unawaited(ws.close(4401, 'Unauthorized'));
        }
      }
    };
    await srv.start();
    addTearDown(srv.stop);

    Future<StreamSession> run() async {
      got = _Collected();
      final s = make(
        kind: StreamKind.webSocket,
        url: srv.url,
        wire: StreamWire.graphql,
        subscribe: {'query': 'subscription T { tick }'},
      );
      s.start();
      await eventually(() => s.status == StreamStatus.closed, 'closed');
      return s;
    }

    await run();
    expect(got.ends.single!.code, PluxErrorCode.dataGraphqlError);
    expect(got.ends.single!.message, isNot(contains('secret')));
    mode = 'complete';
    await run();
    expect(got.ends.single, isNull);
    mode = 'close';
    await run();
    expect(got.ends.single!.code, PluxErrorCode.dataStreamFailed);
    expect(sched.timers, isEmpty, reason: 'none of them reconnects');
  });

  test('while the host says the network is down a session waits, and nudge connects at once [DAT-012]', () async {
    final srv = _WsServer();
    await srv.start();
    addTearDown(srv.stop);
    var up = false;
    final s = make(kind: StreamKind.webSocket, url: srv.url, network: () => up);
    s.start();
    await s.ready;
    expect(s.status, StreamStatus.reconnecting);
    expect(srv.requests, 0);
    up = true;
    s.nudge();
    await eventually(() => s.status == StreamStatus.open, 'open');
    expect(srv.requests, 1);
  });

  group('sources on the data services', () {
    late _WsServer srv;
    late DataServices services;
    late _Events events;
    late List<PluxException> reports;
    const caller = DataCaller(pluginKey: 'shop', domains: ['127.0.0.1']);

    DataSourceSpec spec({String name = 'orders'}) => DataSourceSpec(
      id: '$name-id',
      name: name,
      kind: DataKind.webSocket,
      type: 'Task',
      baseUrls: {'production': 'http://127.0.0.1:${srv.server.port}/v1'},
      path: '/orders/{orderId}',
      params: {'orderId': (_) => 'o1'},
      subscribeMessage: const {'op': 'watch'},
    );

    DataSourceController controller(DataSourceSpec s) => DataSourceController(
      spec: s,
      context: services,
      caller: caller,
      types: types,
    );

    setUp(() async {
      srv = _WsServer();
      await srv.start();
      events = _Events();
      reports = [];
      services = DataServices(
        transport: ClientTransport(http.Client()),
        authDelegate: () => null,
        environment: 'production',
        record: (name, {fields = const {}, route = '', pluginKey = ''}) {},
        report: reports.add,
        events: events,
        ioEvents: events,
        streamTransport: SocketStreamTransport(http.Client()),
        random: TopRandom(),
        scheduler: sched.call,
        allowCleartext: true,
      );
    });

    tearDown(() async {
      await services.close();
      await srv.stop();
    });

    test('subscribe binds the stream to state: messages are mapped, announced and read as data.<name>; unsubscribe closes it [DAT-012]', () async {
      final c = controller(spec());
      addTearDown(c.dispose);
      await c.subscribe(() => const {}, {'since': 5});
      expect(c.subscribed, isTrue);
      await eventually(() => srv.received.isNotEmpty, 'the subscribe message');
      expect(srv.uris.single.path, '/v1/orders/o1');
      expect(srv.uris.single.queryParameters, {'since': '5'});
      srv.sockets.single.add(
        jsonEncode({'id': '1', 'title': 'One', 'done': false, 'extra': 1}),
      );
      await eventually(() => events.values.isNotEmpty, 'the message event');
      expect(c.snapshot['value'], {'id': '1', 'title': 'One', 'done': false});
      expect(c.snapshot['status'], 'ready');
      expect(events.seen, ['message orders']);

      // A message that does not map fails the source but not the stream.
      srv.sockets.single.add(jsonEncode({'id': 1}));
      await eventually(() => events.seen.length == 2, 'the failure');
      expect(events.seen.last, 'failed orders');
      expect(c.subscribed, isTrue);

      await c.unsubscribe();
      expect(c.subscribed, isFalse);
      await eventually(() => srv.closed == 1, 'the server sees the close');
      expect(services.streams!.openCount, 0);
    });

    test('a stream to an undeclared domain never connects and is reported with PLX-5100 [DAT-012] [DAT-030]', () async {
      final c = DataSourceController(
        spec: spec(),
        context: services,
        caller: const DataCaller(pluginKey: 'shop', domains: ['example.com']),
        types: types,
      );
      addTearDown(c.dispose);
      await expectLater(
        c.subscribe(() => const {}, const {}),
        throwsA(
          isA<DataFailure>().having(
            (f) => f.code,
            'code',
            PluxErrorCode.dataDomainBlocked,
          ),
        ),
      );
      expect(srv.requests, 0);
      expect(
        reports.map((e) => e.code),
        contains(PluxErrorCode.dataDomainBlocked),
      );
      expect(c.subscribed, isFalse);
    });

    test('data.streamsOpen bounds the open streams with PLX-5112 [DAT-012] [LIM-004]', () async {
      services.limits = {'data.streamsOpen': 1};
      final a = controller(spec());
      final b = controller(spec(name: 'other'));
      addTearDown(a.dispose);
      addTearDown(b.dispose);
      await a.subscribe(() => const {}, const {});
      await expectLater(
        b.subscribe(() => const {}, const {}),
        throwsA(
          isA<DataFailure>().having(
            (f) => f.code,
            'code',
            PluxErrorCode.dataStreamLimit,
          ),
        ),
      );
      expect(services.streams!.openCount, 1);
      await a.unsubscribe();
      await b.subscribe(() => const {}, const {});
      expect(b.subscribed, isTrue);
    });

    test('the subscribe and unsubscribe handlers run on the page data; a scope closes its streams with it [DAT-012]', () async {
      final c = controller(spec());
      final scope = DataScope(
        services: services,
        own: [c],
        shared: const [],
        roots: () => const {},
      );
      final ctx = StepContext(
        navigator: NoNavigator(),
        emit: (_, _) {},
        nativeActions: const NoNativeActions(),
        data: scope,
      );
      ActionHandler of(String name) =>
          handlerFor(actionDescriptors.firstWhere((d) => d.name == name));
      await of('subscribe').run(ctx, {
        'stream': 'orders',
        'params': {'since': 1},
      });
      expect(c.subscribed, isTrue);
      await expectLater(
        Future.sync(() => of('subscribe').run(ctx, {'stream': 'nope'})),
        throwsA(isA<ActionError>()),
      );
      await of('unsubscribe').run(ctx, {'stream': 'orders'});
      expect(c.subscribed, isFalse);

      await of('subscribe').run(ctx, {'stream': 'orders'});
      scope.dispose();
      await eventually(() => services.streams!.openCount == 0, 'closed');
    });
  });
}
