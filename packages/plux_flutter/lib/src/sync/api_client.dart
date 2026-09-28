// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The device side of the Plux API (ADR-0005, ADR-0021): ConnectRPC with
/// its JSON encoding over the `http` client, so the runtime needs no
/// protobuf library. Only the four calls a device makes are here.
library;

import 'dart:convert';
import 'dart:typed_data';

import 'package:http/http.dart' as http;
import 'package:plux_flutter/src/errors/plux_exception.dart';

/// A failed call: the HTTP status, the Connect error code and the server's
/// `Retry-After`, when it sent one.
final class ApiError implements Exception {
  /// Creates the error.
  const ApiError(this.status, this.code, this.message, {this.retryAfter});

  /// The HTTP status; 0 when no response arrived.
  final int status;

  /// The Connect code, such as `unavailable` or `unauthenticated`.
  final String code;

  /// The server's message.
  final String message;

  /// How long the server asked the client to wait.
  final Duration? retryAfter;

  /// Whether retrying the same call may succeed.
  bool get retryable =>
      status == 0 ||
      status == 408 ||
      status == 429 ||
      status >= 500 ||
      code == 'unavailable' ||
      code == 'resource_exhausted';

  /// The error as the runtime reports it (`PLX-3050`).
  PluxException toException(String call) => PluxException(
    PluxErrorCode.syncFailed,
    '$call failed: $status $code: $message',
    details: {'call': call, 'status': '$status', 'code': code},
  );

  @override
  String toString() => 'ApiError($status, $code, $message)';
}

/// A device's registration: its ID and the secret it exchanges for tokens.
final class DeviceCredential {
  /// Creates a credential.
  const DeviceCredential(this.deviceId, this.secret);

  /// The device ID.
  final String deviceId;

  /// The device secret; never logged or reported (SCH-012).
  final String secret;

  @override
  String toString() => 'DeviceCredential($deviceId, [redacted])';
}

/// What a device reports about itself at registration.
final class DeviceInfo {
  /// Creates the description.
  const DeviceInfo({
    required this.platform,
    required this.osVersion,
    required this.runtimeVersion,
    required this.hostBuild,
  });

  /// `android` or `ios`.
  final String platform;

  /// The operating system version.
  final String osVersion;

  /// The runtime's version.
  final String runtimeVersion;

  /// The host app's build.
  final String hostBuild;
}

/// One bundle of a served manifest with this device's step for it.
final class ServedBundle {
  const ServedBundle._({
    required this.key,
    required this.hash,
    required this.size,
    required this.url,
    required this.action,
    required this.from,
    required this.stepUrl,
    required this.stepSize,
  });

  /// The plugin key; empty for the app bundle.
  final String key;

  /// The bundle hash, `sha256:<hex>`.
  final String hash;

  /// The bundle's size.
  final int size;

  /// Where the full bundle is.
  final Uri url;

  /// `keep`, `delta` or `full`.
  final String action;

  /// For a delta, the installed bundle it applies to.
  final String from;

  /// The delta or full bundle to download.
  final Uri? stepUrl;

  /// The size of what [stepUrl] serves.
  final int stepSize;
}

/// A manifest response: not modified, or the signed document with this
/// device's plan.
final class ManifestResponse {
  const ManifestResponse._({
    required this.notModified,
    required this.etag,
    this.signed,
    this.signatures = const [],
    this.bundles = const [],
  });

  /// Whether the manifest matches the ETag the device sent (NFR-006).
  final bool notModified;

  /// The manifest's ETag.
  final String etag;

  /// The signed document's canonical bytes.
  final Uint8List? signed;

  /// Its signatures, as `{keyid, alg, sig}`.
  final List<Map<String, Object?>> signatures;

  /// The plan per bundle: the app bundle and every plugin.
  final List<ServedBundle> bundles;
}

/// The device API client.
final class PluxApiClient {
  /// Creates a client for the server at [endpoint], which may carry a
  /// base path (`https://example.com/plux`).
  PluxApiClient(this._http, Uri endpoint)
    : endpoint = endpoint.path.endsWith('/')
          ? endpoint
          : endpoint.replace(path: '${endpoint.path}/');

  final http.Client _http;

  /// The server's base URL.
  final Uri endpoint;

  /// Registers this installation (GOV-010).
  Future<DeviceCredential> register({
    required String appId,
    required String environment,
    required DeviceInfo device,
  }) async {
    final r = await _call('plux.v1.DeviceService/RegisterDevice', {
      'appId': appId,
      'environment': environment,
      'platform': device.platform,
      'osVersion': device.osVersion,
      'runtimeVersion': device.runtimeVersion,
      'hostBuild': device.hostBuild,
    });
    final d = r['device']! as Map<String, Object?>;
    return DeviceCredential(d['id']! as String, r['deviceSecret']! as String);
  }

  /// Exchanges the device's credential for a short-lived access token,
  /// kept in memory only.
  Future<String> token(DeviceCredential c) async {
    final r = await _call('plux.v1.TokenService/IssueDeviceToken', {
      'deviceId': c.deviceId,
      'deviceSecret': c.secret,
    });
    return r['accessToken']! as String;
  }

