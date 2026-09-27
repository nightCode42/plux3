// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

// Package storagetest connects integration tests to a real PostgreSQL
// (QA-005).
//
// The database is named by PLUX_TEST_DATABASE_URL — the local server in
// development, a service container in CI. Each test gets its own schema,
// migrated from scratch and dropped afterwards, so tests are independent
// and can run in parallel. Without the variable the tests skip, so
// `go test ./...` stays useful on a machine with no database.
package storagetest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nightCode42/plux3/backend/internal/storage"
)

// URLVariable is the environment variable naming the test database.
const URLVariable = "PLUX_TEST_DATABASE_URL"

// URL returns the configured database URL, or "" when there is none.
func URL() string { return os.Getenv(URLVariable) }

// Skip skips the test when no database is configured, saying how to
// provide one.
func Skip(t testing.TB) string {
	t.Helper()
	url := URL()
	if url == "" {
		t.Skipf("set %s to run this integration test (QA-005)", URLVariable)
	}
	return url
}

// Open returns a database whose search path is a schema of its own, with
// every migration applied. The schema is dropped when the test ends.
func Open(t testing.TB) *storage.DB {
	t.Helper()
	migrations, err := storage.LoadMigrations(storage.Migrations())
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	return OpenWith(t, migrations)
}

// OpenWith is Open with a migration set of the test's own, for testing
// the migration machinery itself.
func OpenWith(t testing.TB, migrations []storage.Migration) *storage.DB {
	t.Helper()
	url := Skip(t)
	schema := "test_" + token()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect to %s: %v", URLVariable, err)
	}
	//nolint:misspell // Sanitize is pgx's own spelling
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("create schema: %v", err)
	}
	if err := admin.Close(ctx); err != nil {
		t.Fatalf("close the admin connection: %v", err)
	}

	db, err := storage.Open(ctx, storage.Options{
		URL:            withSchema(url, schema),
		MaxConnections: 4,
		ConnectTimeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.Migrate(ctx, migrations); err != nil {
		db.Close()
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
		drop, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		c, err := pgx.Connect(drop, url)
		if err != nil {
			t.Logf("drop schema %s: %v", schema, err)
			return
		}
		defer func() { _ = c.Close(drop) }()
		//nolint:misspell // Sanitize is pgx's own spelling
		if _, err := c.Exec(drop, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
			t.Logf("drop schema %s: %v", schema, err)
		}
	})
	return db
}

// withSchema returns the URL with the search path set to one schema, so
// every connection of the pool sees only the test's own tables.
func withSchema(url, schema string) string {
	sep := "?"
	for i := range url {
		if url[i] == '?' {
			sep = "&"
			break
		}
	}
	return url + sep + "search_path=" + schema
}

// token returns a short random identifier for a schema name.
func token() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("storagetest: %v", err))
	}
	return hex.EncodeToString(b[:])
}
