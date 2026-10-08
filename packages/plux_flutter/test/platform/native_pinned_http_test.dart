// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:convert';

import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/platform/native_pinned_http.dart';
import 'package:plux_flutter/src/security/pins.dart';

import '../support/test_certificate.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  const channel = MethodChannel('test.plux/native_http');
  final calls = <MethodCall>[];
  late Object? Function(MethodCall) answer;
  late PinSet pins;
  late NativePinnedClient client;
  late List<Uint8List> chunks;

  setUp(() {
    calls.clear();
    chunks = [
      Uint8List.fromList(utf8.encode('hel')),
      Uint8List.fromList(utf8.encode('lo')),
    ];
    pins = PinSet([unrelatedPin(2), unrelatedPin(1)]);
    client = NativePinnedClient(
      pins: pins,
      userAgent: 'plux-test',
      channel: channel,
    );
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(channel, (call) async {
          calls.add(call);
          return answer(call);
        });
  });
  tearDown(() {
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(channel, null);
  });

  Object? serve(MethodCall call, {Map<String, String>? headers}) {
    switch (call.method) {
      case 'httpOpen':
        return {
          'id': 7,
          'status': 200,
          'headers': headers ?? {'Content-Type': 'text/plain'},
        };
      case 'httpRead':
        return chunks.isEmpty ? null : chunks.removeAt(0);
    }
    return null;
  }

  // Verifies: SEC-041, SYN-010.
  test('a request goes to the platform with the pins in force and its body '
      'is pulled chunk by chunk [SEC-041] [SYN-010]', () async {
    answer = serve;
    final res = await client.post(
      Uri.parse('https://plux.example/x'),
      headers: {'Content-Type': 'application/json', 'User-Agent': 'host/1'},
      body: 'abc',
    );
    expect(res.statusCode, 200);
    expect(res.body, 'hello');
    expect(res.headers['content-type'], 'text/plain');
    final open = calls.first;
    expect(open.method, 'httpOpen');
    final args = open.arguments as Map<Object?, Object?>;
    expect(args['url'], 'https://plux.example/x');
    expect(args['method'], 'POST');
    expect(args['userAgent'], 'host/1');
    expect(args['headers'], {'Content-Type': 'application/json'});
    expect(args['body'], Uint8List.fromList(utf8.encode('abc')));
    expect(args['pins'], ([unrelatedPin(1), unrelatedPin(2)]..sort()));
    expect(
      [for (final c in calls) c.method],
      ['httpOpen', 'httpRead', 'httpRead', 'httpRead', 'httpClose'],
    );
  });

  // Verifies: SEC-041.
  test('a replaced pin set reaches the platform with the next request '
      '[SEC-041] [SEC-050]', () async {
    answer = serve;
    await client.get(Uri.parse('https://plux.example/'));
    chunks.add(Uint8List(1));
    pins.replace([unrelatedPin(3), unrelatedPin(4)], version: 1);
    await client.get(Uri.parse('https://plux.example/'));
    final sent = [
      for (final c in calls)
        if (c.method == 'httpOpen') (c.arguments as Map)['pins'],
    ];
    expect(sent.first, isNot(sent.last));
    expect(sent.last, ([unrelatedPin(3), unrelatedPin(4)]..sort()));
  });

  // Verifies: SEC-041.
  test('a chain that matches no pin fails with PLX-6020 and the host '
      '[SEC-041]', () async {
    answer = (call) =>
        throw PlatformException(code: 'PLUX_PIN_MISMATCH', message: 'pins');
    await expectLater(
      client.get(Uri.parse('https://plux.example/x')),
      throwsA(
        isA<PluxException>().having((e) => e.code.id, 'id', 'PLX-6020').having(
          (e) => e.details,
          'details',
          {'host': 'plux.example'},
        ),
      ),
    );
  });

  // Verifies: SEC-041.
  test('another failure is a client exception, not a pin failure '
      '[SEC-041]', () async {
    answer = (call) => throw PlatformException(code: 'PLUX_HTTP', message: '7');
    await expectLater(
      client.get(Uri.parse('https://plux.example/x')),
      throwsA(isA<http.ClientException>()),
    );
    answer = (call) => call.method == 'httpOpen'
        ? {'id': 1, 'status': 200, 'headers': <String, String>{}}
        : throw PlatformException(code: 'PLUX_HTTP', message: 'reset');
    final res = await client.send(
      http.Request('GET', Uri.parse('https://plux.example/y')),
    );
    await expectLater(
      res.stream.toBytes(),
      throwsA(isA<http.ClientException>()),
    );
    expect(calls.last.method, 'httpClose', reason: 'the request is ended');
  });

  // Verifies: SEC-041.
  test('cancelling the body ends the request; a decoded body loses its '
      'encoding headers [SEC-041]', () async {
    answer = (c) =>
        serve(c, headers: {'Content-Encoding': 'gzip', 'Content-Length': '99'});
    final res = await client.send(
      http.Request('GET', Uri.parse('https://plux.example/z')),
    );
    expect(res.contentLength, isNull);
    expect(res.headers.containsKey('content-encoding'), isFalse);
    await res.stream.first;
    // Cancelling after one chunk closes the request on the platform.
    await Future<void>.delayed(Duration.zero);
    expect(calls.last.method, 'httpClose');
  });
}
