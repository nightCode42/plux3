// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package storage

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Migrations returns the migrations compiled into the binary.
func Migrations() fs.FS {
	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		panic("storage: embedded migrations: " + err.Error())
	}
	return sub
}

// advisoryLockID is the key of the advisory lock migrations are applied
// under. It is an arbitrary constant, chosen once and never changed, so
// that every replica of every version takes the same lock (SRV-021).
const advisoryLockID int64 = 0x504c5558 // "PLUX"

// migrationName matches "0001_init.sql".
var migrationName = regexp.MustCompile(`^(\d{4})_([a-z0-9_]+)\.sql$`)

// Migration is one numbered step.
type Migration struct {
	// Version is the number in the file name; versions are contiguous
	// from 1, so a gap is a merge mistake rather than a valid state.
	Version int
	// Name is the descriptive part of the file name.
	Name string
	// SQL is the whole file, applied as one statement batch.
	SQL string
	// Checksum is the SHA-256 of SQL, recorded when the migration is
	// applied so that editing a released migration is detected.
	Checksum string
}

// LoadMigrations reads and orders the migrations of a filesystem.
func LoadMigrations(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	var out []Migration
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := migrationName.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("migration %q: name must be NNNN_lower_snake.sql", e.Name())
		}
		body, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		v, _ := strconv.Atoi(m[1])
		sum := sha256.Sum256(body)
		out = append(out, Migration{Version: v, Name: m[2], SQL: string(body), Checksum: hex.EncodeToString(sum[:])})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	for i, m := range out {
		if m.Version != i+1 {
			return nil, fmt.Errorf("migration %04d_%s: versions must run from 0001 without gaps", m.Version, m.Name)
		}
	}
	return out, nil
}

// Conn is the part of a connection that Migrate needs. Migrate takes a
// single connection rather than a pool because an advisory lock belongs
// to the session that took it.
type Conn interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Migrate applies every migration that has not been applied yet, in
// order, under an advisory lock (SRV-021). It is safe to call from every
// replica at start-up: the first takes the lock and applies, the others
// wait and then find nothing to do.
func Migrate(ctx context.Context, c Conn, migrations []Migration, log *slog.Logger) error {
	if _, err := c.Exec(ctx, `SELECT pg_advisory_lock($1)`, advisoryLockID); err != nil {
		return fmt.Errorf("take the migration lock: %w", err)
	}
	defer func() {
		if _, err := c.Exec(ctx, `SELECT pg_advisory_unlock($1)`, advisoryLockID); err != nil && log != nil {
			log.WarnContext(ctx, "could not release the migration lock", slog.String("error", err.Error()))
		}
	}()

	if _, err := c.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS plux_migrations (
			version    integer     PRIMARY KEY,
			name       text        NOT NULL,
			checksum   text        NOT NULL,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("create the migration table: %w", err)
	}

	applied, err := appliedMigrations(ctx, c)
	if err != nil {
		return err
	}
	for _, m := range migrations {
		if sum, ok := applied[m.Version]; ok {
			if sum != m.Checksum {
				return fmt.Errorf("migration %04d_%s was changed after it was applied; migrations are append-only", m.Version, m.Name)
			}
			continue
		}
		if err := applyMigration(ctx, c, m); err != nil {
			return err
		}
		if log != nil {
			log.InfoContext(ctx, "migration applied",
				slog.Int("version", m.Version), slog.String("name", m.Name))
		}
	}
	return nil
}

// appliedMigrations reads what the database already has.
func appliedMigrations(ctx context.Context, c Conn) (map[int]string, error) {
	rows, err := c.Query(ctx, `SELECT version, checksum FROM plux_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read applied migrations: %w", err)
	}
	defer rows.Close()
	applied := map[int]string{}
	for rows.Next() {
		var v int
		var sum string
		if err := rows.Scan(&v, &sum); err != nil {
			return nil, fmt.Errorf("read applied migrations: %w", err)
		}
		applied[v] = sum
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read applied migrations: %w", err)
	}
	return applied, nil
}

// applyMigration runs one migration and records it. PostgreSQL runs a
// multi-statement Exec in one implicit transaction, so a migration that
// fails half way leaves nothing behind.
func applyMigration(ctx context.Context, c Conn, m Migration) error {
	if _, err := c.Exec(ctx, m.SQL); err != nil {
		return fmt.Errorf("migration %04d_%s: %w", m.Version, m.Name, err)
	}
	if _, err := c.Exec(ctx,
		`INSERT INTO plux_migrations (version, name, checksum) VALUES ($1, $2, $3)`,
		m.Version, m.Name, m.Checksum); err != nil {
		return fmt.Errorf("record migration %04d_%s: %w", m.Version, m.Name, err)
	}
	return nil
}

// ExpandContract reports whether a migration follows the expand/contract
// rule: it may add and backfill, but it may not drop or retype a column
// or a table in the same release that stops using it, because an N-1
// binary is still running during a rolling upgrade (DEP-030).
//
// It is a lint over the SQL text, not a proof: it catches the statements
// that break a rolling upgrade outright.
func ExpandContract(m Migration) error {
	var found []string
	for _, stmt := range []string{"DROP TABLE", "DROP COLUMN", "ALTER COLUMN", "RENAME TO", "RENAME COLUMN"} {
		if strings.Contains(strings.ToUpper(m.SQL), stmt) {
			found = append(found, stmt)
		}
	}
	if len(found) == 0 {
		return nil
	}
	return fmt.Errorf("migration %04d_%s uses %s; an N-1 binary is still running during a rolling upgrade, so add and backfill now and contract in a later release (DEP-030)",
		m.Version, m.Name, strings.Join(found, ", "))
}

// ErrNoDatabase is returned when a test or a command needs a database
// and none is configured.
var ErrNoDatabase = errors.New("storage: no database configured")
