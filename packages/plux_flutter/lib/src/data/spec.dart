// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// A data source as the bundle declares it (DAT-001, ADR-0048): its
/// request, mapping, caching, pagination, mocks and operations, decoded
/// from the configuration value the compiler checked. Parameters and the
/// transform stay expressions, evaluated per request.
library;

import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/data/failure.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/pxl/vm.dart' show PxlLimits;
import 'package:plux_flutter/src/render/sections.dart';
import 'package:plux_flutter/src/render/values.dart';

/// Evaluates a configured value against PXL roots.
typedef DataValue = Object? Function(Map<String, Object?> roots);

/// The kinds of source; this runtime runs REST and GraphQL (P5 R4) and
/// WebSocket and SSE streams (P5 R5).
enum DataKind {
  /// REST over JSON.
  rest,

  /// GraphQL queries and mutations over HTTP; subscriptions over a
  /// WebSocket.
  graphql,

  /// Messages over a WebSocket (DAT-012).
  webSocket,

  /// Server-sent events (DAT-012).
  sse,

  /// Any other kind: functions, database, static.
  other,
}

/// The directions of a file transfer (DAT-031).
enum TransferKind {
  /// A file is sent.
  upload,

  /// A file is saved.
  download,
}

/// An operation that moves a file (DAT-031): the input field [fileParam]
/// holds the path of the file to upload, or the plain name a download is
/// saved under.
final class TransferSpec {
  /// Creates the transfer.
  const TransferSpec({
    required this.kind,
    required this.fileParam,
    this.raw = false,
    this.field = 'file',
    this.contentType = 'application/octet-stream',
  });

  /// The direction.
  final TransferKind kind;

  /// The input field naming the file.
  final String fileParam;

  /// Whether an upload sends the file as the raw body instead of one
  /// multipart part.
  final bool raw;

  /// The multipart field the file is sent in.
  final String field;

  /// The content type of a raw upload.
  final String contentType;
}

/// How a source uses the response cache (DAT-010).
enum CachePolicy {
  /// Always the network; nothing is cached.
  networkOnly,

  /// A fresh cached response, else the network.
  cacheFirst,

  /// The network, else a cached response within its TTL.
  networkFirst,

  /// A cached response within its TTL at once, revalidated in the
  /// background.
  staleWhileRevalidate,
}

/// A source's caching.
final class CacheSpec {
  /// Creates the caching.
  const CacheSpec({
    this.policy = CachePolicy.networkOnly,
    this.ttl = Duration.zero,
    this.key,
    this.encrypted = false,
  });

  /// The policy.
  final CachePolicy policy;

  /// How long a cached response is used.
  final Duration ttl;

  /// The key prefix instead of the source's name, or null.
  final String? key;

  /// Whether the cached responses are encrypted at rest.
  final bool encrypted;
}

/// The pagination styles (DAT-011).
enum PageStyle {
  /// The response names the next page's cursor.
  cursor,

  /// Pages are numbered.
  page,

  /// Items are counted from an offset.
  offset,
}

/// A source's pagination.
final class PageSpec {
  /// Creates the pagination.
  const PageSpec({
    required this.style,
    required this.pageSize,
    this.sizeParam,
    this.cursorParam,
    this.nextCursor,
    this.pageParam,
    this.firstPage = 1,
    this.offsetParam,
    this.hasMore,
  });

  /// The style.
  final PageStyle style;

  /// Items per page.
  final int pageSize;

  /// The parameter carrying [pageSize], or null.
  final String? sizeParam;

  /// The parameter carrying the cursor (cursor style).
  final String? cursorParam;

  /// The selector of the next cursor in the response (cursor style).
  final String? nextCursor;

  /// The parameter carrying the page number (page style).
  final String? pageParam;

  /// The first page's number (page style).
  final int firstPage;

  /// The parameter carrying the offset (offset style).
  final String? offsetParam;

  /// The selector of a bool saying more pages exist, or null: then a
  /// cursor, or a full page, says so.
  final String? hasMore;
}

/// The mock states a source declares (DAT-080), as JSON.
final class MockSpec {
  /// Creates the mocks.
  const MockSpec({
    this.success,
    this.hasSuccess = false,
    this.empty,
    this.hasEmpty = false,
    this.error,
  });

  /// The success mock: the source's design-time mock.
  final Object? success;

  /// Whether [success] is declared.
  final bool hasSuccess;

  /// The empty mock.
  final Object? empty;

  /// Whether [empty] is declared.
  final bool hasEmpty;

  /// The error the error mock fails with.
  final DataFailure? error;
}

/// An operation apiCall runs.
final class OperationSpec {
  /// Creates the operation.
  const OperationSpec({
    required this.name,
    this.method = 'POST',
    this.path = '',
    this.query = '',
    this.headers = const {},
    required this.auth,
    this.input,
    this.output,
    this.select,
    this.offlineCapable,
    this.transfer,
  });

