// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/data/client.dart';
import 'package:plux_flutter/src/data/services.dart';
import 'package:plux_flutter/src/data/source.dart';
import 'package:plux_flutter/src/data/spec.dart';
import 'package:plux_flutter/src/data/transport.dart';
import 'package:plux_flutter/src/store/kv_store.dart';

import '../support/harness.dart';
import 'io_support.dart';

/// What the server saw of one request.
typedef _Seen = ({String key, String body, String? auth});

/// A local API whose answers a test scripts: a status per request, or
/// `drop` to cut the connection.
final class _Api {
  late HttpServer server;
  final List<_Seen> seen = [];
  final List<Object> script = [];
  Object fallback = 201;

  Future<void> start() async {
    server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    server.listen((r) async {
      final body = await utf8.decoder.bind(r).join();
      seen.add((
        key: r.headers.value('idempotency-key') ?? '',
        body: body,
        auth: r.headers.value('authorization'),
      ));
      final next = script.isEmpty ? fallback : script.removeAt(0);
      if (next == 'drop') {
        (await r.response.detachSocket()).destroy();
        return;
      }
      r.response.statusCode = next as int;
      r.response.write(jsonEncode({'ok': true}));
      await r.response.close();
    });
  }

  String get base => 'http://127.0.0.1:${server.port}/v1';
  List<String> get bodies => [for (final s in seen) s.body];
}

final class _Events implements DataSourceEvents, DataIoEvents {
  final List<String> seen = [];
  final List<Map<String, Object?>> entries = [];

  @override
  void loaded(DataSourceEvent e) {}

  @override
  void failed(DataSourceEvent e) {}

  @override
  void message(DataSourceEvent e) {}

  @override
  void progress(DataSourceEvent e) {}

  @override
  void outbox(OutboxOutcome o, DataSourceEvent e) {
    seen.add('${o.name} ${e.source}');
    entries.add(e.value! as Map<String, Object?>);
  }
}

final class _Auth implements PluxAuthDelegate {
  String? token = 'tok-123';

  @override
  bool get isAuthenticated => token != null;

  @override
  Future<String?> accessToken() async => token;

  @override
  Future<String?> refresh() async => token;

  @override
  void onLogout() {}
}

/// A key-value store in memory whose content a test can read.
final class _MemoryKv implements PluxKeyValueStore {
  Map<String, Object?> content = {};
  int wipes = 0;

  @override
  Future<Map<String, Object?>> load() async =>
      jsonDecode(jsonEncode(content)) as Map<String, Object?>;

  @override
  Future<void> save(Map<String, Object?> entries) async =>
      content = jsonDecode(jsonEncode(entries)) as Map<String, Object?>;

  @override
  Future<void> wipe() async {
    wipes++;
    content = {};
  }
}

