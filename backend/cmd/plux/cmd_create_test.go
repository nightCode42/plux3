// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"archive/zip"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/compiler"
	"github.com/nightCode42/plux3/backend/internal/generator"
	"github.com/nightCode42/plux3/backend/internal/pluxv1"
)

// serveRelease makes the fake serve the compiled routing project as its
// release: an app with push and deep links, and its plugin.
func serveRelease(t *testing.T, f *fake) {
	t.Helper()
	res := compiler.Compile(os.DirFS(filepath.Join("..", "..", "..", "schema", "testdata", "documents", "routing")), compiler.DefaultOptions())
	if res.Diagnostics.HasErrors() {
		t.Fatal(res.Diagnostics)
	}
	f.blobs = map[string][]byte{}
	for _, b := range append([]*compiler.Bundle{res.App}, res.Plugins...) {
		h := hex.EncodeToString(b.Hash[:])
		f.blobs[h] = b.Data
		key := b.Key
		if b == res.App {
			key = ""
		}
		f.release = append(f.release, &pluxv1.PluginVersion{PluginKey: key, Version: 1, BundleSha256: h, KeyId: "k1", Algorithm: "ed25519", Signature: []byte{7}})
	}
}

// TestCreate generates a project from a release into a directory, a zip
// and a Git remote: the shell from the release's app bundle and the
// flags, the release embedded with the environment's keys.
// Verifies: GEN-001, GEN-003.
func TestCreate(t *testing.T) { //nolint:paralleltest // the environment is process-wide
	t.Setenv(tokenEnv, "plux_pat_test")
	t.Setenv(serverEnv, "")
	t.Setenv(orgEnv, "")
	f, url := newFake(t)
	serveRelease(t, f)
	out := filepath.Join(t.TempDir(), "app")
	base := []string{"create", "--server", url, "--application-id", "com.example.demo", "--bundle-id", "com.example.demo"}
	code, stdout, stderr := cli(t, t.TempDir(), append(base, "--out", out, "demo")...)
	if code != exitOK || !strings.Contains(stdout, "release 3 embedded") {
		t.Fatalf("create: %d %s %s", code, stdout, stderr)
	}
	shell, err := os.ReadFile(filepath.Join(out, "plux.shell.json")) //nolint:gosec // the test's own file
	if err != nil {
		t.Fatal(err)
	}
	var spec generator.ShellSpec
	if err := json.Unmarshal(shell, &spec); err != nil {
		t.Fatal(err)
	}
	if spec.Package != "demo" || spec.Plux.EntryRoute != "home" || spec.Plux.Endpoint != url || spec.Plux.AppID != "a1" || !spec.Push ||
		len(spec.DeepLinkHosts) == 0 || spec.ApplicationID != "com.example.demo" {
		t.Errorf("the shell: %+v", spec)
	}
	for _, p := range []string{"assets/plux/bundles/_app.pxb", "assets/plux/bundles/nav.pxb", "assets/plux/keys.json", "assets/plux/baseline.json", "lib/main.dart", "ios/Runner/Runner.entitlements"} {
		if _, err := os.Stat(filepath.Join(out, p)); err != nil {
			t.Errorf("no %s: %v", p, err)
		}
	}
	if code, _, stderr := cli(t, t.TempDir(), append(base, "--out", out, "demo")...); code != exitFailed || !strings.Contains(stderr, "not empty") {
		t.Errorf("a non-empty directory: %d %s", code, stderr)
	}

	zipFile := filepath.Join(t.TempDir(), "demo.zip")
	if code, _, stderr := cli(t, t.TempDir(), append(base, "--zip", zipFile, "demo")...); code != exitOK {
		t.Fatalf("zip: %d %s", code, stderr)
	}
	data, _ := os.ReadFile(zipFile) //nolint:gosec // the test's own file
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(r.File) < 50 || !strings.HasPrefix(r.File[0].Name, "demo/") {
		t.Errorf("the zip: %v", err)
	}

	if _, err := exec.LookPath("git"); err == nil {
		remote := filepath.Join(t.TempDir(), "remote.git")
		if msg, err := exec.CommandContext(t.Context(), "git", "init", "--quiet", "--bare", remote).CombinedOutput(); err != nil { //nolint:gosec // G204: the test's own directory
			t.Fatalf("git init: %v %s", err, msg)
		}
		t.Setenv("GIT_AUTHOR_NAME", "Ada")
		t.Setenv("GIT_AUTHOR_EMAIL", "ada@example.com")
		t.Setenv("GIT_COMMITTER_NAME", "Ada")
		t.Setenv("GIT_COMMITTER_EMAIL", "ada@example.com")
		if code, stdout, stderr := cli(t, t.TempDir(), append(base, "--git", remote, "--branch", "shell", "demo")...); code != exitOK {
			t.Fatalf("git: %d %s %s", code, stdout, stderr)
		}
		files, err := exec.CommandContext(t.Context(), "git", "--git-dir", remote, "ls-tree", "-r", "--name-only", "shell").Output() //nolint:gosec // G204: the test's own directory
		if err != nil || !strings.Contains(string(files), "plux.shell.json") {
			t.Errorf("the pushed branch: %v %s", err, files)
		}
	}

	for _, bad := range [][]string{
		append(base, "demo"), // no output
		append(base, "--out", t.TempDir(), "--zip", zipFile, "demo"), // two outputs
		append(base, "--zip", zipFile),                               // no app
	} {
		if code, _, _ := cli(t, t.TempDir(), bad...); code != exitUsage {
			t.Errorf("%v: %d", bad, code)
		}
	}
	if code, _, stderr := cli(t, t.TempDir(), append(base, "--zip", zipFile, "--version", "1", "demo")...); code != exitFailed || !strings.Contains(stderr, "version") {
		t.Errorf("a bad version: %d %s", code, stderr)
	}
}
