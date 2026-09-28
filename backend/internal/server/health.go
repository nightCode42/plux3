// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

// Package server assembles a process from its configured roles and runs
// it (SRV-001, ADR-0006).
//
// The role decides which dependencies exist at all: a process without
// the worker role never builds a signing client, so a bug in a handler
// cannot reach a key (L-3); a process without the api role opens no
// listener. Components start in dependency order and stop in reverse,
// draining requests and jobs within the configured grace period
// (SRV-007).
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"sync"
	"time"
)

// Check reports whether one dependency is usable. It must return
// quickly and must not retry: /readyz is polled.
type Check func(context.Context) error

// Health answers /livez and /readyz (SRV-007).
type Health struct {
	mu     sync.RWMutex
	checks map[string]Check
	// ready is false until the process has finished starting, and false
	// again once it begins to shut down, so a load balancer stops
	// sending work before the listener closes.
	ready bool
	// timeout bounds one round of checks.
	timeout time.Duration
}

// NewHealth returns a health endpoint that is not yet ready.
func NewHealth() *Health {
	return &Health{checks: map[string]Check{}, timeout: 2 * time.Second}
}

// Register adds a dependency to /readyz.
func (h *Health) Register(name string, c Check) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.checks[name] = c
}

// SetReady marks the process ready, or not.
func (h *Health) SetReady(ready bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ready = ready
}

// Live reports that the process is running. It checks nothing else, so
// a dependency being down never restarts a healthy process.
func (*Health) Live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// Ready reports whether the process can serve: it has finished starting
// and every registered dependency answers (SRV-007).
func (h *Health) Ready(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	ready := h.ready
	checks := make(map[string]Check, len(h.checks))
	for name, c := range h.checks {
		checks[name] = c
	}
	timeout := h.timeout
	h.mu.RUnlock()

	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	results := make(map[string]string, len(checks))
	names := make([]string, 0, len(checks))
	for name := range checks {
		names = append(names, name)
	}
	sort.Strings(names)
	ok := ready
	for _, name := range names {
		if err := checks[name](ctx); err != nil {
			// The message may quote a connection string, so only the
			// fact of the failure is returned; the detail is logged by
			// the component that owns it.
			results[name] = "unavailable"
			ok = false
			continue
		}
		results[name] = "ok"
	}
	status := http.StatusOK
	body := map[string]any{"status": "ok", "checks": results}
	if !ok {
		status = http.StatusServiceUnavailable
		body["status"] = "unavailable"
		if !ready {
			body["reason"] = "starting or shutting down"
		}
	}
	writeJSON(w, status, body)
}

// writeJSON writes a small JSON body.
func writeJSON(w http.ResponseWriter, status int, body map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
