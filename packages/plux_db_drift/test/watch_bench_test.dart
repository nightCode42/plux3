// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_db_drift/plux_db_drift.dart';
import 'package:plux_flutter/plux_flutter.dart';

// The shared suite's fixtures: the tasks collection and its records.
import '../../plux_flutter/test/db/adapter_conformance.dart' show task, tasks;

final class _Secrets implements SecretStore {
  final Map<String, String> values = {};

  @override
  Future<String?> read(String name) async => values[name];

  @override
  Future<void> write(String name, String value) async => values[name] = value;

  @override
  Future<void> delete(String name) async => values.remove(name);
}

/// The latency of a watched query, the measurement of DB-007: over 1,000
/// rows, a single-row change must reach the watch's listener within one
/// frame budget (16 ms) on the mid-tier device. Each sample is the time
/// from the start of the write on the UI isolate to the arrival of the
/// updated list there, with the database running on the adapter's
/// background isolate. `make bench-db` runs it and prints one JSON line;
/// it is not gated here, because the gate is a number from the reference
/// device.
void main() {
  const rows = 1000;
  const warmup = 20;
  const samples = 200;

  String id(int i) => 'task-${i.toString().padLeft(4, '0')}';

  test(
    'measures a watched query over $rows rows after a single-row change '
    '[DB-007]',
    () async {
      final dir = Directory.systemTemp.createTempSync('plux-drift-bench');
      addTearDown(() => dir.deleteSync(recursive: true));
      final db = PluxDriftAdapter(
        path: '${dir.path}/plux.db',
        secrets: _Secrets(),
      );
      addTearDown(db.close);
      await db.open(requireEncryption: false);
      final schema = tasks('plugin.bench');
      final outcomes = await db.migrate([schema]);
      expect(outcomes.every((o) => o.ok), isTrue);
      await db.transaction((tx) async {
        for (var i = 0; i < rows; i++) {
          await tx.insert(schema.name, task(id(i), title: 'Task $i'));
        }
      });
      expect(await db.count(schema.name), rows);

      // The watched query: the open tasks in key order, which is all of
      // them, so every write below changes the list.
      final query = DbQuery(
        filter: const DbCompare('done', DbOp.eq, false),
        sort: const [DbSort('id')],
      );
      List<Map<String, Object?>> latest = const [];
      Completer<void>? changed;
      String? wanted;
      final sub = db.watch(schema.name, query).listen((list) {
        latest = list;
        if (wanted != null && list.any((r) => r['title'] == wanted)) {
          changed?.complete();
        }
      });
      addTearDown(sub.cancel);
      for (var i = 0; latest.length != rows; i++) {
        expect(i, lessThan(500), reason: 'the watch delivered no first list');
        await Future<void>.delayed(const Duration(milliseconds: 10));
      }

      final millis = <double>[];
      final watch = Stopwatch();
      for (var i = 0; i < warmup + samples; i++) {
        final title = 'Changed $i';
        wanted = title;
        changed = Completer<void>();
        watch
          ..reset()
          ..start();
        final write = db.update(schema.name, id(i * 7 % rows), {
          'title': title,
        });
        await changed.future.timeout(const Duration(seconds: 5));
        watch.stop();
        await write;
        expect(latest, hasLength(rows));
        if (i >= warmup) millis.add(watch.elapsedMicroseconds / 1000);
      }
      millis.sort();
      double at(double q) => millis[((millis.length - 1) * q).round()];
      // ignore: avoid_print
      print(
        jsonEncode({
          'benchmark': 'db-watch',
          'rows': rows,
          'samples': samples,
          'p50_ms': double.parse(at(0.5).toStringAsFixed(3)),
          'p95_ms': double.parse(at(0.95).toStringAsFixed(3)),
        }),
      );
    },
    skip: Platform.environment['PLUX_BENCH_DB'] == null
        ? 'set PLUX_BENCH_DB=1, or run make bench-db'
        : false,
    timeout: const Timeout(Duration(minutes: 5)),
  );
}
