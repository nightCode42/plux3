// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitRepo makes a repository with one Go module, one Dart package and
// rules, commits it, and returns its root.
func gitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.work":                  "go 1.27\n\nuse ./srv\n",
		"srv/go.mod":               "module example.com/srv\n",
		"srv/cmd/app/main.go":      "package main\n",
		"pubspec.yaml":             "name: ws\nworkspace:\n  - app\n",
		"app/pubspec.yaml":         "name: app\n",
		".github/workflows/ci.yml": "on: push\njobs:\n  build:\n    runs-on: a\n  app:\n    runs-on: a\n",
		"docs/guide.md":            "# Guide\n",
		"ci/affected.json":         `{"always": ["changes"], "noJob": ["docs/**"], "jobs": {"build": {"go": ["srv/cmd/..."], "make": ["go-build"]}, "app": {"dart": ["app"]}}}`,
	}
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "."},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "base"},
	} {
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", root}, args...)...) //nolint:gosec // G204: git in the test's repository.
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return root
}

func write(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestRunSelectsFromTheWorkingTree checks a change from a base to the
// working tree, untracked files included, and both outputs.
// Verifies: CI-002.
func TestRunSelectsFromTheWorkingTree_CI_002(t *testing.T) {
	t.Parallel()
	root := gitRepo(t)
	write(t, root, "srv/cmd/app/new.go", "package main\n")
	output := filepath.Join(t.TempDir(), "out")
	summary := filepath.Join(t.TempDir(), "summary.md")
	var stdout, stderr bytes.Buffer
	code := run(t.Context(), []string{"-root", root, "-base", "HEAD", "-github-output", output, "-summary", summary}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	got, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if want := "jobs={\"app\":false,\"build\":true}\n"; string(got) != want {
		t.Errorf("GitHub output = %q, want %q", got, want)
	}
	md, err := os.ReadFile(summary)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(md), "- **build**: srv/cmd/app/new.go") || !strings.Contains(stdout.String(), "Not affected: app.") {
		t.Errorf("summary =\n%s\nstdout =\n%s", md, stdout.String())
	}
}

// TestRunComparesWorkflowBlocksBetweenRevisions checks base..head and a
// change to one job's block of ci.yml.
// Verifies: CI-002.
func TestRunComparesWorkflowBlocksBetweenRevisions_CI_002(t *testing.T) {
	t.Parallel()
	root := gitRepo(t)
	write(t, root, ".github/workflows/ci.yml", "on: push\njobs:\n  build:\n    runs-on: a\n  app:\n    runs-on: b\n")
	write(t, root, "docs/guide.md", "# Guide, revised\n")
	for _, args := range [][]string{
		{"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "head"},
	} {
		//nolint:gosec // G204: git in the test's repository.
		if out, err := exec.CommandContext(t.Context(), "git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	var stdout, stderr bytes.Buffer
	if code := run(t.Context(), []string{"-root", root, "-base", "HEAD^1", "-head", "HEAD", "-make"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "repo-check" {
		t.Errorf("make targets = %q, want repo-check (app has none)", got)
	}
	if !strings.Contains(stderr.String(), "- **app**: its block of .github/workflows/ci.yml changed") ||
		!strings.Contains(stderr.String(), "Only CI's runners run: app.") {
		t.Errorf("stderr =\n%s", stderr.String())
	}
}

func TestRunEventsAndErrors(t *testing.T) {
	t.Parallel()
	root := gitRepo(t)
	var stdout, stderr bytes.Buffer
	if code := run(t.Context(), []string{"-root", root, "-event", "push", "-make"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("push: exit %d: %s", code, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "repo-check go-build" {
		t.Errorf("push: make targets = %q", got)
	}
	for name, args := range map[string][]string{
		"no base":      {"-root", root},
		"extra args":   {"-root", root, "-base", "HEAD", "x"},
		"unknown base": {"-root", root, "-base", "nope"},
		"no rules":     {"-root", root, "-base", "HEAD", "-rules", filepath.Join(root, "missing.json")},
	} {
		stdout.Reset()
		stderr.Reset()
		if code := run(t.Context(), args, &stdout, &stderr); code != exitError {
			t.Errorf("%s: exit %d, want %d", name, code, exitError)
		}
	}
}
