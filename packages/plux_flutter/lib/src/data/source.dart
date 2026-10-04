// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// One data source's state on the device (ADR-0048): what `data.<name>`
/// reads — its value, whether it is loading, its error, whether more pages
/// exist and the ListStatus a list bound to it shows (WGT-012) — and how
/// it loads: mocks first (DAT-080), then the cache policy (DAT-010), the
/// client, pagination (DAT-011) and mapping (DAT-004). Loaded and failed
/// loads are announced to [DataSourceEvents].
library;

import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/data/cache.dart';
import 'package:plux_flutter/src/data/client.dart';
import 'package:plux_flutter/src/data/failure.dart';
import 'package:plux_flutter/src/data/mapping.dart';
import 'package:plux_flutter/src/data/mocks.dart';
import 'package:plux_flutter/src/data/spec.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/pxl/types.dart';
import 'package:plux_flutter/src/render/values.dart' show BindingError;

/// A data source's load ended: the seam through which loads become
/// trigger events of action graphs (the data-source-event trigger kind).
final class DataSourceEvent {
  /// Creates the event.
  const DataSourceEvent({
    required this.pluginKey,
    required this.source,
    required this.sourceId,
    this.error,
  });

  /// The plugin whose page loaded it.
  final String pluginKey;

  /// The source's name.
  final String source;

  /// The source's UUID.
  final String sourceId;

  /// The error in PXL form (`PluxActionError`), for a failed load.
  final Map<String, Object?>? error;
}

/// Receives the `loaded` and `failed` events of data sources; the action
/// engine's data-source-event trigger implements it.
abstract interface class DataSourceEvents {
  /// A load succeeded, from the network, the cache or a mock.
  void loaded(DataSourceEvent event);

  /// A load failed.
  void failed(DataSourceEvent event);
}

/// What a controller loads with; [DataServices] provides it.
abstract interface class DataContext {
  /// The client.
  DataClient get client;

  /// The cache for plain or encrypted entries, or null without one.
  ResponseCache? cache({required bool encrypted});

  /// The environment key, part of cache keys.
  String get environment;

  /// The mock selection.
  DataMocks get mocks;

  /// The events listener, or null.
  DataSourceEvents? get events;

  /// Reports a problem.
  void report(PluxException e);

  /// Records telemetry.
  DataRecord get record;
}

/// The state and loading of one source for one plugin.
final class DataSourceController extends ChangeNotifier {
  /// Creates the controller.
  DataSourceController({
    required this.spec,
    required this.context,
    required this.caller,
    required this.types,
  });

  /// The source.
  final DataSourceSpec spec;

  /// What it loads with.
  final DataContext context;

  /// The plugin it loads for.
  final DataCaller caller;

  /// The declared types its values have.
  final Map<String, NamedType> types;

  Object? _value;
  bool _loading = false, _loadingMore = false, _hasMore = false;
  bool _started = false, _disposed = false;
  ActionError? _error;
  int _generation = 0;
  Object? _cursor;
  int _pages = 0, _items = 0;

  late final PxlType _declared = PxlType.parse(spec.type, (n) => types[n]);

  /// The value of `data.<name>`.
  Map<String, Object?> get snapshot => {
    'value': _value,
    'loading': _loading,
    'error': _error?.toPxl(),
    'hasMore': _hasMore,
    'status': _value == null && _error != null
        ? 'error'
        : (_value == null && _loading ? 'loading' : 'ready'),
  };

  /// Starts the first load unless one started; [roots] gives the asking
  /// page's PXL roots. Bindings call it as they read `data.<name>`.
  void ensureLoaded(Map<String, Object?> Function() roots) {
    if (_started) return;
    _started = true;
    scheduleMicrotask(() => unawaited(load(roots)));
  }

  /// Loads the first page; with [refresh], bypassing the cache. A failure
  /// becomes the source's error and is reported, or, with [rethrowing],
  /// is thrown for the step that asked.
  Future<void> load(
    Map<String, Object?> Function() roots, {
    bool refresh = false,
    bool rethrowing = false,
  }) async {
    _started = true;
    final gen = ++_generation;
    final mock = context.mocks.of(caller.pluginKey, spec.name);
    if (mock != null) return _mock(mock, rethrowing);
    _set(() {
      _loading = true;
      _loadingMore = false;
    });
    try {
      if (spec.kind == DataKind.other) {
        throw DataFailure.unavailable(
          'source ${spec.name} is of a kind this runtime does not load',
        );
      }
      final params = _params(roots);
      final raw = await _first(params, roots, refresh, gen);
      if (gen != _generation) return;
      _apply(raw, roots, append: false);
    } on DataFailure catch (f) {
      if (gen == _generation) _fail(f, rethrowing);
    }
  }

