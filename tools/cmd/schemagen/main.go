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
	"github.com/nightCode42/plux3/tools/internal/registry"
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
// With -lock-base it instead checks that the committed permanent-ID lock
// keeps every entry of an earlier version of the lock (BND-011).
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("schemagen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root")
	lockBase := fs.String("lock-base", "", "check the ID lock against this earlier `file` and write nothing")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return exitError
	}
	if *lockBase != "" {
		return checkLock(*root, *lockBase, stdout, stderr)
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
	dir := filepath.Join(root, filepath.FromSlash(codegen.SchemaDir))
	model, err := codegen.LoadModel(dir)
	if err != nil {
		return nil, fmt.Errorf("document model: %w", err)
	}
	schemas, err := codegen.Schemas(dir)
	if err != nil {
		return nil, fmt.Errorf("document model: %w", err)
	}
	modelFiles, err := codegen.ModelFiles(model, schemas)
	if err != nil {
		return nil, fmt.Errorf("document model: %w", err)
	}
	files = append(files, modelFiles...)
	reg, err := registry.Load(root)
	if err != nil {
		return nil, fmt.Errorf("registry: %w", err)
	}
	regFiles, err := codegen.RegistryFiles(reg)
	if err != nil {
		return nil, fmt.Errorf("registry: %w", err)
	}
	files = append(files, regFiles...)
	pxl, err := codegen.LoadPXL(root)
	if err != nil {
		return nil, fmt.Errorf("pxl: %w", err)
	}
	pxlFiles, err := codegen.PXLFiles(pxl)
	if err != nil {
		return nil, fmt.Errorf("pxl: %w", err)
	}
	return append(files, pxlFiles...), nil
}

// checkLock fails unless the lock under root keeps every entry of base.
func checkLock(root, base string, stdout, stderr io.Writer) int {
	old, err := registry.ReadLock(base)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "schemagen: lock base:", err)
		return exitError
	}
	cur, err := registry.ReadLock(filepath.Join(root, filepath.FromSlash(registry.LockFile)))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "schemagen:", err)
		return exitInvalid
	}
	if err := cur.CheckAppendOnly(old); err != nil {
		_, _ = fmt.Fprintln(stderr, "schemagen:", err)
		return exitInvalid
	}
	_, _ = fmt.Fprintf(stdout, "schemagen: %s keeps all %d entries of %s\n", registry.LockFile, len(old.IDs), base)
	return exitOK
}
