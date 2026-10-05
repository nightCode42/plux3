// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/data/client.dart';
import 'package:plux_flutter/src/data/failure.dart';
import 'package:plux_flutter/src/data/services.dart';
import 'package:plux_flutter/src/data/source.dart';
import 'package:plux_flutter/src/data/spec.dart';
import 'package:plux_flutter/src/data/stream_transport.dart';
import 'package:plux_flutter/src/data/transfer_transport.dart';
import 'package:plux_flutter/src/data/transfers.dart';
import 'package:plux_flutter/src/data/transport.dart';
import 'package:plux_flutter/src/data/worker.dart';
import 'package:plux_flutter/src/pxl/types.dart';

import 'io_support.dart';

/// What the server saw of one request.
final class _Seen {
  _Seen(this.method, this.uri, this.contentType, this.body, this.auth);
  final String method;
  final Uri uri;
  final String? contentType;
  final Uint8List body;
  final String? auth;
}

/// A local file server.
final class _Api {
  late HttpServer server;
  final List<_Seen> seen = [];
  int status = 200;

  /// Bytes the download routes send.
  Uint8List payload = Uint8List(0);

  /// Completes to let `/slow` finish its response.
  final Completer<void> release = Completer();
  final List<int> unauthorisedFirst = [];

  Future<void> start() async {
    server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    server.listen((r) async {
      final path = r.uri.path;
      if (path == '/v1/stall') {
        seen.add(_Seen(r.method, r.uri, null, Uint8List(0), null));
        return; // never answers
      }
      final collected = BytesBuilder();
      await r.forEach(collected.add);
      final body = collected.takeBytes();
      seen.add(
        _Seen(
          r.method,
          r.uri,
          r.headers.contentType?.mimeType == 'multipart/form-data'
              ? r.headers.contentType.toString()
              : r.headers.contentType?.toString(),
          body,
          r.headers.value('authorization'),
        ),
      );
      if (unauthorisedFirst.isNotEmpty &&
          r.headers.value('authorization') != 'Bearer fresh') {
        r.response.statusCode = 401;
        await r.response.close();
        return;
      }
      if (status != 200) {
        r.response.statusCode = status;
        await r.response.close();
        return;
      }
      if (path.startsWith('/v1/files/')) {
        if (path.endsWith('/chunked')) {
          // No length: the client has to count.
          for (var i = 0; i < payload.length; i += 1000) {
            r.response.add(
              payload.sublist(i, (i + 1000).clamp(0, payload.length)),
            );
            await r.response.flush();
          }
        } else if (path.endsWith('/slow')) {
          r.response.contentLength = payload.length * 100;
          r.response.add(payload);
          await r.response.flush();
          await release.future;
        } else {
          r.response.contentLength = payload.length;
          r.response.add(payload);
        }
      } else {
        r.response.write(jsonEncode({'total': body.length}));
      }
      await r.response.close();
    });
  }

  String get base => 'http://127.0.0.1:${server.port}/v1';
}

final class _Events implements DataSourceEvents, DataIoEvents {
  final List<Map<String, Object?>> progressEvents = [];

  @override
  void loaded(DataSourceEvent e) {}

  @override
  void failed(DataSourceEvent e) {}

  @override
  void message(DataSourceEvent e) {}

  @override
  void outbox(OutboxOutcome o, DataSourceEvent e) {}

  @override
  void progress(DataSourceEvent e) =>
      progressEvents.add(e.value! as Map<String, Object?>);
}

final class _Auth implements PluxAuthDelegate {
  @override
  bool get isAuthenticated => true;

  @override
  Future<String?> accessToken() async => 'stale';

  @override
  Future<String?> refresh() async => 'fresh';

  @override
  void onLogout() {}
}

final types = <String, NamedType>{
  'Total': NamedType.object('Total')
    ..fields!.addAll({'total': const PxlType(PxlKind.int)}),
};

