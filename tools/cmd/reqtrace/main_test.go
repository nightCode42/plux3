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

// fixture writes a one-requirement specification and a repository whose only
// test cites it, and returns the specification path and the root.
func fixture(t *testing.T, status, testLine string) (specPath, root string) {
	t.Helper()
	root = t.TempDir()
	specPath = filepath.Join(root, "requirements.md")
	spec := "| `QA` | QA | §1 |\n\n| `QA-001` | P0 | MUST | text | " + status + " |\n"
	if err := os.WriteFile(specPath, []byte(spec), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "x_test.go"), []byte(testLine+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return specPath, root
}

// TestRunLintAndReport checks the exit codes of both subcommands, including
// strict mode failing on an unverified DONE requirement.
// Verifies: QA-070.
func TestRunLintAndReport_QA_070(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		status   string
		testLine string
		args     func(spec, root string) []string
		wantCode int
		wantOut  string
	}{
		{"lint passes", "SPEC", "", func(s, _ string) []string { return []string{"lint", "-spec", s} }, exitOK, "1 requirements, no problems"},
		{"report verified", "DONE", "// Verifies: QA-001.", func(s, r string) []string {
			return []string{"report", "-strict", "-spec", s, "-root", r}
		}, exitOK, "1 evidence items, 0 violations"},
		{"report unverified strict", "DONE", "", func(s, r string) []string {
			return []string{"report", "-strict", "-spec", s, "-root", r}
		}, exitFailed, "0 evidence items, 1 violations"},
		{"report unverified lenient", "DONE", "", func(s, r string) []string {
			return []string{"report", "-spec", s, "-root", r}
		}, exitOK, "1 violations"},
		{"unknown subcommand", "SPEC", "", func(_, _ string) []string { return []string{"audit"} }, exitError, ""},
		{"missing spec", "SPEC", "", func(_, r string) []string { return []string{"lint", "-spec", filepath.Join(r, "nope.md")} }, exitError, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			specPath, root := fixture(t, tt.status, tt.testLine)
			var stdout, stderr bytes.Buffer

			code := run(tt.args(specPath, root), &stdout, &stderr)

			if code != tt.wantCode {
				t.Fatalf("exit code = %d, want %d (stderr: %s)", code, tt.wantCode, stderr.String())
			}
			if !strings.Contains(stdout.String(), tt.wantOut) {
				t.Errorf("stdout = %q, want %q", stdout.String(), tt.wantOut)
			}
		})
	}
}

// TestRunReportWritesMarkdownAndJSON checks both output files.
func TestRunReportWritesMarkdownAndJSON(t *testing.T) {
	t.Parallel()
	specPath, root := fixture(t, "DONE", "// Verifies: QA-001.")
	md := filepath.Join(root, "out.md")
	js := filepath.Join(root, "out.json")

	code := run([]string{"report", "-spec", specPath, "-root", root, "-md", md, "-json", js}, &bytes.Buffer{}, &bytes.Buffer{})

	if code != exitOK {
		t.Fatalf("exit code = %d", code)
	}
	for path, want := range map[string]string{md: "| `QA-001` | P0 | MUST | DONE |", js: `"ID": "QA-001"`} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), want) {
			t.Errorf("%s is missing %q", filepath.Base(path), want)
		}
	}
}
