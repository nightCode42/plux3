// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_devtools/plux_devtools.dart';
import 'package:plux_flutter/plux_flutter.dart';

final class _Diagnostics implements PluxDiagnostics {
  @override
  final ValueNotifier<List<PluxDiagnostic>> log = ValueNotifier(const []);

  @override
  final ValueNotifier<PluxReleaseInfo?> release = ValueNotifier(null);

  @override
  final ValueNotifier<SyncEvent?> syncStatus = ValueNotifier(null);
}

void main() {
  late _Diagnostics d;
  setUp(() => d = _Diagnostics());

  Widget app({Future<Object?> Function()? onSync, bool enabled = true}) =>
      MaterialApp(
        home: const Scaffold(body: Text('host')),
        builder: (context, child) => PluxDevtools(
          diagnostics: d,
          onSync: onSync,
          enabled: enabled,
          child: child!,
        ),
      );

  testWidgets(
    'shows the release, the sync status and reported problems [RT-060]',
    (tester) async {
      await tester.pumpWidget(app());
      expect(find.text('host'), findsOneWidget);
      expect(find.text('Plux –'), findsOneWidget);

      d.release.value = const PluxReleaseInfo(
        sequence: 7,
        plugins: [
          PluxPluginInfo(
            key: '',
            version: 1,
            bundleHash: 'aaaaaaaaaaaaaaaaaaaa',
            switchedOff: false,
          ),
          PluxPluginInfo(
            key: 'loans',
            version: 3,
            bundleHash: 'bb',
            switchedOff: true,
          ),
        ],
      );
      d.log.value = [
        PluxDiagnostic(
          at: DateTime.utc(2026),
          error: const PluxException(
            PluxErrorCode.bundleMalformed,
            'the stored bundle of loans is not the signed one',
          ),
        ),
      ];
      d.syncStatus.value = const SyncActivated(7);
      await tester.pump();
      expect(find.text('Plux #7 · 1!'), findsOneWidget);

      await tester.tap(find.text('Plux #7 · 1!'));
      await tester.pump();
      expect(find.text('Status: release 7 active'), findsOneWidget);

      await tester.tap(find.text('release'));
      await tester.pump();
      expect(find.text('Release #7'), findsOneWidget);
      expect(find.text('app bundle'), findsOneWidget);
      expect(find.text('v1 · aaaaaaaaaaaa'), findsOneWidget);
      expect(find.text('switched off'), findsOneWidget);

      await tester.tap(find.text('log'));
      await tester.pump();
      expect(find.textContaining('the stored bundle of loans'), findsOneWidget);
      expect(
        find.textContaining(PluxErrorCode.bundleMalformed.id),
        findsOneWidget,
      );

      await tester.tap(find.text('close'));
      await tester.pump();
      expect(find.text('Plux #7 · 1!'), findsOneWidget);
    },
  );

  testWidgets('empty states, and Sync now runs one sync at a time', (
    tester,
  ) async {
    final done = Completer<Object?>();
    var syncs = 0;
    await tester.pumpWidget(
      app(
        onSync: () {
          syncs++;
          return done.future;
        },
      ),
    );
    await tester.tap(find.text('Plux –'));
    await tester.pump();
    expect(find.text('Status: not synced yet'), findsOneWidget);
    await tester.tap(find.text('Sync now'));
    await tester.pump();
    expect(find.text('Syncing…'), findsOneWidget);
    await tester.tap(find.text('Syncing…'));
    expect(syncs, 1);
    done.complete(null);
    await tester.pump();
    expect(find.text('Sync now'), findsOneWidget);

    await tester.tap(find.text('release'));
    await tester.pump();
    expect(find.text('No release'), findsOneWidget);
    await tester.tap(find.text('log'));
    await tester.pump();
    expect(find.text('No problems'), findsOneWidget);
  });

  testWidgets('without Plux, or when disabled, shows only the host', (
    tester,
  ) async {
    await tester.pumpWidget(
      MaterialApp(
        home: const Text('host'),
        builder: (context, child) => PluxDevtools(child: child!),
      ),
    );
    await tester.tap(find.text('Plux –'));
    await tester.pump();
    expect(find.text('Plux is not initialized'), findsOneWidget);

    await tester.pumpWidget(app(enabled: false));
    expect(find.text('host'), findsOneWidget);
    expect(find.textContaining('Plux'), findsNothing);
  });

  test('describes every sync event', () {
    expect(
      [
        const SyncChecking(),
        const SyncDownloading(10, 20),
        const SyncUpToDate(4),
        const SyncStaged(5),
        const SyncRolledBack(5, 4),
        const SyncFailed(PluxException(PluxErrorCode.syncFailed, 'offline')),
      ].map(describeSyncEvent),
      [
        'checking',
        'downloading 10 of 20 bytes',
        'up to date',
        'release 5 staged',
        'release 5 failed its trial; back to 4',
        'failed: ${PluxErrorCode.syncFailed.id} offline',
      ],
    );
  });
}
