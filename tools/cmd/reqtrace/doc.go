// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Command reqtrace checks the specification and generates the requirements
// traceability report (QA-070, QA-071, QA-073).
//
// Usage:
//
//	reqtrace lint   [-spec docs/requirements.md]
//	reqtrace report [-spec docs/requirements.md] [-root .] [-md report.md] [-json report.json] [-strict]
//
// lint fails on any consistency problem in the specification. report maps
// every requirement to the tests and CI checks that verify it; with -strict it
// fails when a DONE MUST requirement has no evidence or when evidence cites an
// unknown or withdrawn requirement.
package main
