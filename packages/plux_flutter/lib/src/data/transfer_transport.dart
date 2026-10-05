// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// What a file transfer is on the wire (DAT-031, ADR-0048): an upload
/// that streams a file as a multipart part or as the raw body, a download
/// that streams the response into a file, both reporting progress, both
/// cancellable, both bounded by a size limit checked before the transfer
/// (the file's size, the declared length) and counted during it. File
/// and network I/O happen where [HttpTransferTransport] runs: the data
/// isolate in apps (L-6).
library;

import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:http/http.dart' as http;
import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/data/failure.dart';
import 'package:plux_flutter/src/data/spec.dart' show TransferKind;
import 'package:plux_flutter/src/data/transport.dart' show describeUrl;
import 'package:plux_flutter/src/errors/plux_exception.dart';

/// One transfer, built and checked on the UI isolate.
final class TransferRequest {
  /// Creates a request.
  const TransferRequest({
    required this.kind,
    required this.method,
    required this.url,
    this.headers = const {},
    required this.path,
    this.raw = false,
    this.field = 'file',
    this.contentType = 'application/octet-stream',
    this.fields = const {},
    required this.maxBytes,
    required this.maxResponseBytes,
  });

  /// The direction.
  final TransferKind kind;

  /// The HTTP method.
  final String method;

  /// The URL; its domain was checked before (DAT-030).
  final Uri url;

  /// The headers, including the token.
  final Map<String, String> headers;

  /// The file to upload, or the file a download is saved as.
  final String path;

  /// Whether an upload sends the file as the body.
  final bool raw;

  /// The multipart field of the file.
  final String field;

  /// The content type of a raw upload.
  final String contentType;

  /// The other multipart fields.
  final Map<String, String> fields;

  /// The largest file (`data.uploadSize`, `data.downloadSize`).
  final int maxBytes;

  /// The largest response body of an upload (`data.responseSize`).
  final int maxResponseBytes;
}

/// How far a transfer is: bytes done, and the total, 0 when unknown.
typedef TransferProgress = ({int sent, int total});

/// How a transfer ended: the HTTP status, the decoded JSON of an
/// upload's 2xx response, and the bytes moved.
final class TransferResult {
  /// Creates the result.
  const TransferResult(this.status, this.json, this.bytes);

  /// The HTTP status.
  final int status;

  /// The decoded response of an upload, or null.
  final Object? json;

  /// The bytes of the file moved.
  final int bytes;
}

/// A transfer in progress.
final class TransferJob {
  /// Creates the job.
  TransferJob(this.progress, this.result, this._cancel);

  /// Progress, a few times per transfer; ends with it.
  final Stream<TransferProgress> progress;

  /// The end: fails with a [DataFailure] when the transfer failed, was
  /// too large or was cancelled.
  final Future<TransferResult> result;

  final void Function() _cancel;

  /// Stops the transfer; a partial download is removed.
  void cancel() => _cancel();
}

/// Starts transfers.
abstract interface class TransferTransport {
  /// Begins [request].
  TransferJob begin(TransferRequest request);
}

/// Bytes between two progress reports.
const int _progressStep = 64 * 1024;

/// Transfers over an HTTP client and the file system.
final class HttpTransferTransport implements TransferTransport {
  /// Creates the transport.
  HttpTransferTransport(this.client);

  /// The client.
  final http.Client client;

  @override
  TransferJob begin(TransferRequest r) {
    final progress = StreamController<TransferProgress>();
    final abort = Completer<void>();
    final result = _run(r, progress, abort).whenComplete(progress.close);
    // The caller reads the result; an unread failure is not unhandled.
    unawaited(result.then((_) {}, onError: (Object _) {}));
    return TransferJob(progress.stream, result, () {
      if (!abort.isCompleted) abort.complete();
    });
  }

  Future<TransferResult> _run(
    TransferRequest r,
    StreamController<TransferProgress> progress,
    Completer<void> abort,
  ) async {
    try {
      return r.kind == TransferKind.upload
          ? await _upload(r, progress, abort.future)
          : await _download(r, progress, abort.future);
    } on http.RequestAbortedException {
      throw _cancelled();
    } on http.ClientException catch (e) {
      throw abort.isCompleted ? _cancelled() : _network(r.url, e.message);
    } on SocketException catch (e) {
      throw _network(r.url, e.osError?.message ?? 'connection failed');
    } on HandshakeException {
      throw _network(r.url, 'the TLS handshake failed');
    } on HttpException catch (e) {
      throw _network(r.url, e.message);
    } on FileSystemException {
      throw DataFailure(
        ActionErrorKind.custom,
        PluxErrorCode.dataTransferFileFailed,
        'the file of the transfer could not be read or written',
      );
    }
  }

