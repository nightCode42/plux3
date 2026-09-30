// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Package perf reads the results of the runtime benchmark
// (test/bench/runtime) and decides whether a change regressed it
// (QA-007).
//
// A benchmark run is one process: it prints samples per metric, every
// metric lower-is-better. Timings on shared CI runners vary between
// machines by more than the 10% the gate allows, so a change is never
// compared with committed numbers; the base commit's runtime and the
// change's run alternately on the same machine, and [Compare] asks
// whether the change is slower by more than 10% with a one-sided
// Mann–Whitney U test on the runs' medians. Runs, not frames, are the
// independent observations: the frames of one run share its machine's
// state, so pooling them would overstate the evidence.
package perf
