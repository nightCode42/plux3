// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/render/messages.dart';

void main() {
  test('arguments, select, plural and quoting (ICU MessageFormat)', () {
    expect(formatMessage('Hello {name}!', {'name': 'Ada'}), 'Hello Ada!');
    expect(formatMessage('{ name }', {'name': 'x'}), 'x');
    expect(formatMessage('{n, number}', {'n': 2.0}), '2');
    expect(formatMessage('{n}', {'n': 2.5}), '2.5');
    expect(formatMessage('{missing}', {}), '');
    const plural = '{count, plural, =0 {none} one {# item} other {# items}}';
    expect(formatMessage(plural, {'count': 0}), 'none');
    expect(formatMessage(plural, {'count': 1}), '1 item');
    expect(formatMessage(plural, {'count': 5}), '5 items');
    const select = '{g, select, female {she} male {he} other {they}}';
    expect(formatMessage(select, {'g': 'female'}), 'she');
    expect(formatMessage(select, {'g': 'x'}), 'they');
    expect(formatMessage("It''s '{literal}' #", {}), "It's {literal} #");
    expect(formatMessage("'{unterminated", {}), '{unterminated');
    expect(formatMessage("a'b", {}), "a'b");
  });

  test('malformed patterns render as written', () {
    for (final p in [
      '{',
      '{a,',
      '{a, plural, one {x}',
      '{a, date, short}',
      '{a, plural, offset:1 other {#}}',
      'x}',
    ]) {
      expect(formatMessage(p, {'a': 1}), p, reason: p);
    }
  });
}
