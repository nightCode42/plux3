// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// What the runtime gives the data layer (ADR-0048), and the data of one
/// page: its own sources and those of its plugin and the app, read by PXL
/// as `data.<name>` and run by `apiCall` and `refreshData`.
library;

import 'dart:collection';

import 'package:flutter/foundation.dart';
import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/core/config.dart' show PluxAuthDelegate;
import 'package:plux_flutter/src/data/cache.dart';
import 'package:plux_flutter/src/data/client.dart';
import 'package:plux_flutter/src/data/failure.dart';
import 'package:plux_flutter/src/data/mapping.dart';
import 'package:plux_flutter/src/data/mocks.dart';
import 'package:plux_flutter/src/data/source.dart';
import 'package:plux_flutter/src/data/spec.dart';
import 'package:plux_flutter/src/data/transport.dart';
import 'package:plux_flutter/src/db/service.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/pxl/types.dart';

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
    this.database,
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
  final PluxDatabase? database;

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
  );

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
  Future<Object?> callOperation(String operation, Map<String, Object?> input);

  /// Reloads [source] bypassing its cache, or with [more] loads its next
  /// page; fails with an [ActionError].
  Future<void> refresh(String source, {required bool more});
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
      // A change of a database source is what a list rebuilds its changed
      // items for; any other change counts as external to such items.
      final VoidCallback listener = c.spec.kind == DataKind.database
          ? notifyListeners
          : () {
              _external++;
              notifyListeners();
            };
      _listeners[c] = listener;
      c.addListener(listener);
    }
  }

  final Map<DataSourceController, VoidCallback> _listeners = {};
  int _external = 0;

  /// How many times a source that is not a database source changed: what
  /// items of a watched list depend on besides their own row (DB-006).
  int get externalRevision => _external;

  /// The services.
  final DataServices services;

  /// The page's roots, for parameters and transforms.
  final Map<String, Object?> Function() roots;

  final List<DataSourceController> _own;
  final Map<String, DataSourceController> _byName = {};

  /// The controller of [name], or null.
  DataSourceController? operator [](String name) => _byName[name];

  /// The `data` root: each source's snapshot, loading it on first read.
  late final DataRoot root = DataRoot._(this);

  @override
  Future<Object?> callOperation(
    String operation,
    Map<String, Object?> input,
  ) async {
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
      final raw = await services.client.call(c.spec, spec, c.caller, input);
      final out = spec.output;
      if (out == null) return null;
      return mapJson(
        PxlType.parse(out, (n) => c.types[n]),
        select(raw, spec.select),
      );
    } on DataFailure catch (f) {
      throw f.toActionError();
    }
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
  void dispose() {
    _listeners.forEach((c, l) => c.removeListener(l));
    _listeners.clear();
    for (final c in _own) {
      c.dispose();
    }
    super.dispose();
  }
}

/// The `data` root of a page: each source's snapshot, read-only.
final class DataRoot extends MapBase<String, Object?> {
  DataRoot._(this.scope);

  /// The scope the root reads.
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
