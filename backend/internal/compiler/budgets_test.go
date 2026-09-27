// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"os"
	"slices"
	"testing"
	"time"
)

// budgetsEnv enables the timing budgets. Timings depend on the machine, so
// they are checked by `make go-budgets` on the CI reference runner rather
// than by every test run.
const budgetsEnv = "PLUX_BUDGETS"

// percentile returns the p-th percentile of durations (nearest rank).
func percentile(ds []time.Duration, p int) time.Duration {
	s := slices.Clone(ds)
	slices.Sort(s)
	i := (len(s)*p+99)/100 - 1
	return s[max(i, 0)]
}

// Verifies: CMP-050, SCH-042, NFR-033.
// A 50-page plugin of 100-node pages compiles within 1 s (median of five
// runs) and one of its pages validates within 50 ms at the 95th
// percentile.
func TestPerformanceBudgets(t *testing.T) {
	if os.Getenv(budgetsEnv) == "" {
		t.Skip("timing budgets run on the CI reference runner: make go-budgets")
	}
	m := manyPages(t, 100)
	opts := DefaultOptions()
	var compiles []time.Duration
	for range 6 {
		start := time.Now()
		res := Compile(m, opts)
		compiles = append(compiles, time.Since(start))
		if res.App == nil {
			t.Fatalf("diagnostics:\n%s", list(res.Diagnostics))
		}
	}
	compiles = compiles[1:] // the first run warms the caches
	v := newValidator(t, m)
	name := "plugins/loans/pages/calculator-copy0.page.json"
	data := m[name].Data
	var validations []time.Duration
	for range 200 {
		start := time.Now()
		diags := v.ValidatePage(name, data)
		validations = append(validations, time.Since(start))
		if len(diags) > 0 {
			t.Fatalf("diagnostics:\n%s", list(diags))
		}
	}
	compile, validate := percentile(compiles, 50), percentile(validations[20:], 95)
	t.Logf("compile 50 pages: median %v; validate one page: p50 %v, p95 %v", compile, percentile(validations[20:], 50), validate)
	if compile > time.Second {
		t.Errorf("compiling 50 pages takes %v, above the 1 s budget (CMP-050)", compile)
	}
	if validate > 50*time.Millisecond {
		t.Errorf("validating a page takes %v at p95, above the 50 ms budget (SCH-042)", validate)
	}
}

func TestPercentile(t *testing.T) {
	t.Parallel()
	ds := []time.Duration{5, 1, 4, 2, 3}
	for p, want := range map[int]time.Duration{0: 1, 20: 1, 50: 3, 95: 5, 100: 5} {
		if got := percentile(ds, p); got != want {
			t.Errorf("p%d = %v, want %v", p, got, want)
		}
	}
}
