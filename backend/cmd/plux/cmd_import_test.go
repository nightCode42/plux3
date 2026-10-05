// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var importData = filepath.Join("..", "..", "internal", "importer", "testdata")

// Verifies: DAT-002.
func TestImportOpenAPIWritesTheFragment(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"import", "openapi", filepath.Join(importData, "todo30.json")}, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	want, err := os.ReadFile(filepath.Join(importData, "todo30.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	if stdout.String() != string(want) {
		t.Errorf("stdout differs from the golden fragment:\n%s", stdout.String())
	}

	// A file, with the flag after the argument; importing again changes nothing.
	out := filepath.Join(t.TempDir(), "frag.json")
	for range 2 {
		stdout.Reset()
		if code := run([]string{"import", "openapi", filepath.Join(importData, "todo30.json"), "-o", out}, &stdout, &stderr); code != exitOK {
			t.Fatalf("exit %d: %s", code, stderr.String())
		}
		got, err := os.ReadFile(out)
		if err != nil || string(got) != string(want) {
			t.Fatalf("output file differs: %v", err)
		}
	}
}

// Verifies: DAT-002.
func TestImportOpenAPIReportsLeftOutOperations(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := run([]string{"import", "openapi", filepath.Join(importData, "unsupported.yaml")}, &stdout, &stderr)
	if code != exitOK || !strings.Contains(stderr.String(), "PLX-1261") || !strings.Contains(stderr.String(), "/paths/~1poly/get") {
		t.Errorf("exit %d, stderr %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"path": "/good"`) {
		t.Errorf("stdout lacks the supported operation: %s", stdout.String())
	}
}

// Verifies: DAT-002.
func TestImportGraphQLValidatesOperations(t *testing.T) {
	t.Parallel()
	schema := filepath.Join(importData, "schema.graphql")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"import", "graphql", schema, filepath.Join(importData, "ops.graphql")}, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	want, _ := os.ReadFile(filepath.Join(importData, "graphql.golden.json"))
	if stdout.String() != string(want) {
		t.Errorf("stdout differs from the golden fragment:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"import", "graphql", schema, filepath.Join(importData, "bad.graphql")}, &stdout, &stderr); code != exitFailed {
		t.Errorf("an invalid operation exits %d", code)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "PLX-1262") {
		t.Errorf("stdout %q stderr %q", stdout.String(), stderr.String())
	}
}

// Verifies: DAT-002.
func TestImportSourceUsageErrors(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"import", "openapi"},
		{"import", "graphql", "schema.graphql"},
		{"import", "openapi", "a.json", "b.json"},
		{"import", "openapi", "--bogus"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != exitUsage || !strings.Contains(stderr.String(), "Usage: plux import") {
			t.Errorf("%v: exit %d, stderr %q", args, code, stderr.String())
		}
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"import", "openapi", filepath.Join(importData, "missing.json")}, &stdout, &stderr); code != exitFailed {
		t.Errorf("a missing file exits %d", code)
	}
}

// syncBuffer lets the test read what the server printed while it runs.
type syncBuffer struct {
	ch chan string
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.ch <- string(p)
	return len(p), nil
}

// Verifies: TST-004.
func TestMockServesUntilInterrupted(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := &syncBuffer{ch: make(chan string, 4)}
	var stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- newEnv(out, &stderr).mockServe(ctx, []string{filepath.Join(importData, "todo30.json"), "--seed", "7"})
	}()
	var line string
	select {
	case line = <-out.ch:
	case <-time.After(10 * time.Second):
		t.Fatal("the mock did not report its address")
	}
	base := strings.TrimSpace(line[strings.LastIndex(line, "http://"):])
	if !strings.HasPrefix(base, "http://127.0.0.1:") {
		t.Fatalf("the mock must listen on loopback by default: %s", line)
	}
	resp, err := http.Get(base + "/todos")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"items"`) {
		t.Errorf("%d %s", resp.StatusCode, body)
	}
	cancel()
	if code := <-done; code != exitOK {
		t.Errorf("exit %d: %s", code, stderr.String())
	}
}

// Verifies: TST-004.
func TestMockFlagsAndWarnings(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"mock"}, &stdout, &stderr); code != exitUsage {
		t.Errorf("no file exits %d", code)
	}
	stderr.Reset()
	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"mock", bad}, &stdout, &stderr); code != exitFailed || !strings.Contains(stderr.String(), "PLX-1260") {
		t.Errorf("a bad document: exit %d, %s", code, stderr.String())
	}
	for addr, want := range map[string]bool{"127.0.0.1:0": true, "localhost:80": true, "[::1]:0": true, "0.0.0.0:0": false, ":8080": false, "nonsense": false} {
		if loopback(addr) != want {
			t.Errorf("loopback(%q) != %v", addr, want)
		}
	}
}
