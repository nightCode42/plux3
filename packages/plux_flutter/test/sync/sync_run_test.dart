// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/plux_flutter.dart';

void main() {
  const done = SyncResult(
    outcome: SyncOutcome.upToDate,
    duration: Duration.zero,
  );

  test('a sync run is a future of its result [SYN-002]', () async {
    final run = SyncRun(Future.value(done), const Stream.empty());
    expect(await run, same(done));
    expect(await run.asStream().single, same(done));
    expect(await run.timeout(const Duration(seconds: 1)), same(done));
    var completed = false;
    await run.whenComplete(() => completed = true);
    expect(completed, isTrue);
    expect(await run.then((r) => r.outcome), SyncOutcome.upToDate);
    expect(await run.progress.isEmpty, isTrue);
  });

  test('a failed sync run reports its error', () async {
    final run = SyncRun(Future.error(StateError('x')), const Stream.empty());
    expect(await run.catchError((Object _) => done), same(done));
    final pending = SyncRun(
      Completer<SyncResult>().future,
      const Stream.empty(),
    );
    expect(
      await pending.timeout(Duration.zero, onTimeout: () => done),
      same(done),
    );
  });
}
