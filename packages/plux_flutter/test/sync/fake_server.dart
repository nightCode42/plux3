// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// An in-process stand-in for the device side of the Plux server: the
/// ConnectRPC JSON calls a device makes, signed manifests with a per-device
/// plan, and content-addressed objects with range requests — plus knobs
/// that inject the failures of QA-009.
library;

import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:crypto/crypto.dart';
import 'package:cryptography/cryptography.dart' show SimpleKeyPair;
import 'package:cryptography/dart.dart';
import 'package:plux_flutter/src/assets/assets.dart';
import 'package:plux_flutter/src/bundle/container.dart';
import 'package:plux_flutter/src/sync/api_client.dart' show installedDigest;
import 'package:plux_flutter/src/verify/jcs.dart';
import 'package:plux_flutter/src/verify/manifest.dart';

/// The compiled golden bundles and the Go deltas between them.
final class Goldens {
  Goldens._(this.bundles, this.deltas);

  /// Reads `schema/testdata`.
  factory Goldens.load() {
    Uint8List read(String name) =>
        File('../../schema/testdata/bundles/$name').readAsBytesSync();
    final bundles = {
      for (final n in [
        'loan-calculator/demo.pxb',
        'loan-calculator/loans.pxb',
        'routing/routing.pxb',
        'routing/nav.pxb',
        'features/features.pxb',
        'features/tasks.pxb',
        'widgets/widgets.pxb',
        'widgets/gallery.pxb',
      ])
        n: read(n),
    };
    final json = jsonDecode(
      File('../../schema/testdata/delta/vectors.json').readAsStringSync(),
    ) as Map<String, Object?>;
    final deltas = <String, Uint8List>{};
    for (final v
        in (json['vectors']! as List<Object?>).cast<Map<String, Object?>>()) {
      final name = v['name']! as String;
      if (name.startsWith('compiled-loan-calculator/')) {
        deltas[name] = base64.decode(v['delta']! as String);
      }
    }
    return Goldens._(bundles, deltas);
  }

  /// Bundles by path under `schema/testdata/bundles`.
  final Map<String, Uint8List> bundles;

  /// Deltas by vector name.
  final Map<String, Uint8List> deltas;

  /// The bundle hash of a golden, hexadecimal.
  static String hashOf(Uint8List b) => hexEncode(b.sublist(16, 48));
}

/// A release the server serves: the app bundle and plugins by key.
final class FakeRelease {
  /// Creates a release.
  FakeRelease(
    this.sequence,
    this.app,
    this.plugins, {
    this.killSwitches = const [],
    this.appKillSwitch = false,
    this.message = '',
    this.mandatory = false,
    this.requiredFeatures = const ['pxl.v1'],
  });

  /// Its sequence.
  final int sequence;

  /// The app bundle.
  final Uint8List app;

  /// Plugin bundles by key.
  final Map<String, Uint8List> plugins;

  /// Kill switches.
  final List<String> killSwitches;

  /// Whether all Plux content is switched off.
  final bool appKillSwitch;

  /// The message shown while a switch is on.
  final String message;

  /// Whether it is mandatory.
  final bool mandatory;

  /// The features the manifest names for every bundle.
  final List<String> requiredFeatures;
}

/// What the next request to a path suffers.
sealed class Fault {
  const Fault();
}

/// Answer with [status] and an optional `Retry-After`.
final class StatusFault extends Fault {
  /// Creates the fault.
  const StatusFault(this.status, {this.retryAfter, this.code = 'unavailable'});

  /// The status.
  final int status;

  /// The Connect error code of the answer.
  final String code;

  /// The header value.
  final String? retryAfter;
}

/// Send the first [bytes] of the body, then close the connection.
final class DropFault extends Fault {
  /// Creates the fault.
  const DropFault(this.bytes);

  /// Bytes sent before the drop.
  final int bytes;
}

/// Send the body with one byte flipped.
final class CorruptFault extends Fault {
  /// Creates the fault.
  const CorruptFault();
}

/// The fake server.
final class FakePluxServer {
  FakePluxServer._(this._server, this._key, this.publicKey, this.keyId);

  /// Starts a server on the loopback interface.
  static Future<FakePluxServer> start() async {
    final key = await DartEd25519(sha512: const DartSha512())
        .newKeyPairFromSeed(List.filled(32, 9));
    final pub = Uint8List.fromList((await key.extractPublicKey()).bytes);
    final s = FakePluxServer._(
      await HttpServer.bind(InternetAddress.loopbackIPv4, 0),
      key,
      pub,
      sha256.convert(pub).toString().substring(0, 32),
    );
    s._server.listen(s._handle);
    for (final f in fixtureAssets()) {
      s.serveObject('assets', f);
    }
    return s;
  }

