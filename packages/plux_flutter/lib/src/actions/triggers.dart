// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Triggers besides widget events (ACT-002): timers, state watchers, app
/// lifecycle, push opening, host events, data-source events and the
/// owner's error handler (ACT-020). An owner — a page, a plugin or the app
/// — runs its triggers through [OwnerTriggers]; the runtime dispatches
/// events to every registered owner through [TriggerHub]. The state engine
/// provides a [StateWatchSource]; data sources call [TriggerHub.dataLoaded]
/// and [TriggerHub.dataFailed]; `Plux.sendEvent` calls
/// [TriggerHub.hostEvent].
library;

import 'dart:async';

import 'package:plux_flutter/src/actions/clock.dart';
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;

/// The kinds of trigger, as the bundle's `TriggerKind` names them.
enum TriggerKind {
  /// A timer.
  timer,

  /// A state entry changed.
  stateChange,

  /// The app resumed.
  appResume,

  /// The app paused.
  appPause,

  /// A push notification was opened.
  pushOpened,

  /// A host event was sent into Plux.
  hostEvent,

  /// A data source loaded.
  dataLoaded,

  /// A data source failed.
  dataFailed,

  /// A run's error that no `onError` handled.
  error,
}

/// One declared trigger and the handler it runs.
final class TriggerSpec {
  /// Creates a trigger.
  const TriggerSpec({
    required this.kind,
    required this.handler,
    this.name = '',
    this.interval = Duration.zero,
    this.repeat = false,
  });

  /// The kind.
  final TriggerKind kind;

  /// A timer's name, a watcher's state path, a host event's or a data
  /// source's name.
  final String name;

  /// A timer's period, or its delay when it does not repeat.
  final Duration interval;

  /// Whether a timer repeats.
  final bool repeat;

  /// The handler.
  final fbs.Handler handler;
}

/// Decodes a bundle's triggers.
List<TriggerSpec> decodeTriggers(List<fbs.Trigger>? list) => [
  for (final t in list ?? const <fbs.Trigger>[])
    if (t.handler != null && t.kind.value < TriggerKind.values.length)
      TriggerSpec(
        kind: TriggerKind.values[t.kind.value],
        handler: t.handler!,
        name: t.name ?? '',
        interval: Duration(milliseconds: t.intervalMs),
        repeat: t.repeat,
      ),
];

/// A subscription to a state entry.
abstract interface class StateWatch {
  /// Ends the subscription.
  void cancel();
}

/// Where state watchers subscribe (ACT-002): the state engine calls
/// `onChange` with an entry's new value each time it changes.
abstract interface class StateWatchSource {
  /// Subscribes to the entry at [path], `page.count` or `app.visits`.
  StateWatch watch(String path, void Function(Object? value) onChange);
}

/// No state to watch: watchers never fire.
final class NoStateWatches implements StateWatchSource {
  /// Creates the source.
  const NoStateWatches();

  @override
  StateWatch watch(String path, void Function(Object? value) onChange) =>
      const _NoWatch();
}

final class _NoWatch implements StateWatch {
  const _NoWatch();

  @override
  void cancel() {}
}

/// Starts runs for one owner's triggers.
final class OwnerTriggers {
  /// Creates the triggers of an owner; [fire] starts a run of a trigger's
  /// handler with its payload, as `event`.
  OwnerTriggers({
    required this.specs,
    required this.fire,
    this.clock = const ActionClock(),
    this.watches = const NoStateWatches(),
  });

  /// The triggers.
  final List<TriggerSpec> specs;

  /// Starts a run.
  final void Function(TriggerSpec spec, Object? payload) fire;

  /// Time for timers.
  final ActionClock clock;

  /// Where watchers subscribe.
  final StateWatchSource watches;

  final List<Timer> _timers = [];
  final List<StateWatch> _watches = [];
  bool _started = false;

  /// The owner's error handler, or null (ACT-020).
  TriggerSpec? get errorHandler {
    for (final s in specs) {
      if (s.kind == TriggerKind.error) return s;
    }
    return null;
  }

  /// Starts the timers and watchers.
  void start() {
    if (_started) return;
    _started = true;
    for (final s in specs) {
      switch (s.kind) {
        case TriggerKind.timer:
          var ticks = 0;
          void tick() => fire(s, ++ticks);
          _timers.add(
            s.repeat
                ? clock.periodic(s.interval, tick)
                : clock.timer(s.interval, tick),
          );
        case TriggerKind.stateChange:
          _watches.add(watches.watch(s.name, (v) => fire(s, v)));
        default:
          break;
      }
    }
  }

  /// Fires the triggers of [kind] named [name] with [payload].
  void dispatch(TriggerKind kind, {String name = '', Object? payload}) {
    for (final s in specs) {
      if (s.kind == kind && s.name == name) fire(s, payload);
    }
  }

  /// Stops the timers and watchers.
  void dispose() {
    for (final t in _timers) {
      t.cancel();
    }
    for (final w in _watches) {
      w.cancel();
    }
    _timers.clear();
    _watches.clear();
  }
}

/// Dispatches the runtime's events to every registered owner (ACT-002).
final class TriggerHub {
  final Set<OwnerTriggers> _owners = {};

  /// Registers [owner] until the returned function is called.
  void Function() register(OwnerTriggers owner) {
    _owners.add(owner);
    return () => _owners.remove(owner);
  }

  void _all(TriggerKind kind, {String name = '', Object? payload}) {
    for (final o in [..._owners]) {
      o.dispatch(kind, name: name, payload: payload);
    }
  }

  /// The app resumed.
  void appResumed() => _all(TriggerKind.appResume);

  /// The app paused.
  void appPaused() => _all(TriggerKind.appPause);

  /// A push notification was opened.
  void pushOpened() => _all(TriggerKind.pushOpened);

  /// The host sent [name] with its checked [payload] (HST-013).
  void hostEvent(String name, Map<String, Object?> payload) =>
      _all(TriggerKind.hostEvent, name: name, payload: payload);

  /// Data source [source] loaded [value].
  void dataLoaded(String source, Object? value) =>
      _all(TriggerKind.dataLoaded, name: source, payload: value);

  /// Data source [source] failed with [error], a `PluxActionError` value.
  void dataFailed(String source, Map<String, Object?> error) =>
      _all(TriggerKind.dataFailed, name: source, payload: error);
}
