// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"slices"
	"time"

	"connectrpc.com/connect"

	"github.com/nightCode42/plux3/backend/internal/api"
	"github.com/nightCode42/plux3/backend/internal/cache"
	"github.com/nightCode42/plux3/backend/internal/config"
	"github.com/nightCode42/plux3/backend/internal/httpx"
	"github.com/nightCode42/plux3/backend/internal/jobs"
	"github.com/nightCode42/plux3/backend/internal/observability"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/objects"
)

// Component is one runnable part of the process. Start returns once the
// component is running; Stop drains it within the context's deadline.
type Component interface {
	// Name identifies the component in logs and in /readyz.
	Name() string
	// Start begins the component's work.
	Start(context.Context) error
	// Stop drains it.
	Stop(context.Context) error
}

// Server is an assembled process.
type Server struct {
	cfg     *config.Config
	log     *slog.Logger
	metrics *observability.Metrics
	tracer  *observability.Tracer
	health  *Health

	db      *storage.DB
	objects objects.Store
	cache   cache.Cache
	jobs    *jobs.Client
	limits  limits.Set
	// trusted are the proxies whose X-Forwarded-For is believed.
	trusted []netip.Prefix

	// components run in the order they were added and stop in reverse.
	components []Component
	// kms checks the key management service for /readyz; nil for none.
	kms func(context.Context) error
	// mux serves the api role's HTTP surface.
	mux *http.ServeMux
	// http is the listener of the api role; nil without it.
	http *http.Server
	// internalMux and internal serve the traffic between roles over
	// mutual TLS (SEC-043); internal is nil unless one is configured.
	internalMux *http.ServeMux
	internal    *http.Server
	// hsts is the Strict-Transport-Security value; empty disables it.
	hsts string
}

// Deps are the already-built dependencies a Server is assembled from.
// Building them outside the server keeps the wiring visible and lets a
// test substitute any of them.
type Deps struct {
	Config  *config.Config
	Log     *slog.Logger
	Metrics *observability.Metrics
	Tracer  *observability.Tracer
	DB      *storage.DB
	Objects objects.Store
	Cache   cache.Cache
	Jobs    *jobs.Client
	Limits  limits.Set
	// KMS checks the key management service; nil when none is used.
	KMS func(context.Context) error
}

// New assembles a process from its dependencies. It opens no
// connections and starts nothing; call Run.
func New(d Deps) (*Server, error) {
	if d.Config == nil {
		return nil, errors.New("server: a configuration is required")
	}
	if d.Log == nil {
		return nil, errors.New("server: a logger is required")
	}
	s := &Server{
		cfg: d.Config, log: d.Log, metrics: d.Metrics, tracer: d.Tracer,
		db: d.DB, objects: d.Objects, cache: d.Cache, jobs: d.Jobs, limits: d.Limits,
		health: NewHealth(), mux: http.NewServeMux(), internalMux: http.NewServeMux(), kms: d.KMS,
	}
	if s.limits.IsZero() {
		s.limits = limits.Defaults()
	}
	for _, cidr := range d.Config.Server.TrustedProxies {
		prefix, err := netip.ParsePrefix(cidr)
		if err != nil {
			return nil, fmt.Errorf("server: trusted proxy %q: %w", cidr, err)
		}
		s.trusted = append(s.trusted, prefix)
	}
	s.registerChecks()
	if s.cfg.Has(config.RoleAPI) {
		if err := s.buildHTTP(); err != nil {
			return nil, err
		}
	}
	if s.cfg.Has(config.RoleWorker) && s.jobs != nil {
		s.components = append(s.components, jobComponent{client: s.jobs})
	}
	return s, nil
}

// registerChecks adds the dependencies /readyz reports on (SRV-007).
func (s *Server) registerChecks() {
	if s.db != nil {
		s.health.Register("postgres", s.db.Ping)
	}
	if s.cache != nil {
		s.health.Register("cache", s.cache.Ping)
	}
	if s.objects != nil {
		s.health.Register("objectStorage", func(ctx context.Context) error {
			// A missing key is a healthy answer: it proves the service
			// answers without writing anything.
			_, err := s.objects.Stat(ctx, "bundles/00/"+emptyDigest)
			if err == nil || errors.Is(err, objects.ErrNotFound) {
				return nil
			}
			return fmt.Errorf("object storage: %w", err)
		})
	}
	if s.kms != nil {
		s.health.Register("kms", s.kms)
	}
}

