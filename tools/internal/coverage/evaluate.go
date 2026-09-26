// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package coverage

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// Result is the outcome of checking one scope against its floor.
type Result struct {
	// Scope is "total" or a directory prefix.
	Scope string
	// Floor is the required percentage.
	Floor float64
	// Percent is the measured percentage; meaningless when Present is false.
	Percent float64
	// Present is false when no measured file falls under the scope yet.
	Present bool
}

// Passed reports whether the scope meets its floor. Scopes that are not
// present yet pass, so floors can be declared before their packages exist.
func (r Result) Passed() bool { return !r.Present || r.Percent+1e-9 >= r.Floor }

// Evaluate checks the files of one toolchain against its rule.
func Evaluate(rule Rule, files []FileCoverage) []Result {
	results := []Result{summarise("total", rule.Floor, files, "")}
	prefixes := make([]string, 0, len(rule.Packages))
	for prefix := range rule.Packages {
		prefixes = append(prefixes, prefix)
	}
	sort.Strings(prefixes)
	for _, prefix := range prefixes {
		results = append(results, summarise(prefix, rule.Packages[prefix], files, strings.TrimSuffix(prefix, "/")+"/"))
	}
	return results
}

// summarise computes coverage for the files under prefix ("" means all).
func summarise(scope string, floor float64, files []FileCoverage, prefix string) Result {
	var total, covered int
	for _, f := range files {
		if strings.HasPrefix(f.Path, prefix) {
			total += f.Total
			covered += f.Covered
		}
	}
	r := Result{Scope: scope, Floor: floor, Present: total > 0}
	if total > 0 {
		r.Percent = 100 * float64(covered) / float64(total)
	}
	return r
}

// WriteTable writes the results as a Markdown table and reports whether all
// of them passed.
func WriteTable(w io.Writer, kind string, results []Result) (bool, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "| %s scope | Coverage | Floor | Result |\n|---|---|---|---|\n", kind)
	ok := true
	for _, r := range results {
		switch {
		case !r.Present:
			fmt.Fprintf(&b, "| `%s` | — | %.0f%% | not present yet |\n", r.Scope, r.Floor)
		case r.Passed():
			fmt.Fprintf(&b, "| `%s` | %.1f%% | %.0f%% | pass |\n", r.Scope, r.Percent, r.Floor)
		default:
			ok = false
			fmt.Fprintf(&b, "| `%s` | %.1f%% | %.0f%% | **FAIL** |\n", r.Scope, r.Percent, r.Floor)
		}
	}
	if _, err := io.WriteString(w, b.String()); err != nil {
		return false, fmt.Errorf("coverage.WriteTable: %w", err)
	}
	return ok, nil
}
