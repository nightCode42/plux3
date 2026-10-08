// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"

	"github.com/nightCode42/plux3/backend/internal/api"
	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/cache"
	"github.com/nightCode42/plux3/backend/internal/pluxv1"
	"github.com/nightCode42/plux3/backend/internal/schema/uuid7"
	"github.com/nightCode42/plux3/backend/internal/signing"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/storagetest"
	"github.com/nightCode42/plux3/backend/internal/tenancy"
)

// Verifies: SEC-141.
// ListAuditCheckpoints returns the calling organisation's signed
// checkpoints, page by page, and each signature verifies against the
// audit key from what the wire carries.
func TestListAuditCheckpoints(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := storagetest.Open(t)
	backend, err := signing.NewFile(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gen := uuids{g: uuid7.NewGenerator(time.Now, rand.Reader)}
	log := audit.NewLog(gen, nil)
	authService, err := auth.NewService(auth.Options{
		DB: db, Audit: log, Cache: cache.NewMemory(nil), Crypter: backend, IDs: gen, VerificationURI: "https://p.example/device",
	})
	if err != nil {
		t.Fatal(err)
	}
	tenancyService, err := tenancy.NewService(tenancy.Options{
		DB: db, Audit: log, Auth: authService, Crypter: backend, IDs: gen, SigningKeyPrefix: "targets",
	})
	if err != nil {
		t.Fatal(err)
	}
	admin, invitation, err := authService.Bootstrap(ctx, "admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authService.AcceptInvitation(ctx, invitation, "Admin", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	identity := auth.Identity{Kind: auth.KindUser, ID: admin.ID, UserID: admin.ID, Display: "admin", SecondFactor: true, InstallationAdmin: true}
	org, err := tenancyService.CreateOrganization(ctx, identity, "acme", "Acme")
	if err != nil {
		t.Fatal(err)
	}

	checkpointer, err := audit.NewCheckpointer(audit.CheckpointerOptions{DB: db, Log: log, Signer: backend, Key: "audit", IDs: gen})
	if err != nil {
		t.Fatal(err)
	}
	var heads []audit.Entry
	for range 2 {
		cp, err := checkpointer.Checkpoint(ctx, org.ID)
		if err != nil || cp == nil {
			t.Fatalf("Checkpoint = %v, %v", cp, err)
		}
		heads = append(heads, headOf(t, db, log, org.ID))
		if err := db.InTx(ctx, storage.Tenant{OrganizationID: org.ID}, func(ctx context.Context, tx pgx.Tx) error {
			_, err := log.Append(ctx, tx, audit.Entry{OrganizationID: org.ID, Actor: audit.Actor{Kind: "user", ID: "u1"}, Action: audit.SecretSet})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}

	pageKey := make([]byte, 32)
	if _, err := rand.Read(pageKey); err != nil {
		t.Fatal(err)
	}
	pages, err := api.NewPages(pageKey, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := &api.Handlers{Auth: authService, Tenancy: tenancyService, Pages: pages}
	service := api.NewSecurityAdmin(h, audit.NewCheckpointReader(db, log))
	list := func(ctx context.Context, page *pluxv1.Page) (*pluxv1.ListAuditCheckpointsResponse, error) {
		r := connect.NewRequest(&pluxv1.ListAuditCheckpointsRequest{Page: page})
		r.Header().Set(api.OrganizationHeader, org.ID)
		res, err := service.ListAuditCheckpoints(ctx, r)
		if err != nil {
			return nil, err
		}
		return res.Msg, nil
	}
	signed := api.WithIdentity(ctx, identity)

	first, err := list(signed, &pluxv1.Page{PageSize: 1})
	if err != nil || len(first.GetCheckpoints()) != 1 || first.GetPage().GetNextPageToken() == "" {
		t.Fatalf("first page = %v, %v", first, err)
	}
	second, err := list(signed, &pluxv1.Page{PageSize: 1, PageToken: first.GetPage().GetNextPageToken()})
	if err != nil || len(second.GetCheckpoints()) != 1 {
		t.Fatalf("second page = %v, %v", second, err)
	}
	pub, keyID, err := backend.PublicKey(ctx, "audit")
	if err != nil {
		t.Fatal(err)
	}
	for i, cp := range []*pluxv1.AuditCheckpoint{first.GetCheckpoints()[0], second.GetCheckpoints()[0]} {
		if cp.GetSequence() != heads[i].Sequence || hex.EncodeToString(cp.GetEntryHash()) != heads[i].EntryHash || cp.GetKeyId() != keyID {
			t.Errorf("checkpoint %d = %v; want sequence %d, hash %s", i, cp, heads[i].Sequence, heads[i].EntryHash)
		}
		rebuilt := audit.Checkpoint{
			OrganizationID: org.ID, Sequence: cp.GetSequence(), EntryHash: hex.EncodeToString(cp.GetEntryHash()),
			CreatedAt: cp.GetCreatedAt().AsTime(), KeyID: cp.GetKeyId(), Algorithm: signing.Algorithm,
		}
		payload, err := rebuilt.Payload()
		if err != nil {
			t.Fatal(err)
		}
		if !ed25519.Verify(pub, payload, cp.GetSignature()) {
			t.Errorf("the signature of checkpoint %d does not verify", i)
		}
	}
	if _, err := list(ctx, nil); err == nil {
		t.Error("an unauthenticated call was served")
	}
}

// headOf returns the last entry of an organisation's chain.
func headOf(t *testing.T, db *storage.DB, log *audit.Log, org string) audit.Entry {
	t.Helper()
	var head audit.Entry
	if err := db.InTx(context.Background(), storage.Tenant{OrganizationID: org}, func(ctx context.Context, tx pgx.Tx) error {
		entries, err := log.List(ctx, tx, org, 0, 1000)
		if err == nil {
			head = entries[len(entries)-1]
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return head
}
