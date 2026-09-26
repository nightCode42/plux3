// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Command schemagen regenerates the code and reference documents derived
// from schema/ (ADR-0025). It runs as part of `make gen`.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/nightCode42/plux3/tools/internal/codegen"
)

// Exit codes: 0 success, 1 invalid source, 2 usage or I/O error.
const (
	exitOK      = 0
	exitInvalid = 1
	exitError   = 2
)

// main delegates to run so that the logic is testable.
func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run generates every output under -root and lists the files it changed.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("schemagen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return exitError
	}
	files, err := generate(*root)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "schemagen:", err)
		return exitInvalid
	}
	changed, err := codegen.WriteAll(*root, files)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "schemagen:", err)
		return exitError
	}
	for _, p := range changed {
		_, _ = fmt.Fprintln(stdout, "schemagen: wrote", p)
	}
	return exitOK
}

// generate runs every generator.
func generate(root string) ([]codegen.File, error) {
	limits, err := codegen.LoadLimits(filepath.Join(root, filepath.FromSlash(codegen.LimitsSource)))
	if err != nil {
		return nil, fmt.Errorf("limits: %w", err)
	}
	files, err := codegen.LimitsFiles(limits)
	if err != nil {
		return nil, fmt.Errorf("limits: %w", err)
	}
	return files, nil
}
