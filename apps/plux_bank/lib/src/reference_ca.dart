// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:convert';
import 'dart:io';

import 'package:http/http.dart' as http;
import 'package:http/io_client.dart';

/// The certificate authority of the reference API (`test/refapi`), as the
/// base64 of its PEM: a compile-time define, since the clients below run on
/// the runtime's isolates and cannot capture a value. Empty in every build
/// that does not talk to the reference API.
const String referenceCa = String.fromEnvironment('PLUX_REFAPI_CA');

/// An `HttpClient` that trusts the authority [base64Pem] holds and no other,
/// not even the platform's roots. Throws [FormatException] for text that is
/// no base64 and [TlsException] for bytes that are no certificate.
HttpClient trustingHttpClient(String base64Pem) {
  final context = SecurityContext(withTrustedRoots: false)
    ..setTrustedCertificatesBytes(base64Decode(base64Pem));
  return HttpClient(context: context);
}

/// `PluxConfig.httpClient` of end-to-end builds: REST, GraphQL and SSE reach
/// the reference API over HTTPS, trusting its authority. Top-level because
/// it runs on the data isolate.
http.Client referenceHttpClient() => IOClient(trustingHttpClient(referenceCa));

/// `PluxConfig.webSocketClient` of end-to-end builds: the same trust for
/// WebSockets, which `httpClient` does not reach.
HttpClient referenceWebSocketClient() => trustingHttpClient(referenceCa);
