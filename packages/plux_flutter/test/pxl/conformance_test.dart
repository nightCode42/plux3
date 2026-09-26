// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/pxl/program.dart';
import 'package:plux_flutter/src/pxl/types.dart';
import 'package:plux_flutter/src/pxl/vm.dart';

/// The conformance vectors shared with the Go compiler and VM.
final _vectors = Directory('../../schema/testdata/pxl');

PxlLimits _limits(Map<String, Object?>? l) {
  final d = PxlLimits.defaults();
  int pick(String key, int def) => (l?[key] as int?) ?? def;
  return PxlLimits(
    budget: pick('budget', d.budget),
    stringLength: pick('stringLength', d.stringLength),
    collectionSize: pick('collectionSize', d.collectionSize),
    decimalDigits: pick('decimalDigits', d.decimalDigits),
  );
}

bool _jsonEqual(Object? a, Object? b) => switch (a) {
  final num x => b is num && x == b,
  final List<Object?> x =>
    b is List<Object?> &&
        x.length == b.length &&
        [for (var i = 0; i < x.length; i++) _jsonEqual(x[i], b[i])]
            .every((e) => e),
  final Map<String, Object?> x =>
    b is Map<String, Object?> &&
        x.length == b.length &&
        x.entries.every(
          (e) => b.containsKey(e.key) && _jsonEqual(e.value, b[e.key]),
        ),
  _ => a == b,
};

void main() {
  final files =
      _vectors
          .listSync()
          .whereType<File>()
          .where((f) => f.path.endsWith('.json'))
          .toList()
        ..sort((a, b) => a.path.compareTo(b.path));

  test('the vector directory is found', () => expect(files, isNotEmpty));

  for (final file in files) {
    final f = jsonDecode(file.readAsStringSync()) as Map<String, Object?>;
    final env = PxlEnv.fromJson(f['env']! as Map<String, Object?>);
    final limits = _limits(f['limits'] as Map<String, Object?>?);
    group(file.uri.pathSegments.last, () {
      for (final c
          in (f['cases']! as List<Object?>).cast<Map<String, Object?>>()) {
        if (c['program'] == null) continue; // compile-time diagnostics only
        test(
          '${c['name']}: ${c['expr']} [PXL-001] [PXL-005] [PXL-006] [PXL-007] [QA-003]',
          () {
            final program = Program.decode(
              base64.decode(c['program']! as String),
            );
            final inputs =
                (c['inputs'] ?? f['inputs'] ?? const <String, Object?>{})
                    as Map<String, Object?>;
            final values = {
              for (final e in inputs.entries)
                e.key: fromJson(env.roots[e.key]!, e.value),
            };
            final result = evaluate(program, values, limits);
            if (c['error'] case final String kind) {
              expect(
                result,
                isA<PxlFailure>().having(
                  (r) => r.error.kind.name,
                  'kind',
                  kind,
                ),
              );
            } else {
              expect(
                result,
                isA<PxlValue>(),
                reason: result is PxlFailure ? '${result.error}' : null,
              );
              final got = toJson((result as PxlValue).value);
              expect(
                _jsonEqual(got, c['value']),
                isTrue,
                reason:
                    'got ${jsonEncode(got)}, want ${jsonEncode(c['value'])}',
              );
            }
          },
        );
      }
    });
  }
}
