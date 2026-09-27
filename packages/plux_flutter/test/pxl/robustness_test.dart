// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:convert';
import 'dart:io';
import 'dart:math';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/pxl/program.dart';
import 'package:plux_flutter/src/pxl/tables.g.dart';
import 'package:plux_flutter/src/pxl/vm.dart';

/// The golden programs of the conformance vectors.
List<Uint8List> _goldens() => [
  for (final file in Directory(
    '../../schema/testdata/pxl',
  ).listSync().whereType<File>())
    if (file.path.endsWith('.json'))
      for (final c
          in ((jsonDecode(file.readAsStringSync())
                      as Map<String, Object?>)['cases']!
                  as List<Object?>)
              .cast<Map<String, Object?>>())
        if (c['program'] case final String p) base64.decode(p),
];

void main() {
  test('malformed programs are rejected or fail with a typed error, never crash [PXL-003] [SEC-054]', () {
    final random = Random(1);
    final limits = PxlLimits.defaults();
    var rejected = 0, evaluated = 0;
    for (final golden in _goldens()) {
      for (var round = 0; round < 20; round++) {
        final bytes = Uint8List.fromList(golden);
        final mutated = switch (round % 3) {
          0 => Uint8List.sublistView(bytes, 0, random.nextInt(bytes.length)),
          1 => bytes..[random.nextInt(bytes.length)] = random.nextInt(256),
          _ => bytes..[random.nextInt(bytes.length)] ^= 1 << random.nextInt(8),
        };
        final Program program;
        try {
          program = Program.decode(mutated);
        } on InvalidProgram {
          rejected++;
          continue;
        }
        // Inputs are missing on purpose: a program reading a root fails
        // with invalidInput rather than reading garbage.
        expect(evaluate(program, const {}, limits), isA<PxlResult>());
        evaluated++;
      }
    }
    expect(rejected, greaterThan(0));
    expect(evaluated, greaterThan(0));
  });

  test('the decoder rejects each malformed header', () {
    Uint8List bytes(List<int> b) => Uint8List.fromList(b);
    for (final (name, data) in [
      ('empty', bytes([])),
      ('unknown version', bytes([bytecodeVersion + 1])),
      ('bad varint', bytes([bytecodeVersion, ...List.filled(10, 0xFF), 0x7F])),
      ('empty code', bytes([bytecodeVersion, 0, 1, 0, 0, 0, 0])),
      (
        'trailing data',
        bytes([bytecodeVersion, 0, 1, 0, 0, 0, 1, Op.pushNull, 0]),
      ),
      (
        'too many locals',
        bytes([bytecodeVersion, 0x81, 0x02, 1, 0, 0, 0, 1, Op.pushNull]),
      ),
      ('unknown opcode', bytes([bytecodeVersion, 0, 1, 0, 0, 0, 1, 0xFF])),
      (
        'truncated operand',
        bytes([bytecodeVersion, 0, 1, 0, 0, 0, 1, Op.constValue]),
      ),
      (
        'unknown constant tag',
        bytes([bytecodeVersion, 0, 1, 0, 1, 0xEE, 0, 1, Op.pushNull]),
      ),
    ]) {
      expect(
        () => Program.decode(data),
        throwsA(isA<InvalidProgram>()),
        reason: name,
      );
    }
    expect(const InvalidProgram('x').toString(), 'InvalidProgram: x');
    expect(
      const PxlError(PxlErrorKind.overflow, 'x').toString(),
      'pxl: overflow: x',
    );
  });

  test('a program leaving extra values fails as invalid', () {
    // Two PUSH_NULL: verified structurally, but ends with two stack values.
    final p = Program.decode(
      Uint8List.fromList([
        bytecodeVersion,
        0,
        2,
        0,
        0,
        0,
        2,
        Op.pushNull,
        Op.pushNull,
      ]),
    );
    final r = evaluate(p, const {}, PxlLimits.defaults());
    expect(
      r,
      isA<PxlFailure>().having(
        (f) => f.error.kind,
        'kind',
        PxlErrorKind.invalidProgram,
      ),
    );
  });
}
