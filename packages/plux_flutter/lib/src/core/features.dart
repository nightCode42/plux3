// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The features this runtime supports, for `required_features` (BND-008):
/// `pxl.v1`, and registry revisions — `widget.<Type>.v<n>`,
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
  static const Set<String> pxl = {'pxl.v1'};

  /// Whether [feature] is supported.
  bool supports(String feature) {
    if (pxl.contains(feature)) return true;
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