// emptyDigest is the SHA-256 of no bytes, used as a key that is never
// written, only asked about.
const emptyDigest = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// Handler returns the api role's HTTP handler, for tests.
func (s *Server) Handler() http.Handler { return s.wrap(s.mux) }

// HealthEndpoint returns the health endpoint, so components can
// register checks of their own.
func (s *Server) HealthEndpoint() *Health { return s.health }

// Interceptors returns the chain every service handler is registered
// with (ADR-0005). before are the interceptors a service adds inside the
// standard chain, such as authentication; they run before the address
// rate limit's per-principal counterpart.
func (s *Server) Interceptors(before ...api.Around) []connect.Interceptor {
	d := api.Deps{Log: s.log, Metrics: s.metrics, Tracer: s.tracer}
	if s.cache != nil {
		d.Before = append(d.Before, s.addressLimit())
	}
	d.Before = append(d.Before, before...)
	return api.Interceptors(d)
}

// addressLimit counts every call against the client's address, so no
// single client — authenticated or not — can exhaust the API (SRV-065).
// The allowance is a registry limit (LIM-001).
func (s *Server) addressLimit() api.Around {
	limiter := api.RateLimiter{Count: s.cache.Increment, Window: time.Minute}
	limit := s.limits.Get(limits.APIRequestsPerMinutePerAddress)
	return api.RateLimit(func(ctx context.Context, c api.Call) (time.Duration, error) {
		addr := api.ClientAddress(c.PeerAddress, c.Header, s.trusted)
		return limiter.Allow(ctx, "rate:address:"+addr, limit)
	})
}

// Register adds a Connect service handler to the api role's mux.
func (s *Server) Register(path string, h http.Handler) { s.mux.Handle(path, h) }

// AddComponent adds a component to start and stop with the process.
func (s *Server) AddComponent(c Component) { s.components = append(s.components, c) }

// RegisterInternal adds a handler to the internal listener, which only a
// role holding a certificate of the internal CA can reach (SEC-043). It
// is served only when server.internalListen is configured.
func (s *Server) RegisterInternal(path string, h http.Handler) { s.internalMux.Handle(path, h) }

