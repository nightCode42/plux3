// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_flutter/src/devtools_api/diagnostics.dart';

void main() {
  testWidgets('a problem reported while a frame is built reaches listeners '
      'after the frame, and one reported between frames at once', (
    tester,
  ) async {
    final d = RuntimeDiagnostics(
      ValueNotifier(null),
      ValueNotifier<SyncEvent?>(null),
    );
    addTearDown(d.dispose);
    const failed = PluxException(PluxErrorCode.nodeBuildFailed, 'node 3');
    await tester.pumpWidget(
      Directionality(
        textDirection: TextDirection.ltr,
        child: Column(
          children: [
            // What plux_devtools shows: the log, rebuilt when it changes.
            ListenableBuilder(
              listenable: d.log,
              builder: (_, _) => Text('${d.log.value.length} problems'),
            ),
            // A node that reports its failure while it builds.
            Builder(
              builder: (_) {
                d.record(failed);
                return const SizedBox();
              },
            ),
          ],
        ),
      ),
    );
    expect(tester.takeException(), isNull);
    await tester.pump();
    expect(find.text('1 problems'), findsOneWidget);
    d.record(failed);
    expect(d.log.value, hasLength(2), reason: 'between frames: at once');
  });
}
