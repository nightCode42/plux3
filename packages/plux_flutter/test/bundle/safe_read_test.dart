// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/bundle/safe_read.dart';

void main() {
  test('reads a known enum value and tolerates an unknown one [BND-018]', () {
    expect(readEnum(() => fbs.ValueKind.fromValue(2)), fbs.ValueKind.Int);
    expect(readEnum(() => fbs.ValueKind.fromValue(200)), isNull);
  });
}