  /// The asset files of the conformance projects, which their golden
  /// bundles index: the uploaded ones, the icon fonts the compiler made,
  /// and the variants the server's asset job would make.
  static List<Uint8List> fixtureAssets() => [
    for (final e in Directory(
      '../../schema/testdata/documents',
    ).listSync(recursive: true))
      if (e is File &&
          e.path.contains('/assets/') &&
          !e.path.endsWith('index.json'))
        e.readAsBytesSync(),
    for (final e in Directory(
      '../../schema/testdata/bundles',
    ).listSync(recursive: true))
      if (e is File &&
          (e.path.contains('/icons/') || e.path.contains('/variants/')))
        e.readAsBytesSync(),
  ];

  final HttpServer _server;
  final SimpleKeyPair _key;

  /// The signing key's public half.
  final Uint8List publicKey;

  /// Its key ID.
  final String keyId;

  /// The key as the app embeds it.
  TrustedKey get trustedKey => TrustedKey(
    keyId: keyId,
    algorithm: 'ed25519',
    role: 'targets',
    publicKey: publicKey,
  );

  /// The server's base URL.
  Uri get endpoint => Uri.parse('http://127.0.0.1:${_server.port}/');

  /// The app, environment and channel it serves.
  static const app = 'app_fake',
      environment = 'production',
      channel = 'production';

  /// The release it serves.
  FakeRelease? release;

  /// Deltas it can serve, by `from→to` bundle hashes.
  final Map<String, Uint8List> deltas = {};

  /// Faults to inject, per request path, in order.
  final Map<String, List<Fault>> faults = {};

  /// Every request path, in order.
  final List<String> requests = [];

  /// `Range` headers of object requests.
  final List<String> ranges = [];

  /// Bytes of each manifest response body.
  final List<int> manifestResponseSizes = [];

  /// Bytes of each manifest request body, as sent.
  final List<int> manifestRequestSizes = [];

  /// When the manifest expires.
  DateTime expires = DateTime.utc(2100);

  /// Devices registered.
  int registrations = 0;

  /// Telemetry events received, in the order they arrived.
  final List<Map<String, Object?>> events = [];

  /// Paths of requests whose body came compressed with gzip.
  final List<String> compressedRequests = [];

  /// Whether tokens are refused as for an unknown device.
  bool forgetDevices = false;

  /// Stops the server.
  Future<void> close() => _server.close(force: true);

  /// Signs a bundle hash as publish does, for baselines (ADR-0004).
  Future<String> signHash(Uint8List hash) async => base64.encode(
    (await DartEd25519(
      sha512: const DartSha512(),
    ).sign(hash, keyPair: _key)).bytes,
  );

  /// A baseline as `plux pull` writes it, signed with this server's key.
  Future<Map<String, Uint8List>> baseline(
    int sequence,
    Uint8List app,
    Map<String, Uint8List> plugins,
  ) async {
    final files = <String, Uint8List>{};
    final entries = <Map<String, Object?>>[];
    for (final MapEntry(:key, :value) in {'': app, ...plugins}.entries) {
      final file = 'bundles/${key.isEmpty ? '_app' : key}.pxb';
      files[file] = value;
      entries.add({
        'plugin': key,
        'version': sequence,
        'sha256': Goldens.hashOf(value),
        'file': file,
        'keyId': keyId,
        'algorithm': 'ed25519',
        'signature': await signHash(value.sublist(16, 48)),
      });
    }
    // The asset files the bundles index, as pull writes them.
    final assets = <Map<String, Object?>>[];
    final wanted = {
      for (final b in [app, ...plugins.values])
        for (final a in assetsOf(BundleContainer.parse(b)))
          ...preferredFiles(a, AssetDevice.plain).take(1),
    };
    for (final path in _objects.keys) {
      final h = path.split('/').last;
      if (path.startsWith('/v1/objects/assets/') && wanted.contains(h)) {
        files['assets/$h'] = _objects[path]!;
        assets.add({'sha256': h, 'file': 'assets/$h'});
      }
    }
    files['baseline.json'] = Uint8List.fromList(
      utf8.encode(
        jsonEncode({
          'app': app_,
          'environment': environment,
          'channel': channel,
          'releaseSequence': sequence,
          'bundles': entries,
          'assets': assets,
        }),
      ),
    );
    return files;
  }

  static const app_ = app;