  /// Its name in its source.
  final String name;

  /// The HTTP method (REST).
  final String method;

  /// The path template (REST).
  final String path;

  /// The GraphQL document.
  final String query;

  /// Literal headers.
  final Map<String, String> headers;

  /// Whether the auth delegate's token is sent (HST-010).
  final bool auth;

  /// The input type, or null.
  final String? input;

  /// The output type, or null.
  final String? output;

  /// The selector of the output in the response.
  final String? select;

  /// Whether the mutation is queued in the outbox when the network fails
  /// (DAT-020); null follows the source.
  final bool? offlineCapable;

  /// The file transfer this operation is, or null (DAT-031).
  final TransferSpec? transfer;

  /// Whether the operation changes data on the server, in a source of
  /// [kind]: by its method (REST) or its document (GraphQL).
  bool mutates(DataKind kind) => kind == DataKind.graphql
      ? query.trimLeft().startsWith('mutation')
      : const {'POST', 'PUT', 'PATCH', 'DELETE'}.contains(method);
}

/// A data source: REST, GraphQL, WebSocket or SSE.
final class DataSourceSpec {
  /// Creates the source.
  const DataSourceSpec({
    required this.id,
    required this.name,
    required this.kind,
    required this.type,
    this.baseUrls = const {},
    this.method = 'GET',
    this.path = '',
    this.query = '',
    this.params = const {},
    this.headers = const {},
    this.auth = false,
    this.select,
    this.responseType,
    this.transform,
    this.cache = const CacheSpec(),
    this.page,
    this.mocks = const MockSpec(),
    this.operations = const {},
    this.subscription = '',
    this.subscribeMessage,
    this.offlineCapable = false,
  });

  /// Decodes a source of a bundle; [strings] is the table of the section
  /// that declares it, [plugin] the bundle whose programs its expressions
  /// are. Throws [PluxException] for a malformed configuration.
  factory DataSourceSpec.decode(
    fbs.DataSource d, {
    required StringTable strings,
    required BundleView plugin,
    required PxlLimits limits,
  }) {
    final name = strings(d.name);
    final kind = switch (d.kind) {
      fbs.DataSourceKind.Rest => DataKind.rest,
      fbs.DataSourceKind.Graphql => DataKind.graphql,
      fbs.DataSourceKind.Websocket => DataKind.webSocket,
      fbs.DataSourceKind.Sse => DataKind.sse,
      _ => DataKind.other,
    };
    final id = d.id == null ? name : uuidString(uuidOf(d.id!));
    final type = strings(d.type);
    final config = d.config;
    if (kind == DataKind.other || config == null) {
      return DataSourceSpec(id: id, name: name, kind: kind, type: type);
    }
    ValueResolver resolver(Map<String, Object?> roots) => ValueResolver(
      plugin: plugin,
      roots: () => roots,
      token: (_) => null,
      translation: (_) => null,
      limits: limits,
    );
    DataValue value(fbs.Value v) =>
        (roots) => toPxl(resolver(roots).resolve(v, strings));
    final literal = <String, Object?>{};
    final params = <String, DataValue>{};
    DataValue? transform;
    for (final e in config.entries ?? const <fbs.Entry>[]) {
      final key = strings(e.key);
      final v = e.value;
      if (v == null) continue;
      switch (key) {
        case 'params':
          for (final p in v.entries ?? const <fbs.Entry>[]) {
            if (p.value case final pv?) params[strings(p.key)] = value(pv);
          }
        case 'transform':
          transform = value(v);
        default:
          literal[key] = toPxl(resolver(const {}).resolve(v, strings));
      }
    }
    try {
      return DataSourceSpec(
        id: id,
        name: name,
        kind: kind,
        type: type,
        baseUrls: {
          for (final e in _map(literal['baseUrls']).entries)
            e.key: e.value! as String,
        },
        method: literal['method'] as String? ?? 'GET',
        path: literal['path'] as String? ?? '',
        query: literal['query'] as String? ?? '',
        params: params,
        headers: _strings(literal['headers']),
        auth: literal['auth'] as bool? ?? false,
        select: literal['select'] as String?,
        responseType: literal['responseType'] as String?,
        transform: transform,
        cache: _cache(_map(literal['cache'])),
        page: _page(literal['pagination']),
        mocks: _mocks(_map(literal['mocks'])),
        operations: {
          for (final e in _map(literal['operations']).entries)
            e.key: _operation(e.key, _map(e.value), literal['auth'] == true),
        },
        subscription: literal['subscription'] as String? ?? '',
        subscribeMessage: literal['subscribeMessage'],
        offlineCapable: literal['offlineCapable'] as bool? ?? false,
      );
    } on TypeError catch (e) {
      throw PluxException(
        PluxErrorCode.bundleMalformed,
        'data source $name: malformed configuration ($e)',
      );
    }
  }

