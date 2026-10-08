// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/io_client.dart';
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/core/active_release.dart';
import 'package:plux_flutter/src/data/failure.dart';
import 'package:plux_flutter/src/data/transport.dart';
import 'package:plux_flutter/src/data/worker.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/platform/pinned_http.dart';

import '../support/test_certificate.dart';

final class _Recording extends http.BaseClient {
  final List<Uri> sent = [];

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    sent.add(request.url);
    return http.StreamedResponse(const Stream.empty(), 204);
  }
}

/// Makes the data isolate's client, which trusts the test certificate;
/// sent to the isolate, so it holds the certificate as text.
final class _Factory {
  const _Factory(this.pem);
  final String pem;

  http.Client create() => DomainPinsClient(
    other: http.Client(),
    pinned: (set) => IOClient(
      pinnedHttpClient(
        set,
        context: SecurityContext(withTrustedRoots: false)
          ..setTrustedCertificatesBytes(utf8.encode(pem)),
      ),
    ),
  );
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
      request.response
        ..headers.contentType = ContentType.json
        ..write('{"ok":true}');
      await request.response.close();
    });
    url = Uri.parse('https://localhost:${server.port}/v1');
  });
  tearDown(() async {
    await server.close(force: true);
  });

  DomainPinsClient clientFor(http.Client other) => DomainPinsClient(
    other: other,
    pinned: (set) =>
        IOClient(pinnedHttpClient(set, context: cert.clientContext())),
  );

  // Verifies: SEC-042.
  test('a domain with pins is reached only over a connection that matches '
      'them; others go the usual way [SEC-042]', () async {
    final other = _Recording();
    final client = clientFor(other)
      ..pinDomains({
        'Localhost': [unrelatedPin(1), cert.pin],
      });
    addTearDown(client.close);
    expect((await client.get(url)).body, '{"ok":true}');
    expect(requests, 1);
    await client.get(Uri.parse('https://api.elsewhere.test/'));
    expect(other.sent.map((u) => u.host), ['api.elsewhere.test']);
  });

  // Verifies: SEC-042, SEC-041.
  test('a pinned domain whose certificate matches no pin fails closed with '
      'PLX-6020 [SEC-042]', () async {
    final client = clientFor(_Recording())
      ..pinDomains({
        'localhost': [unrelatedPin(1), unrelatedPin(2)],
      });
    addTearDown(client.close);
    await expectLater(
      client.get(url),
      throwsA(
        isA<PluxException>().having((e) => e.code.id, 'id', 'PLX-6020').having(
          (e) => e.details,
          'details',
          {'host': 'localhost'},
        ),
      ),
    );
    expect(requests, 0);
  });

  // Verifies: SEC-042.
  test('pins that change take effect for the next request; a domain left '
      'out is no longer pinned; invalid pins fail closed [SEC-042]', () async {
    final other = _Recording();
    final client = clientFor(other);
    addTearDown(client.close);
    client.pinDomains({
      'localhost': [unrelatedPin(1), unrelatedPin(2)],
    });
    await expectLater(client.get(url), throwsA(isA<PluxException>()));
    client.pinDomains({
      'localhost': [unrelatedPin(1), cert.pin],
    });
    expect((await client.get(url)).statusCode, 200);
    // The pins change under a pooled connection: it is not reused.
    client.pinDomains({
      'localhost': [unrelatedPin(1), unrelatedPin(2)],
    });
    await expectLater(client.get(url), throwsA(isA<PluxException>()));
    client.pinDomains({
      'localhost': [cert.pin],
    });
    await expectLater(
      client.get(url),
      throwsA(
        isA<PluxException>().having(
          (e) => e.code,
          'code',
          PluxErrorCode.missingProperty,
        ),
      ),
      reason: 'one pin is not a valid set: fail closed, not unpinned',
    );
    client.pinDomains(const {});
    await client.get(url).then<void>((_) {}, onError: (Object _) {});
    expect(other.sent, [url], reason: 'unpinned requests use the usual client');
  });

  // Verifies: SEC-042.
  test('the data isolate pins the domains it is told and reports a mismatch '
      'as PLX-6020 [SEC-042]', () async {
    final dir = Directory.systemTemp.createTempSync('plux_pins');
    addTearDown(() => dir.deleteSync(recursive: true));
    final worker = LazyDataWorker(
      () => DataWorker.start(
        httpClient: _Factory(cert.pem).create,
        cacheDirectory: dir.path,
      ),
    );
    addTearDown(worker.close);
    DataRequest request() => DataRequest(
      method: 'GET',
      url: url,
      maxResponseBytes: 1000,
      timeout: const Duration(seconds: 5),
    );
    // Unpinned, the request takes the default client, which does not
    // trust the test certificate.
    await expectLater(worker.send(request()), throwsA(isA<DataFailure>()));
    worker.pinDomains({
      'localhost': [unrelatedPin(1), cert.pin],
    });
    expect((await worker.send(request())).json, {'ok': true});
    worker.pinDomains({
      'localhost': [unrelatedPin(1), unrelatedPin(2)],
    });
    await expectLater(
      worker.send(request()),
      throwsA(
        isA<DataFailure>().having(
          (f) => f.code,
          'code',
          PluxErrorCode.certificatePinMismatch,
        ),
      ),
    );
  });

  // Verifies: SEC-042.
  test('the pins of an app bundle are read by domain [SEC-042]', () {
    final bytes = fbs.MetaObjectBuilder(
      capabilities: fbs.CapabilitiesObjectBuilder(
        networkPins: [
          fbs.DomainPinsObjectBuilder(
            host: 'api.example.com',
            pins: [unrelatedPin(1), unrelatedPin(2)],
          ),
          fbs.DomainPinsObjectBuilder(
            host: 'eu.example.com',
            pins: [unrelatedPin(3), unrelatedPin(4)],
          ),
        ],
      ),
    ).toBytes();
    expect(domainPinsOf(fbs.Meta(bytes)), {
      'api.example.com': [unrelatedPin(1), unrelatedPin(2)],
      'eu.example.com': [unrelatedPin(3), unrelatedPin(4)],
    });
    expect(domainPinsOf(fbs.Meta(fbs.MetaObjectBuilder().toBytes())), isEmpty);
  });
}
