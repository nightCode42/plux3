// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/actions/run.dart';
import 'package:plux_flutter/src/bundle/container.dart';
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/data/client.dart';
import 'package:plux_flutter/src/schema/limit_values.dart';
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

  test('an absent key is the registry default, a present one its value '
      '[LIM-001]', () {
    expect(
      const <String, int>{}.valueOf(PluxLimit.pageNodes),
      PluxLimit.pageNodes.defaultValue,
    );
    expect(const {'page.nodes': 7}.valueOf(PluxLimit.pageNodes), 7);

    final actions = ActionLimits.of(const {});
    expect(actions.stepsPerRun, PluxLimit.actionStepsPerRun.defaultValue);
    expect(
      actions.forEachItemsLimit,
      PluxLimit.actionForEachItems.defaultValue,
    );
    expect(
      ActionLimits.of(const {'action.forEachItems': 5}).forEachItemsLimit,
      5,
    );
    final data = DataLimits.of(const {});
    expect(data.pageSize, PluxLimit.dataPageSize.defaultValue);
    expect(DataLimits.of(const {'data.pageSize': 9}).pageSize, 9);
  });

  test('the compiled conformance bundles carry no limit entries [LIM-001]', () {
    final goldens = Directory('../../schema/testdata/bundles')
        .listSync(recursive: true)
        .whereType<File>()
        .where(
          (f) => f.path.endsWith('.pxb') && !f.path.endsWith('sample.pxb'),
        );
    expect(goldens, isNotEmpty);
    for (final f in goldens) {
      final b = BundleContainer.parse(f.readAsBytesSync());
      final meta = fbs.Meta(b.ofKind(SectionKind.meta).single.data);
      expect(meta.limits ?? const <fbs.Limit>[], isEmpty, reason: f.path);
    }
  });
}