  /// The source's UUID.
  final String id;

  /// Its name: `data.<name>`.
  final String name;

  /// Its kind.
  final DataKind kind;

  /// Its declared type expression.
  final String type;

  /// The base URL per environment key (DAT-003).
  final Map<String, String> baseUrls;

  /// The HTTP method (REST).
  final String method;

  /// The path template.
  final String path;

  /// The GraphQL document.
  final String query;

  /// Parameters: path placeholders, then query (GET) or body (POST);
  /// GraphQL variables.
  final Map<String, DataValue> params;

  /// Literal headers.
  final Map<String, String> headers;

  /// Whether the auth delegate's token is sent (HST-010).
  final bool auth;

  /// The selector of the value in the response (DAT-004).
  final String? select;

  /// The type the selected response has before [transform].
  final String? responseType;

  /// The PXL transform over `response`.
  final DataValue? transform;

  /// The caching.
  final CacheSpec cache;

  /// The pagination, or null.
  final PageSpec? page;

  /// The mock states.
  final MockSpec mocks;

  /// The operations by name.
  final Map<String, OperationSpec> operations;

  /// A GraphQL source's subscription document (DAT-012), or empty.
  final String subscription;

  /// The message a WebSocket source sends after each connection
  /// (DAT-012), or null.
  final Object? subscribeMessage;

  /// Whether the source's mutations are queued in the outbox when the
  /// network fails (DAT-020).
  final bool offlineCapable;

  /// Whether the source is a stream `subscribe` can open: a WebSocket or
  /// SSE source, or a GraphQL source with a subscription (DAT-012).
  bool get isStream =>
      kind == DataKind.webSocket ||
      kind == DataKind.sse ||
      (kind == DataKind.graphql && subscription.isNotEmpty);
}

Map<String, Object?> _map(Object? v) =>
    v is Map<String, Object?> ? v : const {};

Map<String, String> _strings(Object? v) => {
  for (final e in _map(v).entries) e.key: e.value! as String,
};

CacheSpec _cache(Map<String, Object?> m) => m.isEmpty
    ? const CacheSpec()
    : CacheSpec(
        policy: CachePolicy.values.byName(m['policy']! as String),
        ttl: Duration(seconds: m['ttlSeconds'] as int? ?? 0),
        key: m['key'] as String?,
        encrypted: m['encrypted'] as bool? ?? false,
      );

PageSpec? _page(Object? v) {
  if (v is! Map<String, Object?>) return null;
  return PageSpec(
    style: PageStyle.values.byName(v['style']! as String),
    pageSize: v['pageSize']! as int,
    sizeParam: v['sizeParam'] as String?,
    cursorParam: v['cursorParam'] as String?,
    nextCursor: v['nextCursor'] as String?,
    pageParam: v['pageParam'] as String?,
    firstPage: v['firstPage'] as int? ?? 1,
    offsetParam: v['offsetParam'] as String?,
    hasMore: v['hasMore'] as String?,
  );
}

MockSpec _mocks(Map<String, Object?> m) {
  final e = m['error'];
  DataFailure? error;
  if (e is Map<String, Object?>) {
    final kind =
        ActionErrorKind.values.asNameMap()[e['kind']] ?? ActionErrorKind.http;
    error = DataFailure(
      kind,
      kind == ActionErrorKind.http
          ? PluxErrorCode.dataHttpError
          : PluxErrorCode.dataNetworkFailed,
      e['message'] as String? ?? 'the error mock is selected',
      status: e['status'] as int? ?? 0,
    );
  }
  return MockSpec(
    success: m['success'],
    hasSuccess: m.containsKey('success'),
    empty: m['empty'],
    hasEmpty: m.containsKey('empty'),
    error: error,
  );
}

OperationSpec _operation(String name, Map<String, Object?> m, bool auth) =>
    OperationSpec(
      name: name,
      method: m['method'] as String? ?? 'POST',
      path: m['path'] as String? ?? '',
      query: m['query'] as String? ?? '',
      headers: _strings(m['headers']),
      auth: m['auth'] as bool? ?? auth,
      input: m['input'] as String?,
      output: m['output'] as String?,
      select: m['select'] as String?,
      offlineCapable: m['offlineCapable'] as bool?,
      transfer: _transfer(m['transfer']),
    );

TransferSpec? _transfer(Object? v) {
  if (v is! Map<String, Object?>) return null;
  return TransferSpec(
    kind: TransferKind.values.byName(v['kind']! as String),
    fileParam: v['fileParam']! as String,
    raw: v['body'] == 'raw',
    field: v['field'] as String? ?? 'file',
    contentType: v['contentType'] as String? ?? 'application/octet-stream',
  );
}
