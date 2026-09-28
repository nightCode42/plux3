// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Command plux is the Plux command-line interface for developers and CI
// (spec §22.1). It validates and builds projects offline (CLI-005), and
// signs in to a Plux Server to publish, release, pull baselines and move
// drafts in and out (CLI-002–CLI-004); every command has --json output
// and never prompts (CLI-007). Further commands arrive with the phases
// that need them (CLI-003). The token lives in the OS credential store
// (ADR-0028).
package main
