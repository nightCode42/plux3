// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// HTTP to the Plux server with its certificate pinned (SEC-041): every
/// connection to the server's host is checked against the [PinSet] after
/// the platform has validated the certificate chain, and one that matches
/// no pin is closed before a byte of the request is sent.
///
/// `dart:io` shows a client only the leaf certificate of a TLS connection,
/// so on this path a pin is the SPKI hash of the leaf's key; the backup pin
/// is the hash of the leaf key that replaces it. It serves the platforms
/// without Cronet or `URLSession` (desktop and tests); Android and iOS pin
/// the whole chain in `native_pinned_http.dart`.
library;

import 'dart:async';
import 'dart:io';

import 'package:http/http.dart' as http;
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/security/pins.dart';

/// The failure of a connection to [host] whose certificate matches no pin
/// (`PLX-6020`). Only the host is named: never a certificate or a hash.
PluxException pinMismatch(String host, {String? reason}) => PluxException(
  PluxErrorCode.certificatePinMismatch,
  reason ?? 'the certificate of $host matches none of the pins',
  details: {'host': host},
);

/// A `dart:io` client that connects only to pinned hosts, over TLS, and
/// only to servers whose leaf key is in [pins]. The platform's trust store
/// (or [context], for a host that trusts its own authority) still decides
/// first whether the chain is valid; a pin never makes an invalid chain
/// acceptable.
///
/// A mismatch closes the connection, so it is never reused, and fails the
/// request with `PLX-6020`.
HttpClient pinnedHttpClient(
  PinSet pins, {
  String? userAgent,
  SecurityContext? context,
}) {
  Future<ConnectionTask<Socket>> connect(
    Uri url,
    String? proxyHost,
    int? proxyPort,
  ) async {
    final host = url.host;
    if (url.scheme != 'https') {
      throw pinMismatch(host, reason: '$host is pinned and needs https');
    }
    if (proxyHost != null) {
      // Behind a tunnel the handshake is not ours to check.
      throw pinMismatch(host, reason: '$host is pinned and connects directly');
    }
    final task = await SecureSocket.startConnect(
      host,
      url.port,
      context: context,
    );
    final checked = task.socket.then<Socket>((socket) {
      final der = socket.peerCertificate?.der;
      String? pin;
      if (der != null) {
        try {
          pin = spkiPin(der);
        } on FormatException {
          pin = null;
        }
      }
      if (pin == null || !pins.matches([pin])) {
        socket.destroy();
        throw pinMismatch(host);
      }
      return socket;
    });
    return ConnectionTask.fromSocket(checked, task.cancel);
  }

  return HttpClient(context: context)
    ..userAgent = userAgent
    ..findProxy = ((_) => 'DIRECT')
    ..connectionFactory = connect;
}

/// A client that takes the pins of customer API domains (SEC-042).
abstract interface class DomainPinned {
  /// Replaces the pins of the domains: each host with its pins, at least
  /// two distinct ones. A host left out is no longer pinned; a host whose
  /// pins are not valid fails closed.
  void pinDomains(Map<String, List<String>> pins);
}

/// Sends the requests to the domains an app pins (SEC-042) through a
/// client made for each domain's pins, and every other request through
/// [other]. The pins come from the active release, so they change at run
/// time; a domain whose pins change gets a new client, since a pooled
/// connection was accepted under the old pins.
final class DomainPinsClient extends http.BaseClient implements DomainPinned {
  /// Creates a client over [other]; [pinned] makes the client of a domain.
  DomainPinsClient({required this._other, required this._pinned});

  final http.Client _other;
  final http.Client Function(PinSet pins) _pinned;
  final Map<String, PinSet> _sets = {};
  final Map<String, PluxException> _invalid = {};
  final Map<String, http.Client> _clients = {};

  @override
  void pinDomains(Map<String, List<String>> pins) {
    final wanted = {for (final e in pins.entries) e.key.toLowerCase(): e.value};
    for (final host in [..._sets.keys, ..._invalid.keys]) {
      if (!wanted.containsKey(host)) _forget(host);
    }
    for (final MapEntry(key: host, value: list) in wanted.entries) {
      try {
        final next = validatedPins(list);
        final held = _sets[host];
        if (held != null &&
            held.pins.length == next.length &&
            held.pins.containsAll(next)) {
          continue;
        }
        _forget(host);
        _sets[host] = PinSet(next);
      } on PluxException catch (e) {
        _forget(host);
        _invalid[host] = e;
      }
    }
  }

  void _forget(String host) {
    _sets.remove(host);
    _invalid.remove(host);
    _clients.remove(host)?.close();
  }

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) {
    final host = request.url.host.toLowerCase();
    final invalid = _invalid[host];
    if (invalid != null) return Future.error(invalid);
    final pins = _sets[host];
    if (pins == null) return _other.send(request);
    return (_clients[host] ??= _pinned(pins)).send(request);
  }

  @override
  void close() {
    for (final c in _clients.values) {
      c.close();
    }
    _clients.clear();
    _other.close();
  }
}

/// Sends requests to [host] through [pinned] and every other request
/// through [other]: the platform's HTTP/2 client keeps serving CDNs and
/// customer APIs, while the Plux server is reached only over a pinned
/// connection.
final class PinRoutingClient extends http.BaseClient implements DomainPinned {
  /// Creates a client that routes by the host of each request.
  PinRoutingClient({
    required String host,
    required this._pinned,
    required this._other,
  }) : _host = host.toLowerCase();

  final String _host;
  final http.Client _pinned;
  final http.Client _other;

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) =>
      (request.url.host.toLowerCase() == _host ? _pinned : _other).send(
        request,
      );

  @override
  void pinDomains(Map<String, List<String>> pins) {
    final other = _other;
    if (other is DomainPinned) (other as DomainPinned).pinDomains(pins);
  }

  @override
  void close() {
    _pinned.close();
    _other.close();
  }
}
