// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The REST and GraphQL client of data sources (DAT-001, ADR-0048). It
/// builds each request from the source's configuration and parameters,
/// checks it against the plugin's declared network domains before it
/// leaves (DAT-030, SEC-080), adds the auth delegate's token and refreshes
/// it once on 401 (HST-010), bounds request and response sizes and time
/// by the limits registry (LIM-004), and records `api_call` telemetry with
/// timing and status only (ANL-001, SCH-012).
library;

import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/assets/image_providers.dart'
    show domainAllowed;
import 'package:plux_flutter/src/core/config.dart' show PluxAuthDelegate;
import 'package:plux_flutter/src/data/failure.dart';
import 'package:plux_flutter/src/data/spec.dart';
import 'package:plux_flutter/src/data/stream_session.dart'
    show graphqlWsProtocol;
import 'package:plux_flutter/src/data/stream_transport.dart';
import 'package:plux_flutter/src/data/transport.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/pxl/types.dart' show toJson;
import 'package:plux_flutter/src/schema/limit_values.dart';
import 'package:plux_flutter/src/schema/limits.g.dart';

/// The data limits in force (LIM-001): the app bundle's, else the
/// registry defaults.
final class DataLimits {
  /// Creates the limits.
  const DataLimits({
    required this.requestSize,
    required this.responseSize,
    required this.requestTimeout,
    required this.pageSize,
    required this.cacheBytes,
    required this.cacheEntries,
    required this.streamMessageSize,
    required this.streamsOpen,
    required this.streamBackoffMin,
    required this.streamBackoffMax,
    required this.outboxEntries,
    required this.outboxBytes,
    required this.outboxBackoffMin,
    required this.outboxBackoffMax,
    required this.uploadSize,
    required this.downloadSize,
  });

  /// The limits of [limits], an app bundle's values by key.
  factory DataLimits.of(Map<String, int> limits) {
    int of(PluxLimit l) => limits.valueOf(l);
    return DataLimits(
      requestSize: of(PluxLimit.dataRequestSize),
      responseSize: of(PluxLimit.dataResponseSize),
      requestTimeout: Duration(milliseconds: of(PluxLimit.dataRequestTimeout)),
      pageSize: of(PluxLimit.dataPageSize),
      cacheBytes: of(PluxLimit.dataCacheBytes),
      cacheEntries: of(PluxLimit.dataCacheEntries),
      streamMessageSize: of(PluxLimit.dataStreamMessageSize),
      streamsOpen: of(PluxLimit.dataStreamsOpen),
      streamBackoffMin: Duration(
        milliseconds: of(PluxLimit.dataStreamBackoffMin),
      ),
      streamBackoffMax: Duration(
        milliseconds: of(PluxLimit.dataStreamBackoffMax),
      ),
      outboxEntries: of(PluxLimit.dataOutboxEntries),
      outboxBytes: of(PluxLimit.dataOutboxBytes),
      outboxBackoffMin: Duration(
        milliseconds: of(PluxLimit.dataOutboxBackoffMin),
      ),
      outboxBackoffMax: Duration(
        milliseconds: of(PluxLimit.dataOutboxBackoffMax),
      ),
      uploadSize: of(PluxLimit.dataUploadSize),
      downloadSize: of(PluxLimit.dataDownloadSize),
    );
  }

  /// `data.requestSize`.
  final int requestSize;

  /// `data.responseSize`.
  final int responseSize;

  /// `data.requestTimeout`.
  final Duration requestTimeout;

  /// `data.pageSize`.
  final int pageSize;

  /// `data.cacheBytes`.
  final int cacheBytes;

  /// `data.cacheEntries`.
  final int cacheEntries;

  /// `data.streamMessageSize`.
  final int streamMessageSize;

  /// `data.streamsOpen`.
  final int streamsOpen;

  /// `data.streamBackoffMin`.
  final Duration streamBackoffMin;

  /// `data.streamBackoffMax`.
  final Duration streamBackoffMax;

  /// `data.outboxEntries`.
  final int outboxEntries;

  /// `data.outboxBytes`.
  final int outboxBytes;

  /// `data.outboxBackoffMin`.
  final Duration outboxBackoffMin;

  /// `data.outboxBackoffMax`.
  final Duration outboxBackoffMax;

  /// `data.uploadSize`.
  final int uploadSize;