void main() {
  late _Api api;
  late _Events events;
  late List<PluxException> reports;
  late Directory dir;
  late String root, downloads;
  late DataServices services;
  late DataScope scope;
  const caller = DataCaller(pluginKey: 'files', domains: ['127.0.0.1']);

  setUp(() async {
    api = _Api();
    await api.start();
    events = _Events();
    reports = [];
    dir = Directory.systemTemp.createTempSync('plux_xfer');
    root = '${dir.path}/runtime';
    downloads = '$root/downloads';
    Directory(root).createSync();
    services = DataServices(
      transport: ClientTransport(http.Client()),
      authDelegate: _Auth.new,
      environment: 'production',
      record: (name, {fields = const {}, route = '', pluginKey = ''}) {},
      report: reports.add,
      ioEvents: events,
      transferTransport: HttpTransferTransport(http.Client()),
      runtimeRoot: root,
      downloadDirectory: downloads,
      allowCleartext: true,
    );
    scope = DataScope(
      services: services,
      own: [
        DataSourceController(
          spec: DataSourceSpec(
            id: 'files-id',
            name: 'files',
            kind: DataKind.rest,
            type: 'int',
            baseUrls: {'production': api.base},
            path: '/count',
            operations: {
              'upload': const OperationSpec(
                name: 'upload',
                path: '/upload/{folder}',
                auth: false,
                output: 'Total',
                transfer: TransferSpec(
                  kind: TransferKind.upload,
                  fileParam: 'path',
                  field: 'attachment',
                ),
              ),
              'uploadAuth': const OperationSpec(
                name: 'uploadAuth',
                path: '/upload/secure',
                auth: true,
                output: 'Total',
                transfer: TransferSpec(
                  kind: TransferKind.upload,
                  fileParam: 'path',
                ),
              ),
              'raw': const OperationSpec(
                name: 'raw',
                method: 'PUT',
                path: '/raw',
                auth: false,
                output: 'Total',
                transfer: TransferSpec(
                  kind: TransferKind.upload,
                  fileParam: 'path',
                  raw: true,
                  contentType: 'image/png',
                ),
              ),
              'stall': const OperationSpec(
                name: 'stall',
                path: '/stall',
                auth: false,
                transfer: TransferSpec(
                  kind: TransferKind.upload,
                  fileParam: 'path',
                  raw: true,
                ),
              ),
              'download': const OperationSpec(
                name: 'download',
                method: 'GET',
                path: '/files/{name}',
                auth: false,
                transfer: TransferSpec(
                  kind: TransferKind.download,
                  fileParam: 'saveAs',
                ),
              ),
            },
          ),
          context: services,
          caller: caller,
          types: types,
        ),
      ],
      shared: const [],
      roots: () => const {},
    );
  });

  tearDown(() async {
    scope.dispose();
    if (!api.release.isCompleted) api.release.complete();
    await api.server.close(force: true);
    dir.deleteSync(recursive: true);
  });

  File write(String name, int size) {
    final f = File('${dir.path}/$name')
      ..writeAsBytesSync(
        Uint8List.fromList([for (var i = 0; i < size; i++) i % 251]),
      );
    return f;
  }

  Matcher failsWith(PluxErrorCode code) =>
      throwsA(isA<ActionError>().having((e) => e.code, 'code', code));

  test('a multipart upload streams the file with its fields, reports progress up to the total and maps the response [DAT-031]', () async {
    final file = write('photo.bin', 300 * 1024);
    final out = await scope.callOperation('files.upload', {
      'folder': 'inbox',
      'path': file.path,
      'note': 'hello',
    });
    expect(out, {'total': greaterThan(300 * 1024)});
    final req = api.seen.single;
    expect(req.method, 'POST');
    expect(req.uri.path, '/v1/upload/inbox');
    expect(req.uri.query, isEmpty, reason: 'fields go in the form');
    expect(req.contentType, startsWith('multipart/form-data'));
    final text = latin1.decode(req.body);
    expect(text, contains('name="attachment"'));
    expect(text, contains('filename="photo.bin"'));
    expect(text, contains('name="note"'));
    expect(text, contains('hello'));
    expect(events.progressEvents.map((p) => p['operation']).toSet(), {
      'files.upload',
    });
    final sent = [for (final p in events.progressEvents) p['sent']! as int];
    expect(sent.length, greaterThan(2));
    expect(sent, orderedEquals([...sent]..sort()));
    expect(sent.last, 300 * 1024);
    expect(events.progressEvents.last['total'], 300 * 1024);
  });

  test('a raw upload sends the file as the body with its content type, the other inputs in the query [DAT-031]', () async {
    final file = write('a.png', 5000);
    final out = await scope.callOperation('files.raw', {
      'path': file.path,
      'kind': 'avatar',
    });
    expect(out, {'total': 5000});
    final req = api.seen.single;
    expect(req.method, 'PUT');
    expect(req.contentType, 'image/png');
    expect(req.uri.queryParameters, {'kind': 'avatar'});
    expect(req.body, file.readAsBytesSync());
  });

  test('a download is saved under the downloads directory with progress, and no partial file stays [DAT-031]', () async {
    api.payload = Uint8List.fromList([
      for (var i = 0; i < 200000; i++) i % 251,
    ]);
    final out = await scope.callOperation('files.download', {
      'name': 'report',
      'saveAs': 'report.bin',
    });
    expect(out, '$downloads/report.bin');
    expect(File('$downloads/report.bin').readAsBytesSync(), api.payload);
    expect(Directory(downloads).listSync().map((e) => e.path), [
      '$downloads/report.bin',
    ]);
    expect(events.progressEvents.last, {
      'operation': 'files.download',
      'sent': 200000,
      'total': 200000,
    });
    // A file that is downloaded can be uploaded again.
    final again = await scope.callOperation('files.raw', {
      'path': '$downloads/report.bin',
    });
    expect(again, {'total': 200000});
  });

  test('size limits come from the registry: a file over data.uploadSize is refused before anything is sent, a download over data.downloadSize is stopped and removed [DAT-031] [LIM-004]', () async {
    services.limits = {'data.uploadSize': 1000, 'data.downloadSize': 5000};
    final big = write('big.bin', 1001);
    await expectLater(
      scope.callOperation('files.raw', {'path': big.path}),
      failsWith(PluxErrorCode.dataTransferTooLarge),
    );
    expect(api.seen, isEmpty);
    write('ok.bin', 1000);
    expect(
      await scope.callOperation('files.raw', {'path': '${dir.path}/ok.bin'}),
      {'total': 1000},
    );

    // The declared length is checked before the body is read.
    api.payload = Uint8List(6000);
    await expectLater(
      scope.callOperation('files.download', {
        'name': 'x',
        'saveAs': 'declared.bin',
      }),
      failsWith(PluxErrorCode.dataTransferTooLarge),
    );
    // Without a declared length, the bytes are counted as they arrive.
    await expectLater(
      scope.callOperation('files.download', {
        'name': 'chunked',
        'saveAs': 'counted.bin',
      }),
      failsWith(PluxErrorCode.dataTransferTooLarge),
    );
    final left = Directory(downloads).existsSync()
        ? Directory(downloads).listSync()
        : const [];
    expect(left, isEmpty, reason: 'neither a file nor a .part stays');
  });

  test('a cancelled download stops, removes its partial file and fails with the cancelled kind [DAT-031]', () async {
    api.payload = Uint8List(100 * 1024);
    final token = CancelToken();
    final run = scope.callOperation('files.download', {
      'name': 'slow',
      'saveAs': 'slow.bin',
    }, cancel: token);
    final done = run.then<Object?>((v) => v, onError: (Object e) => e);
    await eventually(
      () => events.progressEvents.any((p) => (p['sent']! as int) >= 64 * 1024),
      'the download made progress',
    );
    token.cancel();
    final e = await done;
    expect(e, isA<ActionError>());
    expect((e as ActionError).kind, ActionErrorKind.cancelled);
    expect(
      Directory(downloads).listSync().where((f) => f.path.contains('slow')),
      isEmpty,
    );
  });

  test('a cancelled upload stops and a token cancelled before the start never connects [DAT-031]', () async {
    final file = write('s.bin', 1000);
    final token = CancelToken();
    final run = scope
        .callOperation('files.stall', {'path': file.path}, cancel: token)
        .then<Object?>((v) => v, onError: (Object e) => e);
    await eventually(() => api.seen.isNotEmpty, 'the request arrives');
    token.cancel();
    final e = await run;
    expect((e as ActionError).kind, ActionErrorKind.cancelled);

    final before = api.seen.length;
    final early = CancelToken()..cancel();
    await expectLater(
      scope.callOperation('files.raw', {'path': file.path}, cancel: early),
      failsWith(PluxErrorCode.actionCancelled),
    );
    expect(api.seen.length, before);
  });

  test('the files a transfer may touch are bounded: no private directory, no relative or parent paths, plain download names [DAT-031] [SEC-092]', () async {
    File('$root/plux-state.bin').writeAsBytesSync([1, 2, 3]);
    for (final path in [
      '$root/plux-state.bin',
      'relative.bin',
      '${dir.path}/../${dir.path.split('/').last}/x.bin',
    ]) {
      await expectLater(
        scope.callOperation('files.raw', {'path': path}),
        failsWith(PluxErrorCode.dataTransferFileFailed),
        reason: path,
      );
    }
    await expectLater(
      scope.callOperation('files.raw', {'path': '${dir.path}/missing.bin'}),
      failsWith(PluxErrorCode.dataTransferFileFailed),
    );
    for (final name in ['../x', 'a/b', '..', r'a\b']) {
      await expectLater(
        scope.callOperation('files.download', {'name': 'x', 'saveAs': name}),
        failsWith(PluxErrorCode.dataTransferFileFailed),
        reason: name,
      );
    }
    expect(api.seen, isEmpty);
  });

  test('transfers pass the domain check like any request and refresh a refused token once [DAT-031] [DAT-030] [HST-010]', () async {
    final file = write('a.bin', 100);
    final blocked = DataScope(
      services: services,
      own: [
        DataSourceController(
          spec: DataSourceSpec(
            id: 'blocked-id',
            name: 'blocked',
            kind: DataKind.rest,
            type: 'int',
            baseUrls: {'production': api.base},
            operations: {
              'raw': const OperationSpec(
                name: 'raw',
                method: 'PUT',
                path: '/raw',
                auth: false,
                transfer: TransferSpec(
                  kind: TransferKind.upload,
                  fileParam: 'path',
                  raw: true,
                ),
              ),
            },
          ),
          context: services,
          caller: const DataCaller(
            pluginKey: 'files',
            domains: ['example.com'],
          ),
          types: types,
        ),
      ],
      shared: const [],
      roots: () => const {},
    );
    addTearDown(blocked.dispose);
    await expectLater(
      blocked.callOperation('blocked.raw', {'path': file.path}),
      failsWith(PluxErrorCode.dataDomainBlocked),
    );
    expect(api.seen, isEmpty);
    expect(
      reports.map((e) => e.code),
      contains(PluxErrorCode.dataDomainBlocked),
    );

    api.unauthorisedFirst.add(1);
    final out = await scope.callOperation('files.uploadAuth', {
      'path': file.path,
    });
    expect(out, {'total': greaterThan(0)});
    expect(api.seen.map((s) => s.auth), ['Bearer stale', 'Bearer fresh']);

    api.status = 500;
    await expectLater(
      scope.callOperation('files.raw', {'path': file.path}),
      failsWith(PluxErrorCode.dataHttpError),
    );
  });

  test('the data isolate moves files and streams: progress, results and cancellation cross back to the UI isolate [DAT-031] [DAT-012]', () async {
    final worker = LazyDataWorker(
      () => DataWorker.start(
        httpClient: http.Client.new,
        cacheDirectory: '${dir.path}/cache',
      ),
    );
    addTearDown(worker.close);
    api.payload = Uint8List(150 * 1024);
    final target = '$downloads/isolate.bin';
    TransferRequest download(String name) => TransferRequest(
      kind: TransferKind.download,
      method: 'GET',
      url: Uri.parse('${api.base}/files/$name'),
      path: target,
      maxBytes: 1 << 20,
      maxResponseBytes: 1 << 20,
    );
    final job = worker.begin(download('plain'));
    final progress = await job.progress.toList();
    final result = await job.result;
    expect(result.status, 200);
    expect(result.bytes, 150 * 1024);
    expect(progress.last, (sent: 150 * 1024, total: 150 * 1024));
    expect(File(target).lengthSync(), 150 * 1024);

    final cancelled = worker.begin(download('slow'));
    unawaited(cancelled.progress.drain<void>());
    final outcome = cancelled.result.then<Object?>(
      (v) => v,
      onError: (Object e) => e,
    );
    await Future<void>.delayed(const Duration(milliseconds: 200));
    cancelled.cancel();
    expect(await outcome, isA<DataFailure>());

    // A WebSocket through the worker.
    final ws = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(() => ws.close(force: true));
    final received = <String>[];
    ws.listen((r) async {
      // Closed by the client's close, or the server's.
      // ignore: close_sinks
      final s = await WebSocketTransformer.upgrade(r);
      s.add('{"hello":1}');
      s.listen((m) => received.add(m as String));
    });
    final conn = await worker.open(
      StreamRequest(
        kind: StreamKind.webSocket,
        url: Uri.parse('ws://127.0.0.1:${ws.port}/'),
        maxMessageBytes: 1000,
        connectTimeout: const Duration(seconds: 5),
      ),
    );
    final first = await conn.frames.first;
    expect(first.data, {'hello': 1});
    conn.send({'ping': true});
    await eventually(() => received.isNotEmpty, 'the message arrives');
    expect(jsonDecode(received.single), {'ping': true});
    await conn.close();
  });
}
