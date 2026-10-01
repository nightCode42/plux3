// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Identifies the Plux runtime package in logs and telemetry.
abstract final class PluxRuntimeInfo {
  /// The semantic version of this package.
  ///
  /// It always equals the `version` in `pubspec.yaml`; a test enforces this,
  /// so every release is identifiable at run time (CI-008).
  static const String version = '0.1.0';

  /// The package name, as published.
  static const String packageName = 'plux_flutter';

  /// A single line identifying the runtime, for example `plux_flutter/0.1.0`.
  static String get userAgent => '$packageName/$version';
}
