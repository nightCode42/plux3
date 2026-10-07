// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/actions/triggers.dart';
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/core/features.dart';
import 'package:plux_flutter/src/core/trigger_sources.dart';
import 'package:plux_flutter/src/data/source.dart';
import 'package:plux_flutter/src/data/spec.dart';

/// How streams, the outbox and transfers meet the rest of the runtime.
void main() {
  test('the runtime supports the features a bundle with streams, an outbox or transfers requires [DAT-012] [DAT-020] [DAT-031] [BND-008]', () {
    final f = RuntimeFeatures();
    for (final k in [
      'data.streams.v1',
      'data.outbox.v1',
      'data.transfers.v1',
    ]) {
      expect(f.supports(k), isTrue, reason: k);
    }
    expect(f.supports('data.streams.v2'), isFalse);
  });

  test('the trigger kinds of the runtime follow the bundle\'s enum, so a trigger decodes to the event it handles [DAT-012] [DAT-020] [DAT-031]', () {
    const pairs = {
      fbs.TriggerKind.Error: TriggerKind.error,
      fbs.TriggerKind.DataMessage: TriggerKind.dataMessage,
      fbs.TriggerKind.DataProgress: TriggerKind.dataProgress,
      fbs.TriggerKind.OutboxSynced: TriggerKind.outboxSynced,
      fbs.TriggerKind.OutboxFailed: TriggerKind.outboxFailed,
      fbs.TriggerKind.OutboxConflict: TriggerKind.outboxConflict,
    };
    for (final e in pairs.entries) {
      expect(TriggerKind.values[e.key.value], e.value);
    }
    expect(TriggerKind.values, hasLength(fbs.TriggerKind.values.length));
  });

  test('messages, progress and the ends of queued mutations reach the triggers of the plugin and source they name [DAT-012] [DAT-020] [DAT-031]', () {
    TriggerSpec spec(TriggerKind kind) => TriggerSpec(
      kind: kind,
      name: 'orders',
      handler: fbs.Handler(fbs.HandlerObjectBuilder(event: 0).toBytes()),
    );
    final fired = <(TriggerKind, Object?)>[];
    final hub = TriggerHub()
      ..register(
        OwnerTriggers(
          specs: [
            spec(TriggerKind.dataMessage),
            spec(TriggerKind.dataProgress),
            spec(TriggerKind.outboxSynced),
            spec(TriggerKind.outboxFailed),
            spec(TriggerKind.outboxConflict),
          ],
          fire: (s, p) => fired.add((s.kind, p)),
          plugin: 'shop',
          sourceOf: (n) => 'id-1',
        ),
      );
    final bridge = DataTriggers(hub);
    DataSourceEvent event(Object? value, {String plugin = 'shop'}) =>
        DataSourceEvent(
          pluginKey: plugin,
          source: 'orders',
          sourceId: 'id-1',
          value: value,
        );
    bridge
      ..message(event({'id': '1'}))
      ..message(event({'id': 'x'}, plugin: 'other'))
      ..progress(event({'operation': 'orders.up', 'sent': 1, 'total': 2}))
      ..outbox(OutboxOutcome.synced, event({'key': 'k', 'status': 201}))
      ..outbox(OutboxOutcome.failed, event({'key': 'k', 'status': 400}))
      ..outbox(OutboxOutcome.conflict, event({'key': 'k', 'status': 409}));
    expect(
      [for (final (k, _) in fired) k],
      [
        TriggerKind.dataMessage,
        TriggerKind.dataProgress,
        TriggerKind.outboxSynced,
        TriggerKind.outboxFailed,
        TriggerKind.outboxConflict,
      ],
    );
    expect(
      [for (final (_, p) in fired) p],
      [
        {'id': '1'},
        {'operation': 'orders.up', 'sent': 1, 'total': 2},
        {'key': 'k', 'status': 201},
        {'key': 'k', 'status': 400},
        {'key': 'k', 'status': 409},
      ],
    );
  });

  test(
    'an operation knows whether it mutates, by method or by document [DAT-020]',
    () {
      const get = OperationSpec(name: 'a', method: 'GET', auth: false);
      const post = OperationSpec(name: 'b', auth: false);
      const query = OperationSpec(
        name: 'c',
        query: 'query Q { x }',
        auth: false,
      );
      const mutation = OperationSpec(
        name: 'd',
        query: '  mutation M { x }',
        auth: false,
      );
      expect(get.mutates(DataKind.rest), isFalse);
      expect(post.mutates(DataKind.rest), isTrue);
      expect(query.mutates(DataKind.graphql), isFalse);
      expect(mutation.mutates(DataKind.graphql), isTrue);
    },
  );
}