  /// Loads the next page of a paginated source and appends it (DAT-011);
  /// nothing happens when no page is left or a load is in progress.
  Future<void> loadMore(
    Map<String, Object?> Function() roots, {
    bool rethrowing = false,
  }) async {
    final page = spec.page;
    if (page == null || !_hasMore || _loading || _loadingMore) return;
    if (context.mocks.of(caller.pluginKey, spec.name) != null) return;
    final gen = _generation;
    _set(() => _loadingMore = true);
    try {
      final raw = await context.client.load(
        spec,
        caller,
        _params(roots),
        page: _pageParams(page),
      );
      if (gen != _generation) return;
      _apply(raw, roots, append: true);
    } on DataFailure catch (f) {
      if (gen == _generation) _fail(f, rethrowing);
    } finally {
      if (gen == _generation) _set(() => _loadingMore = false);
    }
  }

  Future<Object?> _first(
    Map<String, Object?> params,
    Map<String, Object?> Function() roots,
    bool refresh,
    int gen,
  ) async {
    _cursor = null;
    _pages = 0;
    _items = 0;
    Future<Object?> net() => context.client.load(
      spec,
      caller,
      params,
      page: spec.page == null ? const {} : _pageParams(spec.page!),
    );
    final c = spec.cache;
    final cache = c.policy == CachePolicy.networkOnly
        ? null
        : context.cache(encrypted: c.encrypted || spec.auth);
    if (cache == null) return net();
    final key = cacheKey(
      '${caller.pluginKey}|${c.key ?? spec.id}|${context.environment}|'
      '${jsonKey(params)}',
    );
    Future<Object?> netAndStore() async {
      final raw = await net();
      await _cacheWrite(cache, key, raw);
      return raw;
    }

    if (refresh) return netAndStore();
    switch (c.policy) {
      case CachePolicy.cacheFirst:
        final hit = await _cacheRead(cache, key, c.ttl);
        if (hit != null) return _hit(hit);
        try {
          return await netAndStore();
        } on DataFailure catch (f) {
          if (f.code == PluxErrorCode.dataDomainBlocked) rethrow;
          // Offline: a stale response beats none.
          final stale = await _cacheRead(
            cache,
            key,
            const Duration(days: 3650),
          );
          if (stale == null) rethrow;
          return _hit(stale);
        }
      case CachePolicy.networkFirst:
        try {
          return await netAndStore();
        } on DataFailure catch (f) {
          if (f.code == PluxErrorCode.dataDomainBlocked) rethrow;
          final hit = await _cacheRead(cache, key, c.ttl);
          if (hit == null) rethrow;
          return _hit(hit);
        }
      case CachePolicy.staleWhileRevalidate:
        final hit = await _cacheRead(cache, key, c.ttl);
        if (hit == null) return netAndStore();
        unawaited(() async {
          try {
            final raw = await netAndStore();
            if (gen == _generation) _apply(raw, roots, append: false);
          } on DataFailure catch (f) {
            // The cached value stays; the failure is reported.
            context.report(f.toException(_details));
          }
        }());
        return _hit(hit);
      case CachePolicy.networkOnly:
        return net();
    }
  }

  Object? _hit(CachedEntry e) {
    context.record(
      'api_call',
      route: caller.route,
      pluginKey: caller.pluginKey,
      fields: {
        'source': spec.name,
        'kind': spec.kind.name,
        'cache': 'hit',
        'bytes': e.bytes,
        'result': 'ok',
      },
    );
    return e.json;
  }

  Future<CachedEntry?> _cacheRead(
    ResponseCache c,
    String key,
    Duration ttl,
  ) async {
    try {
      return await c.read(key, ttl);
    } on DataFailure catch (f) {
      context.report(f.toException(_details));
      return null;
    }
  }

  Future<void> _cacheWrite(ResponseCache c, String key, Object? raw) async {
    try {
      await c.write(key, raw);
    } on DataFailure catch (f) {
      context.report(f.toException(_details));
    }
  }

  Map<String, String> get _details => {
    'plugin': caller.pluginKey,
    'source': spec.name,
    if (caller.route.isNotEmpty) 'route': caller.route,
  };

  Map<String, Object?> _pageParams(PageSpec p) => {
    ?p.sizeParam: p.pageSize,
    if (p.style == PageStyle.cursor) p.cursorParam!: ?_cursor,
    if (p.style == PageStyle.page) p.pageParam!: p.firstPage + _pages,
    if (p.style == PageStyle.offset) p.offsetParam!: _items,
  };

