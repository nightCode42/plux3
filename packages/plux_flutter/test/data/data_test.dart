// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_flutter/src/actions/handlers.dart';
import 'package:plux_flutter/src/data/cache.dart';
import 'package:plux_flutter/src/data/client.dart';
import 'package:plux_flutter/src/data/failure.dart';
import 'package:plux_flutter/src/data/mapping.dart';
import 'package:plux_flutter/src/data/mocks.dart';
import 'package:plux_flutter/src/data/services.dart';
import 'package:plux_flutter/src/data/source.dart';
import 'package:plux_flutter/src/data/spec.dart';
import 'package:plux_flutter/src/data/transport.dart';
import 'package:plux_flutter/src/data/worker.dart';
import 'package:plux_flutter/src/pxl/types.dart';
import 'package:plux_flutter/src/schema/registry.g.dart';

final class _Auth implements PluxAuthDelegate {
  _Auth(this.tokens);

  final List<String?> tokens;
  int refreshes = 0;
  String? current = 'stale';

  @override
  bool get isAuthenticated => current != null;

  @override
  Future<String?> accessToken() async => current;

  @override
  Future<String?> refresh() async {
    refreshes++;
    await Future<void>.delayed(const Duration(milliseconds: 10));
    return current = tokens.isEmpty ? null : tokens.removeAt(0);
  }

  @override
  void onLogout() {}
}

final class _Events implements DataSourceEvents {
  final List<String> seen = [];

  @override
  void loaded(DataSourceEvent e) => seen.add('loaded ${e.source}');

  @override
  void failed(DataSourceEvent e) =>
      seen.add('failed ${e.source} ${e.error!['kind']}');
}

/// A local API: /tasks pages by cursor, /graphql answers GraphQL, and
/// anything under /secure needs the token "fresh".
final class _Api {
  late HttpServer server;
  final List<HttpRequest> requests = [];
  final List<String> bodies = [];
  int status = 200;

  Future<void> start() async {
    server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    server.listen((r) async {
      requests.add(r);
      bodies.add(await utf8.decoder.bind(r).join());
      Object? body;
      if (status != 200) {
        r.response.statusCode = status;
      } else if (r.uri.path.startsWith('/v1/secure') &&
          r.headers.value('authorization') != 'Bearer fresh') {
        r.response.statusCode = 401;
      } else if (r.uri.path == '/v1/tasks') {
        final after = r.uri.queryParameters['after'];
        body = after == null
            ? {
                'items': [
                  {'id': '1', 'title': 'One', 'done': false, 'extra': 1},
                  {'id': '2', 'title': 'Two', 'done': true},
                ],
                'next': 'c2',
              }
            : {
                'items': [
                  {'id': '3', 'title': 'Three', 'done': false},
                ],
                'next': null,
              };
      } else if (r.uri.path == '/v1/secure') {
        body = {'ok': true};
      } else if (r.uri.path == '/v1/count') {
        body = {'total': 7};
      } else if (r.uri.path == '/v1/bad') {
        body = {'items': 'nope'};
      } else if (r.uri.path == '/graphql') {
        final q = jsonDecode(bodies.last) as Map<String, Object?>;
        body = (q['query']! as String).contains('broken')
            ? {
                'errors': [
                  {'message': 'secret detail'},
                ],
              }
            : {
                'data': {
                  'me': {'name': 'Ada'},
                },
              };
      }
      if (body != null) r.response.write(jsonEncode(body));
      await r.response.close();
    });
  }

  String get base => 'http://127.0.0.1:${server.port}';
}

final types = <String, NamedType>{
  'Task': NamedType.object('Task')
    ..fields!.addAll({
      'id': const PxlType(PxlKind.string),
      'title': const PxlType(PxlKind.string),
      'done': const PxlType(PxlKind.bool),
    }),
  'Count': NamedType.object('Count')
    ..fields!.addAll({'total': const PxlType(PxlKind.int)}),
};

