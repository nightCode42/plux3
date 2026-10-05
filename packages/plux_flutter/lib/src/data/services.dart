// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// What the runtime gives the data layer (ADR-0048), and the data of one
/// page: its own sources and those of its plugin and the app, read by PXL
/// as `data.<name>` and run by `apiCall` and `refreshData`.
library;

import 'dart:async';
import 'dart:collection';
import 'dart:math';

import 'package:flutter/foundation.dart';
import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/core/config.dart' show PluxAuthDelegate;
import 'package:plux_flutter/src/data/cache.dart';
import 'package:plux_flutter/src/data/client.dart';
import 'package:plux_flutter/src/data/failure.dart';
import 'package:plux_flutter/src/data/mapping.dart';
import 'package:plux_flutter/src/data/mocks.dart';
import 'package:plux_flutter/src/data/outbox.dart';
import 'package:plux_flutter/src/data/source.dart';
import 'package:plux_flutter/src/data/spec.dart';
import 'package:plux_flutter/src/data/stream_session.dart';
import 'package:plux_flutter/src/data/stream_transport.dart';
import 'package:plux_flutter/src/data/streams.dart';
import 'package:plux_flutter/src/data/transfer_transport.dart';
import 'package:plux_flutter/src/data/transfers.dart';
import 'package:plux_flutter/src/data/transport.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/pxl/types.dart';
import 'package:plux_flutter/src/store/kv_store.dart' show PluxKeyValueStore;

/// The data layer's runtime services: one per runtime.
final class DataServices implements DataContext {
  /// Creates the services. [plainStore] and [secureStore] hold cached
  /// responses; without one, sources of that kind are not cached.
  DataServices({
    required this.transport,
    required PluxAuthDelegate? Function() authDelegate,
    required this.environment,
    required this.record,
    required void Function(PluxException e) report,
    this.plainStore,
    this.secureStore,
    this.mocks = const DataMocks(),
    this.events,
    this.ioEvents,
    this.streamTransport,
    this.transferTransport,
    this.outboxStore,
    this.runtimeRoot = '',
    this.downloadDirectory = '',
    this.random,
    this.scheduler,
    int Function()? now,
    this.allowCleartext = false,
  }) : _report = report, // ignore: prefer_initializing_formals
       _auth = AuthSession(authDelegate),
       now = now ?? (() => DateTime.now().millisecondsSinceEpoch);

  /// Sends requests (the data isolate in apps).
  final DataTransport transport;

  /// Plain cached responses.
  final CacheStore? plainStore;

  /// Encrypted cached responses.
  final CacheStore? secureStore;

  @override
  final String environment;

  @override
  final DataRecord record;

  @override
  final DataMocks mocks;

  @override
  DataSourceEvents? events;

  @override
  DataIoEvents? ioEvents;

  /// Opens stream connections (the data isolate in apps); without one,
  /// streams cannot be opened.
  final StreamTransport? streamTransport;

  /// Moves files (the data isolate in apps); without one, transfers fail.
  final TransferTransport? transferTransport;

  /// Where the offline outbox rests, encrypted; without one, offline
  /// mutations are refused (DAT-020).
  final PluxKeyValueStore? outboxStore;

  /// The runtime's private directory: nothing is uploaded from it but
  /// downloads.
  final String runtimeRoot;

  /// Where downloads are saved.
  final String downloadDirectory;

  /// The source of jitter of reconnection and replay waits; tests inject
  /// one.
  final Random? random;

  /// Starts the timers of reconnection and replay; tests inject one.
  final StreamScheduler? scheduler;

  /// The clock, in milliseconds.
  final int Function() now;

  /// Tests against a local server allow `http`.
  final bool allowCleartext;

  final void Function(PluxException e) _report;
  final AuthSession _auth;
  DataLimits _limits = DataLimits.of(const {});
  ResponseCache? _plain, _secure;

  /// Applies an app bundle's limits.
  set limits(Map<String, int> limits) => _limits = DataLimits.of(limits);

