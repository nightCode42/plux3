// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Package affected decides which CI jobs a change must run (CI-002,
// ADR-0043).
//
// The rules live in ci/affected.json. Each job names its roots:
//
//   - path globs, anchored at the repository root, where "**" matches any
//     number of directories and a leading "!" excludes what an earlier
//     glob included;
//   - Go packages, whose in-repository imports are followed, with or
//     without their tests' imports, so a change to a library runs exactly
//     the jobs that build it;
//   - Dart packages of the pub workspace, whose workspace dependencies are
//     followed the same way.
//
// The graphs are read from the source with the standard library: import
// declarations and //go:embed directives of every Go file, ignoring build
// constraints (a superset is the safe side), and the dependency names of
// every workspace pubspec. No toolchain runs and nothing is downloaded.
//
// Three kinds of change run more than their roots say. A file in the
// rules' "everything" list (the root Makefile, the rules, this tool) runs
// every job. A change to .github/workflows/ci.yml runs the jobs whose
// block changed, and every job when a line outside the job blocks (the
// triggers, env, defaults) changed. A file that no rule names and that is
// not in the "noJob" list (documentation, local scripts) runs every job,
// and the reason says which file. When in doubt, run.
//
// The selection is deterministic: the same change gives the same jobs and
// the same reasons, sorted.
package affected
