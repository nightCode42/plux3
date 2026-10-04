// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The typed errors of action steps (ACT-020, ADR-0039): the kind a step's
/// `onError` reads, the registered code a run reports when nothing handles
/// it, and a message for developers that never holds a payload.
library;

import 'package:plux_flutter/src/errors/plux_exception.dart';

/// The kinds of action error, as the bundle's `ErrorKind` and PXL's
/// `PluxErrorKind` name them.
enum ActionErrorKind {
  /// The network failed.
  network,

  /// A request was answered with an error status.
  http,

  /// A step or run took too long.
  timeout,

  /// An input, output or result does not have its declared type.
  validation,

  /// A function failed.
  function,

  /// A permission was refused.
  permission,

  /// The run was cancelled.
  cancelled,

  /// Anything else: an action this runtime does not run, a refused
  /// navigation, a custom error of `stop`.
  custom,
}

/// A step's failure.
final class ActionError implements Exception {
  /// Creates the error.
  const ActionError(this.kind, this.code, this.message);

  /// A `validation` error: a value does not have its declared type.
  const ActionError.validation(String message)
    : this(
        ActionErrorKind.validation,
        PluxErrorCode.actionValueInvalid,
        message,
      );

  /// The kind `onError` reads.
  final ActionErrorKind kind;

  /// The registered code a run reports when nothing handles the error.
  final PluxErrorCode code;

  /// What went wrong, for developers.
  final String message;

  /// The value of `steps.<id>.error`, a `PluxActionError`.
  Map<String, Object?> toPxl() => {'kind': kind.name, 'message': message};

  /// The error as the runtime reports it, with structured [details].
  PluxException toException(Map<String, String> details) =>
      PluxException(code, message, details: details);

  @override
  String toString() => '${code.id} ${kind.name}: $message';
}
