// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeApps pretends to be the benchmark app: each run writes a result
// whose "open_ms" is the side's value, and records the order of runs.
type fakeApps struct {
	ms        map[string]float64
	order     []string
	fail      string
	scenarios []string // PLUX_BENCH_SCENARIOS of each run
	empty     bool     // write results without samples
}

func (f *fakeApps) launch(_ context.Context, path string, env []string, log io.Writer) error {
	f.order = append(f.order, path)
	_, _ = fmt.Fprintln(log, "started", path)
	if path == f.fail {
		return errors.New("exit status 1")
	}
	vars := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		vars[k] = v
	}
	if _, err := os.Stat(vars["PLUX_BENCH_STORE"]); err != nil {
		return err
	}
	f.scenarios = append(f.scenarios, vars["PLUX_BENCH_SCENARIOS"])
	if f.empty {
		return os.WriteFile(vars["PLUX_BENCH_OUT"], []byte(`{"benchmark":"plux-runtime","format":1,"runtime":"0.1.0","platform":"linux","plugins":50,"samples":{}}`), 0o600)
	}
	ms := f.ms[path] * (1 + 0.01*float64(len(f.order)%3))
	body := fmt.Sprintf(`{"benchmark":"plux-runtime","format":1,"runtime":"0.1.0","platform":"linux","plugins":50,"samples":{"open_ms":[%v,%v]}}`, ms, ms)
	return os.WriteFile(vars["PLUX_BENCH_OUT"], []byte(body), 0o600)
}

// TestRunAlternatesAndGates checks that run starts each app once to warm
// up, then alternately, and fails a head more than 10% slower.
// Verifies: QA-007.
func TestRunAlternatesAndGates_QA_007(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		head float64
		want int
	}{{"same", 10, exitOK}, {"slower", 13, exitRegressed}} {
		out := t.TempDir()
		apps := &fakeApps{ms: map[string]float64{"base": 10, "head": c.head}}
		var stdout, stderr bytes.Buffer
		code := run(context.Background(), []string{"run", "-base", "base", "-head", "head", "-runs", "6", "-out", out}, &stdout, &stderr, apps.launch)
		if code != c.want {
			t.Fatalf("%s: exit %d, stderr:\n%s", c.name, code, stderr.String())
		}
		want := "base head base head head base base head head base base head head base"
		if got := strings.Join(apps.order, " "); got != want {
			t.Errorf("%s: order %s", c.name, got)
		}
		if !strings.Contains(stdout.String(), "| `open_ms` |") {
			t.Errorf("%s: report:\n%s", c.name, stdout.String())
		}
		for _, f := range []string{"base/warmup.json", "head/6.json", "head/6.log", "report.md"} {
			if _, err := os.Stat(filepath.Join(out, f)); err != nil {
				t.Errorf("%s: %v", c.name, err)
			}
		}
		// compare reads the same results again.
		stdout.Reset()
		if code := run(context.Background(), []string{"compare", "-base", filepath.Join(out, "base"), "-head", filepath.Join(out, "head")}, &stdout, &stderr, nil); code != c.want {
			t.Errorf("%s: compare exit %d", c.name, code)
		}
	}
}

func TestMeasureSummarises(t *testing.T) {
	t.Parallel()
	out := t.TempDir()
	apps := &fakeApps{ms: map[string]float64{"app": 10}}
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"measure", "-app", "app", "-runs", "3", "-out", out}, &stdout, &stderr, apps.launch); code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if len(apps.order) != 4 || !strings.Contains(stdout.String(), "3 runs of runtime 0.1.0 on linux, 50 plugins.") {
		t.Errorf("runs %v, report:\n%s", apps.order, stdout.String())
	}
	apps.fail = "app"
	for _, args := range [][]string{{"measure"}, {"measure", "-app", "app", "-runs", "0"}, {"measure", "-app", "app", "-out", out}} {
		if code := run(context.Background(), args, &stdout, &stderr, apps.launch); code != exitError {
			t.Errorf("%q: exit %d", args, code)
		}
	}
}

