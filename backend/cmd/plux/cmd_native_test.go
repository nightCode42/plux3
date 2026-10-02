// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"

	"github.com/nightCode42/plux3/backend/internal/pluxv1"
	"github.com/nightCode42/plux3/backend/internal/pluxv1/pluxv1connect"
)

func putFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestReadYAMLSubset reads plux.yaml and the top of a pubspec.yaml
// without a YAML library.
// Verifies: CLI-006.
func TestReadYAMLSubset(t *testing.T) {
	t.Parallel()
	top, lists, err := readYAMLSubset(strings.NewReader(`# The host's Plux configuration.
slots:
  - MapCard   # the map
  - "Counter"
hostBuild: '1.4.0+52'
dependencies:
  flutter:
    sdk: flutter
  - not a slot
version: 2.0.0+7 # trailing comment
`))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(lists["slots"], []string{"MapCard", "Counter"}) || top["hostBuild"] != "1.4.0+52" || top["version"] != "2.0.0+7" {
		t.Errorf("top %v, lists %v", top, lists)
	}
	if len(lists["dependencies"]) != 1 {
		t.Errorf("an indented item under another key: %v", lists)
	}
	if _, _, err := readYAMLSubset(strings.NewReader("slots\n")); err == nil {
		t.Error("a line without a colon was accepted")
	}
}

// TestNativeScan runs the scanner in the host project with the slots
// plux.yaml lists and the build pubspec.yaml names, and passes its exit
// code through.
// Verifies: CLI-006, WGT-030.
func TestNativeScan(t *testing.T) {
	t.Parallel()
	host := t.TempDir()
	putFile(t, filepath.Join(host, "pubspec.yaml"), "name: host\nversion: 1.4.0+52\n")
	putFile(t, filepath.Join(host, hostConfigFile), "slots:\n  - MapCard\n  - Counter\nappId: dev.plux.host\n")
	record := filepath.Join(host, "args")
	dart := filepath.Join(t.TempDir(), "dart")
	putFile(t, dart, "#!/bin/sh\npwd > \""+record+"\"\necho \"$@\" >> \""+record+"\"\nexit ${PLUX_FAKE_EXIT:-0}\n")
	if err := os.Chmod(dart, 0o700); err != nil { //nolint:gosec // G302: the fake must be executable.
		t.Fatal(err)
	}
	if code, _, stderr := cli(t, t.TempDir(), "native", "scan", "--host", host, "--dart", dart); code != exitOK {
		t.Fatalf("scan: %d %s", code, stderr)
	}
	got, err := os.ReadFile(record) //nolint:gosec // the test's own file
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(got)), "\n")
	resolved, _ := filepath.EvalSymlinks(host)
	if len(lines) != 2 || (lines[0] != host && lines[0] != resolved) ||
		lines[1] != "run plux_native_scan --root . --host 1.4.0+52 --output plux.catalogue.json --slot MapCard --slot Counter --app-id dev.plux.host" {
		t.Errorf("the scanner ran as %q", lines)
	}
	if code, _, _ := cli(t, t.TempDir(), "native", "scan", "--host", host, "--dart", dart, "--build", "9.0.0+1"); code != exitOK {
		t.Errorf("an explicit build: %d", code)
	}
	if got, _ := os.ReadFile(record); !strings.Contains(string(got), "--host 9.0.0+1") { //nolint:gosec // the test's own file
		t.Errorf("--build was not passed: %s", got)
	}
	if code, _, stderr := cli(t, t.TempDir(), "native", "scan", "--host", host, "--dart", filepath.Join(host, "none")); code != exitFailed || !strings.Contains(stderr, "Dart SDK") {
		t.Errorf("no Dart: %d %s", code, stderr)
	}
	bare := t.TempDir()
	if code, _, stderr := cli(t, t.TempDir(), "native", "scan", "--host", bare, "--dart", dart); code != exitUsage || !strings.Contains(stderr, "pubspec.yaml") {
		t.Errorf("no build to scan: %d %s", code, stderr)
	}
	if code, _, _ := cli(t, t.TempDir(), "native", "nope"); code != exitUsage {
		t.Errorf("an unknown subcommand: %d", code)
	}
}

