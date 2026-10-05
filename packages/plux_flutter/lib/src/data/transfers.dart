// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Uploads and downloads of operations marked as transfers (DAT-031,
/// ADR-0048): the file named by the operation's file parameter is
/// streamed to the server as a multipart part or the raw body, or the
/// response is streamed into a file under the runtime's downloads
/// directory. Like any request, a transfer is checked against the
/// plugin's declared domains and carries the user's token (DAT-030,
/// HST-010); unlike one, it reports progress, is cancelled with its run,
/// and is bounded by `data.uploadSize` and `data.downloadSize` (LIM-004)
/// rather than by the request time.
library;

import 'dart:async';
import 'dart:convert';

import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/data/client.dart';
import 'package:plux_flutter/src/data/failure.dart';
import 'package:plux_flutter/src/data/spec.dart';
import 'package:plux_flutter/src/data/transfer_transport.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/pxl/types.dart' show toJson;

/// A request to stop a transfer, made by the run that started it.
final class CancelToken {
  final List<void Function()> _listeners = [];
  bool _cancelled = false;

  /// Whether [cancel] was called.
  bool get isCancelled => _cancelled;

  /// Cancels; each listener is told once.
  void cancel() {
    if (_cancelled) return;
    _cancelled = true;
    final all = [..._listeners];
    _listeners.clear();
    for (final f in all) {
      f();
    }
  }

  /// Calls [f] when cancelled; at once if it already was. Returns a
  /// function that removes the listener.
  void Function() onCancel(void Function() f) {
    if (_cancelled) {
      f();
      return () {};
    }
    _listeners.add(f);
    return () => _listeners.remove(f);
  }
}

/// Runs the transfers of operations.
final class TransferClient {
  /// Creates the client. Downloads are saved under [downloads]; [root] is
  /// the runtime's private directory, from which nothing is uploaded but
  /// downloads.
  TransferClient({
    required this.transport,
    required this.client,
    required this.root,
    required this.downloads,
  });

  /// Moves the bytes.
  final TransferTransport transport;

  /// Builds and checks requests like any data request.
  final DataClient client;

  /// The runtime's private directory.
  final String root;

  /// Where downloads are saved.
  final String downloads;

  /// Runs the transfer [op] of [s] with [input]; completes with an
  /// upload's response JSON, or a download's path. [onProgress] hears
  /// the progress; [cancel] stops the transfer.
  Future<Object?> run(
    DataSourceSpec s,
    OperationSpec op,
    DataCaller caller,
    Map<String, Object?> input, {
    CancelToken? cancel,
    void Function(TransferProgress progress)? onProgress,
  }) async {
    final t = op.transfer!;
    final watch = Stopwatch()..start();
    var status = 0, bytes = 0;
    var result = 'error';
    try {
      final file = input[t.fileParam];
      if (file is! String || file.isEmpty) {
        throw DataFailure(
          ActionErrorKind.validation,
          PluxErrorCode.actionValueInvalid,
          '${t.fileParam} of ${s.name}.${op.name} is not a file',
        );
      }
      final upload = t.kind == TransferKind.upload;
      final path = upload ? _uploadPath(file) : _downloadPath(file);
      final request = _build(s, op, t, caller, input, path);
      Future<TransferResult> attempt(String? token) =>
          _start(request(token), cancel, onProgress);
      String? token;
      if (op.auth) {
        token = await client.auth.token();
        if (token == null) {
          throw DataClient.unauthorised(s, 'no signed-in user');
        }
      }
      var res = await attempt(token);
      if (res.status == 401 && op.auth) {
        token = await client.auth.refresh();
        if (token == null) {
          throw DataClient.unauthorised(s, 'the token was not refreshed');
        }
        res = await attempt(token);
        if (res.status == 401) {
          throw DataClient.unauthorised(s, 'the refreshed token was refused');
        }
      }
      status = res.status;
      bytes = res.bytes;
      if (status < 200 || status >= 300) {
        throw DataFailure(
          ActionErrorKind.http,
          PluxErrorCode.dataHttpError,
          '${s.name}.${op.name} was answered with $status',
          status: status,
        );
      }
      result = 'ok';
      return upload ? res.json : path;
    } finally {
      client.record(
        'api_call',
        route: caller.route,
        pluginKey: caller.pluginKey,
        fields: {
          'source': s.name,
          'operation': op.name,
          'kind': 'transfer',
          'status': status,
          'duration_ms': watch.elapsedMilliseconds,
          'bytes': bytes,
          'result': result,
        },
      );
    }
  }

