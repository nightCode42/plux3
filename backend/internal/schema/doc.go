// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Package schema is the Plux document model (spec §7, ADR-0025).
//
// The model is defined by the JSON Schemas in schema/json, the single source
// of truth (SCH-001); model_gen.go holds the Go types generated from them.
// This package loads a project from the Git layout (SCH-006) through an
// fs.FS, parses each document strictly and canonicalises it (SCH-003),
// migrates it to the current schema version (SCH-000, SCH-043), validates it
// structurally, rejecting unknown properties other than `x-` extensions
// (SCH-004), and decodes it into the generated types. Every problem is
// reported as a diagnostic with a code and a JSON Pointer (SCH-040); the
// loader never stops at the first one.
//
// Documents are stored and hashed as canonical bytes, so extension
// properties are preserved even though the generated types ignore them.
//
// Semantic and policy validation — references, types, budgets — belong to
// the compiler (backend/internal/compiler). Nothing here performs I/O other
// than reading the injected file system (layering rule L-5), and a Validator
// is safe for concurrent use once built.
package schema
