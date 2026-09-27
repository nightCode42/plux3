// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nightCode42/plux3/backend/internal/cache"
	"github.com/nightCode42/plux3/backend/internal/config"
	"github.com/nightCode42/plux3/backend/internal/jobs"
	"github.com/nightCode42/plux3/backend/internal/observability"
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
		migrations, err := storage.LoadMigrations(storage.Migrations())
		if err != nil {
			return fail(err)
		}
		if err := db.Migrate(ctx, migrations); err != nil {
			return fail(err)
		}
		if err := jobs.Migrate(ctx, db.Pool()); err != nil {
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

	jobClient, err := jobs.New(jobs.Options{
		Pool: db.Pool(),
		Run:  cfg.Has(config.RoleWorker),
		Log:  log,
	})
	if err != nil {
		return fail(err)
	}

	limitSet, err := cfg.LimitSet()
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
	return &Built{Server: srv, Close: closeAll}, nil
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
