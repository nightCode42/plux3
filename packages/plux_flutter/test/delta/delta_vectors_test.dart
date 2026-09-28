// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/delta/delta.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';

/// The vectors written by backend/internal/delta (go test -update).
List<Map<String, Object?>> _vectors() {
  final file = File('../../schema/testdata/delta/vectors.json');
  final json = jsonDecode(file.readAsStringSync()) as Map<String, Object?>;
  return (json['vectors']! as List<Object?>).cast<Map<String, Object?>>();
}

Uint8List _bytes(Object? b64) => base64.decode(b64! as String);

void main() {
  final vectors = _vectors();

  test('the vector set covers results and every kind of damage', () {
    expect(vectors.where((v) => v['error'] == null).length, greaterThan(10));
    expect(vectors.where((v) => v['error'] != null).length, greaterThan(10));
  });

  for (final v in vectors) {
    test('applies ${v['name']} as the Go applier does [QA-002] [REL-025]', () {
      final old = _bytes(v['old']), delta = _bytes(v['delta']);
      final maxSize = v['maxSize']! as int;
      final error = v['error'] as String?;
      if (error == null) {
        expect(applyDelta(old, delta, maxSize), _bytes(v['new']));
        return;
      }
      final allowed = v['exact'] == true ? {error} : {'PLX-3011', 'PLX-3012'};
      expect(
        () => applyDelta(old, delta, maxSize),
        throwsA(
          isA<PluxException>().having((e) => e.code.id, 'code', isIn(allowed)),
        ),
      );
    });
  }
}
