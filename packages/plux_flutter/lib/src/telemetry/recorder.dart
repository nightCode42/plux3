// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Records telemetry on the UI isolate (ADR-0034): decides, by consent
/// and sampling, whether an event is kept, redacts it, and hands it to the
/// sync isolate, which buffers and sends it. Recording is a map and a
/// port message: no I/O here (L-6).
library;

import 'dart:convert';
import 'dart:math' as math;

import 'package:crypto/crypto.dart';
import 'package:plux_flutter/src/core/config.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/telemetry/events.dart';

/// The recorder.
final class TelemetryRecorder {
  /// Creates a recorder that hands each kept event's line to [post] and
  /// calls [withdraw] with the categories whose consent is withdrawn.
  TelemetryRecorder({
    required this._post,
    required this._withdraw,
    required this._consent,
    Map<String, double> hostSampling = const {},
    math.Random? random,
    DateTime Function()? clock,
  }) : _host = hostSampling,
       _random = random ?? math.Random(),
       _clock = clock ?? DateTime.now;

  final void Function(List<String> lines) _post;
  final void Function(Set<TelemetryCategory> categories) _withdraw;
  final Map<String, double> _host;
  final math.Random _random;
  final DateTime Function() _clock;
  PluxConsent _consent;

  /// The rates the active app bundle sets (ANL-003), in `[0, 1]`.
  Map<String, double> appSampling = const {};

  /// The active release, which events are attributed to.
  int releaseSequence = 0;

  /// What the user consented to (SEC-161). Withdrawing a category also
  /// deletes its buffered events.
  PluxConsent get consent => _consent;
  set consent(PluxConsent c) {
    final withdrawn = {
      if (_consent.analytics && !c.analytics) TelemetryCategory.analytics,
      if (_consent.experiments && !c.experiments) TelemetryCategory.experiments,
    };
    _consent = c;
    if (withdrawn.isNotEmpty) _withdraw(withdrawn);
  }

  /// Whether events of [category] may be recorded now.
  bool allows(TelemetryCategory category) => switch (category) {
    TelemetryCategory.necessary => true,
    TelemetryCategory.analytics => _consent.analytics,
    TelemetryCategory.experiments => _consent.experiments,
  };

  /// The share of [name] events kept: 1 for necessary events, otherwise
  /// the lower of the app's and the host's rates, 1 when neither sets one.
  double rate(String name) {
    if (telemetryCategories[name] == TelemetryCategory.necessary) return 1;
    return math
        .min(appSampling[name] ?? 1, _host[name] ?? 1)
        .clamp(0, 1)
        .toDouble();
  }

  /// Records [name] with [fields]; returns whether it was kept. Unknown
  /// names, events without consent and events sampling leaves out are
  /// not recorded.
  bool record(
    String name, {
    Map<String, Object?> fields = const {},
    String route = '',
    String pluginKey = '',
  }) {
    final category = telemetryCategories[name];
    if (category == null || !allows(category)) return false;
    final r = rate(name);
    if (r < 1 && _random.nextDouble() >= r) return false;
    _post([
      TelemetryLine(
        category,
        eventJson(
          name: name,
          time: _clock(),
          fields: redactFields(fields),
          releaseSequence: releaseSequence,
          pluginKey: pluginKey,
          route: route,
        ),
      ).encode(),
    ]);
    return true;
  }

  /// Records an `error` event for a problem the runtime reported
  /// (ANL-001): its code, reason, node path and route, and a fingerprint
  /// that groups it without its message, which may hold user data.
  bool error(PluxException e) {
    final node = clipText(e.details['node'] ?? '');
    final route = clipText(e.details['route'] ?? '');
    return record(
      'error',
      route: route,
      pluginKey: e.details['plugin'] ?? '',
      fields: {
        'code': e.code.id,
        'reason': e.code.reason,
        if (node.isNotEmpty) 'node_path': node,
        'fingerprint': fingerprint(e.code.id, node, route),
      },
    );
  }

  /// The first 16 hex digits of SHA-256 over code, node path and route.
  static String fingerprint(String code, String node, String route) => sha256
      .convert(utf8.encode('$code\u0000$node\u0000$route'))
      .toString()
      .substring(0, 16);
}
