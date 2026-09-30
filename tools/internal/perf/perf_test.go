// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package perf

import (
	"bytes"
	"fmt"
	"math"
	"strings"
	"testing"
)

func result(samples map[string][]float64) Run {
	return Run{Benchmark: Benchmark, Format: Format, Runtime: "0.1.0", Platform: "linux", Plugins: 50, Samples: samples}
}

func TestParseRun(t *testing.T) {
	t.Parallel()
	good := `{"benchmark":"plux-runtime","format":1,"runtime":"0.1.0","platform":"linux","plugins":50,"samples":{"a_ms":[1,2]},"problems":[]}`
	r, err := ParseRun([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	if r.Plugins != 50 || len(r.Samples["a_ms"]) != 2 {
		t.Fatalf("got %+v", r)
	}
	for name, bad := range map[string]string{
		"not JSON":      `{`,
		"other":         `{"benchmark":"other","format":1}`,
		"newer format":  `{"benchmark":"plux-runtime","format":2}`,
		"empty samples": `{"benchmark":"plux-runtime","format":1,"samples":{"a_ms":[]}}`,
	} {
		if _, err := ParseRun([]byte(bad)); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
}

func TestParseLogJoinsParts(t *testing.T) {
	t.Parallel()
	json := `{"benchmark":"plux-runtime","format":1,"runtime":"0.1.0","platform":"android","plugins":50,"samples":{"a_ms":[1.5]}}`
	cut := len(json) / 2
	log := strings.Join([]string{
		"I/flutter ( 123): starting",
		"I/flutter ( 123): PLUX_BENCH 1/2 " + json[:cut],
		"D/other: noise",
		"I/flutter ( 123): PLUX_BENCH 2/2 " + json[cut:],
		"PLUX_BENCH 1/1 " + json,
	}, "\n")
	runs, err := ParseLog(strings.NewReader(log))
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[0].Platform != "android" || runs[1].Samples["a_ms"][0] != 1.5 {
		t.Fatalf("got %+v", runs)
	}
	for name, bad := range map[string]string{
		"missing first part": "PLUX_BENCH 2/2 x",
		"unfinished":         "PLUX_BENCH 1/2 {",
		"bad position":       "PLUX_BENCH one/2 x",
		"part beyond count":  "PLUX_BENCH 3/2 x",
		"no space":           "PLUX_BENCH 1/1",
		"damaged JSON":       "PLUX_BENCH 1/1 {",
	} {
		if _, err := ParseLog(strings.NewReader(bad)); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
}

func TestPercentile(t *testing.T) {
	t.Parallel()
	xs := []float64{5, 1, 4, 2, 3}
	for _, c := range []struct{ p, want float64 }{{0, 1}, {50, 3}, {100, 5}, {25, 2}, {95, 4.8}} {
		if got := Percentile(xs, c.p); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("p%v = %v, want %v", c.p, got, c.want)
		}
	}
	if !math.IsNaN(Percentile(nil, 50)) {
		t.Error("no samples must give NaN")
	}
	if xs[0] != 5 {
		t.Error("Percentile sorted its input")
	}
}

// TestMannWhitneyGreater checks the test against values computed by hand
// and its behaviour with ties and without variation.
func TestMannWhitneyGreater(t *testing.T) {
	t.Parallel()
	low := []float64{1, 2, 3, 4, 5}
	high := []float64{6, 7, 8, 9, 10}
	// Complete separation: U = 25, mean 12.5, variance 22.917;
	// z = (25 - 12.5 - 0.5) / 4.787 = 2.507, p = 0.0061.
	if p := MannWhitneyGreater(high, low); math.Abs(p-0.00609) > 1e-4 {
		t.Errorf("separated: p = %v", p)
	}
	if p := MannWhitneyGreater(low, high); p < 0.99 {
		t.Errorf("reversed: p = %v", p)
	}
	if p := MannWhitneyGreater(low, low); p < 0.4 {
		t.Errorf("identical: p = %v", p)
	}
	if p := MannWhitneyGreater([]float64{0, 0, 0}, []float64{0, 0}); p != 1 {
		t.Errorf("no variation: p = %v", p)
	}
	if p := MannWhitneyGreater(nil, low); p != 1 {
		t.Errorf("no samples: p = %v", p)
	}
	// Ties between the samples lower the variance, not the direction.
	if p := MannWhitneyGreater([]float64{2, 3, 3, 4, 5, 6}, []float64{1, 1, 2, 2, 3, 3}); p > 0.05 {
		t.Errorf("tied: p = %v", p)
	}
}

// runs makes n runs whose metric "m" has the run's value repeated, from
// values scaled by factor.
func runs(factor float64, values ...float64) []Run {
	out := make([]Run, 0, len(values))
	for _, v := range values {
		out = append(out, result(map[string][]float64{"m_ms": {v * factor, v * factor}, "n_ms": {1}}))
	}
	return out
}

// TestCompareGatesRegressionsBeyondTenPercent checks that the gate fails
// a consistent slowdown beyond 10%, and passes noise, a slowdown within
// 10%, an improvement and a slowdown beyond 10% that is not significant.
// Verifies: QA-007.
func TestCompareGatesRegressionsBeyondTenPercent_QA_007(t *testing.T) {
	t.Parallel()
	noise := []float64{10, 10.4, 9.8, 10.1, 9.7, 10.3, 9.9, 10.2}
	g := DefaultGate()
	for _, c := range []struct {
		name string
		head []Run
		want bool
	}{
		{"same", runs(1, noise...), false},
		{"5% slower", runs(1.05, noise...), false},
		{"faster", runs(0.5, noise...), false},
		{"25% slower", runs(1.25, noise...), true},
		{"one slow run", runs(1, append([]float64{30}, noise[1:]...)...), false},
	} {
		vs, err := g.Compare(runs(1, noise...), c.head)
		if err != nil {
			t.Fatal(err)
		}
		if len(vs) != 2 || vs[0].Metric != "m_ms" || vs[1].Metric != "n_ms" {
			t.Fatalf("%s: verdicts %+v", c.name, vs)
		}
		if vs[0].Regressed != c.want || vs[1].Regressed {
			t.Errorf("%s: regressed = %v (%+v), want %v", c.name, vs[0].Regressed, vs[0], c.want)
		}
		var b bytes.Buffer
		passed, err := g.WriteVerdicts(&b, vs)
		if err != nil || passed == c.want {
			t.Errorf("%s: passed = %v, %v", c.name, passed, err)
		}
		if c.want && !strings.Contains(b.String(), "**regressed**") {
			t.Errorf("%s: table does not say so:\n%s", c.name, b.String())
		}
	}
}

func TestCompareNeedsRunsAndMetrics(t *testing.T) {
	t.Parallel()
	g := DefaultGate()
	if _, err := g.Compare(runs(1, 1, 2, 3), runs(1, 1, 2, 3, 4, 5)); err == nil {
		t.Error("compared three runs")
	}
	head := runs(1, 1, 2, 3, 4, 5)
	delete(head[2].Samples, "n_ms")
	if _, err := g.Compare(runs(1, 1, 2, 3, 4, 5), head); err == nil {
		t.Error("compared a run without a metric")
	}
}

func TestVerdictRatio(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ base, head, want float64 }{{0, 0, 1}, {2, 3, 1.5}, {0, 1, math.Inf(1)}} {
		if got := (Verdict{Base: c.base, Head: c.head}).Ratio(); got != c.want {
			t.Errorf("%v/%v = %v", c.head, c.base, got)
		}
	}
}

func TestSummarise(t *testing.T) {
	t.Parallel()
	sums := Summarise([]Run{
		result(map[string][]float64{"a_ms": {1, 2, 3}}),
		result(map[string][]float64{"a_ms": {4, 5}, "b_mib": {7}}),
	})
	want := []Summary{
		{Metric: "a_ms", Count: 5, P50: 3, P95: 4.8, Min: 1, Max: 5},
		{Metric: "b_mib", Count: 1, P50: 7, P95: 7, Min: 7, Max: 7},
	}
	if fmt.Sprint(sums) != fmt.Sprint(want) {
		t.Fatalf("got %+v", sums)
	}
	var b bytes.Buffer
	if err := WriteSummary(&b, sums); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "| `a_ms` | 5 | 1.00 | 3.00 | 4.80 | 5.00 |") {
		t.Errorf("table:\n%s", b.String())
	}
}
