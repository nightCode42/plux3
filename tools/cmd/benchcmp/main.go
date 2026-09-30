// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Command benchcmp runs the runtime benchmark (test/bench/runtime) and
// gates regressions (QA-007).
//
//	benchcmp measure -app <app> [-runs 5] [-out dir]
//	benchcmp run -base <app> -head <app> [-runs 10] [-out dir]
//	benchcmp compare -base <dir> -head <dir>
//	benchcmp report <result.json | device.log>...
//
// measure starts one build of the app once to warm up, which also
// installs the embedded release into its store, then -runs times, and
// summarises the samples.
// run starts two builds of the app alternately — base, head, then head,
// base, and so on — after a warm-up of each, then compares them as
// compare does. Run it where the apps can draw:
// under xvfb-run on Linux. compare reads the results run wrote and fails
// (exit 1) when a metric of head is more than 10% slower than base with
// 99% confidence (tools/internal/perf). report summarises results, from
// result files or from a device's log.
//
// run and measure also write their report to report.md in the output
// directory.
//
// Exit codes: 0 no regression, 1 a regression, 2 usage, I/O or a run
// that failed.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nightCode42/plux3/tools/internal/perf"
)

const (
	exitOK        = 0
	exitRegressed = 1
	exitError     = 2
)

// main delegates to run so that the command logic is testable.
func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr, execApp))
}

// launcher starts the benchmark app at path with env added to the
// process environment, its output going to log, and waits for it.
type launcher func(ctx context.Context, path string, env []string, log io.Writer) error

// execApp runs the app as a child process.
func execApp(ctx context.Context, path string, env []string, log io.Writer) error {
	cmd := exec.CommandContext(ctx, path) //nolint:gosec // The app to benchmark is the command's argument.
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = log
	cmd.Stderr = log
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("benchcmp: %s: %w", path, err)
	}
	return nil
}

func usage(stderr io.Writer) int {
	_, _ = fmt.Fprintln(stderr, "usage: benchcmp measure|run|compare|report [flags] (see the package documentation)")
	return exitError
}

// run dispatches the subcommand.
func run(ctx context.Context, args []string, stdout, stderr io.Writer, launch launcher) int {
	if len(args) == 0 {
		return usage(stderr)
	}
	switch args[0] {
	case "measure":
		return measureCmd(ctx, args[1:], stdout, stderr, launch)
	case "run":
		return runCmd(ctx, args[1:], stdout, stderr, launch)
	case "compare":
		return compareCmd(args[1:], stdout, stderr)
	case "report":
		return reportCmd(args[1:], stdout, stderr)
	}
	return usage(stderr)
}

func runCmd(ctx context.Context, args []string, stdout, stderr io.Writer, launch launcher) int {
	fs := flag.NewFlagSet("benchcmp run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	base := fs.String("base", "", "the benchmark app built from the base commit")
	head := fs.String("head", "", "the benchmark app built from the change")
	runs := fs.Int("runs", 10, "measured runs of each app")
	out := fs.String("out", "bench-results", "directory for results, logs and the apps' stores")
	timeout := fs.Duration("timeout", 5*time.Minute, "the longest one run may take")
	if err := fs.Parse(args); err != nil || *base == "" || *head == "" || *runs < 1 || fs.NArg() > 0 {
		return usage(stderr)
	}
	apps := map[string]string{"base": *base, "head": *head}
	for side := range apps {
		if err := os.MkdirAll(filepath.Join(*out, side, "store"), 0o750); err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return exitError
		}
	}
	// Alternating the order of each pair keeps drift in the machine's
	// speed from favouring either side.
	var order []string
	for i := range *runs {
		if i%2 == 0 {
			order = append(order, "base", "head")
		} else {
			order = append(order, "head", "base")
		}
	}
	steps := append([]string{"base", "head"}, order...)
	count := map[string]int{}
	for i, side := range steps {
		warmup := i < 2
		name := "warmup"
		if !warmup {
			count[side]++
			name = strconv.Itoa(count[side])
		}
		_, _ = fmt.Fprintf(stderr, "benchcmp: %s run %s\n", side, name)
		if err := once(ctx, launch, apps[side], filepath.Join(*out, side), name, *timeout); err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return exitError
		}
	}
	return withReport(*out, stdout, stderr, func(w io.Writer) int {
		return compare(filepath.Join(*out, "base"), filepath.Join(*out, "head"), w, stderr)
	})
}

func measureCmd(ctx context.Context, args []string, stdout, stderr io.Writer, launch launcher) int {
	fs := flag.NewFlagSet("benchcmp measure", flag.ContinueOnError)
	fs.SetOutput(stderr)
	app := fs.String("app", "", "the benchmark app")
	runs := fs.Int("runs", 5, "measured runs")
	out := fs.String("out", "bench-results", "directory for results, logs and the app's store")
	timeout := fs.Duration("timeout", 5*time.Minute, "the longest one run may take")
	if err := fs.Parse(args); err != nil || *app == "" || *runs < 1 || fs.NArg() > 0 {
		return usage(stderr)
	}
	if err := os.MkdirAll(filepath.Join(*out, "store"), 0o750); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return exitError
	}
	var paths []string
	for i := range *runs + 1 {
		name := "warmup"
		if i > 0 {
			name = strconv.Itoa(i)
			paths = append(paths, filepath.Join(*out, name+".json"))
		}
		_, _ = fmt.Fprintf(stderr, "benchcmp: run %s\n", name)
		if err := once(ctx, launch, *app, *out, name, *timeout); err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return exitError
		}
	}
	return withReport(*out, stdout, stderr, func(w io.Writer) int {
		return reportCmd(paths, w, stderr)
	})
}

