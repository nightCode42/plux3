// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/forms/validators.dart';
import 'package:plux_flutter/src/pxl/decimal.dart';
import 'package:plux_flutter/src/pxl/regex.dart';
import 'package:plux_flutter/src/pxl/values.dart';

String? code(Validator v, Object? value) => v(value)?.code;

void main() {
  test('required rejects absent, blank and empty values [STA-020]', () {
    final v = Validators.required();
    expect(
      [null, '', '  ', <Object?>[]].map((x) => code(v, x)),
      everyElement('required'),
    );
    expect(code(v, 'a'), isNull);
    expect(code(v, 0), isNull);
  });

  test('length counts code points and list items [STA-020]', () {
    final v = Validators.length(min: 2, max: 3);
    expect(code(v, '😀'), 'tooShort');
    expect(code(v, '😀😀'), isNull);
    expect(v('abcd'), const ValidationFailure('tooLong', {'max': 3}));
    expect(code(v, [1, 2]), isNull);
    expect(code(v, ''), isNull, reason: 'absent values pass');
    expect(code(v, 5), isNull);
  });

  test('range compares numbers exactly as decimals [STA-020]', () {
    final v = Validators.range(
      min: Decimal.tryParse('0.1'),
      max: Decimal.fromInt(10),
    );
    expect(code(v, 0.1), isNull);
    expect(code(v, '0.09'), 'belowMin');
    expect(code(v, 11), 'aboveMax');
    expect(code(v, Decimal.tryParse('10.00')), isNull);
    expect(code(v, 'x'), 'notANumber');
    expect(code(v, null), isNull);
  });

  test('regex uses the pxl.regex.v1 engine [STA-020] [PXL-006]', () {
    const lim = RegexLimits(patternLength: 100, programSize: 200, repeat: 10);
    final v = Validators.regex(r'^[A-Z]{2}\d{3}$', lim);
    expect(code(v, 'AB123'), isNull);
    expect(code(v, 'ab123'), 'pattern');
    expect(code(v, ''), isNull);
    expect(() => Validators.regex('(?=a)', lim), throwsA(isA<RegexError>()));
    expect(() => Validators.regex('a{11}', lim), throwsA(isA<RegexError>()));
  });

  test('email, phone by region and IBAN [STA-020]', () {
    expect(code(Validators.email(), 'a@example.com'), isNull);
    expect(code(Validators.email(), 'a@b'), 'email');
    expect(code(Validators.phone('GB'), '07400 123456'), isNull);
    expect(code(Validators.phone('US'), '07400 123456'), 'phone');
    expect(code(Validators.phone('US'), '+44 7400 123456'), isNull);
    expect(code(Validators.iban(), 'DE89 3704 0044 0532 0130 00'), isNull);
    expect(code(Validators.iban(), 'DE88 3704 0044 0532 0130 00'), 'iban');
  });

  test('date range is inclusive [STA-020]', () {
    final v = Validators.dateRange<PxlDate>(
      min: const PxlDate(10),
      max: const PxlDate(20),
    );
    expect(code(v, const PxlDate(10)), isNull);
    expect(code(v, const PxlDate(9)), 'belowMin');
    expect(code(v, const PxlDate(21)), 'aboveMax');
    expect(code(v, 'not a date'), isNull);
  });

  test('decimal precision ignores trailing zeros [STA-020]', () {
    final v = Validators.decimalPrecision(maxScale: 2, maxIntegerDigits: 3);
    expect(code(v, '123.40'), isNull);
    expect(code(v, '1.234'), 'tooManyDecimals');
    expect(code(v, 1234), 'tooManyDigits');
    expect(code(v, 'abc'), 'notANumber');
  });

  test('first returns the first failure in order', () {
    final vs = [Validators.required(), Validators.length(min: 3)];
    expect(Validators.first(vs, '')?.code, 'required');
    expect(Validators.first(vs, 'ab')?.code, 'tooShort');
    expect(Validators.first(vs, 'abc'), isNull);
    expect(const ValidationFailure('x').toString(), 'ValidationFailure(x, {})');
    expect(
      const ValidationFailure('x').hashCode,
      const ValidationFailure('x').hashCode,
    );
  });
}