  @override
  late final DataClient client = DataClient(
    transport: transport,
    auth: _auth,
    limits: () => _limits,
    environment: environment,
    record: record,
    report: _report,
    allowCleartext: allowCleartext,
    onSuccess: () => outbox?.replaySoon(),
  );

  bool _network = true;
  final Map<String, ({DataSourceSpec spec, DataCaller caller})> _known = {};

  @override
  void register(DataSourceSpec spec, DataCaller caller) {
    _known['${caller.pluginKey}/${spec.name}'] = (spec: spec, caller: caller);
  }

  /// The open streams, or null without a stream transport.
  @override
  late final DataStreams? streams = streamTransport == null
      ? null
      : DataStreams(
          transport: streamTransport!,
          client: client,
          limits: () => _limits,
          random: random,
          scheduler: scheduler,
          networkAvailable: () => _network,
        );

  /// The file transfers, or null without a transfer transport.
  late final TransferClient? transfers = transferTransport == null
      ? null
      : TransferClient(
          transport: transferTransport!,
          client: client,
          root: runtimeRoot,
          downloads: downloadDirectory,
        );

  /// The offline outbox, or null without a store (DAT-020).
  late final Outbox? outbox = outboxStore == null
      ? null
      : Outbox(
          store: outboxStore!,
          limits: () => _limits,
          resolve: (plugin, source) => _known['$plugin/$source'],
          send: (spec, op, caller, input, key) async {
            var status = 0;
            await client.call(
              spec,
              op,
              caller,
              input,
              headers: {idempotencyHeader: key},
              onStatus: (s) => status = s,
            );
            return status;
          },
          emit: _outboxEnded,
          report: _report,
          now: now,
          random: random,
          scheduler: scheduler,
          networkAvailable: () => _network,
        );

  void _outboxEnded(OutboxOutcome outcome, OutboxEntry e, int status) {
    ioEvents?.outbox(
      outcome,
      DataSourceEvent(
        pluginKey: e.plugin,
        source: e.source,
        sourceId: _known['${e.plugin}/${e.source}']?.spec.id ?? '',
        value: {
          'operation': '${e.source}.${e.operation}',
          'key': e.key,
          'status': status,
        },
      ),
    );
  }

  /// The host says whether the network is available (D13): when it is
  /// back, streams waiting to reconnect do so now and the outbox
  /// replays; while it is not, mutations of offline-capable operations
  /// are queued without trying.
  void setNetworkAvailable(bool available) {
    final back = available && !_network;
    _network = available;
    if (!available) return;
    streams?.networkBack();
    outbox?.replaySoon(reset: back);
  }

  /// The app came to the foreground: the outbox replays.
  void appResumed() => outbox?.replaySoon(reset: true);

  /// Runs the offline-capable mutation [op] (DAT-020): sent at once with
  /// its idempotency key unless the host says the network is down or
  /// earlier mutations of the plugin wait; queued when the network
  /// fails. Completes with `queued` and no JSON in that case, and fails
  /// with a typed error when the outbox is full.
  Future<({bool queued, Object? json})> callOffline(
    DataSourceSpec s,
    OperationSpec op,
    DataCaller caller,
    Map<String, Object?> input,
  ) async {
    final box = outbox;
    if (box == null) {
      throw const DataFailure(
        ActionErrorKind.custom,
        PluxErrorCode.dataOutboxUnavailable,
        'this runtime keeps no outbox: the offline mutation was refused',
      );
    }
    final key = box.newKey();
    if (_network && !await box.hasPending(caller.pluginKey)) {
      try {
        final json = await client.call(
          s,
          op,
          caller,
          input,
          headers: {idempotencyHeader: key},
        );
        return (queued: false, json: json);
      } on DataFailure catch (f) {
        final passes =
            f.code == PluxErrorCode.dataNetworkFailed ||
            f.code == PluxErrorCode.dataRequestTimeout;
        if (!passes) rethrow;
      }
    }
    await box.enqueue(
      OutboxEntry(
        key: key,
        plugin: caller.pluginKey,
        source: s.name,
        operation: op.name,
        input: {for (final e in input.entries) e.key: toJson(e.value)},
        queuedAtMs: now(),
      ),
    );
    return (queued: true, json: null);
  }

