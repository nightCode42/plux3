// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package storage

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB is the connection pool and the only handle on PostgreSQL. It is
// safe for concurrent use.
type DB struct {
	pool *pgxpool.Pool
	log  *slog.Logger
}

// Options configures Open.
type Options struct {
	// URL is the libpq connection string.
	URL string
	// MaxConnections bounds the pool.
	MaxConnections int
	// ConnectTimeout bounds the first connection attempt.
	ConnectTimeout time.Duration
	// Log receives migration and pool events; it may be nil.
	Log *slog.Logger
}

// Open connects to PostgreSQL and verifies that the server answers. It
// does not migrate; call Migrate explicitly, so that a read-only replica
// or a command that must not write never migrates by accident.
func Open(ctx context.Context, opts Options) (*DB, error) {
	if opts.URL == "" {
		return nil, ErrNoDatabase
	}
	cfg, err := pgxpool.ParseConfig(opts.URL)
	if err != nil {
		// The URL carries a password, so it is never echoed.
		return nil, fmt.Errorf("database URL is not valid: %w", redactURL(err))
	}
	if opts.MaxConnections > 0 {
		cfg.MaxConns = int32(min(opts.MaxConnections, math.MaxInt32))
	}
	if opts.ConnectTimeout > 0 {
		cfg.ConnConfig.ConnectTimeout = opts.ConnectTimeout
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect to PostgreSQL: %w", redactURL(err))
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to PostgreSQL: %w", redactURL(err))
	}
	return &DB{pool: pool, log: opts.Log}, nil
}

// Close releases the pool.
func (db *DB) Close() { db.pool.Close() }

// Pool exposes the pool to the repositories in this package. It is
// deliberately not part of any interface a domain package sees (L-2).
func (db *DB) Pool() *pgxpool.Pool { return db.pool }

// Ping reports whether the database answers, for /readyz (SRV-007).
func (db *DB) Ping(ctx context.Context) error {
	if err := db.pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping PostgreSQL: %w", redactURL(err))
	}
	return nil
}

// Migrate applies the pending migrations on one connection, under the
// advisory lock (SRV-021).
func (db *DB) Migrate(ctx context.Context, migrations []Migration) error {
	c, err := db.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire a connection to migrate: %w", err)
	}
	defer c.Release()
	return Migrate(ctx, c.Conn(), migrations, db.log)
}

// Tenant is the organisation a transaction acts for. The zero value is
// the installation scope, which sees no tenant rows at all.
type Tenant struct {
	// OrganizationID is the organisation, or "" outside any tenant.
	OrganizationID string
}

// InTx runs f in a transaction bound to a tenant. The organisation is
// set for the transaction only, with set_config's local flag, so a
// pooled connection never carries it to the next caller (SRV-022).
//
// f must not use the transaction after it returns. A panic rolls back and
// is re-raised, so a bug cannot leave a transaction open.
func (db *DB) InTx(ctx context.Context, t Tenant, f func(context.Context, pgx.Tx) error) error {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()
	if _, err := tx.Exec(ctx, `SELECT set_config('plux.organization_id', $1, true)`, t.OrganizationID); err != nil {
		return fmt.Errorf("set the tenant: %w", err)
	}
	if err := f(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	committed = true
	return nil
}

// redactURL removes a connection string from an error, so that a password
// in it cannot reach a log (OBS-003).
func redactURL(err error) error {
	return sanitised{err}
}

// sanitised hides the detail of an error that may quote a URL.
type sanitised struct{ err error }

// Error returns the message with anything after a "://" removed.
func (s sanitised) Error() string {
	msg := s.err.Error()
	if i := indexOf(msg, "://"); i >= 0 {
		return msg[:i] + "://[redacted]"
	}
	return msg
}

// Unwrap keeps errors.Is and errors.As working.
func (s sanitised) Unwrap() error { return s.err }

// indexOf is strings.Index, kept local so that this file has no import
// that could tempt a caller to format the URL back in.
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
