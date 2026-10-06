// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The offline outbox (DAT-020, LIM-004, ADR-0048): mutations of sources
/// marked `offlineCapable` that could not reach the server are kept in an
/// encrypted store — the operation, its inputs and an idempotency key,
/// never a token — and replayed in order, per plugin, one at a time: on
/// app resume, after any request that succeeds, on a backoff timer and
/// when the host says the network is back (`Plux.setNetworkAvailable`,
/// decision D13). A 2xx ends an entry as synced, 409 and 412 as a
/// conflict, any other client error as failed; a server error, a timeout,
/// a network failure or a missing token leaves it for the next replay.
/// Each outcome is an event, so action graphs can reconcile their state.
/// A full outbox refuses the new mutation with a typed error.
library;

import 'dart:async';
import 'dart:convert';
import 'dart:math';

import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/data/client.dart';
import 'package:plux_flutter/src/data/failure.dart';
import 'package:plux_flutter/src/data/source.dart' show OutboxOutcome;
import 'package:plux_flutter/src/data/spec.dart';
import 'package:plux_flutter/src/data/stream_session.dart'
    show StreamScheduler, backoffDelay;
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/store/kv_store.dart';

/// The header that carries an entry's idempotency key on every attempt,
/// so a server that already applied the mutation can answer without
/// applying it twice.
const String idempotencyHeader = 'idempotency-key';

/// A queued mutation.
final class OutboxEntry {
  /// Creates an entry; [bytes] is its encoded size in the store.
  OutboxEntry({
    required this.key,
    required this.plugin,
    required this.source,
    required this.operation,
    required this.input,
    required this.queuedAtMs,
  }) : bytes = utf8
           .encode(
             jsonEncode({
               'key': key,
               'plugin': plugin,
               'source': source,
               'operation': operation,
               'input': input,
               'at': queuedAtMs,
             }),
           )
           .length;

  /// Decodes an entry from the store; null when it is malformed.
  static OutboxEntry? fromJson(Object? v) {
    if (v is! Map<String, Object?>) return null;
    final key = v['key'], plugin = v['plugin'], source = v['source'];
    final operation = v['operation'], input = v['input'], at = v['at'];
    if (key is! String ||
        plugin is! String ||
        source is! String ||
        operation is! String ||
        input is! Map<String, Object?> ||
        at is! int) {
      return null;
    }
    return OutboxEntry(
      key: key,
      plugin: plugin,
      source: source,
      operation: operation,
      input: input,
      queuedAtMs: at,
    );
  }

  /// The idempotency key, made once per mutation.
  final String key;

  /// The plugin whose page issued it.
  final String plugin;

  /// The source's name.
  final String source;

  /// The operation's name.
  final String operation;

  /// The operation's inputs, as JSON.
  final Map<String, Object?> input;

  /// When it was queued, in milliseconds since the epoch.
  final int queuedAtMs;

  /// Its encoded size.
  final int bytes;

  /// The entry as the store keeps it.
  Map<String, Object?> toJson() => {
    'key': key,
    'plugin': plugin,
    'source': source,
    'operation': operation,
    'input': input,
    'at': queuedAtMs,
  };
}

/// What the outbox needs to send an entry: its source and caller, known
/// once a page of the plugin has built its data (null before).
typedef OutboxTarget = ({DataSourceSpec spec, DataCaller caller});

/// Sends one attempt of an entry; completes with the HTTP status of a
/// success, fails with a [DataFailure].
typedef OutboxSender = Future<int> Function(
  DataSourceSpec spec,
  OperationSpec operation,
  DataCaller caller,
  Map<String, Object?> input,
  String key,
);

/// Receives the end of an entry: the outcome and the HTTP status (0 when
/// there was none).
typedef OutboxEmit = void Function(
  OutboxOutcome outcome,
  OutboxEntry entry,
  int status,
);

/// The encrypted queue of offline mutations.
final class Outbox {
  /// Creates the outbox over [store].
  Outbox({
    required this.store,
    required this.limits,
    required this.resolve,
    required this.send,
    required this.emit,
    required this.report,
    Random? random,
    StreamScheduler? scheduler,
    bool Function()? networkAvailable,
  }) : _random = random ?? Random(),
       _schedule = scheduler ?? Timer.new,
       _networkAvailable = networkAvailable ?? (() => true);

  /// Where entries rest, encrypted.
  final PluxKeyValueStore store;

  /// The limits in force.
  final DataLimits Function() limits;

