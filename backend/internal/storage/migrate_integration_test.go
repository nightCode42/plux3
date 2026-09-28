// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package storage_test

import (
	"context"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/storagetest"
)

// Verifies: SRV-021.
// A migration and its record commit together: one that fails half way
// leaves neither its changes nor a record behind, so the next start
// applies it again from a clean state.
func TestFailedMigrationLeavesNothing(t *testing.T) {
	t.Parallel()
	base, err := storage.LoadMigrations(storage.Migrations())
	if err != nil {
		t.Fatal(err)
	}
	db := storagetest.OpenWith(t, base)
	broken := storage.Migration{
		Version:  len(base) + 1,
		Name:     "broken",
		SQL:      "CREATE TABLE half_done (id int); SELECT 1 / 0;",
		Checksum: "x",
	}
	ctx := context.Background()
	if err := db.Migrate(ctx, append(base, broken)); err == nil {
		t.Fatal("a failing migration was reported as applied")
	}
	var tables, records int
	if err := db.Pool().QueryRow(ctx,
		`SELECT count(*) FROM pg_class WHERE relname = 'half_done' AND relnamespace = current_schema()::regnamespace`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool().QueryRow(ctx,
		`SELECT count(*) FROM plux_migrations WHERE version = $1`, broken.Version).Scan(&records); err != nil {
		t.Fatal(err)
	}
	if tables != 0 || records != 0 {
		t.Errorf("a failed migration left %d tables and %d records", tables, records)
	}
}
