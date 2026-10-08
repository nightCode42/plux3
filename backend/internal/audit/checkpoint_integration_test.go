// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package audit_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/signing"
	"github.com/nightCode42/plux3/backend/internal/storage"
)

// auditKey is the reference of the checkpoint key in these tests.
const auditKey = "audit"

// checkpointFixture is an organisation, a log and a checkpointer.
type checkpointFixture struct {
	db     *storage.DB
	org    string
	log    *audit.Log
	signer *signing.File
	cp     *audit.Checkpointer
	keys   audit.PublicKeys
}

// newCheckpointFixture builds the fixture with a file-backed key.
func newCheckpointFixture(t *testing.T) *checkpointFixture {
	t.Helper()
	db, org := withOrganization(t)
	signer, err := signing.NewFile(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	log := audit.NewLog(newIDs(), nil)
	cp, err := audit.NewCheckpointer(audit.CheckpointerOptions{DB: db, Log: log, Signer: signer, Key: auditKey, IDs: newIDs()})
	if err != nil {
		t.Fatal(err)
	}
	pub, keyID, err := signer.PublicKey(context.Background(), auditKey)
	if err != nil {
		t.Fatal(err)
	}
	return &checkpointFixture{db: db, org: org, log: log, signer: signer, cp: cp, keys: audit.PublicKeys{keyID: pub}}
}

// append writes n entries.
func (f *checkpointFixture) append(t *testing.T, n int) {
	t.Helper()
	for range n {
		if err := f.db.InTx(context.Background(), storage.Tenant{OrganizationID: f.org}, func(ctx context.Context, tx pgx.Tx) error {
			_, err := f.log.Append(ctx, tx, audit.Entry{OrganizationID: f.org, Actor: actor, Action: audit.SecretSet})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
}

// checkpoint signs a checkpoint and fails the test when none is made.
func (f *checkpointFixture) checkpoint(t *testing.T) *audit.Checkpoint {
	t.Helper()
	cp, err := f.cp.Checkpoint(context.Background(), f.org)
	if err != nil {
		t.Fatal(err)
	}
	if cp == nil {
		t.Fatal("no checkpoint was signed")
	}
	return cp
}

// errRollback ends a tampering transaction without committing it.
var errRollback = errors.New("roll back")

// tampered runs statements that the append-only triggers would refuse,
// with the triggers disabled by the table's owner, verifies inside the
// same transaction and returns what verification said. The tampering is
// rolled back.
func (f *checkpointFixture) tampered(t *testing.T, statements ...string) (audit.Summary, error) {
	t.Helper()
	var (
		sum audit.Summary
		got error
	)
	err := f.db.InTx(context.Background(), storage.Tenant{OrganizationID: f.org}, func(ctx context.Context, tx pgx.Tx) error {
		for _, stmt := range append([]string{
			`ALTER TABLE audit_log DISABLE TRIGGER audit_log_append_only`,
			`ALTER TABLE audit_checkpoints DISABLE TRIGGER audit_checkpoints_append_only`,
		}, statements...) {
			if _, err := tx.Exec(ctx, stmt); err != nil {
				t.Fatalf("%s: %v", stmt, err)
			}
		}
		sum, got = f.log.VerifyCheckpointed(ctx, tx, f.org, f.keys)
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatalf("the tampering transaction ended with %v", err)
	}
	return sum, got
}

// wantBreak fails the test unless err is a *audit.Break of the kind at
// the sequence.
func wantBreak(t *testing.T, err error, kind string, sequence int64) *audit.Break {
	t.Helper()
	b, ok := errors.AsType[*audit.Break](err)
	if !ok {
		t.Fatalf("error = %v; want a *audit.Break", err)
	}
	if b.Kind != kind || b.Sequence != sequence {
		t.Fatalf("break = %s %d (%s); want %s %d", b.Kind, b.Sequence, b.Reason, kind, sequence)
	}
	return b
}

// Verifies: SEC-141.
// A checkpoint is signed only when there are entries the last one does
// not cover, signs the last entry, and verifies with the audit key.
func TestCheckpointIsCreatedOnlyForNewEntries(t *testing.T) {
	t.Parallel()
	f := newCheckpointFixture(t)
	ctx := context.Background()
	if cp, err := f.cp.Checkpoint(ctx, f.org); err != nil || cp != nil {
		t.Fatalf("Checkpoint of an empty chain = %v, %v; want nothing", cp, err)
	}
	f.append(t, 3)
	first := f.checkpoint(t)
	if first.Sequence != 3 || first.Algorithm != signing.Algorithm || first.OrganizationID != f.org {
		t.Errorf("first checkpoint = %+v", first)
	}
	if err := first.Verify(f.keys); err != nil {
		t.Errorf("the signature does not verify: %v", err)
	}
	if cp, err := f.cp.Checkpoint(ctx, f.org); err != nil || cp != nil {
		t.Fatalf("Checkpoint with nothing new = %v, %v; want nothing", cp, err)
	}
	f.append(t, 1)
	if second := f.checkpoint(t); second.Sequence != 4 {
		t.Errorf("second checkpoint covers %d; want 4", second.Sequence)
	}
	var listed []audit.Checkpoint
	if err := f.db.InTx(ctx, storage.Tenant{OrganizationID: f.org}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		listed, err = f.log.ListCheckpoints(ctx, tx, f.org, 0, 10)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 || listed[0].Sequence != 3 || listed[1].Sequence != 4 {
		t.Fatalf("listed checkpoints = %+v", listed)
	}
	for _, cp := range listed {
		if err := cp.Verify(f.keys); err != nil {
			t.Errorf("stored checkpoint %d: %v", cp.Sequence, err)
		}
	}
	sum, err := f.tampered(t)
	if err != nil || sum.Entries != 4 || sum.Checkpoints != 2 {
		t.Errorf("VerifyCheckpointed = %+v, %v; want 4 entries, 2 checkpoints", sum, err)
	}
}

// Verifies: SEC-141.
// Checkpoints are tenant data: another organisation's context sees none.
func TestCheckpointsAreTenantScoped(t *testing.T) {
	t.Parallel()
	f := newCheckpointFixture(t)
	f.append(t, 1)
	f.checkpoint(t)
	other, err := newIDs().New()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Pool().Exec(context.Background(),
		`INSERT INTO organizations (id, key, name) VALUES ($1, 'other', 'Other')`, other); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := f.db.InTx(context.Background(), storage.Tenant{OrganizationID: other}, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM audit_checkpoints`).Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("another organisation sees %d checkpoints", n)
	}
}

// Verifies: SEC-141, SEC-140.
// An entry changed after it was written is found, and so is one removed
// from the middle of the chain.
func TestVerifyDetectsATamperedAndARemovedEntry(t *testing.T) {
	t.Parallel()
	f := newCheckpointFixture(t)
	f.append(t, 4)
	f.checkpoint(t)

	_, err := f.tampered(t, `UPDATE audit_log SET action = 'app.created' WHERE sequence = 2`)
	_ = wantBreak(t, err, "entry", 2)

	_, err = f.tampered(t, `DELETE FROM audit_log WHERE sequence = 2`)
	if b := wantBreak(t, err, "entry", 3); b.Reason == "" {
		t.Error("the break has no reason")
	}
}

// Verifies: SEC-141.
// A chain rewritten so that every hash is consistent still fails: the
// signed checkpoint names the hash the rewrite replaced.
func TestVerifyDetectsARewrittenChain(t *testing.T) {
	t.Parallel()
	f := newCheckpointFixture(t)
	f.append(t, 3)
	f.checkpoint(t)

	var (
		sum audit.Summary
		got error
	)
	err := f.db.InTx(context.Background(), storage.Tenant{OrganizationID: f.org}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `ALTER TABLE audit_log DISABLE TRIGGER audit_log_append_only`); err != nil {
			return err
		}
		entries, err := f.log.List(ctx, tx, f.org, 0, 10)
		if err != nil {
			return err
		}
		previous := entries[0].EntryHash
		for _, e := range entries[1:] {
			e.PreviousHash = previous
			if e.Sequence == 2 {
				e.Action = audit.AppCreated
			}
			e.EntryHash = e.Hash()
			previous = e.EntryHash
			if _, err := tx.Exec(ctx, `UPDATE audit_log SET action = $1, previous_hash = $2, entry_hash = $3 WHERE sequence = $4`,
				string(e.Action), e.PreviousHash, e.EntryHash, e.Sequence); err != nil {
				return err
			}
		}
		if err := audit.Verify(mustList(ctx, t, f.log, tx, f.org), ""); err != nil {
			t.Errorf("the rewritten chain should verify on its own: %v", err)
		}
		sum, got = f.log.VerifyCheckpointed(ctx, tx, f.org, f.keys)
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatal(err)
	}
	_ = wantBreak(t, got, "checkpoint", 3)
	if sum.Entries != 2 {
		t.Errorf("verified %d entries before the break; want 2", sum.Entries)
	}
}

// mustList reads a whole short chain.
func mustList(ctx context.Context, t *testing.T, log *audit.Log, tx pgx.Tx, org string) []audit.Entry {
	t.Helper()
	entries, err := log.List(ctx, tx, org, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

// Verifies: SEC-141.
// A checkpoint that was altered, or signed by a key that is not the
// audit key, fails verification.
func TestVerifyDetectsAForgedCheckpoint(t *testing.T) {
	t.Parallel()
	f := newCheckpointFixture(t)
	f.append(t, 3)
	f.checkpoint(t)

	_, err := f.tampered(t, `UPDATE audit_checkpoints SET signature = decode(repeat('00', 64), 'hex')`)
	_ = wantBreak(t, err, "checkpoint", 3)

	// The attacker rewrites the checkpoint's hash and keeps the signature.
	_, err = f.tampered(t, `UPDATE audit_checkpoints SET entry_hash = repeat('ab', 32)`)
	_ = wantBreak(t, err, "checkpoint", 3)

	// The attacker signs a checkpoint of their own with another key.
	forger, err := signing.NewFile(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fake, err := audit.NewCheckpointer(audit.CheckpointerOptions{DB: f.db, Log: f.log, Signer: forger, Key: auditKey, IDs: newIDs()})
	if err != nil {
		t.Fatal(err)
	}
	f.append(t, 1)
	forged, err := fake.Checkpoint(context.Background(), f.org)
	if err != nil || forged == nil {
		t.Fatalf("forged checkpoint = %v, %v", forged, err)
	}
	if forged.Sequence != 4 {
		t.Fatalf("forged checkpoint covers %d", forged.Sequence)
	}
	_, err = f.tampered(t)
	_ = wantBreak(t, err, "checkpoint", 4)
}

// Verifies: SEC-141.
// Removing the latest entries leaves a valid chain, but the signed
// checkpoint over them now points past its end.
func TestVerifyDetectsACheckpointPastTheChain(t *testing.T) {
	t.Parallel()
	f := newCheckpointFixture(t)
	f.append(t, 3)
	f.checkpoint(t)

	sum, err := f.tampered(t, `DELETE FROM audit_log WHERE sequence = 3`)
	b := wantBreak(t, err, "checkpoint", 3)
	if sum.Entries != 2 {
		t.Errorf("verified %d entries; want the 2 that remain", sum.Entries)
	}
	if b.Reason == "" {
		t.Error("the break has no reason")
	}
}

// Verifies: SEC-141.
// A checkpoint is never signed over a chain that does not hold together,
// so signing cannot launder a tampered entry.
func TestCheckpointRefusesABrokenChain(t *testing.T) {
	t.Parallel()
	f := newCheckpointFixture(t)
	ctx := context.Background()
	f.append(t, 2)
	f.checkpoint(t)
	f.append(t, 2)
	if err := f.db.InTx(ctx, storage.Tenant{OrganizationID: f.org}, func(ctx context.Context, tx pgx.Tx) error {
		for _, stmt := range []string{
			`ALTER TABLE audit_log DISABLE TRIGGER audit_log_append_only`,
			`UPDATE audit_log SET action = 'app.created' WHERE sequence = 4`,
		} {
			if _, err := tx.Exec(ctx, stmt); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cp, err := f.cp.Checkpoint(ctx, f.org)
	if err == nil || cp != nil {
		t.Fatalf("Checkpoint = %v, %v; want a refusal", cp, err)
	}
}