  /// `data.downloadSize`.
  final int downloadSize;
}

/// The auth delegate's token, refreshed at most once at a time: requests
/// answered 401 together share one refresh (HST-010).
final class AuthSession {
  /// Creates the session over the delegate [delegate] returns.
  AuthSession(this.delegate);

  /// The host's auth delegate, or null.
  final PluxAuthDelegate? Function() delegate;

  Future<String?>? _refreshing;

  /// The current token, or null.
  Future<String?> token() async => delegate()?.accessToken();

  /// A fresh token after a 401, or null; joins a refresh in progress.
  Future<String?> refresh() {
    final d = delegate();
    if (d == null) return Future.value();
    return _refreshing ??= d.refresh().whenComplete(() => _refreshing = null);
  }
}

/// Records a telemetry event, as `TelemetryRecorder.record` does.
typedef DataRecord = void Function(
  String name, {
  Map<String, Object?> fields,
  String route,
  String pluginKey,
});

/// Who asks for a request: the plugin whose domains bound it.
final class DataCaller {
  /// Creates the caller.
  const DataCaller({
    required this.pluginKey,
    required this.domains,
    this.route = '',
  });

  /// The plugin.
  final String pluginKey;

  /// Its declared network domains (SEC-080).
  final List<String> domains;

  /// The route of the page asking, for telemetry.
  final String route;
}

/// Sends the requests of data sources and operations.
final class DataClient {
  /// Creates the client.
  DataClient({
    required this.transport,
    required this.auth,
    required this.limits,
    required this.environment,
    required this.record,
    required this.report,
    this.allowCleartext = false,
    this.onSuccess,
  });

  /// Sends requests.
  final DataTransport transport;

  /// The auth delegate's token.
  final AuthSession auth;

  /// The limits.
  final DataLimits Function() limits;

  /// The environment key whose base URLs apply (DAT-003).
  final String environment;

  /// Records telemetry.
  final DataRecord record;

  /// Reports a problem (blocked requests).
  final void Function(PluxException error) report;

  /// Tests against a local server allow `http`; never set in apps.
  final bool allowCleartext;

  /// Called after each request that succeeded: the network works, so the
  /// outbox may replay (DAT-020).
  final void Function()? onSuccess;

  /// Loads [s] with its parameters [params] (already evaluated) and the
  /// pagination parameters [page]; completes with the response's JSON,
  /// GraphQL's `data`.
  Future<Object?> load(
    DataSourceSpec s,
    DataCaller caller,
    Map<String, Object?> params, {
    Map<String, Object?> page = const {},
  }) {
    final all = {...params, ...page};
    return switch (s.kind) {
      DataKind.graphql => _exchange(
        s,
        caller,
        null,
        'POST',
        s.path,
        const {},
        {'query': s.query, 'variables': _json(all)},
        s.headers,
        s.auth,
        graphql: true,
      ),
      _ => _rest(s, caller, null, s.method, s.path, all, s.headers, s.auth),
    };
  }

  /// Runs operation [op] of [s] with [input]; completes with the selected
  /// part of the response's JSON. [headers] are added to the request: the
  /// outbox's `Idempotency-Key` (DAT-020).
  Future<Object?> call(
    DataSourceSpec s,
    OperationSpec op,
    DataCaller caller,
    Map<String, Object?> input, {
    Map<String, String> headers = const {},
    void Function(int status)? onStatus,
  }) => switch (s.kind) {
    DataKind.graphql => _exchange(
      s,
      caller,
      op.name,
      'POST',
      s.path,
      const {},
      {'query': op.query, 'variables': _json(input)},
      {...s.headers, ...op.headers, ...headers},
      op.auth,
      graphql: true,
      onStatus: onStatus,
    ),
    _ => _rest(
      s,
      caller,
      op.name,
      op.method,
      op.path,
      input,
      {...s.headers, ...op.headers, ...headers},
      op.auth,
      onStatus: onStatus,
    ),
  };