  @override
  ResponseCache? cache({required bool encrypted}) {
    ResponseCache? make(CacheStore? s) => s == null
        ? null
        : ResponseCache(
            s,
            maxBytes: _limits.cacheBytes,
            maxEntries: _limits.cacheEntries,
            now: now,
          );
    return encrypted
        ? _secure ??= make(secureStore)
        : _plain ??= make(plainStore);
  }

  /// Removes every cached response: the seam for logout and
  /// `Plux.wipeData`.
  Future<void> clearCache() async {
    for (final c in [cache(encrypted: false), cache(encrypted: true)]) {
      try {
        await c?.clear();
      } on DataFailure catch (f) {
        _report(f.toException(const {}));
      }
    }
    // Queued mutations of the previous user must not be replayed for the
    // next (DAT-020).
    await outbox?.clear();
  }

  /// Closes the streams and stops the outbox's timer.
  Future<void> close() async {
    await streams?.closeAll();
    outbox?.dispose();
  }

  @override
  void report(PluxException e) => _report(e);

  final Expando<Map<String, DataSourceController>> _shared = Expando();

  /// The controller of a plugin- or app-level [spec] for [caller], shared
  /// by the plugin's pages of the release [owner].
  DataSourceController shared(
    Object owner,
    DataSourceSpec spec,
    DataCaller caller,
    Map<String, NamedType> types,
  ) => (_shared[owner] ??= {}).putIfAbsent(
    '${caller.pluginKey}/${spec.id}',
    () => DataSourceController(
      spec: spec,
      context: this,
      caller: caller,
      types: types,
    ),
  );
}

/// What `apiCall` and `refreshData` run on: the data of the page that runs
/// them (DAT-001, DAT-011).
abstract interface class DataActions {
  /// Runs `<source>.<operation>` with [input]; completes with the output
  /// in PXL form, or fails with an [ActionError].
  Future<Object?> callOperation(
    String operation,
    Map<String, Object?> input, {
    CancelToken? cancel,
  });

  /// Reloads [source] bypassing its cache, or with [more] loads its next
  /// page; fails with an [ActionError].
  Future<void> refresh(String source, {required bool more});

  /// Opens [stream] with [params] (DAT-012); fails with an [ActionError]
  /// when it cannot be opened.
  Future<void> subscribe(String stream, Map<String, Object?> params);

  /// Closes [stream]; nothing happens when it is not open.
  Future<void> unsubscribe(String stream);
}

/// The data a page sees: its sources, then its plugin's, then the app's.
final class DataScope extends ChangeNotifier implements DataActions {
  /// Creates the scope; [own] are the page's controllers, [shared] its
  /// plugin's and the app's, and [roots] the page's PXL roots.
  DataScope({
    required this.services,
    required List<DataSourceController> own,
    required List<DataSourceController> shared,
    required this.roots,
  }) : _own = own {
    for (final c in [...shared.reversed, ...own.reversed]) {
      _byName[c.spec.name] = c;
    }
    for (final c in _byName.values) {
      c.addListener(notifyListeners);
    }
  }

  /// The services.
  final DataServices services;

  /// The page's roots, for parameters and transforms.
  final Map<String, Object?> Function() roots;

  final List<DataSourceController> _own;
  final Map<String, DataSourceController> _byName = {};

  /// The controller of [name], or null.
  DataSourceController? operator [](String name) => _byName[name];

  /// The `data` root: each source's snapshot, loading it on first read.
  late final Map<String, Object?> root = _DataRoot(this);

