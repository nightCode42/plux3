// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	"github.com/nightCode42/plux3/backend/internal/api"
	"github.com/nightCode42/plux3/backend/internal/observability"
	"github.com/nightCode42/plux3/backend/internal/pluxv1"
	"github.com/nightCode42/plux3/backend/internal/pluxv1/pluxv1connect"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// stub is an AppService whose CreateApp does whatever the test wants.
// Using the generated service keeps the test on the real edge.
type stub struct {
	pluxv1connect.UnimplementedAppServiceHandler
	create func() error
}

// CreateApp returns the stub's error, or an empty response.
func (s stub) CreateApp(context.Context, *connect.Request[pluxv1.CreateAppRequest]) (*connect.Response[pluxv1.CreateAppResponse], error) {
	if s.create != nil {
		if err := s.create(); err != nil {
			return nil, err
		}
	}
	return connect.NewResponse(&pluxv1.CreateAppResponse{App: &pluxv1.App{Id: "app"}}), nil
}

// harness serves the stub behind the interceptor chain and returns a
// client for it.
func harness(t *testing.T, s stub, d api.Deps) pluxv1connect.AppServiceClient {
	t.Helper()
	path, handler := pluxv1connect.NewAppServiceHandler(s, connect.WithInterceptors(api.Interceptors(d)...))
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return pluxv1connect.NewAppServiceClient(srv.Client(), srv.URL)
}

// call makes one CreateApp call.
func call(ctx context.Context, c pluxv1connect.AppServiceClient) (*connect.Response[pluxv1.CreateAppResponse], error) {
	return c.CreateApp(ctx, connect.NewRequest(&pluxv1.CreateAppRequest{Key: "demo", Name: "Demo"}))
}

// Verifies: SRV-006.
func TestErrorsCarryTheirReasonAndCode(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		err    error
		want   connect.Code
		reason string
		code   string
	}{
		{"permission", plxerr.New(plxerr.PermissionDenied, "app.create is required"), connect.CodePermissionDenied, "PERMISSION_DENIED", "PLX-8030"},
		{"lock", plxerr.New(plxerr.EditingLockHeld, "held by someone else"), connect.CodeFailedPrecondition, "EDITING_LOCK_HELD", "PLX-8020"},
		{"conflict", plxerr.New(plxerr.RevisionConflict, "revision 3"), connect.CodeAborted, "REVISION_CONFLICT", "PLX-1030"},
		{"not found", plxerr.New(plxerr.ResourceNotFound, "no such app"), connect.CodeNotFound, "RESOURCE_NOT_FOUND", "PLX-8031"},
		{"limit", plxerr.New(plxerr.LimitExceeded, "too many plugins"), connect.CodeResourceExhausted, "LIMIT_EXCEEDED", "PLX-1320"},
		{"invalid", plxerr.New(plxerr.InvalidPageToken, "stale token"), connect.CodeInvalidArgument, "INVALID_PAGE_TOKEN", "PLX-1032"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := harness(t, stub{create: func() error { return tc.err }}, api.Deps{})
			_, err := call(context.Background(), c)
			if err == nil {
				t.Fatal("the call succeeded")
			}
			if got := connect.CodeOf(err); got != tc.want {
				t.Errorf("connect code = %s, want %s", got, tc.want)
			}
			info := errorInfo(t, err)
			if info.GetReason() != tc.reason {
				t.Errorf("reason = %q, want %q", info.GetReason(), tc.reason)
			}
			if info.GetDomain() != api.Domain {
				t.Errorf("domain = %q, want %q", info.GetDomain(), api.Domain)
			}
			if info.GetMetadata()["code"] != tc.code {
				t.Errorf("code = %q, want %q", info.GetMetadata()["code"], tc.code)
			}
			if !strings.Contains(info.GetMetadata()["docURL"], strings.ToLower(tc.code)) {
				t.Errorf("docURL = %q", info.GetMetadata()["docURL"])
			}
		})
	}
}

// errorInfo returns the ErrorInfo detail of a Connect error.
func errorInfo(t *testing.T, err error) *errdetails.ErrorInfo {
	t.Helper()
	var connErr *connect.Error
	if !errors.As(err, &connErr) {
		t.Fatalf("not a Connect error: %v", err)
	}
	for _, d := range connErr.Details() {
		value, valueErr := d.Value()
		if valueErr != nil {
			continue
		}
		if info, ok := value.(*errdetails.ErrorInfo); ok {
			return info
		}
	}
	t.Fatalf("no ErrorInfo in %v", err)
	return nil
}

// Verifies: SRV-006.
// An unrecognised failure becomes one internal error whose body says
// nothing but an incident identifier.
func TestUnrecognisedFailuresRevealNothing(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	log := observability.NewLogger(&logs, observability.LogOptions{Level: "debug", Format: "json"})
	c := harness(t, stub{create: func() error {
		return errors.New("dial tcp 10.0.0.5:5432: connection refused")
	}}, api.Deps{Log: log})
	_, err := call(context.Background(), c)
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("code = %s", connect.CodeOf(err))
	}
	if strings.Contains(err.Error(), "10.0.0.5") {
		t.Errorf("the internal detail leaked into %q", err)
	}
	info := errorInfo(t, err)
	if info.GetReason() != "INTERNAL_SERVER_ERROR" || info.GetMetadata()["incident"] == "" {
		t.Errorf("ErrorInfo = %v", info)
	}
	if !strings.Contains(logs.String(), "unhandled failure") || !strings.Contains(logs.String(), "connection refused") {
		t.Errorf("the failure was not logged: %s", logs.String())
	}
}

