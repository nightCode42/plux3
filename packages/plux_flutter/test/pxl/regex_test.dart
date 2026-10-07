// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/pxl/regex.dart';

/// The engine vectors shared with the Go engine.
final _vectors = File('../../schema/testdata/regex/engine.json');

void main() {
  final f = jsonDecode(_vectors.readAsStringSync()) as Map<String, Object?>;
  final l = f['limits']! as Map<String, Object?>;
  final limits = RegexLimits(
    patternLength: l['patternLength']! as int,
    programSize: l['programSize']! as int,
    repeat: l['repeat']! as int,
  );
  List<int>? ints(Object? v) =>
      v == null ? null : (v as List<Object?>).cast<int>();

  group('engine vectors', () {
    for (final c
        in (f['cases']! as List<Object?>).cast<Map<String, Object?>>()) {
      final pattern = c['pattern']! as String;
      test('$pattern [PXL-006] [PXL-007]', () {
        final error = c['error'] as String?;
        if (error != null) {
          expect(
            () => Regex.compile(pattern, limits),
            throwsA(
              isA<RegexError>()
                  .having((e) => e.kind.name, 'kind', error)
                  .having((e) => e.offset, 'offset', c['offset']),
            ),
          );
          return;
        }
        final re = Regex.compile(pattern, limits);
        expect(re.size, c['size']);
        expect(re.groups, c['groups'] ?? 0);
        for (final i
            in (c['inputs'] as List<Object?>? ?? [])
                .cast<Map<String, Object?>>()) {
          final input = i['input']! as String;
          expect(re.find(input), ints(i['find']), reason: 'find $input');
          expect(re.prefix(input), ints(i['prefix']), reason: 'prefix $input');
          expect(re.fullMatch(input), i['full'], reason: 'full $input');
          expect(re.hasMatch(input), i['find'] != null, reason: 'has $input');
        }
      });
    }
  });

  test('rejects unpaired surrogates and reads them as U+FFFD in input', () {
    expect(
      () => Regex.compile('a\uD800', RegexLimits.trusted),
      throwsA(isA<RegexError>().having((e) => e.offset, 'offset', 1)),
    );
    expect(
      Regex.compile('^�\$', RegexLimits.trusted).hasMatch('\uDC00'),
      isTrue,
    );
    expect(Regex.compile('^.\$', RegexLimits.trusted).hasMatch('😀'), isTrue);
    expect(
      const RegexError(RegexErrorKind.repeat, 2, 'm').toString(),
      'regex: repeat at 2: m',
    );
  });

  test('bounds nesting and program size', () {
    expect(
      () => Regex.compile('${'(' * 101}${')' * 101}', RegexLimits.trusted),
      throwsA(isA<RegexError>()),
    );
    expect(
      () => Regex.compile(
        '((a{1000}){1000}){1000}',
        const RegexLimits(
          patternLength: 100,
          programSize: 100000,
          repeat: 1000,
        ),
      ),
      throwsA(
        isA<RegexError>().having(
          (e) => e.kind,
          'kind',
          RegexErrorKind.programSize,
        ),
      ),
    );
  });
}
