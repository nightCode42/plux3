// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package storage_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5"

	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/storagetest"
)

// tenantTable is a migration that creates a table of tenant data with the
// policy every such table gets (SRV-022).
const tenantTable = `
CREATE TABLE widgets (
    id              uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    name            text NOT NULL
);
SELECT plux_tenant_policy('widgets');
`

// orgA and orgB are two tenants.
const (
	orgA = "01a0c450-6c00-7000-8000-00000000000a"
	orgB = "01a0c450-6c00-7000-8000-00000000000b"
)

// withWidgets returns a database migrated with the committed migrations
// plus the widgets table above.
func withWidgets(t *testing.T) *storage.DB {
	t.Helper()
	base, err := storage.LoadMigrations(storage.Migrations())
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	extra, err := storage.LoadMigrations(fstest.MapFS{
		"0001_widgets.sql": &fstest.MapFile{Data: []byte(tenantTable)},
	})
	if err != nil {
		t.Fatalf("load the test migration: %v", err)
	}
	extra[0].Version = len(base) + 1
	return storagetest.OpenWith(t, append(base, extra...))
}

// Verifies: SRV-021.
func TestMigrateIsIdempotent(t *testing.T) {
	t.Parallel()
	db := storagetest.Open(t)
	migrations, err := storage.LoadMigrations(storage.Migrations())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// storagetest.Open already migrated; a second run must do nothing.
	if err := db.Migrate(ctx, migrations); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	var n int
	if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM plux_migrations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != len(migrations) {
		t.Errorf("%d rows recorded for %d migrations", n, len(migrations))
	}
}

// Verifies: SRV-021.
func TestMigrateRefusesAChangedMigration(t *testing.T) {
	t.Parallel()
	db := storagetest.Open(t)
	migrations, err := storage.LoadMigrations(storage.Migrations())
	if err != nil {
		t.Fatal(err)
	}
	migrations[0].SQL += "\n-- edited after release\n"
	migrations[0].Checksum = "0000"
	err = db.Migrate(context.Background(), migrations)
	if err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("Migrate accepted an edited migration: %v", err)
	}
}

// Verifies: SRV-021.
// Several replicas starting at once take the advisory lock in turn, so
// the migrations are applied exactly once.
func TestMigrateUnderConcurrency(t *testing.T) {
	t.Parallel()
	db := withWidgets(t)
	migrations, err := storage.LoadMigrations(storage.Migrations())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = db.Migrate(context.Background(), migrations)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("replica %d: %v", i, err)
		}
	}
}

// Verifies: SRV-022.
// Row-level security bounds every read and write to the transaction's
// organisation, even though the connection is shared by the pool.
func TestRowLevelSecurityIsolatesTenants(t *testing.T) {
	t.Parallel()
	db := withWidgets(t)
	ctx := context.Background()

	insert := func(org, id, name string) error {
		return db.InTx(ctx, storage.Tenant{OrganizationID: org}, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO widgets (id, organization_id, name) VALUES ($1, $2, $3)`, id, org, name)
			return err
		})
	}
	if err := insert(orgA, "01a0c450-6c00-7000-8000-000000000001", "a1"); err != nil {
		t.Fatalf("insert for A: %v", err)
	}
	if err := insert(orgB, "01a0c450-6c00-7000-8000-000000000002", "b1"); err != nil {
		t.Fatalf("insert for B: %v", err)
	}

	names := func(org string) []string {
		t.Helper()
		var out []string
		err := db.InTx(ctx, storage.Tenant{OrganizationID: org}, func(ctx context.Context, tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `SELECT name FROM widgets ORDER BY name`)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var n string
				if err := rows.Scan(&n); err != nil {
					return err
				}
				out = append(out, n)
			}
			return rows.Err()
		})
		if err != nil {
			t.Fatalf("read for %s: %v", org, err)
		}
		return out
	}
	if got := names(orgA); len(got) != 1 || got[0] != "a1" {
		t.Errorf("organisation A sees %v", got)
	}
	if got := names(orgB); len(got) != 1 || got[0] != "b1" {
		t.Errorf("organisation B sees %v", got)
	}
	if got := names(""); len(got) != 0 {
		t.Errorf("a transaction with no organisation sees %v; it must see nothing", got)
	}

	// Writing another tenant's row is refused by the policy.
	err := db.InTx(ctx, storage.Tenant{OrganizationID: orgA}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO widgets (id, organization_id, name) VALUES ($1, $2, $3)`,
			"01a0c450-6c00-7000-8000-000000000003", orgB, "smuggled")
		return err
	})
	if err == nil {
		t.Error("organisation A inserted a row for organisation B")
	}
}

