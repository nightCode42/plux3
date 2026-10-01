// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/state/providers.dart';
import 'package:plux_flutter/src/sync/sync_event.dart';

SyncFailed failed(String status, String code) => SyncFailed(
  PluxException(
    PluxErrorCode.syncFailed,
    'sync failed',
    details: {'status': status, 'code': code},
  ),
);

void main() {
  test('the device is offline after a sync that reached no server, and '
      'online after one that got an answer [WGT-020]', () {
    final down = failed('0', 'unavailable');
    expect(offlineAfter(down, before: false), isTrue);
    for (final other in [
      failed('503', 'unavailable'),
      failed('0', 'truncated'),
      const SyncChecking(),
      const SyncRolledBack(2, 1),
      null,
    ]) {
      expect(offlineAfter(other, before: true), isTrue, reason: '$other');
      expect(offlineAfter(other, before: false), isFalse, reason: '$other');
    }
    for (final answered in [
      const SyncUpToDate(1),
      const SyncDownloading(1, 2),
      const SyncStaged(2),
      const SyncActivated(2),
    ]) {
      expect(offlineAfter(answered, before: true), isFalse);
    }
  });
}
