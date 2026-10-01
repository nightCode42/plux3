// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Tolerant reads of FlatBuffers enum fields.
///
/// flatc's Dart accessors throw a `StateError` for an enum value they do
/// not know. The verifier (ADR-0029) proves that a buffer is safe to read,
/// not that every enum value is one this runtime knows — a newer compiler
/// may add values (BND-000) — so the runtime reads enum fields only
/// through [readEnum], and treats an unknown value as an unsupported one.
library;

/// Returns what [read] returns, or null when the enum value it reads is
/// unknown to this runtime.
T? readEnum<T extends Object>(T Function() read) {
  try {
    return read();
  } on StateError {
    return null;
  }
}
