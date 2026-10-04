// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// What a data request is on the wire (ADR-0048): one HTTP exchange on the
/// runtime's HTTP client (Cronet, `URLSession`, `dart:io`), bounded by the
/// response size and the request timeout, with the JSON body decoded where
/// it was received. [ClientTransport] runs on the data isolate in apps
/// (L-6) and directly in tests; both see the same code.
library;

import 'dart:async';
import 'dart:convert';
import 'dart:io' show SocketException, HandshakeException, HttpException;
import 'dart:typed_data';

import 'package:http/http.dart' as http;
import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/data/failure.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';

/// One request, built and checked on the UI isolate.
final class DataRequest {
  /// Creates a request.
  const DataRequest({
    required this.method,
    required this.url,
    this.headers = const {},
    this.body,
    required this.maxResponseBytes,
    required this.timeout,
  });

  /// The HTTP method.
  final String method;

  /// The URL; its domain was checked before (DAT-030).
  final Uri url;

  /// The headers.
  final Map<String, String> headers;

  /// The JSON body, encoded, or null.
  final Uint8List? body;

  /// The largest response accepted (`data.responseSize`).
  final int maxResponseBytes;

  /// How long the exchange may take (`data.requestTimeout`).
  final Duration timeout;
}

/// A response: its status, its decoded JSON body (null when empty or not a
/// success) and its size.
final class DataResponse {
  /// Creates a response.
  const DataResponse(this.status, this.json, this.bytes);

  /// The HTTP status.
  final int status;

  /// The decoded body of a 2xx response.
  final Object? json;

  /// The body's size in bytes.
  final int bytes;
}

/// Sends data requests.
abstract interface class DataTransport {
  /// Sends [request]; fails with a [DataFailure] when the network fails, the
  /// time or size limit is passed, or a success body is not JSON.
  Future<DataResponse> send(DataRequest request);

  /// Releases the transport.
  Future<void> close();
}

/// Sends requests on an HTTP client.
final class ClientTransport implements DataTransport {
  /// Creates the transport.
  ClientTransport(this.client);

  /// The client.
  final http.Client client;

  @override
  Future<DataResponse> send(DataRequest r) => _send(r).timeout(
    r.timeout,
    onTimeout: () => throw DataFailure(
      ActionErrorKind.timeout,
      PluxErrorCode.dataRequestTimeout,
      '${r.method} ${_where(r.url)} took longer than '
      '${r.timeout.inMilliseconds} ms (data.requestTimeout)',
    ),
  );

  Future<DataResponse> _send(DataRequest r) async {
    final req = http.Request(r.method, r.url)..headers.addAll(r.headers);
    if (r.body case final b?) req.bodyBytes = b;
    final http.StreamedResponse res;
    try {
      res = await client.send(req);
    } on SocketException catch (e) {
      throw _network(r, e.osError?.message ?? 'connection failed');
    } on HandshakeException {
      throw _network(r, 'the TLS handshake failed');
    } on HttpException catch (e) {
      throw _network(r, e.message);
    } on http.ClientException catch (e) {
      throw _network(r, e.message);
    }
    final ok = res.statusCode >= 200 && res.statusCode < 300;
    final declared = res.contentLength;
    if (declared != null && declared > r.maxResponseBytes) {
      await res.stream.drain<void>();
      throw _tooLarge(r);
    }
    final out = BytesBuilder(copy: false);
    try {
      await for (final chunk in res.stream) {
        out.add(chunk);
        if (out.length > r.maxResponseBytes) throw _tooLarge(r);
      }
    } on http.ClientException catch (e) {
      throw _network(r, e.message);
    }
    final bytes = out.length;
    if (!ok || bytes == 0) return DataResponse(res.statusCode, null, bytes);
    try {
      return DataResponse(
        res.statusCode,
        jsonDecode(utf8.decode(out.takeBytes())),
        bytes,
      );
    } on FormatException {
      throw DataFailure.mapping('the response of ${_where(r.url)} is not JSON');
    }
  }

  DataFailure _network(DataRequest r, String why) => DataFailure(
    ActionErrorKind.network,
    PluxErrorCode.dataNetworkFailed,
    '${r.method} ${_where(r.url)} failed: $why',
  );

  DataFailure _tooLarge(DataRequest r) => DataFailure(
    ActionErrorKind.validation,
    PluxErrorCode.dataSizeExceeded,
    'the response of ${_where(r.url)} exceeds data.responseSize = '
    '${r.maxResponseBytes} bytes',
  );

  @override
  Future<void> close() async => client.close();
}

/// A URL as messages show it: scheme, host and path, never the query,
/// which may carry user data (SCH-012).
String _where(Uri u) => '${u.scheme}://${u.host}${u.path}';

/// [where] for other libraries of the data layer.
String describeUrl(Uri u) => _where(u);
