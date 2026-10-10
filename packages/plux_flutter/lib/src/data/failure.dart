// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The typed failures of the data layer (ADR-0048): the action error kind a
/// step's `onError` and `data.<name>.error` read, the registered code and a
/// message that never holds a payload, a URL's query or a header (SCH-012).
library;

import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';

/// A failed data request or load.
final class DataFailure implements Exception {
  /// Creates the failure.
  const DataFailure(this.kind, this.code, this.message, {this.status = 0});

  /// The source names no environment's base URL, or a kind this runtime
  /// does not load (PLX-5108).
  const DataFailure.unavailable(String message)
    : this(
        ActionErrorKind.custom,
        PluxErrorCode.dataSourceUnavailable,
        message,
      );

  /// The device's assurance level is below the one the source asks for
  /// (`PLX-6002`, SEC-007): nothing was requested.
  const DataFailure.assurance(String message)
    : this(
        ActionErrorKind.custom,
        PluxErrorCode.assuranceInsufficient,
        message,
      );

  /// A response did not map to its declared type (PLX-5104).
  const DataFailure.mapping(String message)
    : this(
        ActionErrorKind.validation,
        PluxErrorCode.dataMappingFailed,
        message,
      );

  /// The kind `onError` reads.
  final ActionErrorKind kind;

  /// The registered code.
  final PluxErrorCode code;

  /// What went wrong, for developers.
  final String message;

  /// The HTTP status, or 0.
  final int status;

  /// The failure as a step's error: an `http` failure keeps the status the
  /// server answered with, which pages read as `steps.<id>.error.status`.
  ActionError toActionError() =>
      ActionError(kind, code, message, status: status == 0 ? null : status);

  /// The failure as the runtime reports it.
  PluxException toException(Map<String, String> details) =>
      PluxException(code, message, details: details);

  @override
  String toString() => '${code.id} ${kind.name}: $message';
}
