// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/render/generated/render.g.dart';

void main() {
  test('the generated builders compile', () {
    expect(generatedBuilders, isNotEmpty);
  });
}
