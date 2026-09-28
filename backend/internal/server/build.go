// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nightCode42/plux3/backend/internal/compiler/media"

	"github.com/nightCode42/plux3/backend/internal/cache"
	"github.com/nightCode42/plux3/backend/internal/config"
	"github.com/nightCode42/plux3/backend/internal/jobs"
	"github.com/nightCode42/plux3/backend/internal/observability"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/objects"
)

// Built is an assembled process together with the function that
// releases everything it opened, in the reverse order it was opened.
type Built struct {
	Server *Server
	Close  func()
}

// Build opens the dependencies the configured roles need and assembles
// a Server. The role decides what is built at all: a process without
// the worker role gets no job client that runs, and — from the next
// milestone — no signing client (L-3, L-4, ADR-0006).
func Build(ctx context.Context, cfg *config.Config, log *slog.Logger, version string) (*Built, error) {
	var closers []func()
	closeAll := func() {
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]()
		}
	}
	fail := func(err error) (*Built, error) {
		closeAll()
		return nil, err
	}

	metrics := observability.NewMetrics()
	tracer, err := observability.NewTracer(ctx, observability.TraceOptions{
		Endpoint:    cfg.Observability.OTLPEndpoint,
		SampleRatio: cfg.Observability.TraceSampleRatio,
		Service:     "plux-server",
		Role:        RoleLabel(cfg),
		Version:     version,
	})
	if err != nil {
		return fail(err)
	}

	db, err := storage.Open(ctx, storage.Options{
		URL:            cfg.Database.URL.Value(),
		MaxConnections: cfg.Database.MaxConnections,
		ConnectTimeout: 15 * time.Second,
		Log:            log,
	})
	if err != nil {
		return fail(err)
	}
	closers = append(closers, db.Close)

	if cfg.Database.MigrateOnStart {
		if err := migrateAll(ctx, db); err != nil {
			return fail(err)
		}
	}

	store, err := buildObjects(ctx, cfg)
	if err != nil {
		return fail(err)
	}

	shared, err := buildCache(cfg)
	if err != nil {
		return fail(err)
	}
	closers = append(closers, func() { _ = shared.Close() })

	limitSet, err := cfg.LimitSet()
	if err != nil {
		return fail(err)
	}

	services, jobClient, err := buildWork(ctx, cfg, log, db, shared, limitSet, store)
	if err != nil {
		return fail(err)
	}

	srv, err := New(Deps{
		Config: cfg, Log: log, Metrics: metrics, Tracer: tracer,
		DB: db, Objects: store, Cache: shared, Jobs: jobClient, Limits: limitSet,
	})
	if err != nil {
		return fail(err)
	}
	if cfg.Has(config.RoleAPI) {
		srv.RegisterAPI(services)
	}
	return &Built{Server: srv, Close: closeAll}, nil
}

// buildWork assembles the domain services and the job client that runs
// their background work (SRV-024).
func buildWork(ctx context.Context, cfg *config.Config, log *slog.Logger, db *storage.DB, shared cache.Cache, set limits.Set, store objects.Store) (*Services, *jobs.Client, error) {
	backend, err := BuildSigning(cfg)
	if err != nil {
		return nil, nil, err
	}
	if !backend.AllowedInProduction() {
		log.WarnContext(ctx, "the file signing backend keeps keys on disk; it is for development only, and production environments refuse it (SEC-056, SEC-120)",
			slog.String("directory", cfg.Signing.Directory))
	}
	queue := &JobQueue{}
	deps := WorkDeps{Objects: store, Queue: queue}
	if cfg.Has(config.RoleWorker) {
		// Only the worker transcodes and signs: compiling the codecs costs
		// start-up time the api role need not pay, and the api role must
		// never hold a signer (SRV-052, ADR-0006).
		if deps.Codecs, err = media.NewCodecs(ctx); err != nil {
			return nil, nil, fmt.Errorf("server: %w", err)
		}
		deps.Signer = backend
	}
	services, err := BuildServices(ctx, cfg, db, shared, set, backend, deps)
	if err != nil {
		return nil, nil, err
	}
	workers := jobs.NewWorkers()
	if deps.Codecs != nil {
		jobs.AddWorker(workers, &assetWorker{svc: services})
	}
	if deps.Signer != nil && services.Releases != nil {
		jobs.AddWorker(workers, &publishWorker{svc: services})
	}
	jobClient, err := jobs.New(jobs.Options{
		Pool:     db.Pool(),
		Workers:  workers,
		Run:      cfg.Has(config.RoleWorker),
		Periodic: MaintenanceJobs(workers, services, log),
		Log:      log,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("server: %w", err)
	}
	queue.client = jobClient
	return services, jobClient, nil
}

// migrateAll applies the server's migrations and River's (SRV-021).
func migrateAll(ctx context.Context, db *storage.DB) error {
	migrations, err := storage.LoadMigrations(storage.Migrations())
	if err != nil {
		return fmt.Errorf("server: %w", err)
	}
	if err := db.Migrate(ctx, migrations); err != nil {
		return fmt.Errorf("server: %w", err)
	}
	if err := jobs.Migrate(ctx, db.Pool()); err != nil {
		return fmt.Errorf("server: %w", err)
	}
	return nil
}

// buildObjects opens the configured object store (SRV-023).
func buildObjects(ctx context.Context, cfg *config.Config) (objects.Store, error) {
	o := cfg.ObjectStorage
	switch o.Backend {
	case "filesystem":
		store, err := objects.NewFilesystem(o.Directory, o.CDNBaseURL)
		if err != nil {
			return nil, fmt.Errorf("server: object storage: %w", err)
		}
		return store, nil
	case "s3":
		store, err := objects.NewS3(ctx, objects.S3Options{
			Endpoint:        o.Endpoint,
			Region:          o.Region,
			Bucket:          o.Bucket,
			PathStyle:       o.PathStyle,
			AccessKeyID:     o.AccessKeyID,
			SecretAccessKey: o.SecretAccessKey.Value(),
			CDNBaseURL:      o.CDNBaseURL,
		})
		if err != nil {
			return nil, fmt.Errorf("server: object storage: %w", err)
		}
		return store, nil
	default:
		return nil, fmt.Errorf("server: unknown object storage backend %q", o.Backend)
	}
}

// buildCache opens the configured shared cache.
func buildCache(cfg *config.Config) (cache.Cache, error) {
	switch cfg.Cache.Backend {
	case "memory":
		return cache.NewMemory(nil), nil
	case "valkey":
		shared, err := cache.NewValkey(cfg.Cache.ValkeyURL.Value())
		if err != nil {
			return nil, fmt.Errorf("server: cache: %w", err)
		}
		return shared, nil
	default:
		return nil, fmt.Errorf("server: unknown cache backend %q", cfg.Cache.Backend)
	}
}

// RoleLabel renders the roles of a process, such as "api+worker", for
// logs and the trace resource.
func RoleLabel(cfg *config.Config) string {
	parts := make([]string, len(cfg.Server.Roles))
	for i, r := range cfg.Server.Roles {
		parts[i] = string(r)
	}
	return strings.Join(parts, "+")
}
