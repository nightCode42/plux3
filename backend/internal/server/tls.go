// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"fmt"
	"net/http"
	"time"

	"github.com/nightCode42/plux3/backend/internal/httpx"
)

// buildTLS applies the transport configuration to the api role's
// listeners: TLS 1.3 on the public one when a certificate is configured
// (SEC-040) and mutual TLS on the internal one (SEC-043).
func (s *Server) buildTLS() error {
	srv := s.cfg.Server
	if srv.TLS.Enabled() {
		tlsConfig, err := httpx.ServerTLS(httpx.ServerTLSOptions{
			CertFile: srv.TLS.CertFile, KeyFile: srv.TLS.KeyFile, AllowTLS12: srv.TLS.AllowTLS12,
		})
		if err != nil {
			return fmt.Errorf("server: %w", err)
		}
		s.http.TLSConfig = tlsConfig
		s.http.Protocols.SetHTTP2(true)
	}
	if srv.InternalListen == "" {
		return nil
	}
	return s.buildInternal()
}

// buildInternal prepares the listener for the traffic between roles. It
// serves the health endpoints and whatever RegisterInternal adds, and
// accepts only a client certificate signed by the internal CA.
func (s *Server) buildInternal() error {
	srv := s.cfg.Server
	tlsConfig, err := httpx.InternalServerTLS(httpx.InternalTLSOptions{
		CAFile: srv.InternalTLS.CAFile, CertFile: srv.InternalTLS.CertFile, KeyFile: srv.InternalTLS.KeyFile,
	})
	if err != nil {
		return fmt.Errorf("server: %w", err)
	}
	s.internalMux.HandleFunc("GET /livez", s.health.Live)
	s.internalMux.HandleFunc("GET /readyz", s.health.Ready)
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetHTTP2(true)
	s.internal = &http.Server{
		Addr:              srv.InternalListen,
		Handler:           s.wrap(s.internalMux),
		Protocols:         protocols,
		TLSConfig:         tlsConfig,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return nil
}
