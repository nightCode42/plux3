// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// maxBody bounds a request body; every request of the apps is far smaller.
const maxBody = 1 << 20

// Options configure the API.
type Options struct {
	// TrackStep is the pause between the status steps of an order's
	// tracking stream.
	TrackStep time.Duration
	// Heartbeat is how often the notification stream sends a comment line
	// to keep the connection open.
	Heartbeat time.Duration
}

// api holds the state the handlers share.
type api struct {
	store *store
	opts  Options
}

// New returns the reference API's handler: the Plux Bank API under /bank
// and the Plux Express API under /express, over fresh seed data.
func New(o Options) http.Handler {
	a := &api{store: newStore(), opts: o}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /bank/v1/login", a.login)
	mux.HandleFunc("GET /bank/v1/accounts", a.authed(a.listAccounts))
	mux.HandleFunc("POST /bank/v1/transfers", a.authed(a.createTransfer))
	mux.HandleFunc("POST /bank/graphql", a.authed(a.graphql))
	mux.HandleFunc("GET /bank/v1/notifications", a.authed(a.notifications))
	mux.HandleFunc("GET /express/v1/products", a.listProducts)
	mux.HandleFunc("GET /express/v1/products/{id}", a.getProduct)
	mux.HandleFunc("POST /express/v1/orders", a.placeOrder)
	mux.HandleFunc("GET /express/v1/orders/{id}/track", a.track)
	mux.HandleFunc("GET /express/v1/courier/jobs", a.listJobs)
	mux.HandleFunc("POST /express/v1/courier/jobs/{id}/deliveries", a.confirmDelivery)
	mux.HandleFunc("GET /express/v1/courier/jobs/{id}/deliveries", a.listDeliveries)
	return mux
}

// Serve serves h on ln over TLS with cfg until ctx ends, then shuts the
// server down gracefully.
func Serve(ctx context.Context, ln net.Listener, h http.Handler, cfg *tls.Config) error {
	srv := &http.Server{
		Handler:           h,
		TLSConfig:         cfg,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ServeTLS(ln, "", "") }()
	select {
	case err := <-errc:
		return fmt.Errorf("serving: %w", err)
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		_ = srv.Close()
	}
	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serving: %w", err)
	}
	return nil
}

// errorBody is the JSON error every endpoint answers with.
type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// writeJSON answers with v as JSON.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError answers with the JSON error body.
func writeError(w http.ResponseWriter, status int, code, message string) {
	var b errorBody
	b.Error.Code, b.Error.Message = code, message
	writeJSON(w, status, b)
}

// readJSON decodes the request body into v; it answers 400 and reports false
// when the body is not the JSON object v describes.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "the body is not valid JSON for this endpoint")
		return false
	}
	return true
}

// bearer returns the token of an Authorization: Bearer header.
func bearer(r *http.Request) (string, bool) {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return "", false
	}
	return token, true
}