  final Map<String, Uint8List> _objects = {};

  /// Serves [data] under its SHA-256 as `kind` and returns its path.
  String serveObject(String kind, Uint8List data, {String? name}) {
    final h = name ?? sha256.convert(data).toString();
    final path = '/v1/objects/$kind/${h.substring(0, 2)}/$h';
    _objects[path] = data;
    return path;
  }

  /// Registers the delta between two bundles.
  void addDelta(Uint8List from, Uint8List to, Uint8List delta) {
    deltas['${Goldens.hashOf(from)}→${Goldens.hashOf(to)}'] = delta;
  }

  Future<void> _handle(HttpRequest req) async {
    requests.add(req.uri.path);
    final queued = faults[req.uri.path];
    final fault = queued == null || queued.isEmpty ? null : queued.removeAt(0);
    final res = req.response;
    if (fault is StatusFault) {
      res.statusCode = fault.status;
      if (fault.retryAfter != null) {
        res.headers.set('Retry-After', fault.retryAfter!);
      }
      res.headers.contentType = ContentType.json;
      res.write(jsonEncode({'code': fault.code, 'message': 'injected'}));
      await res.close();
      return;
    }
    if (req.method == 'POST') {
      final raw = await req.fold<List<int>>([], (a, b) => a..addAll(b));
      final gzipped = req.headers.value('content-encoding') == 'gzip';
      if (gzipped) compressedRequests.add(req.uri.path);
      if (req.uri.path.endsWith('GetManifest')) {
        manifestRequestSizes.add(raw.length);
      }
      final body = jsonDecode(
        utf8.decode(gzipped ? gzip.decode(raw) : raw),
      ) as Map<String, Object?>;
      final out = await _rpc(
        req.uri.path,
        body,
        req.headers.value('authorization'),
      );
      res.statusCode = out.$1;
      res.headers.contentType = ContentType.json;
      final text = jsonEncode(out.$2);
      if (req.uri.path.endsWith('GetManifest')) {
        manifestResponseSizes.add(utf8.encode(text).length);
      }
      res.write(text);
      await res.close();
      return;
    }
    final data = _objects[req.uri.path];
    if (data == null) {
      res.statusCode = 404;
      await res.close();
      return;
    }
    var body = data;
    if (fault is CorruptFault) {
      body = Uint8List.fromList(data)..[data.length ~/ 2] ^= 0xff;
    }
    final range = req.headers.value('range');
    var start = 0;
    if (range != null) {
      ranges.add(range);
      start = int.parse(RegExp(r'bytes=(\d+)-').firstMatch(range)![1]!);
      if (start >= body.length) {
        res.statusCode = 416;
        await res.close();
        return;
      }
      res.statusCode = 206;
      res.headers.set(
        'Content-Range',
        'bytes $start-${body.length - 1}/${body.length}',
      );
    }
    final slice = Uint8List.sublistView(body, start);
    if (fault is DropFault) {
      // Promise the whole body, send part of it, and cut the connection.
      final socket = await res.detachSocket(writeHeaders: false);
      socket
        ..write(
          'HTTP/1.1 ${range == null ? '200 OK' : '206 Partial Content'}\r\n',
        )
        ..write('Content-Length: ${slice.length}\r\n')
        ..write('Content-Type: application/octet-stream\r\n\r\n')
        ..add(Uint8List.sublistView(slice, 0, fault.bytes));
      await socket.flush();
      socket.destroy();
      return;
    }
    res.headers.contentLength = slice.length;
    res.add(slice);
    await res.close();
  }

  Future<(int, Map<String, Object?>)> _rpc(
    String path,
    Map<String, Object?> body,
    String? auth,
  ) async {
    switch (path) {
      case '/plux.v1.DeviceService/RegisterDevice':
        registrations++;
        return (
          200,
          {
            'device': {'id': 'dev_$registrations'},
            'deviceSecret': 'plux_dsec_$registrations',
          },
        );
      case '/plux.v1.TokenService/IssueDeviceToken':
        if (forgetDevices) {
          forgetDevices = false;
          return (
            401,
            {'code': 'unauthenticated', 'message': 'unknown device'},
          );
        }
        return (200, {'accessToken': 'plux_dat_${body['deviceId']}'});
      case '/plux.v1.DeviceService/ReportInstalled':
        return (200, <String, Object?>{});
      case '/plux.v1.TelemetryService/IngestEvents':
        if (auth == null || !auth.startsWith('Bearer plux_dat_')) {
          return (401, {'code': 'unauthenticated', 'message': 'no token'});
        }
        final batch = (body['events']! as List<Object?>)
            .cast<Map<String, Object?>>();
        events.addAll(batch);
        return (200, {'accepted': batch.length});
      case '/plux.v1.ManifestService/GetManifest':
        if (auth == null || !auth.startsWith('Bearer plux_dat_')) {
          return (401, {'code': 'unauthenticated', 'message': 'no token'});
        }
        if (release == null) {
          return (404, {'code': 'not_found', 'message': 'no release'});
        }
        return (200, await _manifest(body));
    }
    return (404, {'code': 'unimplemented', 'message': path});
  }

