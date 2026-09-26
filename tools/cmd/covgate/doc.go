// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Command covgate fails when measured coverage is below the floors in
// coverage.json (QA-001).
//
// Usage:
//
//	covgate -kind go     [-config coverage.json] [-root .] backend/coverage.out tools/coverage.out
//	covgate -kind dart   [-config coverage.json] [-root .] packages/plux_flutter/coverage/lcov.info
//	covgate -kind studio [-config coverage.json] [-root .] studio/coverage/lcov.info
//
// Go inputs are cover profiles; Dart and Studio inputs are LCOV files. The
// result is printed as a Markdown table, suitable for a CI job summary.
package main
