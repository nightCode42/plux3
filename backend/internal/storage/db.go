// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package storage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
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
	var super, bypass bool
	if err := pool.QueryRow(ctx, `SELECT rolsuper, rolbypassrls FROM pg_roles WHERE rolname = current_user`).Scan(&super, &bypass); err != nil {
		pool.Close()
		return nil, fmt.Errorf("read the database role: %w", redactURL(err))
	}
	if err := checkRole(super, bypass); err != nil {
		pool.Close()
		return nil, err
	}
	return &DB{pool: pool, log: opts.Log}, nil
}

// checkRole refuses a database role that row-level security does not
// bind: a superuser or a role with BYPASSRLS would see and write every
// organisation's rows, so tenant isolation would rest on the service
// layer alone (SRV-022, SEC-102).
func checkRole(super, bypass bool) error {
	switch {
	case super:
		return errors.New("the database role is a superuser, which row-level security does not bind; connect as a role without SUPERUSER (SRV-022)")
	case bypass:
		return errors.New("the database role has BYPASSRLS, so row-level security does not bind it; connect as a role without it (SRV-022)")
	}
	return nil
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

// Tenant is what a transaction may see. The zero value sees no tenant
// rows at all.
type Tenant struct {
	// OrganizationID is the organisation, or "" outside any tenant.
	OrganizationID string
	// UserID is the signed-in user, who may read their own memberships
	// in every organisation; "" for none.
	UserID string
	// Scope widens the policies of a few tables for one purpose: "" for
	// none, ScopeAuthentication or ScopeInstallation.
	Scope Scope
}

// Scope names a purpose for which row-level security admits more than
// the organisation's own rows. Each is declared by the one piece of code
// that needs it, never on a caller's behalf.
type Scope string

// The scopes the policies know.
const (
	// ScopeAuthentication lets a credential be found by its hash before
	// its organisation is known.
	ScopeAuthentication Scope = "authentication"
	// ScopeInstallation reads and writes the audit entries that belong
	// to no organisation, such as a sign-in.
	ScopeInstallation Scope = "installation"
	// ScopeRegistration lets a registering device find its app and
	// environment before its organisation is known.
	ScopeRegistration Scope = "registration"
	// ScopeMetadata lets the update metadata of an environment be read
	// by its identifier alone: the files are signed and public, and a
	// device fetches them before it can authenticate (SEC-050).
	ScopeMetadata Scope = "metadata"
)

// InTx runs f in a transaction bound to a tenant. The settings are set
// for the transaction only, with set_config's local flag, so a pooled
// connection never carries them to the next caller (SRV-022).
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
			// A cancelled request must still roll back, so the rollback
			// does not inherit the cancellation.
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()
	if err := Rescope(ctx, tx, t); err != nil {
		return err
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

// Rescope changes what the rest of a transaction may see. It exists for
// the rare flow that learns its organisation from a row it has just
// locked, such as a device authorization approved for one; everything
// else sets the tenant once, in InTx.
func Rescope(ctx context.Context, tx pgx.Tx, t Tenant) error {
	if _, err := tx.Exec(ctx,
		`SELECT set_config('plux.organization_id', $1, true),
		        set_config('plux.user_id', $2, true),
		        set_config('plux.scope', $3, true)`,
		t.OrganizationID, t.UserID, string(t.Scope)); err != nil {
		return fmt.Errorf("set the tenant: %w", err)
	}
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
	if i := strings.Index(msg, "://"); i >= 0 {
		return msg[:i] + "://[redacted]"
	}
	return msg
}

// Unwrap keeps errors.Is and errors.As working.
func (s sanitised) Unwrap() error { return s.err }
