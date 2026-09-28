// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/verify/jcs.dart';

Map<String, Object?> _read(String name) =>
    jsonDecode(File('../../schema/testdata/jcs/$name').readAsStringSync())
        as Map<String, Object?>;

void main() {
  test('canonicalises the shared vectors as Go does [SCH-003]', () {
    final vectors = (_read('vectors.json')['vectors']! as List<Object?>)
        .cast<Map<String, Object?>>();
    for (final v in vectors) {
      final Object? input;
      try {
        input = jsonDecode(v['input']! as String);
      } on FormatException {
        continue; // vectors of input Go's strict parser rejects
      }
      if (v['canonical'] == null) continue;
      expect(canonicalJson(input), v['canonical'], reason: '${v['name']}');
      expect(
        isCanonicalJson(utf8.encode(v['canonical']! as String)),
        isTrue,
        reason: '${v['name']}',
      );
    }
  });

  test('formats every number of RFC 8785 Appendix B', () {
    final numbers = (_read('numbers.json')['numbers']! as List<Object?>)
        .cast<Map<String, Object?>>();
    for (final n in numbers) {
      final bits = n['bits']! as String;
      final d = ByteData(8)
        ..setUint32(0, int.parse(bits.substring(0, 8), radix: 16))
        ..setUint32(4, int.parse(bits.substring(8), radix: 16));
      final value = d.getFloat64(0);
      if (value.isNaN || value.isInfinite) {
        expect(() => canonicalNumber(value), throwsArgumentError);
        continue;
      }
      expect(canonicalNumber(value), n['canonical'], reason: bits);
    }
  });

  test('refuses what is not canonical', () {
    for (final text in [
      '{"b":1,"a":2}',
      '{"a":1,"a":1}',
      '{"a": 1}',
      '[1.0]',
      '"\\u0041"',
      '{"a":1}\n',
      'not json',
    ]) {
      expect(isCanonicalJson(utf8.encode(text)), isFalse, reason: text);
    }
    expect(isCanonicalJson([0xff, 0xfe]), isFalse);
    expect(isCanonicalJson(utf8.encode('{"a":[1,true,null,"x\\n"]}')), isTrue);
    expect(() => canonicalJson(Object()), throwsArgumentError);
  });
}
