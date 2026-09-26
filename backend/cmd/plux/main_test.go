// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestRunHandlesEveryCommand checks the exit code and output stream of each
// supported invocation, including usage errors.
func TestRunHandlesEveryCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{name: "version", args: []string{"version"}, wantCode: exitOK, wantStdout: name + " dev (commit unknown"},
		{name: "version flag", args: []string{"--version"}, wantCode: exitOK, wantStdout: name + " dev"},
		{name: "help", args: []string{"help"}, wantCode: exitOK, wantStdout: "Usage: " + name},
		{name: "no arguments", args: nil, wantCode: exitUsage, wantStderr: "Usage: " + name},
		{name: "too many arguments", args: []string{"version", "extra"}, wantCode: exitUsage, wantStderr: "Usage: " + name},
		{name: "unknown command", args: []string{"deploy"}, wantCode: exitUsage, wantStderr: `unknown command "deploy"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer

			code := run(tt.args, &stdout, &stderr)

			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d", code, tt.wantCode)
			}
			assertContainsOrEmpty(t, "stdout", stdout.String(), tt.wantStdout)
			assertContainsOrEmpty(t, "stderr", stderr.String(), tt.wantStderr)
		})
	}
}

// assertContainsOrEmpty fails the test unless got contains want, or both are
// empty when nothing is expected on that stream.
func assertContainsOrEmpty(t *testing.T, stream, got, want string) {
	t.Helper()
	if want == "" {
		if got != "" {
			t.Errorf("%s = %q, want nothing", stream, got)
		}
		return
	}
	if !strings.Contains(got, want) {
		t.Errorf("%s = %q, want it to contain %q", stream, got, want)
	}
}
