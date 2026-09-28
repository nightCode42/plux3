// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

@TestOn('linux || mac-os')
library;

import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:math';

import 'package:flutter_test/flutter_test.dart';

import 'store_test_support.dart';

/// The Dart SDK's `dart`, found from the Flutter tester's location.
String _dart() {
  var dir = File(Platform.resolvedExecutable).parent;
  while (dir.parent.path != dir.path) {
    final dart = File('${dir.path}/bin/cache/dart-sdk/bin/dart');
    if (dart.existsSync()) return dart.path;
    dir = dir.parent;
  }
  return 'dart';
}

/// The workspace's package configuration, so the worker resolves
/// `package:plux_flutter` without `pub`.
String _packages() {
  var dir = Directory.current;
  while (dir.parent.path != dir.path) {
    final f = File('${dir.path}/.dart_tool/package_config.json');
    if (f.existsSync() && File('${dir.path}/pubspec.lock').existsSync()) {
      return f.path;
    }
    dir = dir.parent;
  }
  throw StateError('no package configuration found');
}

void main() {
  test('a process killed at random moments of staging and activation always leaves a complete release [SYN-005] [QA-009]', () async {
    final dart = _dart(), packages = _packages();
    final rng = Random(5005);
    var kills = 0, observed = 0;
    final root = Directory.systemTemp.createTempSync('plux_kill');
    try {
      for (var round = 0; round < 12; round++) {
        final p = await Process.start(dart, [
          '--packages=$packages',
          'test/support/store_worker.dart',
          root.path,
        ]);
        var last = 0;
        final started = Completer<void>();
        final lines = p.stdout
            .transform(utf8.decoder)
            .transform(const LineSplitter())
            .listen((l) {
              last = int.parse(l);
              if (!started.isCompleted) started.complete();
            });
        final errors = StringBuffer();
        p.stderr.transform(utf8.decoder).listen(errors.write);
        await started.future.timeout(const Duration(seconds: 30));
        // Let it run through more releases, then kill it without warning.
        await Future<void>.delayed(Duration(milliseconds: rng.nextInt(300)));
        p.kill(ProcessSignal.sigkill);
        await p.exitCode;
        await lines.cancel();
        expect(errors.toString(), isEmpty);
        kills++;
        final active = checkComplete(root.path);
        expect(active, isNotNull, reason: 'round $round');
        // What the worker printed was active; nothing older may be.
        expect(active, greaterThanOrEqualTo(last), reason: 'round $round');
        observed = max(observed, active!);
      }
    } finally {
      root.deleteSync(recursive: true);
    }
    expect(kills, 12);
    expect(
      observed,
      greaterThan(12),
      reason: 'the worker made progress between kills',
    );
  }, timeout: const Timeout(Duration(minutes: 3)));
}
