// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/cache"
	"github.com/nightCode42/plux3/backend/internal/jobs"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/storagetest"
)

// Verifies: SEC-141.
// Only a process with a signer schedules checkpoints, at the configured
// interval on the maintenance queue; a run signs for organisations with
// new entries and is idle otherwise; and what it signs verifies.
func TestAuditCheckpointJob(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := storagetest.Open(t)
	cfg := testConfig(t, base+"signing:\n  directory: \""+filepath.Join(t.TempDir(), "keys")+"\"\naudit:\n  checkpointInterval: 15m\n")
	backend, err := BuildSigning(cfg)
	if err != nil {
		t.Fatal(err)
	}
	apiRole, err := BuildServices(ctx, cfg, db, cache.NewMemory(nil), limits.Defaults(), backend, WorkDeps{})
	if err != nil {
		t.Fatal(err)
	}
	idle := jobs.NewWorkers()
	if apiRole.Checkpointer != nil || len(CheckpointJobs(idle, apiRole, time.Hour, discard())) != 0 || idle.Len() != 0 {
		t.Fatal("a process without a signer schedules checkpoints")
	}

	svc, err := BuildServices(ctx, cfg, db, cache.NewMemory(nil), limits.Defaults(), backend, WorkDeps{Signer: backend})
	if err != nil {
		t.Fatal(err)
	}
	workers := jobs.NewWorkers()
	periodic := CheckpointJobs(workers, svc, cfg.Audit.CheckpointInterval.Duration(), discard())
	if len(periodic) != 1 || workers.Len() != 1 {
		t.Fatalf("checkpoint jobs = %d, workers = %d", len(periodic), workers.Len())
	}
	if (AuditCheckpoint{}).Kind() != "audit.checkpoint" || (AuditCheckpoint{}).InsertOpts().Queue != jobs.QueueMaintenance {
		t.Error("the job is not on the maintenance queue")
	}

	org, err := NewIDs().New()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx, `INSERT INTO organizations (id, key, name) VALUES ($1, 'acme', 'Acme')`, org); err != nil {
		t.Fatal(err)
	}
	appendEntry := func() {
		t.Helper()
		if err := db.InTx(ctx, storage.Tenant{OrganizationID: org}, func(ctx context.Context, tx pgx.Tx) error {
			_, err := svc.Audit.Append(ctx, tx, audit.Entry{OrganizationID: org, Actor: audit.Actor{Kind: "system"}, Action: audit.LimitSet})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	count := func() int {
		t.Helper()
		cps, err := svc.AuditCheckpoints.List(ctx, org, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		return len(cps)
	}
	w := &checkpointWorker{svc: svc, log: discard()}
	if err := w.Work(ctx, nil); err != nil || count() != 0 {
		t.Fatalf("an idle run: %d checkpoints, %v", count(), err)
	}
	appendEntry()
	appendEntry()
	for range 2 {
		if err := w.Work(ctx, nil); err != nil || count() != 1 {
			t.Fatalf("after new entries: %d checkpoints, %v; want 1", count(), err)
		}
	}
	appendEntry()
	if err := w.Work(ctx, nil); err != nil || count() != 2 {
		t.Fatalf("after one more entry: %d checkpoints, %v; want 2", count(), err)
	}

	pub, keyID, err := backend.PublicKey(ctx, cfg.Signing.Keys.Audit)
	if err != nil {
		t.Fatal(err)
	}
	report, err := VerifyAudit(ctx, svc, db, audit.PublicKeys{keyID: pub}, "")
	if err != nil || !report.OK() || len(report.Organizations) != 1 || report.Organizations[0].Entries != 3 || report.Organizations[0].Checkpoints != 2 {
		t.Fatalf("VerifyAudit = %+v, %v", report, err)
	}
	one, err := VerifyAudit(ctx, svc, db, audit.PublicKeys{keyID: pub}, org)
	if err != nil || !one.OK() || len(one.Organizations) != 1 {
		t.Fatalf("VerifyAudit of one organisation = %+v, %v", one, err)
	}
	if bad, err := VerifyAudit(ctx, svc, db, audit.PublicKeys{}, org); err != nil || bad.OK() {
		t.Errorf("VerifyAudit without the audit key = %+v, %v; want a broken checkpoint", bad, err)
	}
}
