// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/cache"
	"github.com/nightCode42/plux3/backend/internal/config"
	"github.com/nightCode42/plux3/backend/internal/observability"
)

// testConfig returns a validated configuration for the given YAML body.
func testConfig(t *testing.T, body string) *config.Config {
	t.Helper()
	c, err := config.Parse([]byte(body), nil)
	if err != nil {
		t.Fatalf("configuration: %v", err)
	}
	return c
}

// base is a configuration that validates.
const base = "server:\n  publicBaseURL: \"https://plux.example\"\n" +
	"database:\n  url: \"postgres://plux@db/plux\"\n"

// discard is a logger that writes nowhere.
func discard() *slog.Logger {
	return observability.NewLogger(io.Discard, observability.LogOptions{Level: "error", Format: "json"})
}

// Verifies: SRV-007.
func TestReadyzReportsEveryDependency(t *testing.T) {
	t.Parallel()
	h := NewHealth()
	h.Register("postgres", func(context.Context) error { return nil })
	h.Register("cache", func(context.Context) error { return errors.New("connection refused to 10.0.0.5") })

	// Before the process is ready, /readyz refuses even when every
	// dependency answers.
	body, status := ready(t, h)
	if status != http.StatusServiceUnavailable {
		t.Errorf("status before ready = %d", status)
	}
	if body["reason"] != "starting or shutting down" {
		t.Errorf("body = %v", body)
	}

	h.SetReady(true)
	body, status = ready(t, h)
	if status != http.StatusServiceUnavailable {
		t.Errorf("status with a failing dependency = %d", status)
	}
	checks, _ := body["checks"].(map[string]any)
	if checks["postgres"] != "ok" || checks["cache"] != "unavailable" {
		t.Errorf("checks = %v", checks)
	}
	// The failure's detail may quote a connection string, so it is never
	// returned (OBS-003).
	raw, _ := json.Marshal(body)
	if strings.Contains(string(raw), "10.0.0.5") {
		t.Errorf("a connection detail leaked into %s", raw)
	}

	h.Register("cache", func(context.Context) error { return nil })
	body, status = ready(t, h)
	if status != http.StatusOK || body["status"] != "ok" {
		t.Errorf("status = %d, body = %v", status, body)
	}

	h.SetReady(false)
	if _, status = ready(t, h); status != http.StatusServiceUnavailable {
		t.Errorf("status while draining = %d", status)
	}
}

// ready calls /readyz and returns the decoded body and status.
func ready(t *testing.T, h *Health) (map[string]any, int) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.Ready(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil))
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
	return body, rec.Code
}

// Verifies: SRV-007.
// /livez reports the process, not its dependencies, so a database
// outage never restarts a healthy replica.
func TestLivezIgnoresDependencies(t *testing.T) {
	t.Parallel()
	h := NewHealth()
	h.Register("postgres", func(context.Context) error { return errors.New("down") })
	rec := httptest.NewRecorder()
	h.Live(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/livez", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d", rec.Code)
	}
}

// Verifies: SRV-001.
func TestNewNeedsItsEssentials(t *testing.T) {
	t.Parallel()
	if _, err := New(Deps{}); err == nil {
		t.Error("a server without a configuration was accepted")
	}
	if _, err := New(Deps{Config: testConfig(t, base)}); err == nil {
		t.Error("a server without a logger was accepted")
	}
}

