// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nightCode42/plux3/backend/internal/storage/storagetest"
)

// configFile writes a configuration and returns its path.
func configFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plux-server.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// valid is a configuration that validates without a database being
// reachable.
const valid = `
server:
  publicBaseURL: "https://plux.example"
database:
  url: "postgres://plux@127.0.0.1:5432/plux"
`

// TestRunHandlesEveryCommand checks the exit code and output stream of
// each invocation that needs no database.
func TestRunHandlesEveryCommand(t *testing.T) {
	t.Parallel()
	path := configFile(t, valid)
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
		{name: "unknown command", args: []string{"deploy"}, wantCode: exitUsage, wantStderr: `unknown command "deploy"`},
		{name: "config validate", args: []string{"config", "validate", "-config", path}, wantCode: exitOK, wantStdout: "configuration is valid"},
		{name: "config without a subcommand", args: []string{"config"}, wantCode: exitUsage, wantStderr: "config validate"},
		{name: "config with a wrong subcommand", args: []string{"config", "set"}, wantCode: exitUsage, wantStderr: "config validate"},
		{name: "missing configuration file", args: []string{"config", "validate", "-config", "nowhere.yaml"}, wantCode: exitUsage, wantStderr: "read configuration"},
		{name: "unexpected argument", args: []string{"config", "validate", "extra"}, wantCode: exitUsage},
		{name: "unknown flag", args: []string{"serve", "-nonsense"}, wantCode: exitUsage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), tt.args, &stdout, &stderr)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d (stderr: %s)", code, tt.wantCode, stderr.String())
			}
			if tt.wantStdout != "" && !strings.Contains(stdout.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, want it to contain %q", stdout.String(), tt.wantStdout)
			}
			if tt.wantStderr != "" && !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}

// Verifies: SRV-008.
func TestConfigValidateReportsProblems(t *testing.T) {
	t.Parallel()
	path := configFile(t, "server:\n  roles: [nonsense]\n")
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"config", "validate", "-config", path}, &stdout, &stderr); code != exitUsage {
		t.Errorf("exit code = %d", code)
	}
	for _, want := range []string{"server.roles", "database.url"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr %q does not mention %q", stderr.String(), want)
		}
	}
}

// Verifies: SRV-001, SRV-007, SRV-021.
// The whole process is built from a configuration, migrates, serves
// /livez, /readyz and /metrics, and drains when the context ends.
func TestServeRunsAndDrains(t *testing.T) {
	url := storagetest.SchemaURL(t)
	dir := t.TempDir()
	path := configFile(t, ""+
		"server:\n"+
		"  roles: [api, worker]\n"+
		"  listen: \"127.0.0.1:18081\"\n"+
		"  publicBaseURL: \"http://127.0.0.1:18081\"\n"+
		"  shutdownGrace: 10s\n"+
		"database:\n"+
		"  url: \""+url+"\"\n"+
		"objectStorage:\n"+
		"  directory: \""+filepath.Join(dir, "objects")+"\"\n"+
		"signing:\n"+
		"  directory: \""+filepath.Join(dir, "keys")+"\"\n"+
		"observability:\n"+
		"  logLevel: warn\n")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	var stdout, stderr bytes.Buffer
	go func() { done <- run(ctx, []string{"serve", "-config", path}, &stdout, &stderr) }()

	client := &http.Client{Timeout: 2 * time.Second}
	if !waitFor(t, client, "http://127.0.0.1:18081/readyz", http.StatusOK) {
		cancel()
		<-done
		t.Fatalf("the server never became ready; stderr: %s", stderr.String())
	}
	for _, tc := range []struct {
		path string
		want int
	}{{"/livez", http.StatusOK}, {"/readyz", http.StatusOK}, {"/metrics", http.StatusOK}} {
		resp, err := get(t, client, "http://127.0.0.1:18081"+tc.path)
		if err != nil {
			t.Fatalf("GET %s: %v", tc.path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("GET %s = %d, want %d", tc.path, resp.StatusCode, tc.want)
		}
	}

	cancel()
	select {
	case code := <-done:
		if code != exitOK {
			t.Errorf("exit code = %d; stderr: %s", code, stderr.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the server did not stop")
	}
}

// waitFor polls a URL until it answers with the wanted status.
func waitFor(t *testing.T, c *http.Client, url string, want int) bool {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := get(t, c, url)
		if err == nil {
			status := resp.StatusCode
			_ = resp.Body.Close()
			if status == want {
				return true
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// Verifies: SEC-100.
// bootstrap creates the first administrator once, printing the
// invitation, and refuses a second time.
func TestBootstrapCommand(t *testing.T) {
	url := storagetest.SchemaURL(t)
	dir := t.TempDir()
	path := configFile(t, "server:\n  publicBaseURL: \"https://p.example\"\ndatabase:\n  url: \""+url+"\"\n"+
		"signing:\n  directory: \""+filepath.Join(dir, "keys")+"\"\n")
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"migrate", "-config", path}, &stdout, &stderr); code != exitOK {
		t.Fatalf("migrate: %d; %s", code, stderr.String())
	}
	stdout.Reset()
	if code := run(context.Background(), []string{"bootstrap", "-config", path, "-email", "admin@example.com"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("bootstrap: %d; %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "invitation") || !strings.Contains(stdout.String(), "plux_inv_") {
		t.Errorf("stdout = %q", stdout.String())
	}
	stderr.Reset()
	if code := run(context.Background(), []string{"bootstrap", "-config", path, "-email", "other@example.com"}, &stdout, &stderr); code != exitFailed {
		t.Errorf("a second bootstrap: exit %d", code)
	}
	for _, args := range [][]string{{"bootstrap"}, {"bootstrap", "-config", path}, {"bootstrap", "-email", "a@b.c", "-config", "nowhere.yaml"}} {
		if code := run(context.Background(), args, &stdout, &stderr); code != exitUsage {
			t.Errorf("%v: exit %d; want %d", args, code, exitUsage)
		}
	}
}

// Verifies: SRV-021.
func TestMigrateCommand(t *testing.T) {
	url := storagetest.SchemaURL(t)
	path := configFile(t, "server:\n  publicBaseURL: \"https://p.example\"\ndatabase:\n  url: \""+url+"\"\n")
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"migrate", "-config", path}, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "migrations applied") {
		t.Errorf("stdout = %q", stdout.String())
	}
}

// Verifies: SRV-007.
func TestServeReportsAnUnreachableDatabase(t *testing.T) {
	t.Parallel()
	path := configFile(t, ""+
		"server:\n  publicBaseURL: \"https://p.example\"\n"+
		"database:\n  url: \"postgres://plux:secret@127.0.0.1:1/plux\"\n")
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"serve", "-config", path}, &stdout, &stderr); code != exitFailed {
		t.Errorf("exit code = %d", code)
	}
	if strings.Contains(stderr.String(), "secret") {
		t.Errorf("the password leaked into %q", stderr.String())
	}
}

// get makes a GET request with the test's context.
func get(t *testing.T, c *http.Client, url string) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return c.Do(req) //nolint:wrapcheck // a test helper
}