  /// A REST request: placeholders of [path] from [values], the rest in
  /// the query for GET and DELETE, else in a JSON body.
  Future<Object?> _rest(
    DataSourceSpec s,
    DataCaller caller,
    String? operation,
    String method,
    String path,
    Map<String, Object?> values,
    Map<String, String> headers,
    bool withAuth, {
    void Function(int status)? onStatus,
  }) {
    final (filled, rest) = fillPath(s, path, values);
    final inQuery = method == 'GET' || method == 'DELETE';
    final query = inQuery ? queryOf(rest) : <String, List<String>>{};
    return _exchange(
      s,
      caller,
      operation,
      method,
      filled,
      query,
      inQuery || rest.isEmpty ? null : _json(rest),
      headers,
      withAuth,
      onStatus: onStatus,
    );
  }

  /// Fills the placeholders of [path] from [values]; returns the path
  /// and the values no placeholder took.
  static (String, Map<String, Object?>) fillPath(
    DataSourceSpec s,
    String path,
    Map<String, Object?> values,
  ) {
    final rest = Map.of(values);
    final filled = path.replaceAllMapped(
      RegExp(r'\{([A-Za-z_][A-Za-z0-9_]*)\}'),
      (m) {
        final v = rest.remove(m[1]);
        if (v == null) {
          throw DataFailure(
            ActionErrorKind.validation,
            PluxErrorCode.actionValueInvalid,
            'path parameter ${m[1]} of ${s.name} has no value',
          );
        }
        return Uri.encodeComponent('${toJson(v)}');
      },
    );
    return (filled, rest);
  }

  /// [values] as the query parameters of a URL.
  static Map<String, List<String>> queryOf(Map<String, Object?> values) {
    final query = <String, List<String>>{};
    for (final e in values.entries) {
      final v = toJson(e.value);
      if (v == null) continue;
      query[e.key] = [
        for (final x in v is List ? v : [v])
          x is Map || x is List ? jsonEncode(x) : '$x',
      ];
    }
    return query;
  }

  /// The request that opens the stream of [s] for [caller]: the base URL
  /// of the environment with the path filled from [params] and the rest
  /// in the query, checked against the plugin's domains like any request
  /// (DAT-030), with the auth delegate's token when the source asks for
  /// it. WebSocket URLs use `wss` (`ws` where cleartext is allowed).
  Future<StreamRequest> streamRequest(
    DataSourceSpec s,
    DataCaller caller,
    Map<String, Object?> params, {
    String? lastEventId,
  }) async {
    final limits = this.limits();
    final base = s.baseUrls[environment];
    if (base == null) {
      throw DataFailure.unavailable(
        'source ${s.name} has no base URL for environment $environment',
      );
    }
    final b = Uri.parse(base);
    final graphql = s.kind == DataKind.graphql;
    final (path, rest) = fillPath(s, s.path, params);
    final query = graphql ? <String, List<String>>{} : queryOf(rest);
    final plain = b.replace(
      path: '${b.path}$path',
      queryParameters: query.isEmpty ? null : query,
    );
    checkDomain(plain, s, caller);
    final socket = s.kind != DataKind.sse;
    final url = socket
        ? plain.replace(scheme: plain.scheme == 'https' ? 'wss' : 'ws')
        : plain;
    String? token;
    if (s.auth) {
      token = await auth.token();
      if (token == null) throw unauthorised(s, 'no signed-in user');
    }
    return StreamRequest(
      kind: socket ? StreamKind.webSocket : StreamKind.sse,
      url: url,
      headers: {
        ...s.headers,
        if (token != null) 'authorization': 'Bearer $token',
      },
      protocols: graphql ? const [graphqlWsProtocol] : const [],
      lastEventId: lastEventId,
      maxMessageBytes: limits.streamMessageSize,
      connectTimeout: limits.requestTimeout,
    );
  }

  /// Refreshes the auth delegate's token after a refused connection;
  /// whether a token came back.
  Future<bool> refreshToken() async => await auth.refresh() != null;

