// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package perf

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
)

// Benchmark is the name a runtime benchmark result carries.
const Benchmark = "plux-runtime"

// Format is the result format this package reads.
const Format = 1

// Run is the result of one benchmark process.
type Run struct {
	Benchmark string               `json:"benchmark"`
	Format    int                  `json:"format"`
	Runtime   string               `json:"runtime"`
	Platform  string               `json:"platform"`
	Plugins   int                  `json:"plugins"`
	Samples   map[string][]float64 `json:"samples"`
	Problems  []string             `json:"problems"`
}

// ParseRun reads one result, as the benchmark writes it to a file.
func ParseRun(data []byte) (Run, error) {
	var r Run
	if err := json.Unmarshal(data, &r); err != nil {
		return Run{}, fmt.Errorf("perf.ParseRun: %w", err)
	}
	if r.Benchmark != Benchmark || r.Format != Format {
		return Run{}, fmt.Errorf("perf.ParseRun: not a %s result of format %d", Benchmark, Format)
	}
	for name, xs := range r.Samples {
		if len(xs) == 0 {
			return Run{}, fmt.Errorf("perf.ParseRun: metric %s has no samples", name)
		}
	}
	return r, nil
}

// logPrefix starts every line of a result printed to a device log.
const logPrefix = "PLUX_BENCH "

// ParseLog reads the results printed to a device log (logcat, the Xcode
// console, `flutter run`): each is split over `PLUX_BENCH <n>/<count>
// <part>` lines, possibly with a log tag before the prefix. Other lines
// are ignored.
func ParseLog(r io.Reader) ([]Run, error) {
	var runs []Run
	var parts []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		i := strings.Index(line, logPrefix)
		if i < 0 {
			continue
		}
		head, part, ok := strings.Cut(line[i+len(logPrefix):], " ")
		n, count, ok2 := parsePosition(head)
		if !ok || !ok2 || n != len(parts)+1 {
			return nil, fmt.Errorf("perf.ParseLog: part %q out of order after %d parts", head, len(parts))
		}
		parts = append(parts, part)
		if n < count {
			continue
		}
		run, err := ParseRun([]byte(strings.Join(parts, "")))
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
		parts = nil
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("perf.ParseLog: %w", err)
	}
	if len(parts) > 0 {
		return nil, errors.New("perf.ParseLog: the log ends inside a result")
	}
	return runs, nil
}

// parsePosition reads "<n>/<count>" with 1 <= n <= count.
func parsePosition(s string) (n, count int, ok bool) {
	a, b, found := strings.Cut(s, "/")
	if !found {
		return 0, 0, false
	}
	n, err1 := strconv.Atoi(a)
	count, err2 := strconv.Atoi(b)
	if err1 != nil || err2 != nil || n < 1 || n > count {
		return 0, 0, false
	}
	return n, count, true
}

