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
// schema sources: the first run writes every file, the second none.
func TestRunGeneratesAndIsIdempotent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	src := filepath.Join("..", "..", "..", "schema")
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") || strings.Contains(path, "testdata") {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		dst := filepath.Join(root, "schema", rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer

	if code := run([]string{"-root", root}, &out, &errOut); code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	for _, want := range []string{"limits_gen.go", "model_gen.go", "document.g.dart", "document.gen.ts", "document-schema.md"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("first run did not write %s:\n%s", want, out.String())
		}
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
