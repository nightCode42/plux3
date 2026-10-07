// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/plxtest"
)

// testCmd runs a project's test scenarios headlessly (TST-002): it
// compiles the project, generates a Flutter test project that starts the
// real runtime on a release signed with a key made for the run, runs
// `flutter test` and reports the result. The logic is in plxtest.
func testCmd(args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("test", flag.ContinueOnError)
	dir := set.String("C", ".", "the project `directory`")
	junit := set.String("junit", "", "write the results as JUnit XML to `file`")
	flutter := set.String("flutter", "", "the `path` of the Flutter executable (default flutter on the PATH)")
	runtime := set.String("runtime", "", "use the plux_flutter checkout in `dir` instead of the published package")
	work := set.String("work", "", "generate the Flutter test project in `dir` and keep it (default a temporary directory)")
	set.Usage = func() {
		_, _ = fmt.Fprint(set.Output(), "Usage: plux test [-C dir] [--junit file] [--flutter path]\n\n"+
			"Runs the test scenarios of a project (tests/**/*.scenario.yaml or .json, or the glob\n"+
			"named by tests in plux.yaml) headlessly with Flutter's test harness and the real runtime:\n"+
			"the project is compiled and signed with a key made for the run, and each scenario\n"+
			"becomes one test. Exit codes: 0 every scenario passed, 1 a scenario failed or the\n"+
			"project or a scenario file has errors, 2 usage error.\n\nFlags:\n")
		set.PrintDefaults()
	}
	set.SetOutput(stderr)
	if err := set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if set.NArg() != 0 {
		set.Usage()
		return exitUsage
	}
	fsys, ok := project("test", *dir, stderr)
	if !ok {
		return exitUsage
	}
	cfg, err := readHostConfig(*dir)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s test: %v\n", name, err)
		return exitFailed
	}
	opts := plxtest.Options{
		Project: fsys, Compiler: options(false), Tests: cfg.Tests, Entropy: rand.Reader,
		Runtime: plxtest.Dependency{Version: runtimeConstraint, Path: *runtime},
	}
	plan, err := plxtest.Prepare(opts)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s test: %v\n", name, err)
		return exitFailed
	}
	for _, d := range plan.Compile {
		_, _ = fmt.Fprintln(stderr, d.String())
	}
	for _, f := range plan.Findings {
		_, _ = fmt.Fprintln(stderr, f.String())
	}
	if !plan.Runnable() {
		if len(plan.Cases) == 0 && !plan.Compile.HasErrors() && !plxtest.HasErrors(plan.Findings) {
			_, _ = fmt.Fprintf(stdout, "no scenarios to run\n")
			return exitOK
		}
		return exitFailed
	}
	return runPlan(plan, *work, *flutter, *junit, stdout, stderr)
}

// runPlan writes the Flutter project, runs it and reports.
func runPlan(plan *plxtest.Plan, work, flutter, junit string, stdout, stderr io.Writer) int {
	harnessFailed := func(format string, args ...any) int {
		d := plxerr.NewDiagnostic(plxerr.TestHarnessFailed, plxerr.Location{}, format, args...)
		_, _ = fmt.Fprintln(stderr, d.String())
		return exitFailed
	}
	if flutter == "" {
		path, err := exec.LookPath("flutter")
		if err != nil {
			return harnessFailed("flutter is not on the PATH; install it or pass --flutter")
		}
		flutter = path
	}
	if work == "" {
		tmp, err := os.MkdirTemp("", "plux-test-")
		if err != nil {
			return harnessFailed("%v", err)
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		work = tmp
	}
	if err := plan.Write(work); err != nil {
		return harnessFailed("%v", err)
	}
	report, err := plxtest.Flutter(context.Background(), flutter, work, stderr)
	if err != nil {
		return harnessFailed("%v", err)
	}
	results, err := plxtest.ParseReport(bytes.NewReader(report), plan.Cases)
	if err != nil {
		return harnessFailed("%v", err)
	}
	for _, r := range results {
		mark := "PASS"
		switch r.Status {
		case plxtest.Failed:
			mark = "FAIL"
		case plxtest.Errored, plxtest.NotRun:
			mark = "ERROR"
		case plxtest.Skipped:
			mark = "SKIP"
		case plxtest.Passed:
		}
		_, _ = fmt.Fprintf(stdout, "%-5s %s: %s\n", mark, r.Case.File, r.Case.Name)
		if r.Message != "" {
			_, _ = fmt.Fprintf(stdout, "      %s\n", r.Message)
		}
	}
	sum := plxtest.Summarise(results)
	_, _ = fmt.Fprintf(stdout, "%d scenarios, %d passed, %d failed, %d errors, %d skipped\n", sum.Total, sum.Passed, sum.Failed, sum.Errored, sum.Skipped)
	if junit != "" {
		xmlData, err := plxtest.JUnit(results)
		if err == nil {
			err = writeJUnit(junit, xmlData)
		}
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "%s test: %v\n", name, err)
			return exitFailed
		}
	}
	if !sum.OK() {
		return exitFailed
	}
	return exitOK
}

// writeJUnit writes the JUnit file, creating its directory.
func writeJUnit(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return writeFile(path, data)
}
