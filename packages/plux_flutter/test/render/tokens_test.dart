// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/pxl/values.dart';
import 'package:plux_flutter/src/render/tokens.dart';

void main() {
  test('W3C token values convert to the prop types they bind [THM-001]', () {
    expect(convertToken('color', '#FF0000'), parseColor('#FF0000'));
    expect(convertToken('color', 1), isNull);
    expect(convertToken('dimension', {'value': 12, 'unit': 'px'}), 12.0);
    expect(convertToken('dimension', {'value': 2, 'unit': 'rem'}), 32.0);
    expect(convertToken('dimension', 8), 8.0);
    expect(convertToken('dimension', '4px'), 4.0);
    expect(convertToken('dimension', true), isNull);
    expect(convertToken('number', 1.5), 1.5);
    expect(convertToken('number', 'x'), isNull);
    expect(convertToken('fontFamily', 'Inter'), 'Inter');
    expect(convertToken('fontFamily', ['Inter', 'Noto']), 'Inter');
    expect(convertToken('fontFamily', 3), isNull);
    expect(convertToken('fontWeight', 600), 'w600');
    expect(convertToken('fontWeight', 649.0), 'w600');
    expect(convertToken('fontWeight', 'bold'), 'w700');
    expect(convertToken('fontWeight', 'nope'), isNull);
    expect(convertToken('fontWeight', 50), 'w100');
    expect(
      convertToken('duration', {'value': 2, 'unit': 's'}),
      const PxlDuration(2000),
    );
    expect(
      convertToken('duration', {'value': 150, 'unit': 'ms'}),
      const PxlDuration(150),
    );
    expect(convertToken('duration', '200ms'), const PxlDuration(200));
    expect(convertToken('duration', '1.5s'), const PxlDuration(1500));
    expect(convertToken('duration', 'soon'), isNull);
    expect(convertToken('duration', 'xs'), isNull);
    expect(
      convertToken('typography', {
        'fontFamily': ['Inter', 'Noto Sans Ethiopic'],
        'fontSize': {'value': 16, 'unit': 'px'},
        'fontWeight': 400,
        'letterSpacing': {'value': 1, 'unit': 'px'},
        'lineHeight': 1.5,
      }),
      {
        'fontFamily': 'Inter',
        'fontFamilyFallback': ['Noto Sans Ethiopic'],
        'fontSize': 16.0,
        'fontWeight': 'w400',
        'letterSpacing': 1.0,
        'height': 1.5,
      },
    );
    expect(convertToken('typography', 'x'), isNull);
    expect(
      convertToken('shadow', {
        'color': '#00000080',
        'offsetX': {'value': 1, 'unit': 'px'},
        'offsetY': 2,
        'blur': 4,
        'spread': 1,
      }),
      {
        'color': parseColor('#00000080'),
        'offset': {'dx': 1.0, 'dy': 2.0},
        'blurRadius': 4.0,
        'spreadRadius': 1.0,
      },
    );
    expect(convertToken('shadow', 1), isNull);
    expect(convertToken('cubicBezier', [0, 0, 1, 1]), isNull);
  });
}
