// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:async';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:plux_flutter/src/data/stream_transport.dart';

/// How many connections the host's WebSocket client opened; a test
/// counter, reset by each test.
int _connections = 0;

/// The host's factory: a client that counts the connections it makes.
HttpClient _countingClient() =>
    HttpClient()
      ..connectionFactory = (uri, proxyHost, proxyPort) {
        _connections++;
        return Socket.startConnect(uri.host, uri.port);
      };

StreamRequest _request(HttpServer server) => StreamRequest(
  kind: StreamKind.webSocket,
  url: Uri.parse('ws://127.0.0.1:${server.port}/live'),
  maxMessageBytes: 1000,
  connectTimeout: const Duration(seconds: 5),
);

void main() {
  setUp(() => _connections = 0);

  test('WebSockets connect with the host-supplied client [DAT-012]', () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(() => server.close(force: true));
    server.listen((r) async {
      final ws = await WebSocketTransformer.upgrade(r);
      ws.listen((m) => ws.add('echo $m'));
    });
    final client = http.Client();
    addTearDown(client.close);
    final transport = SocketStreamTransport(
      client,
      webSocketClient: _countingClient,
    );

    final connection = await transport.open(_request(server));
    addTearDown(connection.close);
    final first = connection.frames.first;
    connection.send('hello');

    expect((await first).data, isNotNull);
    expect(_connections, 1);
  });

  test(
    'WebSockets use the dart:io default when no client is supplied [DAT-012]',
    () async {
      final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      addTearDown(() => server.close(force: true));
      server.listen((r) async {
        final ws = await WebSocketTransformer.upgrade(r);
        ws.listen((_) {});
      });
      final client = http.Client();
      addTearDown(client.close);

      final connection = await SocketStreamTransport(client)
          .open(_request(server));
      unawaited(connection.close());

      expect(_connections, 0);
    },
  );
}