func TestRunFailures(t *testing.T) {
	t.Parallel()
	out := t.TempDir()
	apps := &fakeApps{ms: map[string]float64{"base": 10, "head": 10}, fail: "head"}
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"run", "-base", "base", "-head", "head", "-out", out}, &stdout, &stderr, apps.launch); code != exitError {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stderr.String(), "head/warmup.log") {
		t.Errorf("stderr does not name the log:\n%s", stderr.String())
	}
	for _, args := range [][]string{
		nil,
		{"nothing"},
		{"run"},
		{"run", "-base", "a"},
		{"run", "-base", "a", "-head", "b", "-runs", "0"},
		{"compare"},
		{"compare", "-base", out, "-head", filepath.Join(out, "missing")},
		{"report"},
		{"report", filepath.Join(out, "missing.json")},
		{"report", filepath.Join(out, "missing.log")},
	} {
		if code := run(context.Background(), args, &stdout, &stderr, apps.launch); code != exitError {
			t.Errorf("%q: exit %d", args, code)
		}
	}
	// Too few runs to decide.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "1.json"), []byte(`{"benchmark":"plux-runtime","format":1,"samples":{"a":[1]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := run(context.Background(), []string{"compare", "-base", dir, "-head", dir}, &stdout, &stderr, nil); code != exitError {
		t.Errorf("one run compared: exit %d", code)
	}
}

func TestReportReadsFilesAndLogs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	res := `{"benchmark":"plux-runtime","format":1,"runtime":"0.1.0","platform":"android","plugins":50,"samples":{"open_ms":[4,6]}}`
	file := filepath.Join(dir, "1.json")
	log := filepath.Join(dir, "device.log")
	if err := os.WriteFile(file, []byte(res), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(log, []byte("I/flutter: PLUX_BENCH 1/1 "+res+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"report", file, log}, &stdout, &stderr, nil); code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "2 runs of runtime 0.1.0 on android") ||
		!strings.Contains(stdout.String(), "| `open_ms` | 4 | 4.00 | 5.00 |") {
		t.Errorf("report:\n%s", stdout.String())
	}
	empty := filepath.Join(dir, "empty.log")
	if err := os.WriteFile(empty, []byte("nothing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := run(context.Background(), []string{"report", empty}, &stdout, &stderr, nil); code != exitError {
		t.Errorf("empty log: exit %d", code)
	}
	bad := filepath.Join(dir, "bad.log")
	if err := os.WriteFile(bad, []byte("PLUX_BENCH 1/2 {\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := run(context.Background(), []string{"report", bad}, &stdout, &stderr, nil); code != exitError {
		t.Errorf("bad log: exit %d", code)
	}
}

// TestExecApp runs a real child process.
func TestExecApp(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the benchmark runs on Linux")
	}
	var log bytes.Buffer
	if err := execApp(context.Background(), "/bin/sh", nil, &log); err != nil {
		t.Fatal(err)
	}
	if err := execApp(context.Background(), filepath.Join(t.TempDir(), "missing"), nil, &log); err == nil {
		t.Error("ran a missing app")
	}
}

// TestRunPassesTheScenariosAndRefusesAnEmptyComparison checks that every
// run of a shard measures its parts, and that a comparison without a
// metric fails instead of passing.
// Verifies: QA-007.
func TestRunPassesTheScenariosAndRefusesAnEmptyComparison_QA_007(t *testing.T) {
	t.Parallel()
	apps := &fakeApps{ms: map[string]float64{"base": 10, "head": 10}}
	var stdout, stderr bytes.Buffer
	args := []string{"run", "-base", "base", "-head", "head", "-runs", "5", "-scenarios", "open,native", "-out", t.TempDir()}
	if code := run(context.Background(), args, &stdout, &stderr, apps.launch); code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	for i, sc := range apps.scenarios {
		if sc != "open,native" {
			t.Fatalf("run %d had PLUX_BENCH_SCENARIOS=%q", i, sc)
		}
	}
	measured := &fakeApps{ms: map[string]float64{"app": 10}}
	if code := run(context.Background(), []string{"measure", "-app", "app", "-runs", "1", "-out", t.TempDir()}, &stdout, &stderr, measured.launch); code != exitOK {
		t.Fatalf("measure: exit %d: %s", code, stderr.String())
	}
	if measured.scenarios[0] != "" {
		t.Errorf("an unlimited run had PLUX_BENCH_SCENARIOS=%q", measured.scenarios[0])
	}

	empty := &fakeApps{ms: map[string]float64{"base": 10, "head": 10}, empty: true}
	stderr.Reset()
	args = []string{"run", "-base", "base", "-head", "head", "-runs", "5", "-out", t.TempDir()}
	if code := run(context.Background(), args, &stdout, &stderr, empty.launch); code != exitError {
		t.Fatalf("an empty comparison: exit %d, want %d", code, exitError)
	}
	if !strings.Contains(stderr.String(), "no metric to compare") {
		t.Errorf("stderr = %q", stderr.String())
	}
}