// Verifies: SRV-022.
// The tenant setting is local to the transaction, so the next caller on
// the same pooled connection does not inherit it.
func TestTenantDoesNotLeakBetweenTransactions(t *testing.T) {
	t.Parallel()
	db := withWidgets(t)
	ctx := context.Background()
	if err := db.InTx(ctx, storage.Tenant{OrganizationID: orgA}, func(context.Context, pgx.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	var setting string
	if err := db.Pool().QueryRow(ctx, `SELECT current_setting('plux.organization_id', true)`).Scan(&setting); err != nil {
		t.Fatal(err)
	}
	if setting != "" {
		t.Errorf("the organisation leaked out of the transaction: %q", setting)
	}
}

// Verifies: SRV-007.
func TestOpenAndPing(t *testing.T) {
	t.Parallel()
	db := storagetest.Open(t)
	if err := db.Ping(context.Background()); err != nil {
		t.Errorf("Ping: %v", err)
	}
}

// Verifies: OBS-003.
func TestOpenRefusesABadURLWithoutEchoingIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	if _, err := storage.Open(ctx, storage.Options{}); err == nil {
		t.Error("an empty URL was accepted")
	}
	const url = "postgres://plux:" + "hunter2" + "@nowhere.invalid:1/plux" //nolint:gosec // a fake credential for the redaction test
	_, err := storage.Open(ctx, storage.Options{URL: url})
	if err == nil {
		t.Fatal("an unreachable database was accepted")
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Errorf("the password leaked into %q", err)
	}
}

// nonTenantTables are the tables that deliberately hold no tenant data:
// the tenant boundary itself, installation-wide identity, and the
// bookkeeping of the migration runner and the job queue.
var nonTenantTables = map[string]string{ //nolint:gosec // table names, not credentials
	"organizations":         "a row is the tenant",
	"users":                 "users belong to the installation; memberships grant access",
	"mfa_factors":           "a second factor belongs to a user, not to an organisation",
	"sessions":              "a session belongs to a user",
	"mfa_challenges":        "a challenge belongs to a user",
	"device_authorizations": "a grant is anonymous until it is approved",
	"user_identities":       "a provider identity belongs to a person, who may be in many organisations",
	"oidc_logins":           "a sign-in in progress has no person yet",
	"installation_secrets":  "keys of the installation itself, sealed",
	"idempotency_keys":      "keyed by the credential's subject, which may act in no organisation; responses are sealed",
	"plux_migrations":       "schema bookkeeping",
}

// Verifies: SRV-022.
// Every table that holds tenant data carries organization_id and has
// row-level security enabled with the policy bound to the setting. The
// catalogue is walked rather than a list being maintained, so the
// guarantee cannot be lost by adding a table.
func TestEveryTenantTableIsProtected(t *testing.T) {
	t.Parallel()
	db := storagetest.Open(t)
	ctx := context.Background()
	rows, err := db.Pool().Query(ctx, `
		SELECT c.relname,
		       c.relrowsecurity,
		       c.relforcerowsecurity,
		       EXISTS (SELECT 1 FROM pg_policy p WHERE p.polrelid = c.oid AND p.polname = 'plux_tenant'),
		       EXISTS (SELECT 1 FROM pg_attribute a
		               WHERE a.attrelid = c.oid AND a.attname = 'organization_id' AND NOT a.attisdropped)
		  FROM pg_class c
		  JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE c.relkind = 'r'
		   AND n.nspname = current_schema()
		 ORDER BY c.relname`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var name string
		var rls, forced, policy, hasOrg bool
		if err := rows.Scan(&name, &rls, &forced, &policy, &hasOrg); err != nil {
			t.Fatal(err)
		}
		seen++
		if reason, ok := nonTenantTables[name]; ok {
			if hasOrg {
				t.Errorf("%s carries organization_id but is listed as non-tenant (%s)", name, reason)
			}
			continue
		}
		if strings.HasPrefix(name, "river_") {
			continue // the job queue's own tables
		}
		if !hasOrg {
			t.Errorf("%s holds tenant data but has no organization_id; add one or list it in nonTenantTables", name)
			continue
		}
		if !rls || !forced || !policy {
			t.Errorf("%s: row-level security enabled=%v forced=%v policy=%v; call plux_tenant_policy on it",
				name, rls, forced, policy)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if seen < len(nonTenantTables) {
		t.Fatalf("only %d tables were examined", seen)
	}
}
