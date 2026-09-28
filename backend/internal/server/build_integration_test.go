// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/nightCode42/plux3/backend/internal/pluxv1/pluxv1connect"
	"github.com/nightCode42/plux3/backend/internal/storage/storagetest"
)

// Verifies: SRV-001, SRV-003, SRV-007, SRV-020, SRV-021, SRV-023, SRV-024.
// Build opens every dependency the configured roles need, migrates, and
// the running process reports each one on /readyz.
func TestBuildAndRunAllRoles(t *testing.T) {
	url := storagetest.SchemaURL(t)
	dir := t.TempDir()
	cfg := testConfig(t, ""+
		"server:\n"+
		"  roles: [api, worker]\n"+
		"  listen: \"127.0.0.1:18082\"\n"+
		"  publicBaseURL: \"http://127.0.0.1:18082\"\n"+
		"  shutdownGrace: 10s\n"+
		"database:\n"+
		"  url: \""+url+"\"\n"+
		"objectStorage:\n"+
		"  directory: \""+filepath.Join(dir, "objects")+"\"\n"+
		"signing:\n"+
		"  directory: \""+filepath.Join(dir, "keys")+"\"\n"+
		"limits:\n"+
		"  \"page.nodes\": \"2000\"\n")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	built, err := Build(ctx, cfg, discard(), "test")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer built.Close()

	if got := built.Server.limits.Get("page.nodes"); got != 2000 {
		t.Errorf("the configured limit was not applied: page.nodes = %d", got)
	}
	servesEveryP2Service(t, built.Server)

	done := make(chan error, 1)
	go func() { done <- built.Server.Run(ctx) }()

	client := &http.Client{Timeout: 2 * time.Second}
	var body map[string]any
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := get(t, client, "http://127.0.0.1:18082/readyz")
		if err == nil {
			status := resp.StatusCode
			decodeErr := json.NewDecoder(resp.Body).Decode(&body)
			_ = resp.Body.Close()
			if status == http.StatusOK && decodeErr == nil {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	checks, _ := body["checks"].(map[string]any)
	for _, name := range []string{"postgres", "cache", "objectStorage"} {
		if checks[name] != "ok" {
			t.Errorf("/readyz reports %s = %v; want ok (body %v)", name, checks[name], body)
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the server did not stop")
	}
}

// servesEveryP2Service checks that the api role mounts each of the fifteen
// services SRV-003 requires in P2.
func servesEveryP2Service(t *testing.T, s *Server) {
	t.Helper()
	for _, name := range []string{
		pluxv1connect.OrgServiceName, pluxv1connect.IdentityServiceName, pluxv1connect.AppServiceName,
		pluxv1connect.PluginServiceName, pluxv1connect.DocumentServiceName, pluxv1connect.ComponentServiceName,
		pluxv1connect.TemplateServiceName, pluxv1connect.AssetServiceName, pluxv1connect.PublishServiceName,
		pluxv1connect.ReleaseServiceName, pluxv1connect.ManifestServiceName, pluxv1connect.DeviceServiceName,
		pluxv1connect.TokenServiceName, pluxv1connect.TelemetryServiceName, pluxv1connect.ControlServiceName,
	} {
		path := "/" + name + "/"
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, path+"Any", http.NoBody)
		if _, pattern := s.mux.Handler(req); pattern != path {
			t.Errorf("%s is not served (matched %q)", name, pattern)
		}
	}
}

// Verifies: SRV-020.
func TestBuildReportsAnUnreachableDatabase(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t, "server:\n  publicBaseURL: \"https://p.example\"\n"+
		"database:\n  url: \"postgres://plux:secret@127.0.0.1:1/plux\"\n")
	if _, err := Build(context.Background(), cfg, discard(), "test"); err == nil {
		t.Fatal("an unreachable database was accepted")
	}
}
