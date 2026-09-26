// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Package compiler turns a Plux project into bundles (spec §9, ADR-0002).
// It is a pure library used unchanged by the server, the CLI and tests
// (CMP-001, layering rule L-5): it reads an fs.FS, never the clock or the
// environment, and returns bytes and diagnostics, so the same project and
// compiler version give byte-identical bundles on every platform
// (CMP-002).
//
// The pipeline runs the stages of CMP-003 in order: the schema loader
// parses, migrates and validates each document structurally; then
// resolve, typecheck, semantic, policy, optimise, lower, encode, assets
// and hash, each a function over the compilation unit. The checking
// stages always run, so one compilation reports every problem it finds;
// the producing stages run only without errors, so no bundle is produced
// from an invalid project.
//
// Reference: docs/reference/compiler.md.
package compiler