// buildHTTP registers the endpoints the api role always serves and
// prepares its listeners.
func (s *Server) buildHTTP() error {
	h := s.cfg.Server.HSTS
	s.hsts = httpx.HSTSValue(h.MaxAge.Duration(), h.IncludeSubDomains, h.Preload)
	s.mux.HandleFunc("GET /livez", s.health.Live)
	s.mux.HandleFunc("GET /readyz", s.health.Ready)
	if s.metrics != nil && s.cfg.Observability.MetricsListen == "" {
		s.mux.Handle("GET /metrics", s.metrics.Handler())
	}
	// HTTP/2 without TLS is enabled because TLS is terminated by the
	// ingress and gRPC clients need HTTP/2 (ADR-0005).
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	s.http = &http.Server{
		Addr:              s.cfg.Server.Listen,
		Handler:           s.wrap(s.mux),
		Protocols:         protocols,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return s.buildTLS()
}

// wrap applies the HTTP-level guards every request passes. HSTS is the
// outermost, so that every response carries it (SEC-040).
func (s *Server) wrap(h http.Handler) http.Handler {
	return httpx.HSTS(httpx.MaxBytes(h, s.limits.Get(limits.APIRequestSize)), s.hsts)
}

// Run starts every component, serves until the context is cancelled,
// then drains (SRV-007).
func (s *Server) Run(ctx context.Context) error {
	s.log.InfoContext(ctx, "starting",
		slog.String("listen", s.cfg.Server.Listen),
		slog.String("role", RoleLabel(s.cfg)),
	)
	for _, w := range s.cfg.Warnings() {
		s.log.WarnContext(ctx, w)
	}
	started, err := s.startComponents(ctx)
	if err != nil {
		s.health.SetReady(false)
		s.stopComponents(ctx, started)
		return err
	}

	serving := s.listen(ctx)
	s.health.SetReady(true)

	select {
	case err := <-serving:
		s.health.SetReady(false)
		s.stopComponents(ctx, started)
		return err
	case <-ctx.Done():
	}

	// Stop reporting ready before the listener closes, so that a load
	// balancer drains this replica first.
	s.health.SetReady(false)
	err = s.shutdown(ctx)
	s.stopComponents(ctx, started)
	s.log.InfoContext(ctx, "stopped")
	return err
}

// startComponents starts each component in order and returns those that
// started, so a failure can unwind exactly what is running.
func (s *Server) startComponents(ctx context.Context) ([]Component, error) {
	started := make([]Component, 0, len(s.components))
	for _, c := range s.components {
		if err := c.Start(ctx); err != nil {
			return started, fmt.Errorf("start %s: %w", c.Name(), err)
		}
		started = append(started, c)
		s.log.InfoContext(ctx, "component started", slog.String("component", c.Name()))
	}
	return started, nil
}

// stopComponents stops components in the reverse order they started,
// within the configured grace period.
func (s *Server) stopComponents(ctx context.Context, started []Component) {
	grace, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.cfg.Server.ShutdownGrace.Duration())
	defer cancel()
	for _, c := range slices.Backward(started) {
		if err := c.Stop(grace); err != nil {
			s.log.ErrorContext(grace, "component did not stop cleanly",
				slog.String("component", c.Name()), slog.String("error", err.Error()))
		}
	}
}

// listen serves the api role in the background and reports why it
// stopped. A process without the api role never sends on the channel.
func (s *Server) listen(ctx context.Context) <-chan error {
	errs := make(chan error, 2)
	for _, srv := range []*http.Server{s.http, s.internal} {
		if srv != nil {
			go s.serve(ctx, srv, errs)
		}
	}
	return errs
}

// serve runs one listener until it stops, over TLS when it has a TLS
// configuration.
func (s *Server) serve(ctx context.Context, srv *http.Server, errs chan<- error) {
	s.log.InfoContext(ctx, "listening", slog.String("address", srv.Addr), slog.Bool("tls", srv.TLSConfig != nil))
	var err error
	if srv.TLSConfig != nil {
		// The certificate is in TLSConfig, so the file names stay empty.
		err = srv.ListenAndServeTLS("", "")
	} else {
		err = srv.ListenAndServe()
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		errs <- fmt.Errorf("listen %s: %w", srv.Addr, err)
		return
	}
	errs <- nil
}

// shutdown closes the listener and flushes traces.
func (s *Server) shutdown(ctx context.Context) error {
	grace, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.cfg.Server.ShutdownGrace.Duration())
	defer cancel()
	var err error
	for _, srv := range []*http.Server{s.http, s.internal} {
		if srv == nil {
			continue
		}
		if shutdownErr := srv.Shutdown(grace); shutdownErr != nil && err == nil {
			err = fmt.Errorf("shut down the listener: %w", shutdownErr)
		}
	}
	if s.tracer != nil {
		if flushErr := s.tracer.Shutdown(grace); flushErr != nil && err == nil {
			err = fmt.Errorf("flush traces: %w", flushErr)
		}
	}
	return err
}

// jobComponent runs the worker role's queues.
type jobComponent struct{ client *jobs.Client }

// Name identifies the component.
func (jobComponent) Name() string { return "jobs" }

// Start begins working jobs.
func (j jobComponent) Start(ctx context.Context) error {
	if err := j.client.Start(ctx); err != nil {
		return fmt.Errorf("jobs: %w", err)
	}
	return nil
}

// Stop drains the queues.
func (j jobComponent) Stop(ctx context.Context) error {
	if err := j.client.Stop(ctx); err != nil {
		return fmt.Errorf("jobs: %w", err)
	}
	return nil
}
