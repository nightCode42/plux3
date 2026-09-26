// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunGeneratesAndIsIdempotent runs schemagen on a copy of the real
// registry: the first run writes every file, the second none.
func TestRunGeneratesAndIsIdempotent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "schema", "limits.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "schema"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "schema", "limits.json"), src, 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer

	if code := run([]string{"-root", root}, &out, &errOut); code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "limits_gen.go") {
		t.Errorf("first run output: %s", out.String())
	}
	out.Reset()
	if code := run([]string{"-root", root}, &out, &errOut); code != exitOK || out.Len() != 0 {
		t.Errorf("second run: exit %d, output %q", code, out.String())
	}
}

// TestRunReportsFailures checks the exit codes.
func TestRunReportsFailures(t *testing.T) {
	t.Parallel()
	var out, errOut bytes.Buffer
	if code := run([]string{"extra"}, &out, &errOut); code != exitError {
		t.Errorf("extra argument: exit %d", code)
	}
	if code := run([]string{"-root", t.TempDir()}, &out, &errOut); code != exitInvalid {
		t.Errorf("missing registry: exit %d", code)
	}
}
