// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// TestRunWritesTheCatalogueFiles checks the generator output and its exit
// codes.
// Verifies: DX-003.
func TestRunWritesTheCatalogueFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, dir := range []string{"docs/reference", "schema", "packages/plux_flutter/lib/src/errors"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	var stderr bytes.Buffer

	if code := run([]string{"-root", root}, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}

	md, err := os.ReadFile(filepath.Join(root, plxerr.CatalogueMarkdownPath))
	if err != nil || !bytes.Equal(md, plxerr.RenderMarkdown()) {
		t.Errorf("markdown not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, plxerr.CatalogueJSONPath)); err != nil {
		t.Errorf("json not written: %v", err)
	}
	dart, err := os.ReadFile(filepath.Join(root, plxerr.CatalogueDartPath))
	if err != nil || !bytes.Equal(dart, plxerr.RenderDart()) {
		t.Errorf("dart not written: %v", err)
	}
}

// TestRunReportsFailures checks usage and write errors.
func TestRunReportsFailures(t *testing.T) {
	t.Parallel()
	var stderr bytes.Buffer
	if code := run([]string{"-unknown"}, &stderr); code != 2 {
		t.Errorf("bad flag: exit %d", code)
	}
	if code := run([]string{"-root", filepath.Join(t.TempDir(), "missing")}, &stderr); code != 1 {
		t.Errorf("missing root: exit %d", code)
	}
}
