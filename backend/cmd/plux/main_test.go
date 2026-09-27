// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
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

// projectDir is the conformance project the compiler's goldens come from.
var projectDir = filepath.Join("..", "..", "..", "schema", "testdata", "documents", "loan-calculator")

// brokenProject copies the conformance project and breaks one page.
func brokenProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(projectDir)); err != nil {
		t.Fatal(err)
	}
	page := filepath.Join(dir, "plugins", "loans", "pages", "result.page.json")
	if err := os.WriteFile(page, []byte(`{"kind": "page"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Verifies: CLI-005.
func TestValidateAndBuildCommands(t *testing.T) {
	t.Parallel()
	notDir := filepath.Join(projectDir, "app.json")
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{name: "validate", args: []string{"validate", projectDir}, wantCode: exitOK, wantStderr: "0 errors, 0 warnings"},
		{name: "validate help", args: []string{"validate", "-h"}, wantCode: exitOK, wantStderr: "Usage: plux validate"},
		{name: "validate without a directory", args: []string{"validate"}, wantCode: exitUsage, wantStderr: "Usage: plux validate"},
		{name: "validate a file", args: []string{"validate", notDir}, wantCode: exitUsage, wantStderr: "is not a directory"},
		{name: "validate an unknown flag", args: []string{"validate", "--fast", projectDir}, wantCode: exitUsage, wantStderr: "flag provided but not defined"},
		{name: "validate errors", args: []string{"validate", brokenProject(t)}, wantCode: exitFailed, wantStderr: "PLX-1002"},
		{name: "build without output", args: []string{"build", projectDir}, wantCode: exitUsage, wantStderr: "Usage: plux build"},
		{name: "build a file", args: []string{"build", "-o", t.TempDir(), notDir}, wantCode: exitUsage, wantStderr: "is not a directory"},
		{name: "build errors", args: []string{"build", "-o", t.TempDir(), brokenProject(t)}, wantCode: exitFailed, wantStderr: "PLX-1002"},
		{name: "build into a file", args: []string{"build", "-o", notDir, projectDir}, wantCode: exitFailed, wantStderr: "create the output directory"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			code := run(tt.args, &stdout, &stderr)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d; stderr:\n%s", code, tt.wantCode, stderr.String())
			}
			assertContainsOrEmpty(t, "stdout", stdout.String(), tt.wantStdout)
			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}

// Verifies: CLI-005, CMP-002.
// An offline build writes the bundles the compiler's golden tests pin,
// and a release source map beside each.
func TestBuildWritesTheGoldenBundles(t *testing.T) {
	t.Parallel()
	out := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"build", "-o", out, projectDir}, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit code %d; stderr:\n%s", code, stderr.String())
	}
	golden := filepath.Join("..", "..", "..", "schema", "testdata", "bundles", "loan-calculator")
	for file, want := range map[string]string{"app/demo.pxb": "demo.pxb", "plugins/loans.pxb": "loans.pxb"} {
		got, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(file)))
		if err != nil {
			t.Fatal(err)
		}
		wantData, err := os.ReadFile(filepath.Join(golden, want)) //nolint:gosec // G304: a path under the repository.
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, wantData) {
			t.Errorf("%s differs from the golden %s", file, want)
		}
		if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(strings.TrimSuffix(file, ".pxb")+".sourcemap"))); err != nil {
			t.Errorf("no source map for %s: %v", file, err)
		}
	}
	if !strings.Contains(stdout.String(), filepath.Join(out, "plugins", "loans.pxb")) {
		t.Errorf("stdout lacks the plugin bundle: %s", stdout.String())
	}
}

// Verifies: CLI-005.
func TestBuildJSON(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"build", "--dev", "--json", "-o", t.TempDir(), projectDir}, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit code %d; stderr:\n%s", code, stderr.String())
	}
	var rep report
	if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if !rep.OK || len(rep.Bundles) != 2 || rep.Bundles[0].Role != "app" || rep.Bundles[1].Key != "loans" {
		t.Fatalf("report %+v", rep)
	}
	for _, b := range rep.Bundles {
		if !b.Development || b.SourceMap != "" || len(b.Hash) != 64 || b.Size == 0 {
			t.Errorf("bundle %+v", b)
		}
	}
	if stderr.Len() > 0 {
		t.Errorf("stderr = %q", stderr.String())
	}

	stdout.Reset()
	if code := run([]string{"validate", "--json", brokenProject(t)}, &stdout, &stderr); code != exitFailed {
		t.Fatalf("exit code %d", code)
	}
	rep = report{}
	if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.OK || rep.Errors == 0 || len(rep.Diagnostics) != rep.Errors+rep.Warnings || rep.Diagnostics[0].DocURL == "" {
		t.Errorf("report %+v", rep)
	}
}
