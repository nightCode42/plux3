// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The runtime's telemetry events (Appendix G.2, ADR-0034): their consent
/// categories, the redaction every event passes and the form in which the
/// sync isolate buffers and sends them. Shared by both isolates.
library;

import 'dart:convert';

/// What a user must have agreed to before an event is sent (SEC-161).
enum TelemetryCategory {
  /// Anonymous operational data, sent without consent: needed to deliver
  /// releases, keep them working and decide which a device can run.
  necessary,

  /// What the user does and looks at; needs `PluxConsent.analytics`.
  analytics,

  /// Experiment exposures; needs `PluxConsent.experiments`.
  experiments,
}

/// The category of each catalogued event (ADR-0034 § Consent).
const Map<String, TelemetryCategory> telemetryCategories = {
  'session_start': TelemetryCategory.necessary,
  'sync_result': TelemetryCategory.necessary,
  'error': TelemetryCategory.necessary,
  'rasp_detection': TelemetryCategory.necessary,
  'session_end': TelemetryCategory.analytics,
  'screen_view': TelemetryCategory.analytics,
  'render_perf': TelemetryCategory.analytics,
  'action_run': TelemetryCategory.analytics,
  'api_call': TelemetryCategory.analytics,
  'function_call': TelemetryCategory.analytics,
  'custom': TelemetryCategory.analytics,
  'experiment_exposure': TelemetryCategory.experiments,
};

/// Words that mark a field name as sensitive; the server refuses such a
/// field, so the runtime never sends one (SCH-012). Kept in step with
/// `internal/telemetry` on the server.
const _sensitiveWords = [
  'password',
  'passcode',
  'secret',
  'token',
  'pin',
  'otp',
  'cvv',
  'card',
  'iban',
  'ssn',
  'email',
  'phone',
];

final _fieldName = RegExp(r'^[a-z][A-Za-z0-9_]{0,63}$');

/// The longest string a field or route may hold, in UTF-8 bytes.
const maxTelemetryText = 256;

/// At most this many fields are kept.
const maxTelemetryFields = 32;

/// Whether a field name marks sensitive data.
bool isSensitiveField(String name) {
  final lower = name.toLowerCase();
  return _sensitiveWords.any(lower.contains);
}

/// The fields an event may carry: flat scalars under well-formed,
/// non-sensitive names, strings no longer than [maxTelemetryText] bytes,
/// at most [maxTelemetryFields] of them, in name order. Anything else is
/// dropped rather than sent, so no event is ever refused whole.
Map<String, Object> redactFields(Map<String, Object?> fields) {
  final out = <String, Object>{};
  for (final name in fields.keys.toList()..sort()) {
    if (out.length == maxTelemetryFields) break;
    final v = fields[name];
    if (!_fieldName.hasMatch(name) || isSensitiveField(name)) continue;
    switch (v) {
      case String() when utf8.encode(v).length <= maxTelemetryText:
      case bool():
      case int():
        out[name] = v!;
      case double() when v.isFinite:
        out[name] = v;
      default:
        break;
    }
  }
  return out;
}

/// Cuts [s] to at most [maxTelemetryText] UTF-8 bytes, at a character
/// boundary.
String clipText(String s) {
  var bytes = 0;
  final out = StringBuffer();
  for (final r in s.runes) {
    final n = r < 0x80 ? 1 : (r < 0x800 ? 2 : (r < 0x10000 ? 3 : 4));
    if (bytes + n > maxTelemetryText) break;
    bytes += n;
    out.writeCharCode(r);
  }
  return out.toString();
}

/// One recorded event, as the sync isolate buffers it: one JSON line with
/// its category, so that withdrawing consent can delete the category's
/// events, and the event in the form `IngestEvents` takes.
final class TelemetryLine {
  /// Creates a line.
  const TelemetryLine(this.category, this.event);

  /// Reads a buffered line; null when it is not one.
  static TelemetryLine? parse(String line) {
    try {
      final m = jsonDecode(line) as Map<String, Object?>;
      final c = TelemetryCategory.values.asNameMap()[m['c']];
      final e = m['e'];
      if (c == null || e is! Map<String, Object?>) return null;
      return TelemetryLine(c, e);
    } on FormatException {
      return null;
    } on TypeError {
      return null;
    }
  }

  /// The event's category.
  final TelemetryCategory category;

  /// The event as Connect's JSON encoding of `plux.v1.Event`.
  final Map<String, Object?> event;

  /// The buffered form: one line of JSON, without its newline.
  String encode() => jsonEncode({'c': category.name, 'e': event});
}

/// Builds the Connect JSON of a `plux.v1.Event` (Appendix G.2). The device
/// is the one the request's token names, so it is not repeated; fields are
/// canonical JSON, carried as bytes.
Map<String, Object?> eventJson({
  required String name,
  required DateTime time,
  required Map<String, Object> fields,
  int releaseSequence = 0,
  String pluginKey = '',
  String route = '',
}) => {
  'name': name,
  'time': time.toUtc().toIso8601String(),
  if (releaseSequence > 0) 'releaseSequence': '$releaseSequence',
  if (pluginKey.isNotEmpty) 'pluginKey': pluginKey,
  if (route.isNotEmpty) 'route': clipText(route),
  if (fields.isNotEmpty)
    'fields': base64.encode(utf8.encode(jsonEncode(fields))),
};
