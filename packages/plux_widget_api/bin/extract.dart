// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Writes `schema/widgets/flutter-api.json` from the pinned Flutter SDK, or
/// with `--check` fails if the committed snapshot is stale (WGT-003).
///
/// Usage: `dart run bin/extract.dart --flutter-version X.Y.Z
/// [--root <repository root>] [--check]`. The run fails unless the workspace
/// resolves Flutter X.Y.Z, so the snapshot always describes the pinned SDK.
library;

import 'dart:io';

import 'package:path/path.dart' as p;
import 'package:plux_widget_api/plux_widget_api.dart';

Future<void> main(List<String> args) async {
  var root = '../..';
  String? version;
  var check = false;
  for (var i = 0; i < args.length; i++) {
    switch (args[i]) {
      case '--root' when i + 1 < args.length:
        root = args[++i];
      case '--flutter-version' when i + 1 < args.length:
        version = args[++i];
      case '--check':
        check = true;
      default:
        stderr.writeln(
          'usage: extract --flutter-version X.Y.Z [--root DIR] [--check]',
        );
        exitCode = 2;
        return;
    }
  }
  if (version == null) {
    stderr.writeln('--flutter-version is required');
    exitCode = 2;
    return;
  }
  final rootDir = Directory(root).absolute;
  final active = flutterSdkVersion(rootDir);
  if (active != version) {
    stderr.writeln(
      '✗ The workspace resolves Flutter ${active ?? 'unknown'}; the snapshot must come '
      "from the pinned Flutter $version. Install it and run 'flutter pub get'.",
    );
    exitCode = 1;
    return;
  }
  final targets = readTargets(
    Directory(p.join(rootDir.path, 'schema', 'widgets')),
  );
  final snapshot = await extract(
    targets,
    contextRoot: Directory(p.join(rootDir.path, 'packages', 'plux_flutter'))
        .resolveSymbolicLinksSync(),
    flutterVersion: version,
  );
  final out = File(
    p.join(rootDir.path, 'schema', 'widgets', 'flutter-api.json'),
  );
  final text = encode(snapshot);
  if (check) {
    if (!out.existsSync() || out.readAsStringSync() != text) {
      stderr.writeln(
        '✗ schema/widgets/flutter-api.json does not match Flutter $version. '
        "Run 'make widgets-api gen' and review schema/widgets/COVERAGE.md.",
      );
      exitCode = 1;
    }
    return;
  }
  out.writeAsStringSync(text);
  stdout.writeln(
    'wrote schema/widgets/flutter-api.json (${targets.length} declarations)',
  );
}
