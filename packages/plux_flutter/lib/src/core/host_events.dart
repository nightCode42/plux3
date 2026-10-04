// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Host events (HST-013): the typed events plugins send to the host with
/// `emitHostEvent`, declared in the app document's `hostEvents`.
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
