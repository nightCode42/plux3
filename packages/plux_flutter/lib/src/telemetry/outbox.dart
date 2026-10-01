// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The sync isolate's buffer of unsent telemetry events and their upload
/// (ANL-002, ADR-0034): one JSON line per event in
/// `<store>/telemetry/events.jsonl`, bounded by `telemetry.bufferBytes`
/// with the oldest dropped first, sent in gzip-compressed batches. Runs on
/// the sync isolate only (L-6).
library;

import 'dart:convert';
import 'dart:io';

import 'package:plux_flutter/src/sync/api_client.dart';
import 'package:plux_flutter/src/telemetry/events.dart';

/// What one flush did.
final class TelemetryFlush {
  /// Creates a report.
  const TelemetryFlush({
    required this.sent,
    required this.dropped,
    required this.left,
    this.error,
  });

  /// Events the server answered for, accepted or refused.
  final int sent;

  /// Events dropped because the server refused their batch for good.
  final int dropped;

  /// Events still buffered.
  final int left;

  /// Why the flush stopped early, when it did.
  final ApiError? error;
}

/// The buffer.
final class TelemetryOutbox {
  /// Opens the buffer of the store at [storeRoot].
  TelemetryOutbox(String storeRoot)
    : _file = File('$storeRoot/telemetry/events.jsonl');

  final File _file;

  /// The most batches one flush sends, so a long-offline device catches
  /// up over several flushes rather than holding the radio.
  static const maxBatches = 8;

  /// Buffered events.
  int get length => _lines().length;

  List<String> _lines() {
    if (!_file.existsSync()) return [];
    return [
      for (final l in _file.readAsLinesSync())
        if (l.isNotEmpty) l,
    ];
  }

  void _write(List<String> lines) {
    _file.parent.createSync(recursive: true);
    final tmp = File('${_file.path}.tmp')
      ..writeAsStringSync(lines.map((l) => '$l\n').join(), flush: true);
    tmp.renameSync(_file.path);
  }

  static int _size(String line) => utf8.encode(line).length + 1;

  /// Appends [lines], dropping the oldest events so that the buffer stays
  /// within [maxBytes].
  void append(List<String> lines, int maxBytes) {
    if (lines.isEmpty) return;
    final size = _file.existsSync() ? _file.lengthSync() : 0;
    final added = lines.fold(0, (n, l) => n + _size(l));
    if (size + added <= maxBytes) {
      _file.parent.createSync(recursive: true);
      _file.writeAsStringSync(
        lines.map((l) => '$l\n').join(),
        mode: FileMode.append,
      );
      return;
    }
    final all = [..._lines(), ...lines];
    var kept = 0;
    var start = all.length;
    while (start > 0 && kept + _size(all[start - 1]) <= maxBytes) {
      kept += _size(all[--start]);
    }
    _write(all.sublist(start));
  }

  /// Deletes the buffered events of [categories], whose consent was
  /// withdrawn (SEC-161).
  void purge(Set<TelemetryCategory> categories) {
    if (categories.isEmpty || !_file.existsSync()) return;
    final lines = _lines();
    final kept = [
      for (final l in lines)
        if (!categories.contains(TelemetryLine.parse(l)?.category)) l,
    ];
    if (kept.length != lines.length) _write(kept);
  }

  /// Sends the buffered events in batches of at most [perRequest], oldest
  /// first. A batch the server answers is removed, events it refused
  /// included; one refused for good (a client error other than
  /// authentication) is dropped, so a bad event is never retried for
  /// ever; a retryable failure or a lost token stops the flush and keeps
  /// the rest for the next one.
  Future<TelemetryFlush> flush({
    required PluxApiClient api,
    required Future<String> Function() token,
    required String appId,
    required String environment,
    required int perRequest,
  }) async {
    var sent = 0;
    var dropped = 0;
    for (var i = 0; i < maxBatches; i++) {
      final lines = _lines();
      if (lines.isEmpty) break;
      final batch = lines.take(perRequest.clamp(1, lines.length)).toList();
      final events = [for (final l in batch) ?TelemetryLine.parse(l)?.event];
      try {
        if (events.isNotEmpty) {
          await api.ingestEvents(
            await token(),
            appId: appId,
            environment: environment,
            events: events,
          );
        }
        sent += events.length;
      } on ApiError catch (e) {
        if (e.retryable || e.code == 'unauthenticated') {
          return TelemetryFlush(
            sent: sent,
            dropped: dropped,
            left: lines.length,
            error: e,
          );
        }
        dropped += batch.length;
      }
      _write(_lines().sublist(batch.length));
    }
    return TelemetryFlush(sent: sent, dropped: dropped, left: length);
  }
}
