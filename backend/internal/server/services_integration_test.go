// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/nightCode42/plux3/backend/internal/cache"
	"github.com/nightCode42/plux3/backend/internal/config"
	"github.com/nightCode42/plux3/backend/internal/jobs"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
	"github.com/nightCode42/plux3/backend/internal/storage/storagetest"
)

// Verifies: SEC-120.
// The signing backend is chosen by configuration, and a backend that
// arrives in a later phase is refused by name.
func TestBuildSigning(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := testConfig(t, base+"signing:\n  directory: \""+filepath.Join(dir, "keys")+"\"\n")
	b, err := BuildSigning(file)
	if err != nil || b.Name() != "file" || b.AllowedInProduction() {
		t.Errorf("file backend = %v, %v", b, err)
	}
	vault := testConfig(t, base+"signing:\n  backend: vault\n  vault:\n    address: \"https://vault.example:8200\"\n    token: \"s.token\"\n")
	if b, err := BuildSigning(vault); err != nil || b.Name() != "vault" {
		t.Errorf("vault backend = %v, %v", b, err)
	}
	vault.Signing.Backend = "pkcs11"
	if _, err := BuildSigning(vault); err == nil {
		t.Error("a later-phase backend was built")
	}
	vault.Signing.Backend = "vault"
	vault.Signing.Vault.Address = "not a url"
	if _, err := BuildSigning(vault); err == nil {
		t.Error("a malformed Vault address was accepted")
	}
}

// Verifies: GOV-031, SRV-005, SRV-024.
// The maintenance sweep runs every part and is scheduled hourly on the
// maintenance queue.
func TestMaintenanceSweep(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := storagetest.Open(t)
	cfg := testConfig(t, base+"signing:\n  directory: \""+filepath.Join(t.TempDir(), "keys")+"\"\n")
	backend, err := BuildSigning(cfg)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := BuildServices(ctx, cfg, db, cache.NewMemory(nil), limits.Defaults(), backend, WorkDeps{})
	if err != nil {
		t.Fatalf("BuildServices: %v", err)
	}
	if verificationURI(cfg) != "https://plux.example/device" {
		t.Errorf("verification URI = %q", verificationURI(cfg))
	}
	workers := jobs.NewWorkers()
	periodic := MaintenanceJobs(workers, svc, discard())
	if len(periodic) != 1 || workers.Len() != 1 {
		t.Fatalf("maintenance jobs = %d, workers = %d", len(periodic), workers.Len())
	}
	if (Maintenance{}).Kind() != "maintenance.sweep" || (Maintenance{}).InsertOpts().Queue != jobs.QueueMaintenance {
		t.Error("the sweep is not on the maintenance queue")
	}
	w := &maintenanceWorker{svc: svc, log: discard()}
	if err := w.Work(ctx, nil); err != nil {
		t.Errorf("Work: %v", err)
	}
	if _, err := BuildServices(ctx, cfg, nil, nil, limits.Defaults(), backend, WorkDeps{}); err == nil {
		t.Error("services without a database were built")
	}
	id, err := NewIDs().New()
	if err != nil || len(id) != 36 {
		t.Errorf("NewIDs = %q, %v", id, err)
	}
}

// Verifies: SEC-020, SEC-022, SEC-024.
// The api role is built with the trust that authenticates devices, bound
// to the public base URL and sharing its nonce key across replicas; a
// process without the api role has none, and the API refuses to be
// mounted without it.
func TestBuildServicesDeviceTrust(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := storagetest.Open(t)
	dir := filepath.Join(t.TempDir(), "keys")
	cfg := testConfig(t, base+"signing:\n  directory: \""+dir+"\"\n")
	backend, err := BuildSigning(cfg)
	if err != nil {
		t.Fatal(err)
	}
	first, err := BuildServices(ctx, cfg, db, cache.NewMemory(nil), limits.Defaults(), backend, WorkDeps{Log: discard()})
	if err != nil {
		t.Fatal(err)
	}
	trust := first.DeviceTrust
	if trust == nil || trust.Tokens.Issuer != "https://plux.example" || trust.Tokens.Audience != "https://plux.example" ||
		trust.Window != time.Minute || trust.FallbackWindow != 15*time.Second || trust.Proofs.Nonces != trust.Nonces {
		t.Fatalf("DeviceTrust = %+v", trust)
	}
	second, err := BuildServices(ctx, cfg, db, cache.NewMemory(nil), limits.Defaults(), backend, WorkDeps{})
	if err != nil {
		t.Fatal(err)
	}
	if !second.DeviceTrust.Nonces.Valid(trust.Nonces.Current()) {
		t.Error("two replicas do not share the nonce key")
	}

	worker := testConfig(t, base+"signing:\n  directory: \""+dir+"\"\n")
	worker.Server.Roles = []config.Role{config.RoleWorker}
	svc, err := BuildServices(ctx, worker, db, cache.NewMemory(nil), limits.Defaults(), backend, WorkDeps{})
	if err != nil {
		t.Fatal(err)
	}
	if svc.DeviceTrust != nil {
		t.Error("the worker role was given device trust")
	}
	srv, err := New(Deps{Config: cfg, Log: discard(), DB: db})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.RegisterAPI(svc); err == nil {
		t.Error("the API was mounted without device trust")
	}
	if err := srv.RegisterAPI(first); err != nil {
		t.Errorf("RegisterAPI: %v", err)
	}
}
