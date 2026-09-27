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
	for _, want := range []string{
		"limits_gen.go", "model_gen.go", "document.g.dart", "document.gen.ts", "document-schema.md",
		"registry_gen.go", "registry.g.dart", "registry.gen.ts", "widgets.md", "actions.md", "COVERAGE.md",
	} {
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

// TestLockBase checks the append-only comparison against an earlier lock.
//
// Verifies: BND-011.
func TestLockBase(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write := func(path, content string) string {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return full
	}
	write("schema/widgets/ids.lock.json", `{"$comment": "", "ids": {"widget/A": 1, "widget/B": 2}}`)
	tests := []struct {
		name, base string
		code       int
	}{
		{"kept", `{"$comment": "", "ids": {"widget/A": 1}}`, exitOK},
		{"removed", `{"$comment": "", "ids": {"widget/C": 3}}`, exitInvalid},
		{"changed", `{"$comment": "", "ids": {"widget/A": 4}}`, exitInvalid},
		{"malformed base", `{"ids": `, exitError},
	}
	for i, tt := range tests {
		base := write("base"+string(rune('0'+i))+".json", tt.base)
		var out, errOut bytes.Buffer
		if code := run([]string{"-root", root, "-lock-base", base}, &out, &errOut); code != tt.code {
			t.Errorf("%s: exit %d, want %d: %s", tt.name, code, tt.code, errOut.String())
		}
	}
	var out, errOut bytes.Buffer
	if code := run([]string{"-root", t.TempDir(), "-lock-base", filepath.Join(root, "base0.json")}, &out, &errOut); code != exitInvalid {
		t.Errorf("no current lock: exit %d", code)
	}
}
