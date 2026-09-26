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

// TestRunEnforcesFloors checks that covgate passes above the floor, fails
// below it, and rejects bad usage.
// Verifies: QA-001.
func TestRunEnforcesFloors_QA_001(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	config := filepath.Join(root, "coverage.json")
	lcov := filepath.Join(root, "studio", "coverage", "lcov.info")
	if err := os.MkdirAll(filepath.Dir(lcov), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte(`{"studio": {"floor": 70}, "dart": {"floor": 90}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lcov, []byte("SF:src/a.ts\nLF:10\nLH:8\nend_of_record\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantOut  string
	}{
		{"above floor", []string{"-config", config, "-root", root, "-kind", "studio", lcov}, exitOK, "| `total` | 80.0% | 70% | pass |"},
		{"below floor", []string{"-config", config, "-root", root, "-kind", "dart", lcov}, exitFailed, "**FAIL**"},
		{"unknown kind", []string{"-config", config, "-kind", "rust", lcov}, exitError, ""},
		{"no inputs", []string{"-config", config, "-kind", "studio"}, exitError, ""},
		{"missing input", []string{"-config", config, "-kind", "studio", filepath.Join(root, "nope")}, exitError, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer

			code := run(tt.args, &stdout, &stderr)

			if code != tt.wantCode {
				t.Fatalf("exit code = %d, want %d (stderr: %s)", code, tt.wantCode, stderr.String())
			}
			if !strings.Contains(stdout.String(), tt.wantOut) {
				t.Errorf("stdout = %q, want %q", stdout.String(), tt.wantOut)
			}
		})
	}
}
