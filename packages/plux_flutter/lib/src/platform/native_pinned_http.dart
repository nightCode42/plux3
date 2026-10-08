// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// HTTP/2 to the Plux server with its certificate chain pinned, done by the
/// platform's own stack (SEC-041, SYN-010): Cronet on Android, `URLSession`
/// on iOS. `cronet_http` cannot add public-key pins to its engine and
/// `cupertino_http` cannot answer a server-trust challenge, so the plugin
/// runs these requests itself and this client speaks to it over the
/// platform channel.
///
/// The exchange is pull-based, since a platform channel reaches only the
/// root isolate from native code and the sync and data isolates call from
/// background isolates: `httpOpen` sends the request (its body in memory)
/// and answers with the status and headers; `httpRead` answers with the
/// next chunk of the body, or null at its end, which also gives the
/// download the platform's flow control; `httpClose` ends a request.
library;

import 'dart:async';

import 'package:flutter/foundation.dart' show visibleForTesting;
import 'package:flutter/services.dart';
import 'package:http/http.dart' as http;
import 'package:plux_flutter/src/platform/pinned_http.dart';
import 'package:plux_flutter/src/security/pins.dart';

/// The plugin's channel (ADR-0029).
const _runtimeChannel = MethodChannel('dev.plux/runtime');

/// The code with which the plugin reports a certificate chain that matches
/// no pin.
const _pinMismatchCode = 'PLUX_PIN_MISMATCH';

/// Sends requests through the platform's pinned HTTP/2 stack. Every request
/// carries the pins in force, so the plugin builds a new engine or session
/// whenever the set changes and lets the requests in flight finish on the
/// old one.
final class NativePinnedClient extends http.BaseClient {
  /// Creates a client that pins to [pins] and sends [userAgent].
  NativePinnedClient({
    required this.pins,
    required this.userAgent,
    @visibleForTesting this._channel = _runtimeChannel,
  });

  /// The pins to enforce.
  final PinSet pins;

  /// The `User-Agent` of requests that name none.
  final String userAgent;

  final MethodChannel _channel;

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    final url = request.url;
    final body = await request.finalize().toBytes();
    final headers = <String, String>{};
    var agent = userAgent;
    for (final MapEntry(:key, :value) in request.headers.entries) {
      if (key.toLowerCase() == 'user-agent') {
        agent = value;
      } else {
        headers[key] = value;
      }
    }
    final Map<Object?, Object?>? opened;
    try {
      opened = await _channel.invokeMapMethod<Object?, Object?>('httpOpen', {
        'url': url.toString(),
        'method': request.method,
        'headers': headers,
        'userAgent': agent,
        'body': body.isEmpty ? null : body,
        'pins': pins.pins.toList()..sort(),
        'followRedirects': request.followRedirects,
        'maxRedirects': request.maxRedirects,
      });
    } on PlatformException catch (e) {
      throw _failure(e, url);
    } on MissingPluginException {
      throw http.ClientException('the pinned HTTP client is unavailable', url);
    }
    if (opened == null) {
      throw http.ClientException('the platform gave no response', url);
    }
    final id = opened['id']! as int;
    final status = opened['status']! as int;
    final received = {
      for (final e in (opened['headers']! as Map<Object?, Object?>).entries)
        (e.key! as String).toLowerCase(): e.value! as String,
    };
    // The platform has decoded a compressed body: the headers that
    // described the encoded one no longer fit.
    if (const {
      'gzip',
      'br',
      'deflate',
    }.contains(received['content-encoding'])) {
      received
        ..remove('content-encoding')
        ..remove('content-length');
    }
    final length = int.tryParse(received['content-length'] ?? '');
    return http.StreamedResponse(
      _chunks(id, url),
      status,
      contentLength: length,
      request: request,
      headers: received,
      isRedirect: status >= 300 && status < 400 && received['location'] != null,
    );
  }

  /// The body of request [id], pulled chunk by chunk; ending or cancelling
  /// the stream ends the request.
  Stream<List<int>> _chunks(int id, Uri url) async* {
    try {
      while (true) {
        final Uint8List? chunk;
        try {
          chunk = await _channel.invokeMethod<Uint8List>('httpRead', {
            'id': id,
          });
        } on PlatformException catch (e) {
          throw _failure(e, url);
        }
        if (chunk == null) return;
        yield chunk;
      }
    } finally {
      try {
        await _channel.invokeMethod<void>('httpClose', {'id': id});
      } on Object {
        // The request is over either way.
      }
    }
  }

  Exception _failure(PlatformException e, Uri url) => e.code == _pinMismatchCode
      ? pinMismatch(url.host)
      : http.ClientException(
          'the request failed (${e.message ?? e.code})',
          url,
        );
}
