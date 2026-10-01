// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The runtime's typed failure: a registered error code from the shared
/// catalogue (ADR-0018) and a message for developers.
library;

import 'package:plux_flutter/src/errors/codes.g.dart';

export 'package:plux_flutter/src/errors/codes.g.dart';

/// A failure with a registered code. Messages never contain sensitive
/// values (SCH-012); structured context goes in [details].
final class PluxException implements Exception {
  /// Creates the failure.
  const PluxException(this.code, this.message, {this.details = const {}});

  /// The registered code.
  final PluxErrorCode code;

  /// What happened, for developers.
  final String message;

  /// Structured context, for example the section kind or the limit key.
  final Map<String, String> details;

  @override
  String toString() => '${code.id} ${code.reason}: $message';
}
