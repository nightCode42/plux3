// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The features this runtime supports, for `required_features` (BND-008):
/// `pxl.v1`, `pxl.regex.v1` and `pxl.phone.v1` (0.3.0), `navigation.guards.v1`
/// (route guards, ADR-0040), `data.v1` (the data layer, ADR-0048), the state
/// engine's `state.write.v1`, `state.computed.v1` and `state.persistence.v1`,
/// forms' `forms.v1` (ADR-0047), and
/// registry revisions — `widget.<Type>.v<n>`,
/// `type.<Name>.v<n>`, `enum.<Name>.v<n>` — up to the revision this
/// runtime's generated registry knows, for widgets it can build.
library;

import 'package:plux_flutter/src/schema/registry.g.dart';

/// Answers whether this runtime supports a required feature.
final class RuntimeFeatures {
  /// Creates the answer for a runtime that builds the widgets [canBuild]
  /// accepts; by default, every widget of the registry.
  RuntimeFeatures({bool Function(String type)? canBuild})
    : _canBuild = canBuild ?? ((_) => true);

  final bool Function(String type) _canBuild;

  static final Map<String, int> _widgets = {
    for (final w in widgetDescriptors) w.type: w.revision,
  };
  static final Map<String, int> _types = {
    for (final t in valueTypeDescriptors) t.name: t.revision,
  };
  static final Map<String, int> _enums = {
    for (final e in enumDescriptors) e.name: e.revision,
  };

  /// The PXL features this runtime evaluates.
  static const Set<String> pxl = {'pxl.v1', 'pxl.regex.v1', 'pxl.phone.v1'};

  /// The navigation features this runtime honours: a bundle with a guarded
  /// page requires them, so a runtime without guards never opens it.
  static const Set<String> navigation = {'navigation.guards.v1'};

  /// The action engine features this runtime runs, first in 0.3.0:
  /// triggers, concurrency policies and detached runs, retries, flows and
  /// R1's control actions (ACT-002–ACT-006, ACT-061).
  static const Set<String> actions = {
    'actions.triggers.v1',
    'actions.concurrency.v1',
    'actions.retry.v1',
    'actions.flows.v1',
    'actions.control.v1',
  };

  /// The data layer (ADR-0048): a bundle declaring a REST or GraphQL
  /// source requires it.
  static const Set<String> data = {'data.v1'};

  /// The state engine's features (STA-*): writes, computed entries and
  /// session, persisted and secure entries.
  static const Set<String> state = {
    'state.write.v1',
    'state.computed.v1',
    'state.persistence.v1',
  };

  /// Forms (STA-020, ADR-0047): a bundle declaring a form or running a
  /// form action requires them.
  static const Set<String> forms = {'forms.v1'};

  /// Whether [feature] is supported.
  bool supports(String feature) {
    if (forms.contains(feature) ||
        pxl.contains(feature) ||
        navigation.contains(feature) ||
        actions.contains(feature) ||
        data.contains(feature) ||
        state.contains(feature)) {
      return true;
    }
    final m = RegExp(
      r'^(widget|type|enum)\.([A-Za-z][A-Za-z0-9]*)\.v([1-9]\d*)$',
    ).firstMatch(feature);
    if (m == null) return false;
    final revision = int.parse(m[3]!);
    final known = switch (m[1]) {
      'widget' => _widgets[m[2]],
      'type' => _types[m[2]],
      _ => _enums[m[2]],
    };
    if (known == null || revision > known) return false;
    return m[1] != 'widget' || _canBuild(m[2]!);
  }
}
