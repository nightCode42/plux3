// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/nightCode42/plux3/tools/internal/coverage"
)

// Exit codes: 0 all floors met, 1 a floor was missed, 2 usage or I/O error.
const (
	exitOK     = 0
	exitFailed = 1
	exitError  = 2
)

// main delegates to run so that the command logic is testable.
func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run parses the flags, reads the coverage inputs and checks the floors.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("covgate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "coverage.json", "path to the coverage floors")
	kind := fs.String("kind", "", "toolchain: go, dart or studio")
	root := fs.String("root", ".", "repository root")
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	cfg, err := coverage.LoadConfig(*configPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return exitError
	}
	rule, ok := cfg[*kind]
	if !ok || fs.NArg() == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: covgate -kind go|dart|studio [-config file] [-root dir] <coverage files>")
		return exitError
	}
	files, err := readAll(*kind, fs.Args(), *root)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return exitError
	}
	passed, err := coverage.WriteTable(stdout, *kind, coverage.Evaluate(rule, files))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return exitError
	}
	if !passed {
		_, _ = fmt.Fprintf(stderr, "covgate: %s coverage is below its floor (QA-001)\n", *kind)
		return exitFailed
	}
	return exitOK
}

// readAll reads every input with the parser for the toolchain.
func readAll(kind string, paths []string, root string) ([]coverage.FileCoverage, error) {
	var all []coverage.FileCoverage
	for _, p := range paths {
		var files []coverage.FileCoverage
		var err error
		if kind == "go" {
			files, err = coverage.ReadGoProfile(p, root)
		} else {
			files, err = coverage.ReadLCOV(p, root)
		}
		if err != nil {
			return nil, fmt.Errorf("covgate: %w", err)
		}
		all = append(all, files...)
	}
	return all, nil
}