void main() {
  late _Api api;
  late FakeScheduler sched;
  late _Events events;
  late List<PluxException> reports;
  late Directory dir;
  late MemorySecretStore secrets;
  late _Auth auth;
  const caller = DataCaller(pluginKey: 'shop', domains: ['127.0.0.1']);

  setUp(() async {
    api = _Api();
    await api.start();
    sched = FakeScheduler();
    events = _Events();
    reports = [];
    dir = Directory.systemTemp.createTempSync('plux_outbox');
    secrets = MemorySecretStore();
    auth = _Auth();
  });

  tearDown(() async {
    await api.server.close(force: true);
    dir.deleteSync(recursive: true);
  });

  EncryptedFileStore fileStore() => EncryptedFileStore(
    path: '${dir.path}/outbox.pxk',
    secrets: secrets,
    keyName: 'data-outbox.a',
    label: 'data-outbox/a',
    maxBytes: 1 << 20,
  );

  DataServices make(PluxKeyValueStore store) => DataServices(
    transport: ClientTransport(http.Client()),
    authDelegate: () => auth,
    environment: 'production',
    record: (name, {fields = const {}, route = '', pluginKey = ''}) {},
    report: reports.add,
    ioEvents: events,
    outboxStore: store,
    random: TopRandom(),
    scheduler: sched.call,
    allowCleartext: true,
  );

  DataSourceSpec tasks({bool withAuth = false}) => DataSourceSpec(
    id: 'tasks-id',
    name: 'tasks',
    kind: DataKind.rest,
    type: 'int',
    baseUrls: {'production': api.base},
    path: '/count',
    auth: withAuth,
    operations: {
      'create': OperationSpec(
        name: 'create',
        path: '/tasks',
        auth: withAuth,
        offlineCapable: true,
      ),
      'read': const OperationSpec(
        name: 'read',
        method: 'GET',
        path: '/tasks',
        auth: false,
      ),
    },
  );

  /// A page scope over the source: registers it with the services.
  DataScope scope(DataServices services, {bool withAuth = false}) {
    final c = DataSourceController(
      spec: tasks(withAuth: withAuth),
      context: services,
      caller: caller,
      types: const {},
    );
    final s = DataScope(
      services: services,
      own: [c],
      shared: const [],
      roots: () => const {},
    );
    addTearDown(s.dispose);
    return s;
  }

  Future<Object?> create(DataScope s, String title) =>
      s.callOperation('tasks.create', {'title': title});

  test('mutations made while offline are queued encrypted, then replayed in order, each with its own idempotency key [DAT-020] [SEC-073]', () async {
    final services = make(fileStore());
    addTearDown(services.close);
    final s = scope(services);
    services.setNetworkAvailable(false);
    expect(await create(s, 'secret-title-A'), isNull);
    expect(await create(s, 'secret-title-B'), isNull);
    expect(await create(s, 'secret-title-C'), isNull);
    expect(api.seen, isEmpty, reason: 'nothing leaves while offline');
    expect(services.outbox!.length, 3);

    // At rest the outbox is ciphertext: no title, plugin, operation or key.
    final bytes = File('${dir.path}/outbox.pxk').readAsBytesSync();
    final text = latin1.decode(bytes);
    for (final plain in [
      'secret-title',
      'shop',
      'tasks',
      'create',
      'entries',
    ]) {
      expect(text, isNot(contains(plain)), reason: plain);
    }

    services.setNetworkAvailable(true);
    await eventually(() => api.seen.length == 3, 'the replay');
    await eventually(() => events.seen.length == 3, 'the events');
    expect(
      api.bodies.map((b) => (jsonDecode(b) as Map<String, Object?>)['title']),
      ['secret-title-A', 'secret-title-B', 'secret-title-C'],
    );
    final keys = api.seen.map((s) => s.key).toList();
    expect(keys.toSet(), hasLength(3));
    expect(keys.every((k) => RegExp(r'^[0-9a-f-]{36}$').hasMatch(k)), isTrue);
    expect(events.seen, everyElement('synced tasks'));
    expect(events.entries.map((e) => e['key']), keys);
    expect(events.entries.first['operation'], 'tasks.create');
    expect(events.entries.first['status'], 201);
    expect(services.outbox!.length, 0);
    expect(await fileStore().load(), {'v': 1, 'entries': <Object?>[]});
  });

  test('a mutation that fails with the network is queued, and every attempt carries the same idempotency key [DAT-020]', () async {
    final services = make(fileStore());
    addTearDown(services.close);
    final s = scope(services);
    api.script.add('drop');
    expect(await create(s, 'A'), isNull, reason: 'queued, not failed');
    expect(api.seen, hasLength(1));
    expect(services.outbox!.length, 1);
    services.appResumed();
    await eventually(() => events.seen.length == 1, 'the replay on resume');
    expect(api.seen, hasLength(2));
    expect(api.seen[1].key, api.seen[0].key);
    expect(api.seen[1].key, isNotEmpty);
  });

  test('a new mutation queues behind earlier ones so the order holds, and a request that succeeds triggers the replay [DAT-020]', () async {
    final services = make(fileStore());
    addTearDown(services.close);
    final s = scope(services);
    services.setNetworkAvailable(false);
    await create(s, 'A');
    // The host forgets to say the network is back; the next request that
    // works (a read) triggers the replay, and a mutation made meanwhile
    // waits its turn.
    services.setNetworkAvailable(true);
    await eventually(() => events.seen.length == 1, 'A replays');
    services.setNetworkAvailable(false);
    await create(s, 'B');
    final read = await s.callOperation('tasks.read', const {});
    expect(read, isNull);
    services.setNetworkAvailable(true);
    await eventually(() => events.seen.length == 2, 'B replays');
    expect(
      api.bodies
          .where((b) => b.isNotEmpty)
          .map((b) => (jsonDecode(b) as Map<String, Object?>)['title']),
      ['A', 'B'],
    );
  });

  test('a conflict (409, 412) and a client error end the entry with their events; a server error keeps it and backs off [DAT-020]', () async {
    final services = make(fileStore());
    addTearDown(services.close);
    final s = scope(services);
    services.setNetworkAvailable(false);
    for (final t in ['A', 'B', 'C']) {
      await create(s, t);
    }
    api.script.addAll([409, 400, 503]);
    services.setNetworkAvailable(true);
    await eventually(() => events.seen.length == 2, 'two outcomes');
    expect(events.seen, ['conflict tasks', 'failed tasks']);
    expect(events.entries.map((e) => e['status']), [409, 400]);
    expect(
      reports.map((e) => e.code),
      containsAll([
        PluxErrorCode.dataOutboxConflict,
        PluxErrorCode.dataOutboxRejected,
      ]),
    );
    await eventually(() => sched.active.isNotEmpty, 'the backoff timer');
    expect(services.outbox!.length, 1, reason: 'the 503 keeps its entry');
    expect(sched.delays.last, const Duration(seconds: 5));

    api.script.add(503);
    sched.active.first.fire();
    await eventually(() => api.seen.length == 4, 'the retry');
    await eventually(() => sched.active.isNotEmpty, 'the next timer');
    expect(sched.delays.last, const Duration(seconds: 10), reason: 'doubled');
    api.script.add(412);
    sched.active.first.fire();
    await eventually(() => events.seen.length == 3, 'the 412');
    expect(events.seen.last, 'conflict tasks');
    expect(services.outbox!.length, 0);
  });

  test('a full outbox refuses the mutation with PLX-5120 and keeps what it holds [DAT-020] [LIM-004]', () async {
    final services = make(fileStore());
    addTearDown(services.close);
    final s = scope(services);
    services.setNetworkAvailable(false);
    services.limits = {'data.outboxEntries': 2};
    await create(s, 'A');
    await create(s, 'B');
    await expectLater(
      create(s, 'C'),
      throwsA(
        isA<ActionError>()
            .having((e) => e.code, 'code', PluxErrorCode.dataOutboxFull)
            .having((e) => e.kind, 'kind', ActionErrorKind.custom),
      ),
    );
    expect(services.outbox!.length, 2);

    services.limits = {'data.outboxBytes': 250};
    await expectLater(
      create(s, 'a title that does not fit'),
      throwsA(
        isA<ActionError>().having(
          (e) => e.code,
          'code',
          PluxErrorCode.dataOutboxFull,
        ),
      ),
    );
    expect(services.outbox!.length, 2);
    services.limits = {};
    services.setNetworkAvailable(true);
    await eventually(() => events.seen.length == 2, 'the replay');
    await create(s, 'D');
    expect(services.outbox!.length, 0, reason: 'room again: sent at once');
  });

  test('an outbox whose key cannot be had refuses the mutation with PLX-5121 and never stores it in the clear [DAT-020]', () async {
    final failing = EncryptedFileStore(
      path: '${dir.path}/outbox.pxk',
      secrets: _FailingSecrets(),
      keyName: 'data-outbox.a',
      label: 'data-outbox/a',
      maxBytes: 1 << 20,
    );
    final services = make(failing);
    addTearDown(services.close);
    final s = scope(services);
    services.setNetworkAvailable(false);
    await expectLater(
      create(s, 'A'),
      throwsA(
        isA<ActionError>().having(
          (e) => e.code,
          'code',
          PluxErrorCode.dataOutboxUnavailable,
        ),
      ),
    );
    expect(File('${dir.path}/outbox.pxk').existsSync(), isFalse);
    expect(services.outbox!.length, 0);
  });

  test('queued mutations survive a restart and replay once their source is known; no token is stored and the replay asks the delegate again [DAT-020] [HST-010]', () async {
    final kv = _MemoryKv();
    var services = make(kv);
    var s = scope(services, withAuth: true);
    services.setNetworkAvailable(false);
    await create(s, 'A');
    expect(jsonEncode(kv.content), isNot(contains('tok-123')));
    expect(jsonEncode(kv.content), isNot(contains('uthorization')));
    await services.close();

    // A new runtime: the entry waits until a page of the plugin builds
    // its data, because only then is the source known.
    auth.token = 'tok-456';
    services = make(kv);
    addTearDown(services.close);
    await services.outbox!.replay();
    expect(api.seen, isEmpty);
    expect(services.outbox!.length, 1);
    s = scope(services, withAuth: true);
    await services.outbox!.replay();
    expect(api.seen, hasLength(1));
    expect(api.seen.single.auth, 'Bearer tok-456');
    expect(events.seen, ['synced tasks']);

    // Without a signed-in user an entry waits.
    services.setNetworkAvailable(false);
    await create(s, 'B');
    auth.token = null;
    services.setNetworkAvailable(true);
    await Future<void>.delayed(const Duration(milliseconds: 100));
    expect(services.outbox!.length, 1);
    expect(events.seen, hasLength(1));
  });

  test('logout and Plux.wipeData clear the outbox, so the next user\'s session never replays the previous user\'s mutations [DAT-020] [HST-001]', () async {
    final kv = _MemoryKv();
    final services = make(kv);
    addTearDown(services.close);
    final s = scope(services);
    services.setNetworkAvailable(false);
    await create(s, 'A');
    expect(kv.content['entries'], hasLength(1));
    await services.clearCache();
    expect(services.outbox!.length, 0);
    expect(kv.content, isEmpty);
    expect(kv.wipes, 1);
    services.setNetworkAvailable(true);
    await Future<void>.delayed(const Duration(milliseconds: 50));
    expect(api.seen, isEmpty);
  });

  test('a runtime without an outbox refuses an offline-capable mutation with PLX-5121 [DAT-020]', () async {
    final services = DataServices(
      transport: ClientTransport(http.Client()),
      authDelegate: () => null,
      environment: 'production',
      record: (name, {fields = const {}, route = '', pluginKey = ''}) {},
      report: reports.add,
      allowCleartext: true,
    );
    final s = scope(services);
    await expectLater(
      create(s, 'A'),
      throwsA(
        isA<ActionError>().having(
          (e) => e.code,
          'code',
          PluxErrorCode.dataOutboxUnavailable,
        ),
      ),
    );
    expect(api.seen, isEmpty);
  });
}

final class _FailingSecrets implements SecretStore {
  @override
  Future<String?> read(String name) async => throw StateError('no keystore');

  @override
  Future<void> write(String name, String value) async =>
      throw StateError('no keystore');

  @override
  Future<void> delete(String name) async {}
}