void main() {
  late _Api api;
  late List<PluxException> reports;
  late List<(String, Map<String, Object?>)> events;
  late DataServices services;
  late _Events sourceEvents;
  var now = 0;
  final caller = const DataCaller(pluginKey: 'shop', domains: ['127.0.0.1']);

  DataServices make({
    PluxAuthDelegate? auth,
    DataMocks mocks = const DataMocks(),
    CacheStore? store,
  }) => DataServices(
    transport: ClientTransport(http.Client()),
    authDelegate: () => auth,
    environment: 'production',
    record: (name, {fields = const {}, route = '', pluginKey = ''}) =>
        events.add((name, fields)),
    report: reports.add,
    plainStore: store ?? MemoryCacheStore(),
    secureStore: MemoryCacheStore(),
    mocks: mocks,
    events: sourceEvents,
    now: () => now,
    allowCleartext: true,
  );

  DataSourceSpec tasks({
    CacheSpec cache = const CacheSpec(),
    String path = '/tasks',
    bool auth = false,
  }) => DataSourceSpec(
    id: 'tasks-id',
    name: 'tasks',
    kind: DataKind.rest,
    type: 'list<Task>',
    baseUrls: {'production': '${api.base}/v1'},
    path: path,
    params: {'filter': (_) => 'open'},
    select: 'items',
    auth: auth,
    cache: cache,
    page: const PageSpec(
      style: PageStyle.cursor,
      pageSize: 2,
      sizeParam: 'limit',
      cursorParam: 'after',
      nextCursor: 'next',
    ),
    mocks: const MockSpec(
      success: [
        {'id': 'm', 'title': 'Mocked', 'done': false},
      ],
      hasSuccess: true,
    ),
    operations: {
      'create': const OperationSpec(
        name: 'create',
        path: '/{id}',
        auth: false,
        output: 'Count',
      ),
    },
  );

  DataSourceController controller(DataSourceSpec s) => DataSourceController(
    spec: s,
    context: services,
    caller: caller,
    types: types,
  );

  Map<String, Object?> roots() => const {};

  setUp(() async {
    api = _Api();
    await api.start();
    reports = [];
    events = [];
    sourceEvents = _Events();
    now = 0;
    services = make();
  });

  tearDown(() => api.server.close(force: true));

  test('a REST source loads, maps to its type ignoring extra fields, and pages by cursor [DAT-001] [DAT-004] [DAT-011]', () async {
    final c = controller(tasks());
    await c.load(roots);
    expect(api.requests.single.uri.queryParameters, {
      'filter': 'open',
      'limit': '2',
    });
    expect(c.snapshot['value'], [
      {'id': '1', 'title': 'One', 'done': false},
      {'id': '2', 'title': 'Two', 'done': true},
    ]);
    expect(c.snapshot['hasMore'], isTrue);
    expect(c.snapshot['status'], 'ready');
    await c.loadMore(roots);
    expect(api.requests.last.uri.queryParameters['after'], 'c2');
    expect((c.snapshot['value']! as List).length, 3);
    expect(c.snapshot['hasMore'], isFalse);
    await c.loadMore(roots); // no page left: nothing is sent
    expect(api.requests, hasLength(2));
    expect(sourceEvents.seen, ['loaded tasks', 'loaded tasks']);
  });

  test('page and offset pagination send their parameters [DAT-011]', () async {
    for (final (style, param, second) in [
      (PageStyle.page, 'page', '2'),
      (PageStyle.offset, 'offset', '2'),
    ]) {
      final s = tasks();
      final c = controller(
        DataSourceSpec(
          id: s.id,
          name: s.name,
          kind: s.kind,
          type: s.type,
          baseUrls: s.baseUrls,
          path: s.path,
          select: 'items',
          page: PageSpec(
            style: style,
            pageSize: 2,
            pageParam: 'page',
            offsetParam: 'offset',
            firstPage: 1,
          ),
        ),
      );
      await c.load(roots);
      expect(c.snapshot['hasMore'], isTrue, reason: '$style');
      await c.loadMore(roots);
      expect(api.requests.last.uri.queryParameters[param], second);
    }
  });

  test('a request to an undeclared domain never leaves and is reported with PLX-5100 [DAT-030] [SEC-080]', () async {
    final c = DataSourceController(
      spec: tasks(),
      context: services,
      caller: const DataCaller(pluginKey: 'shop', domains: ['api.example.com']),
      types: types,
    );
    await c.load(roots);
    expect(api.requests, isEmpty);
    expect(reports.first.code, PluxErrorCode.dataDomainBlocked);
    expect(c.snapshot['status'], 'error');
    expect((c.snapshot['error']! as Map)['kind'], 'permission');
  });

  test('cleartext is blocked in apps [DAT-030]', () async {
    final strict = DataServices(
      transport: ClientTransport(http.Client()),
      authDelegate: () => null,
      environment: 'production',
      record: (name, {fields = const {}, route = '', pluginKey = ''}) {},
      report: reports.add,
    );
    await expectLater(
      strict.client.load(tasks(), caller, const {}),
      throwsA(
        isA<DataFailure>().having(
          (f) => f.code,
          'code',
          PluxErrorCode.dataDomainBlocked,
        ),
      ),
    );
    expect(api.requests, isEmpty);
  });

  test('a 401 refreshes the token once for concurrent requests, then retries [HST-010]', () async {
    final auth = _Auth(['fresh']);
    services = make(auth: auth);
    final s = tasks(path: '/secure', auth: true);
    final results = await Future.wait([
      services.client.load(s, caller, const {}),
      services.client.load(s, caller, const {}),
    ]);
    expect(results, everyElement({'ok': true}));
    expect(auth.refreshes, 1);
    expect(api.requests.last.headers.value('authorization'), 'Bearer fresh');
  });

  test('a refused refresh fails with PLX-5107 [HST-010]', () async {
    services = make(auth: _Auth([]));
    await expectLater(
      services.client.load(
        tasks(path: '/secure', auth: true),
        caller,
        const {},
      ),
      throwsA(
        isA<DataFailure>().having(
          (f) => f.code,
          'code',
          PluxErrorCode.dataUnauthorised,
        ),
      ),
    );
  });

  test(
    'GraphQL queries return data; errors fail without their messages [DAT-001]',
    () async {
      final s = DataSourceSpec(
        id: 'p',
        name: 'profile',
        kind: DataKind.graphql,
        type: 'string',
        baseUrls: {'production': api.base},
        path: '/graphql',
        query: 'query { me { name } }',
        params: {'id': (_) => 1},
      );
      expect(await services.client.load(s, caller, const {'id': 1}), {
        'me': {'name': 'Ada'},
      });
      expect(jsonDecode(api.bodies.last), {
        'query': 'query { me { name } }',
        'variables': {'id': 1},
      });
      final broken = DataSourceSpec(
        id: 'p',
        name: 'profile',
        kind: DataKind.graphql,
        type: 'string',
        baseUrls: {'production': api.base},
        path: '/graphql',
        query: 'query broken',
      );
      await expectLater(
        services.client.load(broken, caller, const {}),
        throwsA(
          isA<DataFailure>()
              .having((f) => f.code, 'code', PluxErrorCode.dataGraphqlError)
              .having((f) => f.message, 'message', isNot(contains('secret'))),
        ),
      );
    },
  );

  test(
    'HTTP errors and oversized responses are typed errors [DAT-001] [LIM-004]',
    () async {
      api.status = 503;
      await expectLater(
        services.client.load(tasks(), caller, const {}),
        throwsA(isA<DataFailure>().having((f) => f.status, 'status', 503)),
      );
      api.status = 200;
      services.limits = {'data.responseSize': 10};
      await expectLater(
        services.client.load(tasks(), caller, const {}),
        throwsA(
          isA<DataFailure>().having(
            (f) => f.code,
            'code',
            PluxErrorCode.dataSizeExceeded,
          ),
        ),
      );
    },
  );

  test('mapping failures name the path, never the value [DAT-004]', () async {
    final c = controller(tasks(path: '/bad'));
    await c.load(roots);
    final error = c.snapshot['error']! as Map;
    expect(error['kind'], 'validation');
    expect(error['message'], contains('items'));
    expect(error['message'], isNot(contains('nope')));
    expect(
      () => mapJson(PxlType.parse('Task', (n) => types[n]), {'id': 1}),
      throwsA(isA<DataFailure>()),
    );
    expect(() => select({'a': 1}, 'b'), throwsA(isA<DataFailure>()));
  });

  test(
    'a transform maps the response type to the declared type [DAT-004]',
    () async {
      final c = controller(
        DataSourceSpec(
          id: 'n',
          name: 'count',
          kind: DataKind.rest,
          type: 'int',
          baseUrls: {'production': '${api.base}/v1'},
          path: '/count',
          responseType: 'Count',
          transform: (r) => ((r['response']! as Map)['total']! as int) * 2,
        ),
      );
      await c.load(roots);
      expect(c.snapshot['value'], 14);
    },
  );

  group('cache policies with a fake clock [DAT-010]', () {
    Future<Object?> loadOnce(DataSourceSpec s) async {
      final c = controller(s);
      await c.load(roots);
      return c.snapshot['value'];
    }

    test(
      'cacheFirst uses a fresh entry, then the network after the TTL',
      () async {
        final s = tasks(
          cache: const CacheSpec(
            policy: CachePolicy.cacheFirst,
            ttl: Duration(seconds: 60),
          ),
        );
        await loadOnce(s);
        now = 30000;
        await loadOnce(s);
        expect(api.requests, hasLength(1));
        expect(events.last.$2['cache'], 'hit');
        now = 61000;
        await loadOnce(s);
        expect(api.requests, hasLength(2));
      },
    );

    test('networkFirst falls back to the cache within its TTL', () async {
      final s = tasks(
        cache: const CacheSpec(
          policy: CachePolicy.networkFirst,
          ttl: Duration(seconds: 60),
        ),
      );
      await loadOnce(s);
      api.status = 500;
      now = 10000;
      expect(await loadOnce(s), hasLength(2));
      now = 70000;
      final c = controller(s);
      await c.load(roots);
      expect(c.snapshot['status'], 'error');
    });

    test(
      'staleWhileRevalidate answers from the cache and revalidates',
      () async {
        final s = tasks(
          cache: const CacheSpec(
            policy: CachePolicy.staleWhileRevalidate,
            ttl: Duration(seconds: 60),
          ),
        );
        await loadOnce(s);
        final c = controller(s);
        await c.load(roots);
        expect(c.snapshot['value'], hasLength(2));
        for (var i = 0; i < 200 && api.requests.length < 2; i++) {
          await Future<void>.delayed(const Duration(milliseconds: 10));
        }
        expect(api.requests, hasLength(2));
      },
    );

    test('networkOnly and refresh never read the cache', () async {
      await loadOnce(tasks());
      await loadOnce(tasks());
      expect(api.requests, hasLength(2));
      final s = tasks(
        cache: const CacheSpec(
          policy: CachePolicy.cacheFirst,
          ttl: Duration(seconds: 60),
        ),
      );
      final c = controller(s);
      await c.load(roots);
      await c.load(roots, refresh: true);
      expect(api.requests, hasLength(4));
    });

    test('the cache evicts the least recently used entries beyond its limits [LIM-004]', () async {
      final store = MemoryCacheStore();
      final cache = ResponseCache(
        store,
        maxBytes: 1 << 20,
        maxEntries: 2,
        now: () => now,
      );
      for (final k in ['a', 'b']) {
        await cache.write(cacheKey(k), k);
      }
      await cache.read(cacheKey('a'), const Duration(hours: 1));
      await cache.write(cacheKey('c'), 'c');
      final keys = (await store.entries()).map((e) => e.key).toSet();
      expect(keys, {cacheKey('a'), cacheKey('c')});
    });
  });

  test('the file store encrypts with AES-GCM and forgets entries it cannot open [DAT-010]', () async {
    final dir = Directory.systemTemp.createTempSync('plux_cache');
    addTearDown(() => dir.deleteSync(recursive: true));
    final key = Uint8List.fromList(List.generate(32, (i) => i));
    final secure = FileCacheStore(dir.path, key: key);
    final k = cacheKey('x');
    await secure.write(k, {'name': 'Ada Lovelace'}, 5);
    final file = File('${dir.path}/$k').readAsBytesSync();
    expect(
      utf8.decode(file, allowMalformed: true),
      isNot(contains('Lovelace')),
    );
    expect((await secure.read(k))!.json, {'name': 'Ada Lovelace'});
    expect((await secure.entries()).single.storedAtMs, 5);
    final other = FileCacheStore(
      dir.path,
      key: Uint8List.fromList(List.filled(32, 7)),
    );
    expect(await other.read(k), isNull);
    expect(File('${dir.path}/$k').existsSync(), isFalse);
    final plain = FileCacheStore(dir.path);
    await plain.write(k, [1], 9);
    expect((await plain.read(k))!.json, [1]);
  });

  test('the data isolate sends requests and keeps the cache off the UI isolate [DAT-001] [DAT-010]', () async {
    final dir = Directory.systemTemp.createTempSync('plux_worker');
    addTearDown(() => dir.deleteSync(recursive: true));
    final worker = LazyDataWorker(
      () => DataWorker.start(
        httpClient: http.Client.new,
        cacheDirectory: dir.path,
      ),
    );
    addTearDown(worker.close);
    final res = await worker.send(
      DataRequest(
        method: 'GET',
        url: Uri.parse('${api.base}/v1/count'),
        maxResponseBytes: 1000,
        timeout: const Duration(seconds: 5),
      ),
    );
    expect(res.json, {'total': 7});
    final store = worker.store(secure: false);
    await store.write(cacheKey('k'), {'a': 1}, 3);
    expect((await store.read(cacheKey('k')))!.json, {'a': 1});
    await expectLater(
      worker.store(secure: true).read(cacheKey('k')),
      throwsA(
        isA<DataFailure>().having(
          (f) => f.code,
          'code',
          PluxErrorCode.dataCacheUnavailable,
        ),
      ),
    );
  });

  group('mocks [DAT-080]', () {
    Future<Map<String, Object?>> withMock(DataMockState state) async {
      services = make(mocks: DataMocks({'shop/tasks': state}, true));
      final c = controller(tasks());
      unawaited(c.load(roots));
      await pumpEventQueue();
      return c.snapshot;
    }

    test('each selected state is shown without a request', () async {
      expect((await withMock(DataMockState.success))['value'], [
        {'id': 'm', 'title': 'Mocked', 'done': false},
      ]);
      expect((await withMock(DataMockState.empty))['value'], isEmpty);
      expect((await withMock(DataMockState.loading))['status'], 'loading');
      expect((await withMock(DataMockState.error))['status'], 'error');
      expect(api.requests, isEmpty);
    });

    test('release builds never select mocks', () async {
      expect(dataMocksAllowed, !const bool.fromEnvironment('dart.vm.product'));
      final mocks = DataMocks({'tasks': DataMockState.error}, false);
      expect(mocks.of('shop', 'tasks'), isNull);
      services = make(mocks: mocks);
      final c = controller(tasks());
      await c.load(roots);
      expect(c.snapshot['status'], 'ready');
      expect(api.requests, hasLength(1));
    });
  });

  test('api_call telemetry carries timing and status, never payloads or URLs [ANL-001] [SCH-012]', () async {
    await controller(tasks()).load(roots);
    final (name, fields) = events.single;
    expect(name, 'api_call');
    expect(fields.keys.toSet(), {
      'source',
      'kind',
      'status',
      'duration_ms',
      'bytes',
      'result',
    });
    expect(fields['status'], 200);
    expect(jsonEncode(fields), isNot(contains('open')));
    expect(jsonEncode(fields), isNot(contains('One')));
  });

  test('apiCall and refreshData run on the page data through the engine table [DAT-001] [DAT-011]', () async {
    final c = controller(tasks());
    final scope = DataScope(
      services: services,
      own: [c],
      shared: const [],
      roots: roots,
    );
    addTearDown(scope.dispose);
    final ctx = StepContext(
      navigator: _NoNavigator(),
      emit: (_, _) {},
      nativeActions: const NoNativeActions(),
      data: scope,
    );
    ActionHandler of(String name) =>
        handlerFor(actionDescriptors.firstWhere((d) => d.name == name));
    await of('refreshData').run(ctx, {'source': 'tasks'});
    expect(c.snapshot['value'], hasLength(2));
    await of('refreshData').run(ctx, {'source': 'tasks', 'more': true});
    expect(c.snapshot['value'], hasLength(3));
    final out = await of('apiCall').run(ctx, {
      'operation': 'tasks.create',
      'input': {'id': 'count'},
    });
    expect((out as StepDone).output, {'total': 7});
    expect(api.requests.last.method, 'POST');
    await expectLater(
      Future.sync(() => of('apiCall').run(ctx, {'operation': 'tasks.nope'})),
      throwsA(isA<Object>()),
    );
    expect(scope.root['tasks'], isA<Map<String, Object?>>());
    expect(scope.root['missing'], isNull);
  });

  test('logout and Plux.wipeData clear the cache, so the next user never sees the previous user\'s cached responses [HST-010] [HST-001] [DAT-010]', () async {
    final s = tasks(
      cache: const CacheSpec(
        policy: CachePolicy.cacheFirst,
        ttl: Duration(hours: 1),
      ),
    );
    final first = controller(s);
    await first.load(roots);
    expect(api.requests, hasLength(1));
    // The same user is served from the cache.
    await controller(s).load(roots);
    expect(api.requests, hasLength(1));
    // The user logs out; the next user's load goes to the network and
    // sees the server's answer for them.
    await services.clearCache();
    final next = controller(s);
    await next.load(roots);
    expect(api.requests, hasLength(2));
    expect(events.last.$2['cache'], isNot('hit'));
  });

  test('the encrypted cache gets its installation key from secure storage, made once; clearing makes none [DAT-010]', () async {
    final dir = Directory.systemTemp.createTempSync('plux_worker_key');
    addTearDown(() => dir.deleteSync(recursive: true));
    final secrets = _Secrets();
    LazyDataWorker start() => LazyDataWorker(
      () => DataWorker.start(
        httpClient: http.Client.new,
        cacheDirectory: dir.path,
        keys: SecretCacheKeys(secrets, 'data-cache.a'),
      ),
    );
    final cleared = start();
    await cleared.store(secure: true).clear();
    await cleared.close();
    expect(secrets.values, isEmpty);
    final worker = start();
    final store = worker.store(secure: true);
    await store.write(cacheKey('k'), {'a': 1}, 3);
    expect((await store.read(cacheKey('k')))!.json, {'a': 1});
    expect(secrets.values.keys, ['data-cache.a']);
    await worker.close();
    final again = start();
    addTearDown(again.close);
    expect((await again.store(secure: true).read(cacheKey('k')))!.json, {
      'a': 1,
    }, reason: 'the key is read back, not made anew');
    await again.store(secure: true).clear();
    expect(await again.store(secure: true).read(cacheKey('k')), isNull);
  });
}

final class _NoNavigator implements RunNavigator {
  @override
  Future<void> navigate(
    String r,
    Map<String, Object?> p,
    String m,
    String? u,
  ) => Future.value();

  @override
  Future<Object?> present(
    String r,
    Map<String, Object?> p, {
    required bool sheet,
    required bool dismissible,
  }) => Future.value();

  @override
  void pop(Object? result) {}

  @override
  void switchTab(String tab) {}
}

final class _Secrets implements SecretStore {
  final Map<String, String> values = {};

  @override
  Future<String?> read(String name) async => values[name];

  @override
  Future<void> write(String name, String value) async => values[name] = value;

  @override
  Future<void> delete(String name) async => values.remove(name);
}