// fakeNative is NativeCatalogueService, storing catalogues in memory.
type fakeNative struct {
	pluxv1connect.UnimplementedNativeCatalogueServiceHandler
	mu     sync.Mutex
	stored map[string][]byte
}

func (f *fakeNative) UploadNativeCatalogue(_ context.Context, r *connect.Request[pluxv1.UploadNativeCatalogueRequest]) (*connect.Response[pluxv1.UploadNativeCatalogueResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header().Get("Authorization") != "Bearer plux_pat_native" {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}
	key := r.Msg.GetAppId() + "/" + r.Msg.GetHostBuild()
	old, exists := f.stored[key]
	if exists && string(old) != string(r.Msg.GetCatalogue()) {
		return nil, connect.NewError(connect.CodeAlreadyExists, nil)
	}
	f.stored[key] = r.Msg.GetCatalogue()
	return connect.NewResponse(&pluxv1.UploadNativeCatalogueResponse{
		HostBuild: &pluxv1.HostBuild{AppId: r.Msg.GetAppId(), HostBuild: r.Msg.GetHostBuild(), Sha256: "ab"},
		Created:   !exists,
	}), nil
}

// TestNativeSync uploads the catalogue of the build pubspec.yaml names,
// and reports a second upload of the same content as unchanged.
// Verifies: CLI-006.
func TestNativeSync(t *testing.T) { //nolint:paralleltest // the environment is process-wide
	t.Setenv(tokenEnv, "plux_pat_native")
	t.Setenv(serverEnv, "")
	t.Setenv(orgEnv, "")
	f := &fakeNative{stored: map[string][]byte{}}
	mux := http.NewServeMux()
	mux.Handle(pluxv1connect.NewNativeCatalogueServiceHandler(f))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	host := t.TempDir()
	putFile(t, filepath.Join(host, "pubspec.yaml"), "name: host\nversion: 1.4.0+52\n")
	args := []string{"native", "sync", "--server", srv.URL, "--app", "a1", "--host", host}
	if code, _, stderr := cli(t, t.TempDir(), args...); code != exitFailed || !strings.Contains(stderr, "plux native scan") {
		t.Errorf("no catalogue yet: %d %s", code, stderr)
	}
	putFile(t, filepath.Join(host, catalogueFile), `{"kind": "nativeCatalogue"}`)
	if code, out, stderr := cli(t, t.TempDir(), args...); code != exitOK || out != "host build 1.4.0+52: uploaded (sha256 ab)\n" {
		t.Fatalf("sync: %d %q %s", code, out, stderr)
	}
	if string(f.stored["a1/1.4.0+52"]) != `{"kind": "nativeCatalogue"}` {
		t.Errorf("stored %v", f.stored)
	}
	if code, out, _ := cli(t, t.TempDir(), append(args, "--json")...); code != exitOK || !strings.Contains(out, `"created": false`) {
		t.Errorf("the same catalogue again: %d %s", code, out)
	}
	putFile(t, filepath.Join(host, catalogueFile), `{"kind": "nativeCatalogue", "routes": []}`)
	if code, _, _ := cli(t, t.TempDir(), args...); code != exitFailed {
		t.Errorf("another catalogue for the build: %d", code)
	}
	if code, out, _ := cli(t, t.TempDir(), append(args, "--build", "2.0.0+1")...); code != exitOK || !strings.Contains(out, "2.0.0+1") {
		t.Errorf("an explicit build: %d %s", code, out)
	}
	t.Setenv(tokenEnv, "plux_pat_other")
	if code, _, _ := cli(t, t.TempDir(), append(args, "--build", "3.0.0+1")...); code != exitAuth {
		t.Errorf("a refused token: %d", code)
	}
}