// Verifies: SRV-007.
// A panic in a handler becomes an internal error, and the process keeps
// serving.
func TestPanicsBecomeInternalErrors(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	log := observability.NewLogger(&logs, observability.LogOptions{Level: "debug", Format: "json"})
	c := harness(t, stub{create: func() error { panic("boom") }}, api.Deps{Log: log})
	if _, err := call(context.Background(), c); connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("code = %s", connect.CodeOf(err))
	}
	if !strings.Contains(logs.String(), "panic in a handler") {
		t.Errorf("the panic was not logged: %s", logs.String())
	}
}

// Verifies: OBS-001, OBS-003.
func TestRequestIDIsEchoedAndLogged(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	log := observability.NewLogger(&logs, observability.LogOptions{Level: "debug", Format: "json"})
	c := harness(t, stub{}, api.Deps{Log: log})
	resp, err := call(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Header().Get("X-Request-Id") == "" {
		t.Error("the response carries no request ID")
	}
	// A caller's own identifier is kept, so a trace spans both sides.
	req := connect.NewRequest(&pluxv1.CreateAppRequest{Key: "demo"})
	req.Header().Set("X-Request-Id", "caller-1")
	resp, err = c.CreateApp(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Header().Get("X-Request-Id"); got != "caller-1" {
		t.Errorf("X-Request-Id = %q; want the caller's", got)
	}
	// An absurdly long one is replaced rather than echoed.
	req = connect.NewRequest(&pluxv1.CreateAppRequest{Key: "demo"})
	req.Header().Set("X-Request-Id", strings.Repeat("x", 200))
	resp, err = c.CreateApp(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Header().Get("X-Request-Id"); len(got) > 64 {
		t.Errorf("X-Request-Id = %q; want a generated one", got)
	}
}

// Verifies: OBS-002.
func TestMetricsRecordEveryCall(t *testing.T) {
	t.Parallel()
	m := observability.NewMetrics()
	c := harness(t, stub{}, api.Deps{Metrics: m})
	if _, err := call(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `service="plux.v1.AppService"`) || !strings.Contains(body, `method="CreateApp"`) {
		t.Errorf("the call was not recorded: %s", body)
	}
}

// Verifies: OBS-001.
func TestTracingWrapsEveryCall(t *testing.T) {
	t.Parallel()
	tr, err := observability.NewTracer(context.Background(), observability.TraceOptions{Service: "test"})
	if err != nil {
		t.Fatal(err)
	}
	c := harness(t, stub{}, api.Deps{Tracer: tr})
	if _, err := call(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	c = harness(t, stub{create: func() error { return plxerr.New(plxerr.PermissionDenied, "no") }}, api.Deps{Tracer: tr})
	if _, err := call(context.Background(), c); err == nil {
		t.Error("the failing call succeeded")
	}
}

// Verifies: SRV-065.
func TestRateLimitRefusesAndSaysWhenToRetry(t *testing.T) {
	t.Parallel()
	var calls int
	limiter := api.RateLimiter{
		Count: func(context.Context, string, time.Duration) (int64, error) {
			calls++
			return int64(calls), nil
		},
		Limit:  2,
		Window: time.Minute,
	}
	c := harness(t, stub{}, api.Deps{RateLimit: limiter.Allow})
	for i := range 2 {
		if _, err := call(context.Background(), c); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	_, err := call(context.Background(), c)
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("code = %s", connect.CodeOf(err))
	}
	var connErr *connect.Error
	if errors.As(err, &connErr) && connErr.Meta().Get("Retry-After") != "60" {
		t.Errorf("Retry-After = %q", connErr.Meta().Get("Retry-After"))
	}
}

// Verifies: SRV-065.
// A cache failure must not close the API: the limit protects the server,
// it is not a correctness property.
func TestRateLimitFailsOpen(t *testing.T) {
	t.Parallel()
	limiter := api.RateLimiter{
		Count: func(context.Context, string, time.Duration) (int64, error) {
			return 0, errors.New("cache is down")
		},
		Limit:  1,
		Window: time.Minute,
	}
	wait, err := limiter.Allow(context.Background(), "k")
	if err != nil || wait != 0 {
		t.Errorf("Allow = %v, %v; want the call to proceed", wait, err)
	}
}

func TestConnectCodeOfAnUnmappedCode(t *testing.T) {
	t.Parallel()
	if got := api.ConnectCode(plxerr.ContentIDCollision); got != connect.CodeInternal {
		t.Errorf("ConnectCode = %s", got)
	}
	if api.Error(nil, "x") != nil {
		t.Error("Error(nil) must be nil")
	}
	// A Connect error made by a handler passes through unchanged.
	made := connect.NewError(connect.CodeUnavailable, errors.New("try later"))
	if got := api.Error(made, "x"); !errors.Is(got, made) {
		t.Errorf("Error changed a Connect error: %v", got)
	}
}

// Verifies: SEC-104.
// The JSON form of the API is what an integrator uses; a malformed body
// is refused rather than reaching a handler.
func TestJSONRequestsAreChecked(t *testing.T) {
	t.Parallel()
	path, handler := pluxv1connect.NewAppServiceHandler(stub{}, connect.WithInterceptors(api.Interceptors(api.Deps{})...))
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		srv.URL+path+"CreateApp", strings.NewReader(`{"key":`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["code"] != "invalid_argument" {
		t.Errorf("body = %v", body)
	}
}
