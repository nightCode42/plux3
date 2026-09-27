// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package storage

import (
	"strings"
	"testing"
	"testing/fstest"
)

// set builds a migration filesystem from name/SQL pairs.
func set(files map[string]string) fstest.MapFS {
	m := fstest.MapFS{}
	for name, body := range files {
		m[name] = &fstest.MapFile{Data: []byte(body)}
	}
	return m
}

// Verifies: SRV-021.
func TestLoadMigrations(t *testing.T) {
	t.Parallel()
	got, err := LoadMigrations(set(map[string]string{
		"0002_apps.sql": "CREATE TABLE apps ();",
		"0001_init.sql": "CREATE TABLE orgs ();",
	}))
	if err != nil {
		t.Fatalf("LoadMigrations: %v", err)
	}
	if len(got) != 2 || got[0].Version != 1 || got[1].Name != "apps" {
		t.Fatalf("migrations %+v", got)
	}
	if got[0].Checksum == got[1].Checksum || len(got[0].Checksum) != 64 {
		t.Errorf("checksums %q %q", got[0].Checksum, got[1].Checksum)
	}
	for _, tc := range []struct {
		name, want string
		files      map[string]string
	}{
		{"bad name", "NNNN_lower_snake.sql", map[string]string{"init.sql": "SELECT 1;"}},
		{"gap", "without gaps", map[string]string{"0001_a.sql": "SELECT 1;", "0003_b.sql": "SELECT 1;"}},
		{"upper case", "NNNN_lower_snake.sql", map[string]string{"0001_Init.sql": "SELECT 1;"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := LoadMigrations(set(tc.files))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %v does not mention %q", err, tc.want)
			}
		})
	}
}

// Verifies: SRV-021, CI-003.
// The committed migrations must load, so a badly named or out-of-order
// file fails the build rather than the deployment.
func TestEmbeddedMigrationsLoad(t *testing.T) {
	t.Parallel()
	got, err := LoadMigrations(Migrations())
	if err != nil {
		t.Fatalf("LoadMigrations: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("no migrations are embedded")
	}
	for _, m := range got {
		if !strings.Contains(m.SQL, "SPDX-License-Identifier") {
			t.Errorf("migration %04d_%s has no SPDX header", m.Version, m.Name)
		}
	}
}

// Verifies: DEP-030.
func TestExpandContract(t *testing.T) {
	t.Parallel()
	if err := ExpandContract(Migration{SQL: "ALTER TABLE apps ADD COLUMN label text;"}); err != nil {
		t.Errorf("an additive migration was refused: %v", err)
	}
	for _, sql := range []string{
		"DROP TABLE apps;",
		"ALTER TABLE apps DROP COLUMN label;",
		"ALTER TABLE apps ALTER COLUMN label TYPE integer;",
		"ALTER TABLE apps RENAME TO applications;",
	} {
		if err := ExpandContract(Migration{Version: 2, Name: "x", SQL: sql}); err == nil {
			t.Errorf("%q was accepted", sql)
		}
	}
}

// Verifies: DEP-030.
// Every committed migration follows expand/contract, so a rolling
// upgrade never needs downtime.
func TestEmbeddedMigrationsExpandOnly(t *testing.T) {
	t.Parallel()
	got, err := LoadMigrations(Migrations())
	if err != nil {
		t.Fatalf("LoadMigrations: %v", err)
	}
	for _, m := range got {
		if err := ExpandContract(m); err != nil {
			t.Error(err)
		}
	}
}
