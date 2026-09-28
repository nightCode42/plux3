// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeServer answers the four calls the tool makes; failAfter > 0 makes
// later manifest checks fail.
func fakeServer(t *testing.T, failAfter int64) *httptest.Server {
	t.Helper()
	var checks atomic.Int64
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, v any) { _ = json.NewEncoder(w).Encode(v) }
	mux.HandleFunc("POST /plux.v1.DeviceService/RegisterDevice", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Forwarded-For") == "" {
			w.WriteHeader(http.StatusBadRequest)
		}
		reply(w, map[string]any{"device": map[string]any{"id": "d"}, "deviceSecret": "s"})
	})
	mux.HandleFunc("POST /plux.v1.TokenService/IssueDeviceToken", func(w http.ResponseWriter, _ *http.Request) {
		reply(w, map[string]any{"accessToken": "t"})
	})
	mux.HandleFunc("POST /plux.v1.ManifestService/GetManifest", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch {
		case body["ifNoneMatch"] == "\"e\"":
			if failAfter > 0 && checks.Add(1) > failAfter {
				w.WriteHeader(http.StatusTooManyRequests)
				reply(w, map[string]any{})
				return
			}
			_, _ = w.Write([]byte(`{"notModified":true,"etag":"\"e\""}`))
		case body["installed"] != nil:
			reply(w, map[string]any{"etag": "\"e\""})
		default:
			reply(w, map[string]any{"manifest": map[string]any{
				"releaseSequence": 1, "appBundle": map[string]any{"sha256": "sha256:a"},
				"plugins": []any{map[string]any{"key": "p", "bundle": map[string]any{"sha256": "sha256:b"}}},
			}})
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// Verifies: NFR-020.
func TestManifestLoad(t *testing.T) {
	t.Parallel()
	srv := fakeServer(t, 0)
	var out, errOut bytes.Buffer
	code := run([]string{"-server", srv.URL, "-app", "a", "-devices", "5", "-rate", "200", "-duration", "300ms", "-p99", "5s"}, &out, &errOut)
	if code != 0 || !strings.Contains(out.String(), "failures 0") {
		t.Fatalf("exit %d: %s %s", code, out.String(), errOut.String())
	}
}

func TestManifestLoadFails(t *testing.T) {
	t.Parallel()
	srv := fakeServer(t, 3)
	var out, errOut bytes.Buffer
	if code := run([]string{"-server", srv.URL, "-app", "a", "-devices", "2", "-rate", "100", "-duration", "200ms"}, &out, &errOut); code != 1 ||
		!strings.Contains(out.String(), "status 429") {
		t.Errorf("failing checks: exit %d: %s", code, out.String())
	}
	if code := run([]string{"-server", "http://127.0.0.1:1", "-app", "a", "-devices", "1"}, &out, &errOut); code != 1 {
		t.Errorf("an unreachable server: exit %d", code)
	}
	if code := run([]string{"-rate", "1"}, &out, &errOut); code != 2 {
		t.Errorf("no app: exit %d", code)
	}
	if got := (result{}).p(0.5); got != time.Duration(0) {
		t.Errorf("an empty result's percentile: %v", got)
	}
}