  @override
  Future<Object?> callOperation(
    String operation,
    Map<String, Object?> input, {
    CancelToken? cancel,
  }) async {
    final (name, op) = switch (operation.split('.')) {
      [final s, final o] => (s, o),
      _ => ('', ''),
    };
    final c = _byName[name];
    final spec = c?.spec.operations[op];
    if (c == null || spec == null) {
      throw DataFailure.unavailable('no operation $operation').toActionError();
    }
    final mock = services.mocks.of(c.caller.pluginKey, name);
    if (mock == DataMockState.error) {
      throw (c.spec.mocks.error ??
              const DataFailure(
                ActionErrorKind.http,
                PluxErrorCode.dataHttpError,
                'the error mock is selected',
              ))
          .toActionError();
    }
    if (mock != null) return null;
    try {
      if (spec.transfer != null) return await _transfer(c, spec, input, cancel);
      final Object? raw;
      if ((spec.offlineCapable ?? c.spec.offlineCapable) &&
          spec.mutates(c.spec.kind)) {
        final r = await services.callOffline(c.spec, spec, c.caller, input);
        if (r.queued) return null;
        raw = r.json;
      } else {
        raw = await services.client.call(c.spec, spec, c.caller, input);
      }
      return _output(c, spec, raw);
    } on DataFailure catch (f) {
      throw f.toActionError();
    }
  }

  Object? _output(DataSourceController c, OperationSpec spec, Object? raw) {
    final out = spec.output;
    if (out == null) return null;
    return mapJson(
      PxlType.parse(out, (n) => c.types[n]),
      select(raw, spec.select),
    );
  }

  Future<Object?> _transfer(
    DataSourceController c,
    OperationSpec spec,
    Map<String, Object?> input,
    CancelToken? cancel,
  ) async {
    final transfers = services.transfers;
    if (transfers == null) {
      throw DataFailure.unavailable('this runtime cannot transfer files');
    }
    final raw = await transfers.run(
      c.spec,
      spec,
      c.caller,
      input,
      cancel: cancel,
      onProgress: (p) => services.ioEvents?.progress(
        DataSourceEvent(
          pluginKey: c.caller.pluginKey,
          source: c.spec.name,
          sourceId: c.spec.id,
          value: {
            'operation': '${c.spec.name}.${spec.name}',
            'sent': p.sent,
            'total': p.total,
          },
        ),
      ),
    );
    return spec.transfer!.kind == TransferKind.download
        ? raw
        : _output(c, spec, raw);
  }

  @override
  Future<void> refresh(String source, {required bool more}) async {
    final c = _byName[source];
    if (c == null) {
      throw DataFailure.unavailable('no data source $source').toActionError();
    }
    try {
      await (more
          ? c.loadMore(roots, rethrowing: true)
          : c.load(roots, refresh: true, rethrowing: true));
    } on DataFailure catch (f) {
      throw f.toActionError();
    }
  }

  @override
  Future<void> subscribe(String stream, Map<String, Object?> params) async {
    final c = _byName[stream];
    if (c == null || !c.spec.isStream) {
      throw DataFailure.unavailable('no stream $stream').toActionError();
    }
    try {
      await c.subscribe(roots, params);
    } on DataFailure catch (f) {
      throw f.toActionError();
    }
  }

  @override
  Future<void> unsubscribe(String stream) async {
    final c = _byName[stream];
    if (c == null) {
      throw DataFailure.unavailable('no stream $stream').toActionError();
    }
    await c.unsubscribe();
  }

  @override
  void dispose() {
    for (final c in _byName.values) {
      c.removeListener(notifyListeners);
    }
    for (final c in _own) {
      c.dispose();
    }
    super.dispose();
  }
}

final class _DataRoot extends MapBase<String, Object?> {
  _DataRoot(this.scope);

  final DataScope scope;

  @override
  Object? operator [](Object? key) {
    final c = key is String ? scope[key] : null;
    if (c == null) return null;
    c.ensureLoaded(scope.roots);
    return c.snapshot;
  }

  @override
  void operator []=(String key, Object? value) =>
      throw UnsupportedError('data is read-only');

  @override
  void clear() => throw UnsupportedError('data is read-only');

  @override
  Iterable<String> get keys => scope._byName.keys;

  @override
  Object? remove(Object? key) => throw UnsupportedError('data is read-only');
}
