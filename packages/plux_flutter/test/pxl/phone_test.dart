// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/pxl/phone.dart';
import 'package:plux_flutter/src/pxl/phone.g.dart';
import 'package:plux_flutter/src/pxl/regex.dart';

/// The metadata and the golden shared with the Go implementation.
final _metadata = File('../../schema/pxl/phone.json');
final _examples = File('../../schema/testdata/phone/examples.json');

/// The inputs derived from one example, in the golden's variant order.
final Map<String, (String, String) Function(String, int, String)> _variants = {
  'national': (id, _, ex) => (ex, id),
  'international': (_, cc, ex) => ('+$cc $ex', ''),
  'idd': (_, cc, ex) => ('00$cc$ex', 'DE'),
  'longer': (id, _, ex) => ('${ex}0', id),
  'shorter': (id, _, ex) => (ex.substring(0, ex.length - 1), id),
};

void main() {
  test('agrees with Go on every example-derived input [PXL-006] [PXL-007] '
      '[STA-020]', () {
    final meta =
        jsonDecode(_metadata.readAsStringSync()) as Map<String, Object?>;
    final golden =
        jsonDecode(_examples.readAsStringSync()) as Map<String, Object?>;
    expect(golden['variants'], _variants.keys.toList());
    final results = StringBuffer();
    for (final t
        in (meta['territories']! as List<Object?>)
            .cast<Map<String, Object?>>()) {
      final id = t['id']! as String;
      final cc = t['countryCode']! as int;
      for (final ty
          in (t['types']! as List<Object?>).cast<Map<String, Object?>>()) {
        final ex = ty['example'] as String?;
        if (ex == null) continue;
        for (final MapEntry(key: name, value: input) in _variants.entries) {
          var (number, region) = input(id, cc, ex);
          if (id == '001' && name != 'international' && name != 'idd') {
            region = 'ZZ';
          }
          results.write(isValidPhone(number, region) ? '1' : '0');
        }
      }
    }
    expect(results.toString(), golden['results']);
  });

  test('every metadata pattern is in the regex subset', () {
    for (final line in phoneTable.trim().split('\n')) {
      final f = line.split(' ');
      for (var i = 3; i < f.length; i++) {
        if (i == 6 ||
            i == 8 ||
            i == 9 ||
            (i >= 10 && i.isEven) ||
            f[i] == '~') {
          continue;
        }
        expect(
          () => Regex.compile(f[i], RegexLimits.trusted),
          returnsNormally,
          reason: '${f[0]} field $i',
        );
      }
    }
    expect(phoneMetadataVersion, startsWith('v'));
  });

  test('reads regions in either case, and nothing else', () {
    expect(isPhoneRegion('gb'), isTrue);
    expect(isPhoneRegion('001'), isFalse);
    expect(isPhoneRegion('GBR'), isFalse);
    expect(isValidPhone('+44 7400 123456', ''), isTrue);
    expect(isValidPhone('1' * 251, ''), isFalse);
    expect(isValidPhone('7400 123456 x', 'GB'), isFalse);
    expect(isValidPhone('0 11 15-2345-6789', 'AR'), isTrue);
  });
}