  Map<String, Object?> _params(Map<String, Object?> Function() roots) {
    if (spec.params.isEmpty) return const {};
    final r = roots();
    try {
      return {for (final e in spec.params.entries) e.key: e.value(r)};
    } on BindingError catch (e) {
      throw DataFailure(
        ActionErrorKind.validation,
        PluxErrorCode.actionValueInvalid,
        'a parameter of ${spec.name} could not be evaluated: ${e.message}',
      );
    }
  }

  void _apply(
    Object? raw,
    Map<String, Object?> Function() roots, {
    required bool append,
  }) {
    final Object? value;
    try {
      value = _map(raw, roots);
    } on DataFailure catch (f) {
      _fail(f, false);
      return;
    }
    final page = spec.page;
    _set(() {
      if (page != null && value is List<Object?>) {
        final before = append && _value is List<Object?>
            ? _value! as List<Object?>
            : const <Object?>[];
        _value = [...before, ...value];
        _pages++;
        _items += value.length;
        _cursor = page.style == PageStyle.cursor
            ? _quiet(() => select(raw, page.nextCursor))
            : null;
        final more = page.hasMore == null
            ? null
            : _quiet(() => select(raw, page.hasMore));
        _hasMore = more is bool
            ? more
            : (page.style == PageStyle.cursor
                  ? _cursor != null
                  : value.length >= page.pageSize);
      } else {
        _value = value;
      }
      _loading = false;
      _error = null;
    });
    context.events?.loaded(
      DataSourceEvent(
        pluginKey: caller.pluginKey,
        source: spec.name,
        sourceId: spec.id,
      ),
    );
  }

  Object? _map(Object? raw, Map<String, Object?> Function() roots) {
    final selected = select(raw, spec.select);
    final transform = spec.transform;
    final responseType = spec.responseType;
    if (transform == null || responseType == null) {
      return mapJson(_declared, selected, spec.select ?? 'response');
    }
    final response = mapJson(
      PxlType.parse(responseType, (n) => types[n]),
      selected,
    );
    try {
      return transform({...roots(), 'response': response});
    } on BindingError catch (e) {
      throw DataFailure.mapping(
        'the transform of ${spec.name} failed: ${e.message}',
      );
    }
  }

  void _fail(DataFailure f, bool rethrowing) {
    _set(() {
      _loading = false;
      _error = f.toActionError();
    });
    context.events?.failed(
      DataSourceEvent(
        pluginKey: caller.pluginKey,
        source: spec.name,
        sourceId: spec.id,
        error: _error!.toPxl(),
      ),
    );
    if (rethrowing) throw f;
    context.report(f.toException(_details));
  }

  void _mock(DataMockState state, bool rethrowing) {
    final m = spec.mocks;
    switch (state) {
      case DataMockState.loading:
        _set(() {
          _value = null;
          _error = null;
          _loading = true;
        });
      case DataMockState.error:
        _value = null;
        _hasMore = false;
        _fail(
          m.error ??
              const DataFailure(
                ActionErrorKind.http,
                PluxErrorCode.dataHttpError,
                'the error mock is selected',
                status: 500,
              ),
          rethrowing,
        );
      case DataMockState.empty || DataMockState.success:
        final empty = state == DataMockState.empty;
        final declared = empty ? m.hasEmpty : m.hasSuccess;
        final json = empty ? m.empty : m.success;
        try {
          if (!declared && !(empty && _declared.kind == PxlKind.list)) {
            throw DataFailure.unavailable(
              'source ${spec.name} declares no ${state.name} mock',
            );
          }
          final v = declared ? mapJson(_declared, json, 'mock') : <Object?>[];
          _set(() {
            _value = v;
            _error = null;
            _loading = false;
            _hasMore = false;
          });
          context.events?.loaded(
            DataSourceEvent(
              pluginKey: caller.pluginKey,
              source: spec.name,
              sourceId: spec.id,
            ),
          );
        } on DataFailure catch (f) {
          _fail(f, rethrowing);
        }
    }
  }

  void _set(void Function() change) {
    change();
    if (!_disposed) notifyListeners();
  }

  @override
  void dispose() {
    _disposed = true;
    _generation++;
    super.dispose();
  }
}

Object? _quiet(Object? Function() f) {
  try {
    return f();
  } on DataFailure {
    return null;
  }
}

/// A stable text of evaluated parameters, for cache keys.
String jsonKey(Map<String, Object?> params) {
  final keys = params.keys.toList()..sort();
  return [for (final k in keys) '$k=${toJson(params[k])}'].join('&');
}
