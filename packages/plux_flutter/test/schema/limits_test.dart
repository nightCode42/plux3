// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/schema/limits.g.dart';

void main() {
  test('the generated registry holds the specified defaults [LIM-001]', () {
    expect(PluxLimit.pageNodes.key, 'page.nodes');
    expect(PluxLimit.pageNodes.defaultValue, 5000);
    expect(PluxLimit.pageNodes.warning, 1000);
    expect(PluxLimit.pxlOperationBudget.defaultValue, 10000);
    expect(PluxLimit.pxlOperationBudget.unit, PluxLimitUnit.operations);
    expect(PluxLimit.deviceDiskQuota.defaultValue, 200 * 1024 * 1024);
  });

  test('every limit has a unique key and a default within its maximum', () {
    final keys = <String>{};
    for (final limit in PluxLimit.values) {
      expect(keys.add(limit.key), isTrue, reason: limit.key);
      expect(limit.defaultValue, inInclusiveRange(1, limit.max));
      expect(limit.warning, lessThan(limit.defaultValue));
    }
  });
}
