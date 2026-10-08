// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"
)

// password is a value that declares itself sensitive.
type password string

func (password) Redacted() string { return "[redacted]" }

// logged runs f against a logger and returns the one record it wrote.
func logged(t *testing.T, f func(*slog.Logger)) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	f(NewLogger(&buf, LogOptions{Level: "debug", Format: "json", Role: "api", Version: "test"}))
	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &rec); err != nil {
		t.Fatalf("log line %q: %v", buf.String(), err)
	}
	return rec
}

// Verifies: OBS-003, SEC-092.
func TestLoggerRedactsSecrets(t *testing.T) {
	t.Parallel()
	rec := logged(t, func(l *slog.Logger) {
		l.Info("stored",
			slog.String("databaseURL", "postgres://u:hunter2@db/plux"),
			slog.String("Authorization", "Bearer abc"),
			slog.Any("value", password("hunter2")),
			slog.Group("device", slog.String("token", "abc"), slog.String("platform", "android")),
			slog.String("plugin", "loans"))
	})
	body, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"hunter2", "Bearer abc", `"abc"`} {
		if strings.Contains(string(body), leak) {
			t.Errorf("%q leaked into %s", leak, body)
		}
	}
	if rec["plugin"] != "loans" {
		t.Errorf("an ordinary attribute was changed: %v", rec["plugin"])
	}
	if d, ok := rec["device"].(map[string]any); !ok || d["platform"] != "android" || d["token"] != placeholder {
		t.Errorf("group not redacted in place: %v", rec["device"])
	}
	if rec["role"] != "api" || rec["version"] != "test" {
		t.Errorf("process labels missing: %v", rec)
	}
}

// Verifies: OBS-003.
func TestLoggerRedactsPreboundAttributes(t *testing.T) {
	t.Parallel()
	rec := logged(t, func(l *slog.Logger) {
		l.With(slog.String("sessionToken", "abc")).WithGroup("g").Info("hello", slog.String("app", "demo"))
	})
	body, _ := json.Marshal(rec)
	if strings.Contains(string(body), `"abc"`) {
		t.Errorf("a pre-bound secret leaked into %s", body)
	}
}

// Verifies: OBS-001, OBS-003.
func TestLoggerCarriesTheRequestID(t *testing.T) {
	t.Parallel()
	rec := logged(t, func(l *slog.Logger) {
		l.InfoContext(WithRequestID(context.Background(), "req-1"), "hello")
	})
	if rec["requestID"] != "req-1" {
		t.Errorf("requestID = %v", rec["requestID"])
	}
	if RequestID(context.Background()) != "" {
		t.Error("a context without a request ID should report an empty one")
	}
}

func TestLoggerLevelsAndFormats(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		level string
		debug bool
	}{{"debug", true}, {"info", false}, {"warn", false}, {"error", false}, {"nonsense", false}} {
		var buf bytes.Buffer
		NewLogger(&buf, LogOptions{Level: tc.level, Format: "json"}).Debug("x")
		if got := buf.Len() > 0; got != tc.debug {
			t.Errorf("level %q: debug written = %v", tc.level, got)
		}
	}
	var buf bytes.Buffer
	NewLogger(&buf, LogOptions{Level: "info", Format: "text"}).Info("hello", slog.String("token", "abc"))
	if !strings.Contains(buf.String(), "msg=hello") || strings.Contains(buf.String(), "abc") {
		t.Errorf("text output %q", buf.String())
	}
}

