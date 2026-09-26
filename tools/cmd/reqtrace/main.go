// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/nightCode42/plux3/tools/internal/spec"
	"github.com/nightCode42/plux3/tools/internal/trace"
)

// Exit codes: 0 success, 1 check failed, 2 usage or I/O error.
const (
	exitOK     = 0
	exitFailed = 1
	exitError  = 2
)

// main delegates to run so that the command logic is testable.
func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run dispatches to the lint or report subcommand.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: reqtrace lint|report [flags]")
		return exitError
	}
	switch args[0] {
	case "lint":
		return runLint(args[1:], stdout, stderr)
	case "report":
		return runReport(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "reqtrace: unknown subcommand %q\n", args[0])
		return exitError
	}
}

// loadSpec parses the specification at path.
func loadSpec(path string) (*spec.Document, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("reqtrace: %w", err)
	}
	defer func() { _ = f.Close() }()
	doc, err := spec.Parse(f)
	if err != nil {
		return nil, fmt.Errorf("reqtrace: %w", err)
	}
	return doc, nil
}

// runLint prints every consistency problem in the specification.
func runLint(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("lint", flag.ContinueOnError)
	fs.SetOutput(stderr)
	specPath := fs.String("spec", "docs/requirements.md", "path to the specification")
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	doc, err := loadSpec(*specPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return exitError
	}
	problems := doc.Lint()
	for _, p := range problems {
		_, _ = fmt.Fprintf(stderr, "%s:%s\n", *specPath, p)
	}
	if len(problems) > 0 {
		return exitFailed
	}
	_, _ = fmt.Fprintf(stdout, "%s: %d requirements, no problems\n", *specPath, len(doc.Requirements))
	return exitOK
}

// runReport builds the traceability report and writes it where requested.
func runReport(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	fs.SetOutput(stderr)
	specPath := fs.String("spec", "docs/requirements.md", "path to the specification")
	root := fs.String("root", ".", "repository root to scan for evidence")
	mdPath := fs.String("md", "", "write the Markdown report to this file")
	jsonPath := fs.String("json", "", "write the JSON report to this file")
	strict := fs.Bool("strict", false, "fail on traceability violations")
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	doc, err := loadSpec(*specPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return exitError
	}
	evidence, err := trace.Scan(*root)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return exitError
	}
	report := trace.Build(doc, evidence)
	if err := writeOutputs(report, *mdPath, *jsonPath); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return exitError
	}
	violations := report.Violations()
	_, _ = fmt.Fprintf(stdout, "%d evidence items, %d violations\n", len(evidence), len(violations))
	if len(violations) > 0 {
		_, _ = fmt.Fprintln(stderr, strings.Join(violations, "\n"))
		if *strict {
			return exitFailed
		}
	}
	return exitOK
}

// writeOutputs writes the Markdown and JSON reports when paths are given.
func writeOutputs(report trace.Report, mdPath, jsonPath string) error {
	if mdPath != "" {
		f, err := os.Create(mdPath)
		if err != nil {
			return fmt.Errorf("reqtrace: %w", err)
		}
		werr := report.WriteMarkdown(f)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return fmt.Errorf("reqtrace: %w", werr)
		}
	}
	if jsonPath != "" {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return fmt.Errorf("reqtrace: %w", err)
		}
		if err := os.WriteFile(jsonPath, append(data, '\n'), 0o600); err != nil {
			return fmt.Errorf("reqtrace: %w", err)
		}
	}
	return nil
}
