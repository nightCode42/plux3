// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Package coverage enforces the coverage floors of QA-001 across toolchains.
// It reads Go cover profiles and LCOV files (produced by `flutter test` and
// `bun test`), groups coverage by directory, and compares it with the floors
// in the repository's coverage.json.
package coverage