  Future<Object?> _exchange(
    DataSourceSpec s,
    DataCaller caller,
    String? operation,
    String method,
    String path,
    Map<String, List<String>> query,
    Object? body,
    Map<String, String> headers,
    bool withAuth, {
    bool graphql = false,
    void Function(int status)? onStatus,
  }) async {
    final limits = this.limits();
    final watch = Stopwatch()..start();
    var status = 0, bytes = 0;
    var result = 'error';
    try {
      final base = s.baseUrls[environment];
      if (base == null) {
        throw DataFailure.unavailable(
          'source ${s.name} has no base URL for environment $environment',
        );
      }
      final b = Uri.parse(base);
      final url = b.replace(
        path: '${b.path}$path',
        queryParameters: query.isEmpty ? null : query,
      );
      checkDomain(url, s, caller);
      final encoded = body == null
          ? null
          : Uint8List.fromList(utf8.encode(jsonEncode(body)));
      if (encoded != null && encoded.length > limits.requestSize) {
        throw DataFailure(
          ActionErrorKind.validation,
          PluxErrorCode.dataSizeExceeded,
          'the request of ${s.name} is ${encoded.length} bytes, over '
          'data.requestSize = ${limits.requestSize}',
        );
      }
      Future<DataResponse> send(String? token) => transport.send(
        DataRequest(
          method: method,
          url: url,
          headers: {
            'accept': 'application/json',
            if (encoded != null) 'content-type': 'application/json',
            ...headers,
            if (token != null) 'authorization': 'Bearer $token',
          },
          body: encoded,
          maxResponseBytes: limits.responseSize,
          timeout: limits.requestTimeout,
        ),
      );
      String? token;
      if (withAuth) {
        token = await auth.token();
        if (token == null) throw unauthorised(s, 'no signed-in user');
      }
      var res = await send(token);
      if (res.status == 401 && withAuth) {
        token = await auth.refresh();
        if (token == null) {
          throw unauthorised(s, 'the token was not refreshed');
        }
        res = await send(token);
        if (res.status == 401) {
          throw unauthorised(s, 'the refreshed token was refused');
        }
      }
      status = res.status;
      bytes = res.bytes;
      onStatus?.call(status);
      if (status < 200 || status >= 300) {
        throw DataFailure(
          ActionErrorKind.http,
          PluxErrorCode.dataHttpError,
          '${s.name}${operation == null ? '' : '.$operation'} was answered '
          'with $status',
          status: status,
        );
      }
      final json = graphql ? _graphqlData(s, res.json) : res.json;
      result = 'ok';
      onSuccess?.call();
      return json;
    } finally {
      record(
        'api_call',
        route: caller.route,
        pluginKey: caller.pluginKey,
        fields: {
          'source': s.name,
          'operation': ?operation,
          'kind': s.kind.name,
          'status': status,
          'duration_ms': watch.elapsedMilliseconds,
          'bytes': bytes,
          'result': result,
        },
      );
    }
  }

  /// Blocks a request to a host the plugin does not declare, or over
  /// anything but HTTPS, before it leaves, and reports it (DAT-030).
  void checkDomain(Uri url, DataSourceSpec s, DataCaller caller) {
    final scheme =
        url.scheme == 'https' || allowCleartext && url.scheme == 'http';
    if (scheme && domainAllowed(url.host, caller.domains)) return;
    final f = DataFailure(
      ActionErrorKind.permission,
      PluxErrorCode.dataDomainBlocked,
      'a request of ${s.name} to ${url.scheme}://${url.host} was blocked: '
      'not an HTTPS domain plugin ${caller.pluginKey} declares',
    );
    report(
      f.toException({
        'plugin': caller.pluginKey,
        'source': s.name,
        'host': url.host,
        if (caller.route.isNotEmpty) 'route': caller.route,
      }),
    );
    throw f;
  }

  static Object? _graphqlData(DataSourceSpec s, Object? body) {
    if (body is! Map<String, Object?>) {
      throw DataFailure.mapping(
        'the GraphQL response of ${s.name} is malformed',
      );
    }
    final data = body['data'];
    final errors = body['errors'];
    if (data == null && errors is List && errors.isNotEmpty) {
      // The messages may echo user data: only their count is reported.
      throw DataFailure(
        ActionErrorKind.http,
        PluxErrorCode.dataGraphqlError,
        'the GraphQL request of ${s.name} failed with ${errors.length} '
        'error(s)',
      );
    }
    return data;
  }

  /// The failure of a request that needs the user's token and has none
  /// (PLX-5107).
  static DataFailure unauthorised(DataSourceSpec s, String why) => DataFailure(
    ActionErrorKind.http,
    PluxErrorCode.dataUnauthorised,
    '${s.name} needs the user\'s token: $why',
    status: 401,
  );

  static Map<String, Object?> _json(Map<String, Object?> m) => {
    for (final e in m.entries) e.key: toJson(e.value),
  };
}
