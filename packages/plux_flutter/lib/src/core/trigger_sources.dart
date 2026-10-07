// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Where the runtime's events become triggers (ACT-002): data-source loads
/// and failures, and the host events `Plux.sendEvent` delivers (HST-013),
/// dispatched through the runtime's [TriggerHub].
library;

import 'package:plux_flutter/src/actions/triggers.dart';
import 'package:plux_flutter/src/core/host_events.dart';
import 'package:plux_flutter/src/data/source.dart';

/// Feeds data-source events to the trigger hub: a load to the
/// `dataLoaded` triggers with the value loaded, a failure to the
/// `dataFailed` triggers with its `PluxActionError`; a stream's message
/// to the `dataMessage` triggers, a transfer's progress to the
/// `dataProgress` triggers, and the end of a queued mutation to the
/// `outbox…` triggers (DAT-012, DAT-020, DAT-031).
final class DataTriggers implements DataSourceEvents, DataIoEvents {
  /// Creates the bridge to [hub].
  const DataTriggers(this.hub);

  /// The runtime's trigger hub.
  final TriggerHub hub;

  @override
  void loaded(DataSourceEvent event) => hub.dataLoaded(
    event.source,
    event.value,
    plugin: event.pluginKey,
    sourceId: event.sourceId,
  );

  @override
  void failed(DataSourceEvent event) => hub.dataFailed(
    event.source,
    event.error ?? const {},
    plugin: event.pluginKey,
    sourceId: event.sourceId,
  );

  @override
  void message(DataSourceEvent event) => hub.dataMessage(
    event.source,
    event.value,
    plugin: event.pluginKey,
    sourceId: event.sourceId,
  );

  @override
  void progress(DataSourceEvent event) => hub.dataProgress(
    event.source,
    (event.value as Map<String, Object?>?) ?? const {},
    plugin: event.pluginKey,
    sourceId: event.sourceId,
  );

  @override
  void outbox(OutboxOutcome outcome, DataSourceEvent event) => hub.outbox(
    switch (outcome) {
      OutboxOutcome.synced => TriggerKind.outboxSynced,
      OutboxOutcome.failed => TriggerKind.outboxFailed,
      OutboxOutcome.conflict => TriggerKind.outboxConflict,
    },
    event.source,
    (event.value as Map<String, Object?>?) ?? const {},
    plugin: event.pluginKey,
    sourceId: event.sourceId,
  );
}

/// Delivers host events to the `hostEvent` triggers (HST-013). The
/// compiler accepts a host-event trigger only for an event the app
/// document declares, so an event is accepted when a running owner — the
/// app, a plugin or a page shown — declares a trigger for it; it runs
/// nothing and is refused otherwise.
final class HostEventTriggers implements HostEventSink {
  /// Creates the sink over [hub].
  const HostEventTriggers(this.hub);

  /// The runtime's trigger hub.
  final TriggerHub hub;

  @override
  bool deliver(PluxHostEvent event) {
    if (!hub.handles(TriggerKind.hostEvent, event.name)) return false;
    hub.hostEvent(event.name, event.payload);
    return true;
  }
}