  /// Fetches the channel's manifest with this device's plan (REL-032).
  Future<ManifestResponse> manifest({
    required String token,
    required String appId,
    required String environment,
    required String channel,
    required int installedSequence,
    required Map<String, String> installed,
    required String ifNoneMatch,
  }) async {
    final r = await _call('plux.v1.ManifestService/GetManifest', {
      'appId': appId,
      'environment': environment,
      'channel': channel,
      'installedSequence': '$installedSequence',
      'installed': [
        for (final MapEntry(:key, :value) in installed.entries)
          {'key': key, 'sha256': value},
      ],
      'ifNoneMatch': ifNoneMatch,
    }, token: token);
    final etag = r['etag'] as String? ?? '';
    if (r['notModified'] == true) {
      return ManifestResponse._(notModified: true, etag: etag);
    }
    final m = r['manifest']! as Map<String, Object?>;
    ServedBundle bundle(String key, Map<String, Object?> b) {
      final step = b['sync'] as Map<String, Object?>? ?? const {};
      final stepUrl = step['url'] as String?;
      return ServedBundle._(
        key: key,
        hash: b['sha256']! as String,
        size: _int(b['size']),
        url: endpoint.resolve(b['url'] as String? ?? ''),
        action: step['action'] as String? ?? 'full',
        from: step['from'] as String? ?? '',
        stepUrl: stepUrl == null || stepUrl.isEmpty
            ? null
            : endpoint.resolve(stepUrl),
        stepSize: _int(step['size']),
      );
    }

    return ManifestResponse._(
      notModified: false,
      etag: etag,
      signed: base64.decode(m['signed']! as String),
      signatures: [
        for (final s
            in (m['signatures'] as List<Object?>? ?? const [])
                .cast<Map<String, Object?>>())
          {'keyid': s['keyId'], 'alg': s['algorithm'], 'sig': s['signature']},
      ],
      bundles: [
        bundle('', m['appBundle']! as Map<String, Object?>),
        for (final p
            in (m['plugins'] as List<Object?>? ?? const [])
                .cast<Map<String, Object?>>())
          bundle(p['key']! as String, p['bundle']! as Map<String, Object?>),
      ],
    );
  }

  /// Reports the release this device activated.
  Future<void> reportInstalled(String token, String deviceId, int sequence) =>
      _call('plux.v1.DeviceService/ReportInstalled', {
        'deviceId': deviceId,
        'releaseSequence': '$sequence',
      }, token: token);

  Future<Map<String, Object?>> _call(
    String procedure,
    Map<String, Object?> body, {
    String? token,
  }) async {
    final http.Response res;
    try {
      res = await _http.post(
        endpoint.resolve(procedure),
        headers: {
          'Content-Type': 'application/json',
          'Connect-Protocol-Version': '1',
          if (token != null) 'Authorization': 'Bearer $token',
        },
        body: jsonEncode(body),
      );
    } on Exception catch (e) {
      throw ApiError(0, 'unavailable', '$e');
    }
    Map<String, Object?> json;
    try {
      json = res.body.isEmpty
          ? const {}
          : jsonDecode(res.body) as Map<String, Object?>;
    } on Object {
      json = const {};
    }
    if (res.statusCode != 200) {
      throw ApiError(
        res.statusCode,
        json['code'] as String? ?? 'unknown',
        json['message'] as String? ?? res.reasonPhrase ?? '',
        retryAfter: parseRetryAfter(res.headers['retry-after'], DateTime.now()),
      );
    }
    return json;
  }
}

int _int(Object? v) => switch (v) {
  int() => v,
  String() => int.tryParse(v) ?? 0,
  _ => 0,
};

/// Reads a `Retry-After` header — seconds or an HTTP date — relative to
/// [now]; null when absent or unreadable. Capped at five minutes.
Duration? parseRetryAfter(String? header, DateTime now) {
  if (header == null || header.trim().isEmpty) return null;
  final seconds = int.tryParse(header.trim());
  Duration? d;
  if (seconds != null) {
    d = Duration(seconds: seconds);
  } else {
    try {
      d = HttpDateParser.parse(header.trim()).difference(now);
    } on FormatException {
      return null;
    }
  }
  if (d.isNegative) return Duration.zero;
  const cap = Duration(minutes: 5);
  return d > cap ? cap : d;
}

/// Parses the IMF-fixdate form of HTTP dates (RFC 9110), such as
/// `Sun, 06 Nov 1994 08:49:37 GMT`.
abstract final class HttpDateParser {
  static const _months = [
    'Jan',
    'Feb',
    'Mar',
    'Apr',
    'May',
    'Jun',
    'Jul',
    'Aug',
    'Sep',
    'Oct',
    'Nov',
    'Dec',
  ];

  /// Parses [s]; throws [FormatException].
  static DateTime parse(String s) {
    final m = RegExp(
      r'^[A-Za-z]{3}, (\d{2}) ([A-Za-z]{3}) (\d{4}) (\d{2}):(\d{2}):(\d{2}) GMT$',
    ).firstMatch(s);
    final month = m == null ? -1 : _months.indexOf(m[2]!);
    if (m == null || month < 0) throw FormatException('not an HTTP date', s);
    return DateTime.utc(
      int.parse(m[3]!),
      month + 1,
      int.parse(m[1]!),
      int.parse(m[4]!),
      int.parse(m[5]!),
      int.parse(m[6]!),
    );
  }
}
