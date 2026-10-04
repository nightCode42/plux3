// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Package trace maps requirements to the automated evidence that verifies
// them, so the traceability report is generated from code rather than kept by
// hand (QA-070, QA-071).
//
// Evidence is found in test files (Go, Dart, TypeScript) and in CI definitions
// (workflows, the Makefile and its fragments in mk/) in three forms:
//
//   - a comment line starting with "Verifies:" and listing identifiers:
//     `// Verifies: SYN-005, QA-070.` (Go, Dart, TypeScript) or
//     `# Verifies: CI-006.` (workflows, Makefile, mk/*.mk)
//   - a Go test, fuzz or benchmark name ending in the identifier with
//     underscores: `TestActivationIsAtomic_SYN_005`
//   - an identifier in square brackets in a Dart or TypeScript test file:
//     `testWidgets('keeps last known good [SYN-006]', ...)`
//
// Comments and declarations count only at the start of a line, so identifiers
// quoted inside string literals, such as test fixtures, are never evidence.
package trace