  /// Finds the source an entry names, or null.
  final OutboxTarget? Function(String plugin, String source) resolve;

  /// Sends an attempt.
  final OutboxSender send;

  /// Announces the end of an entry.
  final OutboxEmit emit;

  /// Reports a problem.
  final void Function(PluxException e) report;

  final Random _random;

  /// Idempotency keys are unguessable whatever the jitter source is.
  final Random _keyRandom = Random.secure();
  final StreamScheduler _schedule;
  final bool Function() _networkAvailable;

  /// The bytes of the file around the entries.
  static const int _overhead = 16;

  final List<OutboxEntry> _entries = [];
  int _bytes = _overhead;
  bool _loaded = false, _replaying = false, _again = false;
  bool _disposed = false;
  Timer? _timer;
  int _failures = 0;
  Future<void> _tail = Future.value();

  /// A new idempotency key: 128 random bits as a UUID-shaped string.
  String newKey() {
    final b = [for (var i = 0; i < 16; i++) _keyRandom.nextInt(256)];
    b[6] = (b[6] & 0x0f) | 0x40;
    b[8] = (b[8] & 0x3f) | 0x80;
    String h(int from, int to) => b
        .sublist(from, to)
        .map((x) => x.toRadixString(16).padLeft(2, '0'))
        .join();
    return '${h(0, 4)}-${h(4, 6)}-${h(6, 8)}-${h(8, 10)}-${h(10, 16)}';
  }

  /// How many entries wait.
  int get length => _entries.length;

  /// Loads the entries once; a store that fails is reported and the
  /// outbox starts empty, trying again next time.
  Future<void> _load() async {
    if (_loaded) return;
    try {
      final all = await store.load();
      final list = all['entries'];
      if (list is List<Object?>) {
        for (final v in list) {
          final e = OutboxEntry.fromJson(v);
          if (e == null) continue;
          _entries.add(e);
          _bytes += e.bytes + 1;
        }
      }
      _loaded = true;
    } on StoreException catch (e) {
      report(
        PluxException(
          PluxErrorCode.dataOutboxUnavailable,
          'the outbox could not be read: ${e.message}',
        ),
      );
      // A file that did not authenticate was removed: start empty.
      _loaded = e.failure == StoreFailure.corrupt;
    }
  }

  /// Whether entries of [plugin] wait: a new mutation then queues behind
  /// them, so mutations replay in the order they were made.
  Future<bool> hasPending(String plugin) async {
    await _load();
    return _entries.any((e) => e.plugin == plugin);
  }

  /// Queues a mutation. Fails with `PLX-5120` when the outbox holds
  /// `data.outboxEntries` entries or `data.outboxBytes` bytes (LIM-004),
  /// and with `PLX-5121` when it cannot be stored encrypted.
  Future<void> enqueue(OutboxEntry entry) async {
    await _load();
    final l = limits();
    if (!_loaded) throw _unavailable('the outbox could not be read');
    if (_entries.length >= l.outboxEntries ||
        _bytes + entry.bytes + 1 > l.outboxBytes) {
      throw DataFailure(
        ActionErrorKind.custom,
        PluxErrorCode.dataOutboxFull,
        'the outbox is full: ${_entries.length} entries, $_bytes bytes '
        '(data.outboxEntries = ${l.outboxEntries}, '
        'data.outboxBytes = ${l.outboxBytes})',
      );
    }
    _entries.add(entry);
    _bytes += entry.bytes + 1;
    try {
      await _persist();
    } on StoreException catch (e) {
      _entries.remove(entry);
      _bytes -= entry.bytes + 1;
      throw _unavailable(
        e.failure == StoreFailure.tooLarge
            ? 'the entries exceed what the store may hold'
            : e.message,
        full: e.failure == StoreFailure.tooLarge,
      );
    }
    _arm();
  }

  DataFailure _unavailable(String why, {bool full = false}) => DataFailure(
    ActionErrorKind.custom,
    full ? PluxErrorCode.dataOutboxFull : PluxErrorCode.dataOutboxUnavailable,
    'the offline mutation was refused: $why',
  );

  Future<void> _persist() {
    final snapshot = <String, Object?>{
      'v': 1,
      'entries': [for (final e in _entries) e.toJson()],
    };
    final run = _tail.then((_) => store.save(snapshot));
    _tail = run.then((_) {}, onError: (Object _) {});
    return run;
  }

