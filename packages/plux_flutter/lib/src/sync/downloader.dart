// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Resumable, retried, parallel downloads for the sync (SYN-010): each
/// object downloads to a `.part` file, resumes with an HTTP range request
/// after a failure, and retries with exponential backoff and full jitter,
/// honouring the server's `Retry-After`.
library;

import 'dart:async';
import 'dart:io';
import 'dart:math';

import 'package:http/http.dart' as http;
import 'package:plux_flutter/src/sync/api_client.dart';

/// How downloads retry (SYN-010).
final class RetryPolicy {
  /// Creates a policy.
  const RetryPolicy({
    this.attempts = 5,
    this.base = const Duration(milliseconds: 500),
    this.max = const Duration(seconds: 30),
  });

  /// Attempts per object, the first included.
  final int attempts;

  /// The first backoff.
  final Duration base;

  /// The longest backoff.
  final Duration max;

  /// The backoff before retry [attempt] (1 for the first retry): a random
  /// duration between zero and `base × 2^(attempt−1)`, capped at [max]
  /// ("full jitter").
  Duration backoff(int attempt, Random random) {
    final cap = min(
      max.inMicroseconds,
      base.inMicroseconds * pow(2, attempt - 1).toInt(),
    );
    return Duration(microseconds: random.nextInt(cap + 1));
  }
}

/// One object to download into [part].
final class Download {
  /// Creates a download.
  const Download(this.url, this.part, {this.expectedSize});

  /// Where it is.
  final Uri url;

  /// The partial file it is written to and resumed from.
  final String part;

  /// Its size when known, for progress.
  final int? expectedSize;
}

/// A download that failed after its retries.
final class DownloadFailed implements Exception {
  /// Creates the failure.
  const DownloadFailed(this.url, this.cause);

  /// The object.
  final Uri url;

  /// The last error.
  final Object cause;

  @override
  String toString() => 'download of $url failed: $cause';
}

/// Downloads objects with bounded parallelism.
final class Downloader {
  /// Creates a downloader.
  Downloader(
    this._http, {
    this.parallelism = 4,
    this.policy = const RetryPolicy(),
    Future<void> Function(Duration)? sleep,
    Random? random,
  }) : _sleep = sleep ?? Future<void>.delayed,
       _random = random ?? Random.secure();

  final http.Client _http;

  /// Downloads running at once (SYN-010, default 4).
  final int parallelism;

  /// How failures retry.
  final RetryPolicy policy;

  final Future<void> Function(Duration) _sleep;
  final Random _random;

  /// Downloads every item; [onBytes] is called with each chunk's size, for
  /// progress. Throws [DownloadFailed] for the first object that fails
  /// after its retries; the others finish or stop first.
  Future<void> fetchAll(
    List<Download> items, {
    void Function(int bytes)? onBytes,
  }) async {
    var next = 0;
    DownloadFailed? failure;
    Future<void> worker() async {
      while (failure == null && next < items.length) {
        final item = items[next++];
        try {
          await fetch(item, onBytes: onBytes);
        } on DownloadFailed catch (e) {
          failure ??= e;
        }
      }
    }

    await Future.wait([
      for (var i = 0; i < min(parallelism, items.length); i++) worker(),
    ]);
    final f = failure;
    if (f != null) throw f;
  }

  /// Downloads one object, resuming from what [Download.part] holds.
  Future<void> fetch(Download d, {void Function(int bytes)? onBytes}) async {
    Object? last;
    for (var attempt = 1; attempt <= policy.attempts; attempt++) {
      if (attempt > 1) {
        final wait = last is ApiError && last.retryAfter != null
            ? last.retryAfter!
            : policy.backoff(attempt - 1, _random);
        await _sleep(wait);
      }
      try {
        await _attempt(d, onBytes);
        return;
      } on ApiError catch (e) {
        last = e;
        if (!e.retryable) break;
      } on IOException catch (e) {
        last = e;
      } on http.ClientException catch (e) {
        last = e;
      }
    }
    throw DownloadFailed(d.url, last ?? 'no attempt');
  }

  Future<void> _attempt(Download d, void Function(int)? onBytes) async {
    final part = File(d.part);
    final have = part.existsSync() ? part.lengthSync() : 0;
    final req = http.Request('GET', d.url);
    if (have > 0) req.headers['Range'] = 'bytes=$have-';
    final res = await _http.send(req);
    final append = switch (res.statusCode) {
      206 => true,
      200 => false,
      // The part is complete, or longer than the object: start over.
      416 => false,
      _ => throw ApiError(
        res.statusCode,
        'download',
        res.reasonPhrase ?? '',
        retryAfter: parseRetryAfter(res.headers['retry-after'], DateTime.now()),
      ),
    };
    if (res.statusCode == 416) {
      await res.stream.drain<void>();
      if (part.existsSync()) part.deleteSync();
      throw ApiError(
        0,
        'range',
        'the partial download does not fit the object',
      );
    }
    if (append && !_rangeStartsAt(res.headers['content-range'], have)) {
      await res.stream.drain<void>();
      if (part.existsSync()) part.deleteSync();
      throw ApiError(0, 'range', 'the server resumed at another offset');
    }
    final sink = part.openWrite(
      mode: append ? FileMode.append : FileMode.write,
    );
    try {
      await for (final chunk in res.stream) {
        sink.add(chunk);
        onBytes?.call(chunk.length);
      }
      await sink.flush();
    } finally {
      await sink.close();
    }
    final length = res.contentLength;
    if (length != null && part.lengthSync() != (append ? have : 0) + length) {
      throw ApiError(0, 'truncated', 'the body ended early');
    }
  }

  static bool _rangeStartsAt(String? contentRange, int offset) {
    final m = RegExp(r'^bytes (\d+)-').firstMatch(contentRange ?? '');
    return m != null && int.parse(m[1]!) == offset;
  }
}