  Future<TransferResult> _start(
    TransferRequest request,
    CancelToken? cancel,
    void Function(TransferProgress progress)? onProgress,
  ) async {
    if (cancel != null && cancel.isCancelled) {
      throw const DataFailure(
        ActionErrorKind.cancelled,
        PluxErrorCode.actionCancelled,
        'the transfer was cancelled',
      );
    }
    final job = transport.begin(request);
    final stop = cancel?.onCancel(job.cancel);
    final progress = job.progress.listen(onProgress);
    try {
      return await job.result;
    } finally {
      stop?.call();
      await progress.cancel();
    }
  }

  /// The request of a transfer, for a token; checked against the domains
  /// before it is built.
  TransferRequest Function(String? token) _build(
    DataSourceSpec s,
    OperationSpec op,
    TransferSpec t,
    DataCaller caller,
    Map<String, Object?> input,
    String path,
  ) {
    final base = s.baseUrls[client.environment];
    if (base == null) {
      throw DataFailure.unavailable(
        'source ${s.name} has no base URL for environment '
        '${client.environment}',
      );
    }
    final upload = t.kind == TransferKind.upload;
    final multipart = upload && !t.raw;
    final values = {...input}..remove(t.fileParam);
    final (filled, rest) = DataClient.fillPath(s, op.path, values);
    final query = multipart
        ? <String, List<String>>{}
        : DataClient.queryOf(rest);
    final b = Uri.parse(base);
    final url = b.replace(
      path: '${b.path}$filled',
      queryParameters: query.isEmpty ? null : query,
    );
    client.checkDomain(url, s, caller);
    final limits = client.limits();
    final fields = <String, String>{
      if (multipart)
        for (final e in rest.entries)
          if (toJson(e.value) case final v?)
            e.key: v is String ? v : jsonEncode(v),
    };
    return (token) => TransferRequest(
      kind: t.kind,
      method: op.method,
      url: url,
      headers: {
        ...s.headers,
        ...op.headers,
        if (token != null) 'authorization': 'Bearer $token',
      },
      path: path,
      raw: t.raw,
      field: t.field,
      contentType: t.contentType,
      fields: fields,
      maxBytes: upload ? limits.uploadSize : limits.downloadSize,
      maxResponseBytes: limits.responseSize,
    );
  }

  /// The file to upload: absolute, without `..`, and not from the
  /// runtime's private directory (its keys, state and outbox), except
  /// what was downloaded.
  String _uploadPath(String file) {
    final p = file.replaceAll('\\', '/');
    final segments = p.split('/');
    final inRoot = p == root || p.startsWith('$root/');
    final inDownloads = p.startsWith('$downloads/');
    if (!p.startsWith('/') && !RegExp(r'^[A-Za-z]:/').hasMatch(p) ||
        segments.contains('..') ||
        p.contains('\u0000') ||
        inRoot && !inDownloads) {
      throw const DataFailure(
        ActionErrorKind.custom,
        PluxErrorCode.dataTransferFileFailed,
        'the file to upload is not an absolute path outside the runtime\'s '
        'private directory',
      );
    }
    return file;
  }

  /// Where a download named [name] is saved: a plain file name under the
  /// downloads directory.
  String _downloadPath(String name) {
    if (name == '.' ||
        name == '..' ||
        name.length > 255 ||
        name.contains(RegExp(r'[/\\\u0000]'))) {
      throw const DataFailure(
        ActionErrorKind.custom,
        PluxErrorCode.dataTransferFileFailed,
        'a download is saved under a plain file name',
      );
    }
    return '$downloads/$name';
  }
}
