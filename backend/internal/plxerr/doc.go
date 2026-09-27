// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Package plxerr is the unified error model of Plux (ADR-0018).
//
// Every error and diagnostic carries a stable code (PLX-NNNN, spec Appendix
// F) and a reason (UPPER_SNAKE_CASE) registered once in this package, together
// with its default severity, cause and suggested fix (DX-003). Programs branch
// on codes and reasons, never on messages.
//
// Two shapes share the registry:
//   - Diagnostic is a finding about a document, located by file, JSON Pointer
//     and, for PXL, a code-point range, optionally with a machine-applicable
//     JSON Patch fix (CMP-004, SCH-040).
//   - Error is a Go error for failures that are not document findings.
//
// The published catalogue, docs/reference/errors.md, and its machine-readable
// form, schema/errors.json, are generated from the registry by go generate; a
// test fails when either is stale.
//
// Messages never contain secrets or values of fields tagged sensitive
// (SEC-092); they name paths and types instead.
//
// Everything in this package is immutable after construction and safe for
// concurrent use.
package plxerr

//go:generate go run ./internal/gencatalogue -root ../../..