// Percentile is the p-th percentile (0 to 100) of xs, interpolated
// linearly between the closest ranks; NaN for no samples.
func Percentile(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	pos := p / 100 * float64(len(s)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	return s[lo] + (s[hi]-s[lo])*(pos-float64(lo))
}

// MannWhitneyGreater is the p-value of a one-sided Mann–Whitney U test
// that samples x tend to be greater than samples y: small when x is
// stochastically greater. It uses the normal approximation with a
// correction for ties and for continuity; with no variation at all it
// returns 1.
func MannWhitneyGreater(x, y []float64) float64 {
	nx, ny := float64(len(x)), float64(len(y))
	if nx == 0 || ny == 0 {
		return 1
	}
	type obs struct {
		v   float64
		inX bool
	}
	all := make([]obs, 0, len(x)+len(y))
	for _, v := range x {
		all = append(all, obs{v, true})
	}
	for _, v := range y {
		all = append(all, obs{v, false})
	}
	slices.SortFunc(all, func(a, b obs) int {
		switch {
		case a.v < b.v:
			return -1
		case a.v > b.v:
			return 1
		}
		return 0
	})
	var rankX, ties float64
	for i := 0; i < len(all); {
		j := i
		for j < len(all) && all[j].v == all[i].v {
			j++
		}
		rank := float64(i+j+1) / 2 // the mean of ranks i+1 … j
		for k := i; k < j; k++ {
			if all[k].inX {
				rankX += rank
			}
		}
		t := float64(j - i)
		ties += t*t*t - t
		i = j
	}
	n := nx + ny
	u := rankX - nx*(nx+1)/2
	mean := nx * ny / 2
	variance := nx * ny / 12 * ((n + 1) - ties/(n*(n-1)))
	if variance <= 0 {
		return 1
	}
	z := (u - mean - 0.5) / math.Sqrt(variance)
	return 0.5 * math.Erfc(z/math.Sqrt2)
}

// Gate is the regression rule of QA-007.
type Gate struct {
	// Threshold is the allowed slowdown, as a fraction (0.10 for 10%).
	Threshold float64
	// Alpha is the significance level of the test.
	Alpha float64
	// MinRuns is how many runs each side needs for a decision.
	MinRuns int
}

// DefaultGate fails a change that is more than 10% slower with 99%
// confidence, from at least five runs a side.
func DefaultGate() Gate { return Gate{Threshold: 0.10, Alpha: 0.01, MinRuns: 5} }

// Verdict is the comparison of one metric.
type Verdict struct {
	Metric string
	// Base and Head are the medians of the runs' medians.
	Base, Head float64
	// P is the test's p-value that Head exceeds Base by more than the
	// threshold.
	P float64
	// Regressed is true when Head/Base exceeds 1 + threshold and P is
	// below alpha.
	Regressed bool
}

// Ratio is Head/Base; 1 when both are 0.
func (v Verdict) Ratio() float64 {
	if v.Base == 0 {
		if v.Head == 0 {
			return 1
		}
		return math.Inf(1)
	}
	return v.Head / v.Base
}

// Compare decides, metric by metric, whether head regressed from base.
// Every metric of base must be in every run of both sides.
func (g Gate) Compare(base, head []Run) ([]Verdict, error) {
	if len(base) < g.MinRuns || len(head) < g.MinRuns {
		return nil, fmt.Errorf("perf.Compare: %d base and %d head runs; each side needs %d", len(base), len(head), g.MinRuns)
	}
	metrics := Metrics(base)
	out := make([]Verdict, 0, len(metrics))
	for _, m := range metrics {
		b, err := runMedians(base, m)
		if err != nil {
			return nil, err
		}
		h, err := runMedians(head, m)
		if err != nil {
			return nil, err
		}
		scaled := make([]float64, len(b))
		for i, v := range b {
			scaled[i] = v * (1 + g.Threshold)
		}
		v := Verdict{Metric: m, Base: Percentile(b, 50), Head: Percentile(h, 50), P: MannWhitneyGreater(h, scaled)}
		v.Regressed = v.Ratio() > 1+g.Threshold && v.P < g.Alpha
		out = append(out, v)
	}
	return out, nil
}

// runMedians is each run's median of metric m.
func runMedians(runs []Run, m string) ([]float64, error) {
	out := make([]float64, len(runs))
	for i, r := range runs {
		xs := r.Samples[m]
		if len(xs) == 0 {
			return nil, fmt.Errorf("perf.Compare: run %d has no samples of %s", i+1, m)
		}
		out[i] = Percentile(xs, 50)
	}
	return out, nil
}

// Metrics lists the metrics of runs, sorted.
func Metrics(runs []Run) []string {
	var out []string
	for _, r := range runs {
		for m := range r.Samples {
			if !slices.Contains(out, m) {
				out = append(out, m)
			}
		}
	}
	slices.Sort(out)
	return out
}

// Summary is the distribution of one metric over all samples of a set of
// runs.
type Summary struct {
	Metric             string
	Count              int
	P50, P95, Max, Min float64
}

// Summarise pools each metric's samples over runs.
func Summarise(runs []Run) []Summary {
	metrics := Metrics(runs)
	out := make([]Summary, 0, len(metrics))
	for _, m := range metrics {
		var xs []float64
		for _, r := range runs {
			xs = append(xs, r.Samples[m]...)
		}
		out = append(out, Summary{
			Metric: m, Count: len(xs),
			P50: Percentile(xs, 50), P95: Percentile(xs, 95),
			Min: slices.Min(xs), Max: slices.Max(xs),
		})
	}
	return out
}

// WriteSummary writes summaries as a Markdown table.
func WriteSummary(w io.Writer, sums []Summary) error {
	var b strings.Builder
	b.WriteString("| Metric | Samples | Min | p50 | p95 | Max |\n|---|---:|---:|---:|---:|---:|\n")
	for _, s := range sums {
		fmt.Fprintf(&b, "| `%s` | %d | %.2f | %.2f | %.2f | %.2f |\n", s.Metric, s.Count, s.Min, s.P50, s.P95, s.Max)
	}
	_, err := io.WriteString(w, b.String())
	if err != nil {
		return fmt.Errorf("perf.WriteSummary: %w", err)
	}
	return nil
}

// WriteVerdicts writes verdicts as a Markdown table and reports whether
// none regressed.
func (g Gate) WriteVerdicts(w io.Writer, vs []Verdict) (bool, error) {
	var b strings.Builder
	passed := true
	fmt.Fprintf(&b, "| Metric | Base (median) | Head (median) | Change | p (head > base × %.2f) | Result |\n|---|---:|---:|---:|---:|---|\n", 1+g.Threshold)
	for _, v := range vs {
		result := "pass"
		if v.Regressed {
			result = "**regressed**"
			passed = false
		}
		fmt.Fprintf(&b, "| `%s` | %.3f | %.3f | %+.1f%% | %.4f | %s |\n", v.Metric, v.Base, v.Head, 100*(v.Ratio()-1), v.P, result)
	}
	if _, err := io.WriteString(w, b.String()); err != nil {
		return false, fmt.Errorf("perf.WriteVerdicts: %w", err)
	}
	return passed, nil
}