// withReport runs f with a writer that copies its report to stdout and
// to report.md in dir, which CI adds to the job summary.
func withReport(dir string, stdout, stderr io.Writer, f func(io.Writer) int) int {
	path := filepath.Join(dir, "report.md")
	file, err := os.Create(path) //nolint:gosec // A path under the output directory the caller chose.
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return exitError
	}
	code := f(io.MultiWriter(stdout, file))
	if err := file.Close(); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return exitError
	}
	return code
}

// once runs the app once, writing name.json and name.log in dir.
func once(ctx context.Context, launch launcher, app, dir, name string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	logPath := filepath.Join(dir, name+".log")
	log, err := os.Create(logPath) //nolint:gosec // A path under the output directory the caller chose.
	if err != nil {
		return fmt.Errorf("benchcmp: %w", err)
	}
	store, _ := filepath.Abs(filepath.Join(dir, "store"))
	result, _ := filepath.Abs(filepath.Join(dir, name+".json"))
	runErr := launch(ctx, app, []string{"PLUX_BENCH_STORE=" + store, "PLUX_BENCH_OUT=" + result}, log)
	closeErr := log.Close()
	if err := errors.Join(runErr, closeErr); err != nil {
		return fmt.Errorf("%w (output in %s)", err, logPath)
	}
	return nil
}

func compareCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("benchcmp compare", flag.ContinueOnError)
	fs.SetOutput(stderr)
	base := fs.String("base", "", "directory of the base commit's results")
	head := fs.String("head", "", "directory of the change's results")
	if err := fs.Parse(args); err != nil || *base == "" || *head == "" || fs.NArg() > 0 {
		return usage(stderr)
	}
	return compare(*base, *head, stdout, stderr)
}

// compare reads the numbered results of two directories and gates them.
func compare(baseDir, headDir string, stdout, stderr io.Writer) int {
	base, err := readDir(baseDir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return exitError
	}
	head, err := readDir(headDir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return exitError
	}
	gate := perf.DefaultGate()
	verdicts, err := gate.Compare(base, head)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return exitError
	}
	_, _ = fmt.Fprintf(stdout, "Runtime benchmark: %d runs of the base (runtime %s) and of the change (runtime %s), alternating.\n\n",
		len(base), base[0].Runtime, head[0].Runtime)
	passed, err := gate.WriteVerdicts(stdout, verdicts)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return exitError
	}
	_, _ = fmt.Fprintln(stdout, "\nThe change's samples:")
	_, _ = fmt.Fprintln(stdout)
	if err := perf.WriteSummary(stdout, perf.Summarise(head)); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return exitError
	}
	if !passed {
		_, _ = fmt.Fprintln(stderr, "benchcmp: the change is more than 10% slower (QA-007)")
		return exitRegressed
	}
	return exitOK
}

// readDir reads the numbered results (1.json, 2.json, …) of dir; the
// warm-up is left out.
func readDir(dir string) ([]perf.Run, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("benchcmp: %w", err)
	}
	type numbered struct {
		n    int
		path string
	}
	var files []numbered
	for _, e := range entries {
		n, err := strconv.Atoi(strings.TrimSuffix(e.Name(), ".json"))
		if err == nil && strings.HasSuffix(e.Name(), ".json") {
			files = append(files, numbered{n, filepath.Join(dir, e.Name())})
		}
	}
	slices.SortFunc(files, func(a, b numbered) int { return a.n - b.n })
	runs := make([]perf.Run, 0, len(files))
	for _, f := range files {
		r, err := readRun(f.path)
		if err != nil {
			return nil, err
		}
		runs = append(runs, r)
	}
	return runs, nil
}

func readRun(path string) (perf.Run, error) {
	data, err := os.ReadFile(path) //nolint:gosec // A result file the caller named.
	if err != nil {
		return perf.Run{}, fmt.Errorf("benchcmp: %w", err)
	}
	r, err := perf.ParseRun(data)
	if err != nil {
		return perf.Run{}, fmt.Errorf("benchcmp: %s: %w", path, err)
	}
	return r, nil
}

func reportCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usage(stderr)
	}
	var runs []perf.Run
	for _, path := range args {
		if strings.HasSuffix(path, ".json") {
			r, err := readRun(path)
			if err != nil {
				_, _ = fmt.Fprintln(stderr, err)
				return exitError
			}
			runs = append(runs, r)
			continue
		}
		f, err := os.Open(path) //nolint:gosec // A log file the caller named.
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return exitError
		}
		rs, err := perf.ParseLog(f)
		_ = f.Close()
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "benchcmp: %s: %v\n", path, err)
			return exitError
		}
		runs = append(runs, rs...)
	}
	if len(runs) == 0 {
		_, _ = fmt.Fprintln(stderr, "benchcmp: no results found")
		return exitError
	}
	_, _ = fmt.Fprintf(stdout, "%d runs of runtime %s on %s, %d plugins.\n\n", len(runs), runs[0].Runtime, runs[0].Platform, runs[0].Plugins)
	if err := perf.WriteSummary(stdout, perf.Summarise(runs)); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return exitError
	}
	return exitOK
}
