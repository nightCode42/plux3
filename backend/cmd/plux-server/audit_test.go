// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/schema/uuid7"
	"github.com/nightCode42/plux3/backend/internal/signing"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/storagetest"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden files")

// auditOrg is the organisation the audit command tests verify, fixed so
// that the golden output is.
const auditOrg = "0198f6a2-0000-7000-8000-0000000000a1"

// auditIDs generates entry identifiers.
type auditIDs struct{ g *uuid7.Generator }

func (i auditIDs) New() (string, error) {
	u, err := i.g.New()
	return u.String(), err
}

// auditFixture is a migrated database with one organisation whose chain
// has three entries and a checkpoint over them.
type auditFixture struct {
	config string
	db     *storage.DB
}

// newAuditFixture builds the fixture through the commands and services a
// real installation uses.
func newAuditFixture(t *testing.T) *auditFixture {
	t.Helper()
	url := storagetest.SchemaURL(t)
	keys := filepath.Join(t.TempDir(), "keys")
	path := configFile(t, "server:\n  publicBaseURL: \"https://p.example\"\ndatabase:\n  url: \""+url+"\"\n"+
		"signing:\n  directory: \""+keys+"\"\n")
	var stdout, stderr bytes.Buffer
	ctx := context.Background()
	if code := run(ctx, []string{"migrate", "-config", path}, &stdout, &stderr); code != exitOK {
		t.Fatalf("migrate: %d; %s", code, stderr.String())
	}
	db, err := storage.Open(ctx, storage.Options{URL: url, MaxConnections: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if _, err := db.Pool().Exec(ctx, `INSERT INTO organizations (id, key, name) VALUES ($1, 'acme', 'Acme')`, auditOrg); err != nil {
		t.Fatal(err)
	}
	gen := auditIDs{g: uuid7.NewGenerator(time.Now, rand.Reader)}
	log := audit.NewLog(gen, nil)
	for range 3 {
		if err := db.InTx(ctx, storage.Tenant{OrganizationID: auditOrg}, func(ctx context.Context, tx pgx.Tx) error {
			_, err := log.Append(ctx, tx, audit.Entry{
				OrganizationID: auditOrg, Actor: audit.Actor{Kind: "user", ID: "u1", Display: "Ada"}, Action: audit.SecretSet,
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	signer, err := signing.NewFile(keys)
	if err != nil {
		t.Fatal(err)
	}
	checkpointer, err := audit.NewCheckpointer(audit.CheckpointerOptions{DB: db, Log: log, Signer: signer, Key: "audit", IDs: gen})
	if err != nil {
		t.Fatal(err)
	}
	if cp, err := checkpointer.Checkpoint(ctx, auditOrg); err != nil || cp == nil {
		t.Fatalf("Checkpoint = %v, %v", cp, err)
	}
	return &auditFixture{config: path, db: db}
}

// tamper commits statements the append-only triggers would refuse.
func (f *auditFixture) tamper(t *testing.T, statements ...string) {
	t.Helper()
	if err := f.db.InTx(context.Background(), storage.Tenant{OrganizationID: auditOrg}, func(ctx context.Context, tx pgx.Tx) error {
		for _, stmt := range append([]string{`ALTER TABLE audit_log DISABLE TRIGGER audit_log_append_only`}, statements...) {
			if _, err := tx.Exec(ctx, stmt); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// verify runs `audit verify` and returns its exit code and output.
func (f *auditFixture) verify(t *testing.T, extra ...string) (int, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), append([]string{"audit", "verify", "-config", f.config}, extra...), &stdout, &stderr)
	if stderr.Len() > 0 && code != exitFailed {
		t.Logf("stderr: %s", stderr.String())
	}
	return code, stdout.String()
}

// golden compares output with a file of testdata.
func golden(t *testing.T, file, got string) {
	t.Helper()
	path := filepath.Join("testdata", file)
	if *updateGolden {
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Errorf("%s differs; run go test ./cmd/plux-server -run TestAuditVerify -update and review\n got: %s\nwant: %s", path, got, want)
	}
}

// Verifies: SEC-140, SEC-141.
// `audit verify` reports a healthy chain with a summary, and a changed,
// tampered record by naming the first one, exiting non-zero.
func TestAuditVerifyCommand(t *testing.T) {
	f := newAuditFixture(t)

	code, out := f.verify(t)
	if code != exitOK {
		t.Fatalf("a healthy chain: exit %d; %s", code, out)
	}
	golden(t, "audit_verify_ok.txt", out)
	code, out = f.verify(t, "--org", auditOrg, "--json")
	if code != exitOK {
		t.Fatalf("a healthy chain with --json: exit %d; %s", code, out)
	}
	golden(t, "audit_verify_ok.json", out)

	f.tamper(t, `UPDATE audit_log SET action = 'app.created' WHERE sequence = 2`)
	code, out = f.verify(t)
	if code != exitFailed {
		t.Fatalf("a changed entry: exit %d; %s", code, out)
	}
	golden(t, "audit_verify_tampered.txt", out)
	code, out = f.verify(t, "--json")
	if code != exitFailed {
		t.Fatalf("a changed entry with --json: exit %d", code)
	}
	golden(t, "audit_verify_tampered.json", out)
}

// Verifies: SEC-141.
// A checkpoint over entries that were removed from the end of the chain
// is reported as pointing past it.
func TestAuditVerifyReportsACheckpointPastTheChain(t *testing.T) {
	f := newAuditFixture(t)
	f.tamper(t, `DELETE FROM audit_log WHERE sequence = 3`)
	code, out := f.verify(t)
	if code != exitFailed {
		t.Fatalf("exit %d; %s", code, out)
	}
	golden(t, "audit_verify_truncated.txt", out)
}

// Verifies: SEC-141.
func TestAuditVerifyUsage(t *testing.T) {
	t.Parallel()
	path := configFile(t, valid)
	for _, args := range [][]string{
		{"audit"},
		{"audit", "list"},
		{"audit", "verify", "extra"},
		{"audit", "verify", "--org", "not-a-uuid", "-config", path},
		{"audit", "verify", "--nonsense"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), args, &stdout, &stderr); code != exitUsage {
			t.Errorf("%v: exit %d; want %d (stderr: %s)", args, code, exitUsage, stderr.String())
		}
	}
}
