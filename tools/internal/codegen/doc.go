// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Package codegen generates Go, Dart and TypeScript code and reference
// documentation from the language-neutral contracts in schema/ (SCH-001,
// WGT-002, LIM-001, ADR-0025).
//
// Generators are pure functions from parsed sources to File values; output
// is deterministic and formatted, and every file carries the licence header
// and the generated-code marker. Only the standard library is used (tools
// policy).
package codegen