  Future<Map<String, Object?>> _manifest(Map<String, Object?> req) async {
    final r = release!;
    final installed = {
      for (final i
          in (req['installed']! as List<Object?>).cast<Map<String, Object?>>())
        i['key']! as String: (i['sha256']! as String).replaceFirst(
          'sha256:',
          '',
        ),
    };
    // As the server does: the ETag names the manifest; "not modified"
    // needs the device to hold exactly its bundles, by list or by digest,
    // and a digest that does not match asks for the list (NFR-006).
    final etag =
        '"${sha256.convert(utf8.encode('${r.sequence}|${r.killSwitches}|${r.appKillSwitch}')).toString().substring(0, 16)}"';
    final targets = {
      '': Goldens.hashOf(r.app),
      for (final MapEntry(:key, :value) in r.plugins.entries)
        key: Goldens.hashOf(value),
    };
    final digest = req['installedDigest'] as String?;
    final holds = digest != null
        ? digest == base64.encode(installedDigest(targets))
        : targets.length == installed.length &&
              targets.entries.every((e) => installed[e.key] == e.value);
    if (req['ifNoneMatch'] == etag && holds) {
      return {'notModified': true, 'etag': etag};
    }
    if (digest != null) return {'installedRequired': true, 'etag': etag};
    Map<String, Object?> bundleJson(Uint8List b) => {
      'hash': 'sha256:${Goldens.hashOf(b)}',
      'size': b.length,
      'requiredFeatures': r.requiredFeatures,
      'minRuntime': '0.1.0',
    };
    final signedDoc = {
      'type': 'manifest',
      'specVersion': 1,
      'role': 'targets',
      'app': app,
      'environment': environment,
      'channel': channel,
      'releaseSequence': r.sequence,
      'issuedAt': '2026-09-28T00:00:00Z',
      'expires': expires.toUtc().toIso8601String().replaceFirst('.000', ''),
      'appBundle': bundleJson(r.app),
      'plugins': [
        for (final MapEntry(:key, :value)
            in (r.plugins.entries.toList()
              ..sort((a, b) => a.key.compareTo(b.key))))
          {'key': key, 'version': r.sequence, ...bundleJson(value)},
      ],
      'control': {
        'killSwitches': r.killSwitches,
        'appKillSwitch': r.appKillSwitch,
        'mandatory': r.mandatory,
        'message': r.message,
      },
      'experiments': <Object?>[],
    };
    final signed = utf8.encode(canonicalJson(signedDoc));
    final sig = await DartEd25519(sha512: const DartSha512())
        .sign(signed, keyPair: _key);
    Map<String, Object?> descriptor(String key, Uint8List b) {
      final hash = Goldens.hashOf(b);
      final full = serveObject('bundles', b, name: hash);
      final have = installed[key];
      Map<String, Object?> step;
      if (have == hash) {
        step = {'action': 'keep'};
      } else if (have != null && deltas.containsKey('$have→$hash')) {
        final d = deltas['$have→$hash']!;
        step = {
          'action': 'delta',
          'from': 'sha256:$have',
          'url': serveObject('deltas', d),
          'size': '${d.length}',
        };
      } else {
        step = {'action': 'full', 'url': full, 'size': '${b.length}'};
      }
      return {
        'sha256': 'sha256:$hash',
        'size': '${b.length}',
        'url': full,
        'sync': step,
      };
    }

    return {
      'etag': etag,
      'manifest': {
        'appId': app,
        'releaseSequence': '${r.sequence}',
        'signed': base64.encode(signed),
        'signatures': [
          {
            'keyId': keyId,
            'algorithm': 'ed25519',
            'signature': base64.encode(sig.bytes),
          },
        ],
        'appBundle': descriptor('', r.app),
        'plugins': [
          for (final MapEntry(:key, :value) in r.plugins.entries)
            {
              'key': key,
              'version': '${r.sequence}',
              'bundle': descriptor(key, value),
            },
        ],
      },
    };
  }
}
