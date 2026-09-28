// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package audit_test

import (
	"context"
	"crypto/rand"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/schema/uuid7"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/storagetest"
)

// ids generates UUIDv7 identifiers for the tests.
type ids struct{ g *uuid7.Generator }

func (i ids) New() (string, error) {
	u, err := i.g.New()
	return u.String(), err
}

func newIDs() ids { return ids{g: uuid7.NewGenerator(time.Now, rand.Reader)} }

// withOrganization opens a database with one organisation in it.
func withOrganization(t *testing.T) (*storage.DB, string) {
	t.Helper()
	db := storagetest.Open(t)
	org, err := newIDs().New()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(context.Background(),
		`INSERT INTO organizations (id, key, name) VALUES ($1, 'acme', 'Acme')`, org); err != nil {
		t.Fatal(err)
	}
	return db, org
}

var actor = audit.Actor{Kind: "user", ID: "u1", Display: "Ada"}

// Verifies: SEC-140.
// Entries commit with the transaction that writes them, carry the
// request they came from, and form a chain that verifies end to end.
func TestAppendedEntriesFormAVerifiedChain(t *testing.T) {
	t.Parallel()
	db, org := withOrganization(t)
	log := audit.NewLog(newIDs(), nil)
	ctx := audit.WithRequest(context.Background(), audit.Request{SourceIP: "203.0.113.7", UserAgent: "plux-cli/1", RequestID: "req-1"})
	for range 5 {
		if err := db.InTx(ctx, storage.Tenant{OrganizationID: org}, func(ctx context.Context, tx pgx.Tx) error {
			_, err := log.Append(ctx, tx, audit.Entry{OrganizationID: org, Actor: actor, Action: audit.AppCreated, TargetKind: "app", TargetID: "a1"})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	// A rolled-back transaction leaves no entry behind.
	_ = db.InTx(ctx, storage.Tenant{OrganizationID: org}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := log.Append(ctx, tx, audit.Entry{OrganizationID: org, Actor: actor, Action: audit.AppDeleted}); err != nil {
			t.Fatal(err)
		}
		return context.Canceled
	})
	var entries []audit.Entry
	if err := db.InTx(ctx, storage.Tenant{OrganizationID: org}, func(ctx context.Context, tx pgx.Tx) error {
		n, err := log.VerifyChain(ctx, tx, org)
		if err != nil || n != 5 {
			t.Errorf("VerifyChain = %d, %v; want 5 entries", n, err)
		}
		entries, err = log.List(ctx, tx, org, 0, 100)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for i, e := range entries {
		if e.Sequence != int64(i+1) || e.SourceIP != "203.0.113.7" || e.UserAgent != "plux-cli/1" || e.RequestID != "req-1" {
			t.Errorf("entry %d = %+v", i, e)
		}
	}
}

// Verifies: SEC-140.
// Concurrent writers to one chain are serialised, so the chain never
// forks and no sequence number is taken twice.
func TestConcurrentAppendsFormOneChain(t *testing.T) {
	t.Parallel()
	db, org := withOrganization(t)
	log := audit.NewLog(newIDs(), nil)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for range 16 {
		wg.Go(func() {
			errs <- db.InTx(ctx, storage.Tenant{OrganizationID: org}, func(ctx context.Context, tx pgx.Tx) error {
				_, err := log.Append(ctx, tx, audit.Entry{OrganizationID: org, Actor: actor, Action: audit.VariableSet})
				return err
			})
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("a concurrent append failed: %v", err)
		}
	}
	if err := db.InTx(ctx, storage.Tenant{OrganizationID: org}, func(ctx context.Context, tx pgx.Tx) error {
		n, err := log.VerifyChain(ctx, tx, org)
		if n != 16 {
			t.Errorf("chain has %d entries; want 16", n)
		}
		return err
	}); err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
}

// Verifies: SEC-140, SRV-022.
// The installation's chain is written and read only in its scope, and an
// organisation never sees it.
func TestInstallationChainNeedsItsScope(t *testing.T) {
	t.Parallel()
	db, org := withOrganization(t)
	log := audit.NewLog(newIDs(), nil)
	ctx := context.Background()
	entry := audit.Entry{Actor: actor, Action: audit.UserSignedIn, TargetKind: "session", TargetID: "s1"}
	if err := db.InTx(ctx, storage.Tenant{}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := log.Append(ctx, tx, entry)
		return err
	}); err == nil {
		t.Error("an installation entry was written outside the installation scope")
	}
	if err := db.InTx(ctx, storage.Tenant{Scope: storage.ScopeInstallation}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := log.Append(ctx, tx, entry)
		return err
	}); err != nil {
		t.Fatalf("installation append: %v", err)
	}
	if err := db.InTx(ctx, storage.Tenant{OrganizationID: org}, func(ctx context.Context, tx pgx.Tx) error {
		got, err := log.List(ctx, tx, "", 0, 10)
		if len(got) != 0 {
			t.Errorf("an organisation read %d installation entries", len(got))
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

// Verifies: SEC-140.
// The database itself refuses to change or remove an entry, whoever
// asks; and an entry changed behind its back — here with the trigger
// disabled by the table's owner — is found by verification.
func TestAuditLogIsAppendOnlyAndTamperEvident(t *testing.T) {
	t.Parallel()
	db, org := withOrganization(t)
	log := audit.NewLog(newIDs(), nil)
	ctx := context.Background()
	tenant := storage.Tenant{OrganizationID: org}
	for range 3 {
		if err := db.InTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
			_, err := log.Append(ctx, tx, audit.Entry{OrganizationID: org, Actor: actor, Action: audit.SecretSet})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, stmt := range []string{
		`UPDATE audit_log SET action = 'app.created' WHERE sequence = 2`,
		`DELETE FROM audit_log WHERE sequence = 3`,
		`TRUNCATE audit_log`,
	} {
		err := db.InTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, stmt)
			return err
		})
		if err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Errorf("%s: err = %v; want the append-only refusal", stmt, err)
		}
	}
	if err := db.InTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		for _, stmt := range []string{
			`ALTER TABLE audit_log DISABLE TRIGGER audit_log_append_only`,
			`UPDATE audit_log SET action = 'app.created' WHERE sequence = 2`,
		} {
			if _, err := tx.Exec(ctx, stmt); err != nil {
				return err
			}
		}
		if _, err := log.VerifyChain(ctx, tx, org); err == nil || !strings.Contains(err.Error(), "entry 2") {
			t.Errorf("VerifyChain after tampering = %v; want entry 2 named", err)
		}
		return context.Canceled // roll the tampering back
	}); err == nil {
		t.Fatal("the tampering transaction committed")
	}
}

// Verifies: SEC-140.
func TestAppendRefusesWhatIsNotAnEntry(t *testing.T) {
	t.Parallel()
	db, org := withOrganization(t)
	log := audit.NewLog(newIDs(), nil)
	ctx := context.Background()
	for _, e := range []audit.Entry{
		{OrganizationID: org, Actor: actor, Action: "made.up"},
		{OrganizationID: org, Action: audit.AppCreated},
		{OrganizationID: "not-a-uuid", Actor: actor, Action: audit.AppCreated},
	} {
		if err := db.InTx(ctx, storage.Tenant{OrganizationID: org}, func(ctx context.Context, tx pgx.Tx) error {
			_, err := log.Append(ctx, tx, e)
			return err
		}); err == nil {
			t.Errorf("Append(%+v) was accepted", e)
		}
	}
}
