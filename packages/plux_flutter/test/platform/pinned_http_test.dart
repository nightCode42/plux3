// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:async';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/io_client.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/platform/pinned_http.dart';
import 'package:plux_flutter/src/security/pins.dart';

import '../support/test_certificate.dart';

final class _Recording extends http.BaseClient {
  final List<Uri> sent = [];

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    sent.add(request.url);
    return http.StreamedResponse(const Stream.empty(), 204);
  }
}

void main() {
  late TestCertificate cert;
  late HttpServer server;
  late int requests;
  late Uri url;

  setUpAll(() async {
    cert = await TestCertificate.generate();
  });

  setUp(() async {
    requests = 0;
    server = await HttpServer.bindSecure('localhost', 0, cert.serverContext());
    server.listen((request) async {
      requests++;
      request.response.write('hello');
      await request.response.close();
    });
    url = Uri.parse('https://localhost:${server.port}/');
  });
  tearDown(() async {
    await server.close(force: true);
  });

  http.Client clientFor(PinSet pins, {SecurityContext? context}) => IOClient(
    pinnedHttpClient(pins, context: context ?? cert.clientContext()),
  );

  // Verifies: SEC-041.
  test('a connection whose key matches a pin is served [SEC-041]', () async {
    final client = clientFor(PinSet([unrelatedPin(1), cert.pin]));
    addTearDown(client.close);
    final res = await client.get(url);
    expect(res.statusCode, 200);
    expect(res.body, 'hello');
    // The same connection serves the next request.
    expect((await client.get(url)).body, 'hello');
    expect(requests, 2);
  });

  // Verifies: SEC-041.
  test('a connection whose key matches no pin fails closed with PLX-6020 '
      'before the request is sent [SEC-041]', () async {
    final pins = PinSet([unrelatedPin(1), unrelatedPin(2)]);
    final client = clientFor(pins);
    addTearDown(client.close);
    await expectLater(
      client.get(url),
      throwsA(
        isA<PluxException>()
            .having((e) => e.code, 'code', PluxErrorCode.certificatePinMismatch)
            .having((e) => e.code.id, 'id', 'PLX-6020')
            .having((e) => e.details, 'details', {'host': 'localhost'})
            .having((e) => e.message, 'message', isNot(contains(cert.pin))),
      ),
    );
    expect(requests, 0, reason: 'the server never saw the request');
    // The refused connection is not kept: once the pins include the key
    // (signed metadata, SEC-050), the next request connects anew.
    pins.replace([unrelatedPin(1), cert.pin], version: 1);
    expect((await client.get(url)).body, 'hello');
    expect(requests, 1);
  });

  // Verifies: SEC-041.
  test('a matching pin does not make an untrusted chain acceptable '
      '[SEC-041]', () async {
    // The default trust store does not hold the test certificate.
    final client = IOClient(
      pinnedHttpClient(PinSet([cert.pin, unrelatedPin(1)])),
    );
    addTearDown(client.close);
    await expectLater(client.get(url), throwsA(isNot(isA<PluxException>())));
    expect(requests, 0);
  });

  // Verifies: SEC-041.
  test('a pinned host is never reached without TLS or through a proxy '
      '[SEC-041]', () async {
    final client = clientFor(PinSet([cert.pin, unrelatedPin(1)]));
    addTearDown(client.close);
    await expectLater(
      client.get(Uri.parse('http://localhost:${server.port}/')),
      throwsA(
        isA<PluxException>().having(
          (e) => e.code,
          'code',
          PluxErrorCode.certificatePinMismatch,
        ),
      ),
    );
    expect(requests, 0);
  });

  // Verifies: SEC-041.
  test('only requests to the Plux host are pinned [SEC-041]', () async {
    final pinned = _Recording();
    final other = _Recording();
    final client = PinRoutingClient(
      host: 'Plux.Example.com',
      pinned: pinned,
      other: other,
    );
    await client.get(Uri.parse('https://plux.example.com/a'));
    await client.get(Uri.parse('https://cdn.example.com/b'));
    await client.get(Uri.parse('https://plux.example.com.evil.test/c'));
    expect([for (final u in pinned.sent) u.path], ['/a']);
    expect([for (final u in other.sent) u.path], ['/b', '/c']);
  });
}
