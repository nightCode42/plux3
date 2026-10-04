// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:convert';
import 'dart:io';
import 'dart:math';

import 'package:path/path.dart' as p;
import 'package:plux_native_scan/plux_native_scan.dart';

const _usage = '''
Usage: dart run plux_native_scan --host <version+build> [--slot Class]... [--root dir] [--output file] [--id uuid] [--app-id id]

Writes the host app's native catalogue (ADR-0041). `plux native scan` runs
it with the slots plux.yaml lists and the build pubspec.yaml names.''';

/// Runs the scanner; `plux native scan` calls it in the host project.
Future<void> main(List<String> args) async {
  final slots = <String>[];
  String? host, id, appId;
  var root = '.';
  var output = 'plux.catalogue.json';
  for (var i = 0; i < args.length; i++) {
    final flag = args[i];
    final value = i + 1 < args.length ? args[i + 1] : null;
    if (value == null || !flag.startsWith('--')) {
      stderr.writeln(_usage);
      exitCode = 2;
      return;
    }
    i++;
    switch (flag) {
      case '--slot':
        slots.add(value);
      case '--host':
        host = value;
      case '--root':
        root = value;
      case '--output':
        output = value;
      case '--id':
        id = value;
      case '--app-id':
        appId = value;
      default:
        stderr.writeln(_usage);
        exitCode = 2;
        return;
    }
  }
  if (host == null) {
    stderr.writeln(_usage);
    exitCode = 2;
    return;
  }
  final out = File(p.isAbsolute(output) ? output : p.join(root, output));
  id ??= _previousId(out) ?? _newId();
  final result = await scan(
    root: root,
    slots: slots,
    host: host,
    id: id,
    appId: appId,
  );
  for (final problem in result.problems) {
    stderr.writeln(problem);
  }
  out.writeAsStringSync(encodeCatalogue(result.catalogue));
  stdout.writeln(
    '${p.relative(out.path, from: root)}: '
    '${(result.catalogue['routes']! as List).length} routes, '
    '${(result.catalogue['slots']! as List).length} slots, '
    '${(result.catalogue['actions']! as List).length} custom actions',
  );
}

/// The identifier of the catalogue a previous scan wrote, so scanning the
/// same code again writes the same bytes.
String? _previousId(File out) {
  if (!out.existsSync()) return null;
  try {
    final doc = jsonDecode(out.readAsStringSync());
    return doc is Map && doc['id'] is String ? doc['id'] as String : null;
  } on FormatException {
    return null;
  }
}

/// A fresh UUIDv7 (SCH-002), for a host's first scan.
String _newId() {
  final r = Random.secure();
  final ms = DateTime.now().millisecondsSinceEpoch;
  final b = List<int>.generate(16, (_) => r.nextInt(256));
  for (var i = 0; i < 6; i++) {
    b[i] = (ms >> (8 * (5 - i))) & 0xff;
  }
  b[6] = 0x70 | (b[6] & 0x0f);
  b[8] = 0x80 | (b[8] & 0x3f);
  final h = [for (final x in b) x.toRadixString(16).padLeft(2, '0')].join();
  return '${h.substring(0, 8)}-${h.substring(8, 12)}-${h.substring(12, 16)}-'
      '${h.substring(16, 20)}-${h.substring(20)}';
}
