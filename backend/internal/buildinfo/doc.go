// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Package buildinfo reports the version metadata stamped into Plux binaries
// at link time.
//
// The values are set with -ldflags "-X" by the Makefile and CI. They are
// derived from the commit being built, never from the build machine or the
// wall clock, so that two builds of the same commit are byte-identical
// (CI-006).
package buildinfo