  Future<TransferResult> _upload(
    TransferRequest r,
    StreamController<TransferProgress> progress,
    Future<void> abort,
  ) async {
    final file = File(r.path);
    final int total;
    try {
      total = await file.length();
    } on FileSystemException {
      throw DataFailure(
        ActionErrorKind.custom,
        PluxErrorCode.dataTransferFileFailed,
        'the file to upload does not exist or cannot be read',
      );
    }
    if (total > r.maxBytes) {
      throw _tooLarge(
        'the file to upload',
        total,
        r.maxBytes,
        'data.uploadSize',
      );
    }
    var sent = 0, reported = 0;
    progress.add((sent: 0, total: total));
    final body = file.openRead().transform(
      StreamTransformer<List<int>, List<int>>.fromHandlers(
        handleData: (chunk, sink) {
          sent += chunk.length;
          if (sent - reported >= _progressStep || sent == total) {
            reported = sent;
            if (!progress.isClosed) progress.add((sent: sent, total: total));
          }
          sink.add(chunk);
        },
      ),
    );
    final http.BaseRequest request;
    if (r.raw) {
      final req = http.AbortableStreamedRequest(
        r.method,
        r.url,
        abortTrigger: abort,
      )..contentLength = total;
      req.headers
        ..addAll(r.headers)
        ..['content-type'] = r.contentType;
      unawaited(
        req.sink
            .addStream(body)
            .then((_) => req.sink.close(), onError: (Object _) {}),
      );
      request = req;
    } else {
      final req =
          http.AbortableMultipartRequest(r.method, r.url, abortTrigger: abort)
            ..headers.addAll(r.headers)
            ..fields.addAll(r.fields)
            ..files.add(
              http.MultipartFile(
                r.field,
                body,
                total,
                filename: r.path.split(Platform.pathSeparator).last,
              ),
            );
      request = req;
    }
    final res = await client.send(request);
    final bytes = await _bounded(res.stream, r.maxResponseBytes, r.url);
    final ok = res.statusCode >= 200 && res.statusCode < 300;
    return TransferResult(
      res.statusCode,
      ok && bytes.isNotEmpty ? _json(bytes, r.url) : null,
      sent,
    );
  }

  Future<TransferResult> _download(
    TransferRequest r,
    StreamController<TransferProgress> progress,
    Future<void> abort,
  ) async {
    final req = http.AbortableRequest(r.method, r.url, abortTrigger: abort)
      ..headers.addAll(r.headers);
    final res = await client.send(req);
    final ok = res.statusCode >= 200 && res.statusCode < 300;
    if (!ok) {
      await res.stream.drain<void>();
      return TransferResult(res.statusCode, null, 0);
    }
    final declared = res.contentLength;
    if (declared != null && declared > r.maxBytes) {
      await res.stream.drain<void>();
      throw _tooLarge(
        'the download',
        declared,
        r.maxBytes,
        'data.downloadSize',
      );
    }
    final part = File('${r.path}.part');
    await part.parent.create(recursive: true);
    final sink = part.openWrite();
    var written = 0, reported = 0;
    progress.add((sent: 0, total: declared ?? 0));
    try {
      await for (final chunk in res.stream) {
        written += chunk.length;
        if (written > r.maxBytes) {
          throw _tooLarge(
            'the download',
            written,
            r.maxBytes,
            'data.downloadSize',
          );
        }
        sink.add(chunk);
        if (written - reported >= _progressStep) {
          reported = written;
          if (!progress.isClosed) {
            progress.add((sent: written, total: declared ?? 0));
          }
        }
      }
      await sink.close();
      await part.rename(r.path);
    } on Object {
      await sink.close().then((_) {}, onError: (Object _) {});
      if (part.existsSync()) await part.delete();
      rethrow;
    }
    progress.add((sent: written, total: declared ?? written));
    return TransferResult(res.statusCode, null, written);
  }
}

Future<Uint8List> _bounded(Stream<List<int>> s, int max, Uri url) async {
  final out = BytesBuilder(copy: false);
  await for (final chunk in s) {
    out.add(chunk);
    if (out.length > max) {
      throw DataFailure(
        ActionErrorKind.validation,
        PluxErrorCode.dataSizeExceeded,
        'the response of ${describeUrl(url)} exceeds data.responseSize = '
        '$max bytes',
      );
    }
  }
  return out.takeBytes();
}

Object? _json(Uint8List bytes, Uri url) {
  try {
    return jsonDecode(utf8.decode(bytes));
  } on FormatException {
    throw DataFailure.mapping(
      'the response of ${describeUrl(url)} is not JSON',
    );
  }
}

DataFailure _cancelled() => const DataFailure(
  ActionErrorKind.cancelled,
  PluxErrorCode.actionCancelled,
  'the transfer was cancelled',
);

DataFailure _network(Uri url, String why) => DataFailure(
  ActionErrorKind.network,
  PluxErrorCode.dataNetworkFailed,
  'the transfer at ${describeUrl(url)} failed: $why',
);

DataFailure _tooLarge(String what, int size, int max, String limit) =>
    DataFailure(
      ActionErrorKind.validation,
      PluxErrorCode.dataTransferTooLarge,
      '$what is at least $size bytes, over $limit = $max',
    );