  /// Removes the entries of [plugin], or every entry (logout,
  /// `Plux.wipeData`).
  Future<void> clear({String? plugin}) async {
    await _load();
    _entries.removeWhere((e) => plugin == null || e.plugin == plugin);
    _bytes = _overhead + _entries.fold(0, (n, e) => n + e.bytes + 1);
    if (_entries.isEmpty) {
      _timer?.cancel();
      _timer = null;
    }
    try {
      if (plugin == null) {
        await _tail;
        await store.wipe();
      } else {
        await _persist();
      }
    } on StoreException catch (e) {
      report(
        PluxException(
          PluxErrorCode.dataOutboxUnavailable,
          'the outbox could not be cleared: ${e.message}',
        ),
      );
    }
  }

  /// Replays now: the app resumed, a request succeeded, the host says
  /// the network is back.
  void replaySoon({bool reset = false}) {
    if (reset) _failures = 0;
    unawaited(replay());
  }

  /// Replays the entries in order; completes when the pass is done.
  Future<void> replay() async {
    if (_disposed) return;
    if (_replaying) {
      _again = true;
      return;
    }
    _replaying = true;
    try {
      do {
        _again = false;
        await _pass();
      } while (_again && !_disposed);
    } finally {
      _replaying = false;
    }
  }

  Future<void> _pass() async {
    await _load();
    if (_entries.isEmpty || !_networkAvailable()) return;
    final blocked = <String>{};
    var progressed = false;
    for (final e in [..._entries]) {
      if (_disposed) return;
      if (blocked.contains(e.plugin)) continue;
      final (outcome, status) = await _try(e);
      if (outcome == null) {
        blocked.add(e.plugin);
        continue;
      }
      progressed = true;
      _entries.remove(e);
      _bytes -= e.bytes + 1;
      try {
        await _persist();
      } on StoreException catch (x) {
        report(
          PluxException(
            PluxErrorCode.dataOutboxUnavailable,
            'the outbox could not be updated: ${x.message}',
          ),
        );
      }
      _announce(outcome, e, status);
    }
    if (progressed) _failures = 0;
    if (_entries.isNotEmpty) _arm(replayed: true);
  }

  void _announce(OutboxOutcome outcome, OutboxEntry e, int status) {
    if (outcome != OutboxOutcome.synced) {
      final conflict = outcome == OutboxOutcome.conflict;
      report(
        PluxException(
          conflict
              ? PluxErrorCode.dataOutboxConflict
              : PluxErrorCode.dataOutboxRejected,
          'the queued mutation ${e.source}.${e.operation} was answered with '
          '$status',
          details: {'plugin': e.plugin, 'source': e.source},
        ),
      );
    }
    emit(outcome, e, status);
  }

  /// Tries one entry; a null outcome leaves it for the next replay.
  Future<(OutboxOutcome?, int)> _try(OutboxEntry e) async {
    final target = resolve(e.plugin, e.source);
    if (target == null) return (null, 0);
    final op = target.spec.operations[e.operation];
    if (op == null) return (OutboxOutcome.failed, 0);
    try {
      final status = await send(target.spec, op, target.caller, e.input, e.key);
      return (OutboxOutcome.synced, status);
    } on DataFailure catch (f) {
      return (_classify(f), f.status);
    }
  }

  /// A failure that can pass leaves the entry; one that cannot ends it.
  static OutboxOutcome? _classify(DataFailure f) {
    switch (f.code) {
      case PluxErrorCode.dataNetworkFailed:
      case PluxErrorCode.dataRequestTimeout:
      case PluxErrorCode.dataUnauthorised:
        return null;
      case PluxErrorCode.dataHttpError:
        if (f.status == 409 || f.status == 412) return OutboxOutcome.conflict;
        if (f.status >= 500 || f.status == 408 || f.status == 429) return null;
        return OutboxOutcome.failed;
      default:
        return OutboxOutcome.failed;
    }
  }

  /// Arms the backoff timer when entries wait.
  void _arm({bool replayed = false}) {
    if (_disposed || _entries.isEmpty) return;
    if (!replayed && _timer != null) return;
    _timer?.cancel();
    final l = limits();
    final wait = backoffDelay(
      _failures,
      l.outboxBackoffMin,
      l.outboxBackoffMax,
      _random,
    );
    _failures++;
    _timer = _schedule(wait, () {
      _timer = null;
      unawaited(replay());
    });
  }

  /// Stops the timer.
  void dispose() {
    _disposed = true;
    _timer?.cancel();
    _timer = null;
  }
}
