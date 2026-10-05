// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Host events (HST-013): the typed events plugins send to the host with
/// `emitHostEvent`, and those the host sends into Plux with
/// `Plux.sendEvent`, declared in the app document's `hostEvents`.
library;

/// An event a plugin emitted. Listen with `Plux.events`; `plux codegen`
/// generates a class per declared event that reads [payload].
final class PluxHostEvent {
  /// Creates an event.
  const PluxHostEvent(this.name, this.payload);

  /// The event's declared name.
  final String name;

  /// The event's fields in the JSON form of their types
  /// (document-model.md §3): numbers, strings and booleans as they are;
  /// decimals, dates and colours as strings; money as
  /// `{"amount", "currency"}`; durations as milliseconds.
  final Map<String, Object?> payload;

  @override
  String toString() => 'PluxHostEvent($name, $payload)';
}

/// Receives the events the host sends into Plux with `Plux.sendEvent`
/// (HST-013); the action engine's host-event trigger implements it.
abstract interface class HostEventSink {
  /// Delivers [event], whose payload is in its JSON form; returns whether
  /// the app declares the event for the host to send and its payload has
  /// the declared fields and types. A refused event runs nothing.
  bool deliver(PluxHostEvent event);
}