// Verifies: OBS-002.
func TestMetricsExposeTheCatalogue(t *testing.T) {
	t.Parallel()
	m := NewMetrics()
	m.RPC("plux.v1.AppService", "CreateApp", "ok", 12*time.Millisecond)
	m.Manifest("production", "not_modified")
	m.Download("delta", 2048)
	m.DeltaGenerated("on_demand", 30*time.Millisecond)
	m.PublishFinished("succeeded")
	m.PublishStage("encode", 5*time.Millisecond)
	m.QueueDepth("publish", 3)
	m.ReplayCacheFallback()

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	for _, name := range []string{
		"plux_rpc_requests_total", "plux_rpc_duration_seconds", "plux_manifest_requests_total",
		"plux_delta_bytes", "plux_delta_generation_seconds", "plux_publish_jobs_total",
		"plux_publish_duration_seconds", "plux_jobs_queue_depth",
		"plux_dpop_replay_cache_degraded", "plux_dpop_replay_cache_fallback_total",
	} {
		if !strings.Contains(body, name) {
			t.Errorf("%s is missing from /metrics", name)
		}
	}
	if m.Registry() == nil {
		t.Error("Registry must be available for component collectors")
	}
}

// Verifies: OBS-002, SEC-023.
// The replay-cache gauge rises with a fallback, falls when the shared
// cache answers again, and the counter only ever counts fallbacks.
func TestReplayCacheMetricsTransitions(t *testing.T) {
	t.Parallel()
	m := NewMetrics()
	read := func() (degraded, fallbacks float64) {
		t.Helper()
		rec := httptest.NewRecorder()
		m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil))
		for _, line := range strings.Split(rec.Body.String(), "\n") {
			if v, ok := strings.CutPrefix(line, "plux_dpop_replay_cache_degraded "); ok {
				degraded, _ = strconv.ParseFloat(v, 64)
			}
			if v, ok := strings.CutPrefix(line, "plux_dpop_replay_cache_fallback_total "); ok {
				fallbacks, _ = strconv.ParseFloat(v, 64)
			}
		}
		return degraded, fallbacks
	}
	if d, f := read(); d != 0 || f != 0 {
		t.Fatalf("at start degraded=%v fallbacks=%v, want 0 and 0", d, f)
	}
	m.ReplayCacheFallback()
	m.ReplayCacheFallback()
	if d, f := read(); d != 1 || f != 2 {
		t.Errorf("after two fallbacks degraded=%v fallbacks=%v, want 1 and 2", d, f)
	}
	m.ReplayCacheRecovered()
	if d, f := read(); d != 0 || f != 2 {
		t.Errorf("after recovery degraded=%v fallbacks=%v, want 0 and 2", d, f)
	}
	m.ReplayCacheFallback()
	if d, f := read(); d != 1 || f != 3 {
		t.Errorf("after a second outage degraded=%v fallbacks=%v, want 1 and 3", d, f)
	}
}

// Verifies: OBS-001.
func TestTracerWithoutAnEndpointIsANoOp(t *testing.T) {
	t.Parallel()
	tr, err := NewTracer(context.Background(), TraceOptions{Service: "plux-server"})
	if err != nil {
		t.Fatalf("NewTracer: %v", err)
	}
	ctx, span := tr.Start(context.Background(), "test")
	span.End()
	if span.SpanContext().IsValid() {
		t.Error("a no-op tracer should not produce a recording span")
	}
	if err := tr.Timed(ctx, "step", func(context.Context) error { return nil }); err != nil {
		t.Errorf("Timed: %v", err)
	}
	if tr.Propagator == nil {
		t.Error("the propagator must exist even when tracing is off")
	}
	if err := tr.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
}

// Verifies: OBS-001.
func TestTracerExportsToACollector(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(httptest.NewServer(nil).Config.Handler)
	defer srv.Close()
	tr, err := NewTracer(context.Background(), TraceOptions{
		Endpoint: srv.URL, SampleRatio: 1, Service: "plux-server", Role: "api", Version: "test",
	})
	if err != nil {
		t.Fatalf("NewTracer: %v", err)
	}
	err = tr.Timed(context.Background(), "publish", func(ctx context.Context) error {
		if !tracing(ctx) {
			t.Error("Timed did not put a recording span in the context")
		}
		return context.Canceled
	})
	if err == nil {
		t.Error("Timed must return the function's error")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := tr.Shutdown(ctx); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
}

// tracing reports whether the context carries a recording span.
func tracing(ctx context.Context) bool {
	return trace.SpanContextFromContext(ctx).IsValid()
}
