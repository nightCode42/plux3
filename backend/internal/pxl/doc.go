// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Package pxl implements PXL, the Plux expression language (spec §14.2,
// Appendix E, ADR-0009): lexer and parser with exact source ranges, a type
// checker against the environment of each use site (PXL-002), the standard
// library, constant folding (CMP-022), read sets (CMP-023), and a compact
// typed bytecode with its encoding and VM (PXL-003).
//
// Evaluation is pure, deterministic and total: every run stops within an
// operation budget with a value or a typed EvalError (PXL-001). Decimal and
// money arithmetic is exact with explicit rounding (PXL-005, package
// decimal). The Dart runtime runs the same bytecode; the conformance
// vectors in schema/testdata/pxl keep both implementations identical
// (PXL-007). The opcode, error-kind, standard-library and currency tables
// are generated from schema/pxl by schemagen.
package pxl
