// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Host events (HST-013): the typed events plugins send to the host with
/// `emitHostEvent`, and those the host sends into Plux with
/// `Plux.sendEvent`, declared in the app document's `hostEvents`.
library;

import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/core/active_release.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/pxl/types.dart';

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
  /// the app declares the event for the host to send and something
  /// handles it. A refused event runs nothing. Throws a [PluxException]
  /// with `PLX-5500` when the payload does not have the declared fields
  /// and types.
  bool deliver(PluxHostEvent event);
}

/// Checks the events the host sends against the app bundle's `hostEvents`
/// declarations (HST-013) before [next] sees them: an event the app does
/// not declare with the direction `toPlux` or `both` is refused, and a
/// payload is converted to the PXL values of its declared field types, so
/// a trigger reads a decimal, a date or a money amount as such. A payload
/// with a missing, unknown or mistyped field throws `PLX-5500`, which
/// names the field and never its value.
final class TypedHostEvents implements HostEventSink {
  /// Creates the sink over the active release's declarations.
  const TypedHostEvents({
    required this.release,
    required this.types,
    required this.next,
  });

  /// The active release, or null before one is active.
  final ActiveRelease? Function() release;

  /// The named types the app's field types may use.
  final Map<String, NamedType> Function(ActiveRelease release) types;

  /// Receives the events that pass, with typed payloads.
  final HostEventSink next;

  @override
  bool deliver(PluxHostEvent event) {
    final r = release();
    final decl = r == null ? null : _declaration(r, event.name);
    if (r == null ||
        decl == null ||
        decl.direction == fbs.HostEventDirection.ToHost) {
      return false;
    }
    return next.deliver(PluxHostEvent(event.name, _typed(r, decl, event)));
  }

  static fbs.HostEvent? _declaration(ActiveRelease r, String name) {
    for (final e in r.meta('').hostEvents ?? const <fbs.HostEvent>[]) {
      if (e.name == name) return e;
    }
    return null;
  }

  Map<String, Object?> _typed(
    ActiveRelease r,
    fbs.HostEvent decl,
    PluxHostEvent event,
  ) {
    PluxException refuse(String why) => PluxException(
      PluxErrorCode.hostEventPayloadInvalid,
      'host event ${event.name}: $why',
      details: {'event': event.name},
    );
    final fields = decl.fields ?? const <fbs.HostEventField>[];
    final declared = {for (final f in fields) ?f.name};
    for (final k in event.payload.keys) {
      if (!declared.contains(k)) throw refuse('$k is not a declared field');
    }
    final named = types(r);
    final out = <String, Object?>{};
    for (final f in fields) {
      final name = f.name!;
      try {
        out[name] = fromJson(
          PxlType.parse(f.type!, (n) => named[n]),
          event.payload[name],
        );
      } on FormatException {
        throw refuse(
          event.payload.containsKey(name)
              ? '$name is not a ${f.type}'
              : '$name is missing',
        );
      }
    }
    return out;
  }
}