// Verifies: SRV-001, SEC-104.
// The api role serves the endpoints every process exposes, and bounds
// every request body.
func TestAPIRoleServesItsEndpoints(t *testing.T) {
	t.Parallel()
	s, err := New(Deps{
		Config:  testConfig(t, base),
		Log:     discard(),
		Metrics: observability.NewMetrics(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.HealthEndpoint().SetReady(true)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	for _, tc := range []struct {
		path string
		want int
	}{{"/livez", http.StatusOK}, {"/readyz", http.StatusOK}, {"/metrics", http.StatusOK}, {"/nowhere", http.StatusNotFound}} {
		resp, err := get(t, srv.Client(), srv.URL+tc.path)
		if err != nil {
			t.Fatalf("GET %s: %v", tc.path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("GET %s = %d, want %d", tc.path, resp.StatusCode, tc.want)
		}
	}

	// A body larger than the configured maximum is refused before a
	// handler sees it.
	big := strings.NewReader(strings.Repeat("x", 9<<20))
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/nowhere", big)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("an oversized body was accepted with %d", resp.StatusCode)
	}
}

// Verifies: SRV-001.
// A process without the api role opens no listener and registers no
// HTTP endpoints.
func TestWorkerOnlyProcessServesNothing(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t, "server:\n  roles: [worker]\n"+
		"database:\n  url: \"postgres://plux@db/plux\"\n"+
		"cache:\n  backend: valkey\n  valkeyURL: \"redis://valkey:6379\"\n")
	s, err := New(Deps{Config: cfg, Log: discard()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s.http != nil {
		t.Error("a worker-only process built a listener")
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/livez", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /livez on a worker = %d, want 404", rec.Code)
	}
}

// Verifies: SRV-007.
// Run stops when its context is cancelled, and stops components in the
// reverse order they started.
func TestRunStartsAndStopsComponentsInOrder(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t, "server:\n  roles: [worker]\n"+
		"database:\n  url: \"postgres://plux@db/plux\"\n"+
		"cache:\n  backend: valkey\n  valkeyURL: \"redis://valkey:6379\"\n")
	s, err := New(Deps{Config: cfg, Log: discard()})
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	s.AddComponent(recorder{name: "first", order: &order})
	s.AddComponent(recorder{name: "second", order: &order})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{"start first", "start second", "stop second", "stop first"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v; want %v", order, want)
	}
}

// Verifies: SRV-007.
// A component that fails to start stops the ones already running and
// reports why.
func TestRunUnwindsAFailedStart(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t, "server:\n  roles: [worker]\n"+
		"database:\n  url: \"postgres://plux@db/plux\"\n"+
		"cache:\n  backend: valkey\n  valkeyURL: \"redis://valkey:6379\"\n")
	s, err := New(Deps{Config: cfg, Log: discard()})
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	s.AddComponent(recorder{name: "first", order: &order})
	s.AddComponent(recorder{name: "broken", order: &order, startErr: errors.New("no")})
	err = s.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "start broken") {
		t.Fatalf("Run = %v", err)
	}
	if strings.Join(order, ",") != "start first,start broken,stop first" {
		t.Errorf("order = %v", order)
	}
}

// recorder is a component that records when it starts and stops.
type recorder struct {
	name     string
	order    *[]string
	startErr error
}

// Name identifies the component.
func (r recorder) Name() string { return r.name }

// Start records and may fail.
func (r recorder) Start(context.Context) error {
	*r.order = append(*r.order, "start "+r.name)
	return r.startErr
}

// Stop records.
func (r recorder) Stop(context.Context) error {
	*r.order = append(*r.order, "stop "+r.name)
	return nil
}

// Verifies: SRV-023, DEP-041.
func TestBuildObjectsAndCacheFollowTheConfiguration(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fsCfg := testConfig(t, base+"objectStorage:\n  directory: \""+dir+"\"\n")
	if _, err := buildObjects(context.Background(), fsCfg); err != nil {
		t.Errorf("filesystem backend: %v", err)
	}
	s3Cfg := testConfig(t, base+"objectStorage:\n  backend: s3\n  bucket: plux\n  region: eu-west-1\n")
	if _, err := buildObjects(context.Background(), s3Cfg); err != nil {
		t.Errorf("s3 backend: %v", err)
	}
	bad := *fsCfg
	bad.ObjectStorage.Backend = "gcs"
	if _, err := buildObjects(context.Background(), &bad); err == nil {
		t.Error("an unknown object storage backend was accepted")
	}

	if _, err := buildCache(fsCfg); err != nil {
		t.Errorf("memory cache: %v", err)
	}
	valkey := testConfig(t, "server:\n  roles: [worker]\n"+
		"database:\n  url: \"postgres://plux@db/plux\"\n"+
		"cache:\n  backend: valkey\n  valkeyURL: \"redis://valkey:6379\"\n")
	if _, err := buildCache(valkey); err != nil {
		t.Errorf("valkey cache: %v", err)
	}
	badCache := *fsCfg
	badCache.Cache.Backend = "memcache"
	if _, err := buildCache(&badCache); err == nil {
		t.Error("an unknown cache backend was accepted")
	}
	if got := RoleLabel(fsCfg); got != "api+worker" {
		t.Errorf("RoleLabel = %q", got)
	}
}

// Verifies: SRV-065.
// The interceptor chain includes the rate limit when a cache is
// available, and leaves it out otherwise.
func TestInterceptorsFollowTheDependencies(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t, base)
	bare, err := New(Deps{Config: cfg, Log: discard()})
	if err != nil {
		t.Fatal(err)
	}
	withCache, err := New(Deps{
		Config: cfg, Log: discard(),
		Metrics: observability.NewMetrics(),
		Cache:   cache.NewMemory(nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(withCache.Interceptors()) <= len(bare.Interceptors()) {
		t.Error("the rate limit was not added when a cache is available")
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
