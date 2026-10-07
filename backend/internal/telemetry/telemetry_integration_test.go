// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package telemetry_test

import (
	"context"
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/cache"
	"github.com/nightCode42/plux3/backend/internal/device"
	"github.com/nightCode42/plux3/backend/internal/device/devicetest"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
	"github.com/nightCode42/plux3/backend/internal/schema/uuid7"
	"github.com/nightCode42/plux3/backend/internal/signing"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/storagetest"
	"github.com/nightCode42/plux3/backend/internal/telemetry"
	"github.com/nightCode42/plux3/backend/internal/tenancy"
)

type ids struct{ g *uuid7.Generator }

func (i ids) New() (string, error) {
	u, err := i.g.New()
	return u.String(), err
}

func code(err error) plxerr.Code {
	c, _ := plxerr.CodeOf(err)
	return c
}

// Verifies: SCH-012, REL-080.
// A device's events are stored when catalogued and clean, refused one by
// one otherwise, listed for people who can read the app, and purged
// after their retention.
func TestIngestListPurge(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := storagetest.Open(t)
	backend, err := signing.NewFile(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gen := ids{g: uuid7.NewGenerator(time.Now, rand.Reader)}
	log := audit.NewLog(gen, nil)
	authService, err := auth.NewService(auth.Options{DB: db, Audit: log, Cache: cache.NewMemory(nil), Crypter: backend, IDs: gen, VerificationURI: "https://p.example/device"})
	if err != nil {
		t.Fatal(err)
	}
	ten, err := tenancy.NewService(tenancy.Options{DB: db, Audit: log, Auth: authService, Crypter: backend, IDs: gen, SigningKeyPrefix: "targets"})
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
	id := auth.Identity{Kind: auth.KindUser, ID: admin.ID, UserID: admin.ID, Display: "Admin", SecondFactor: true, InstallationAdmin: true}
	o, err := ten.CreateOrganization(ctx, id, "acme", "Acme")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := authService.Resolve(ctx, id, o.ID)
	if err != nil {
		t.Fatal(err)
	}
	app, err := ten.CreateApp(ctx, owner, "demo", "Demo")
	if err != nil {
		t.Fatal(err)
	}
	devices, err := device.NewService(device.Options{DB: db, IDs: gen, Cache: cache.NewMemory(nil)})
	if err != nil {
		t.Fatal(err)
	}
	challenge, _, err := devices.CreateChallenge(ctx, app.ID, "development")
	if err != nil {
		t.Fatal(err)
	}
	_, jwk := devicetest.NewKey(t)
	d, err := devices.RegisterAttested(ctx, device.AttestedRegistration{
		AppID: app.ID, Environment: "development", Platform: "ios", Challenge: challenge, DPoPKeyJWK: jwk,
		KeyStorage: device.KeyStorageSoftware, Evidence: device.Evidence{Development: &device.DevelopmentEvidence{BuildID: "test"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	set, err := limits.Defaults().Tighten(limits.TelemetryEventsPerRequest, limits.ScopeInstallation, 3)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := telemetry.NewService(telemetry.Options{DB: db, IDs: gen, Limits: set, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	src := telemetry.Source{OrganizationID: o.ID, AppID: app.ID, EnvironmentID: d.EnvironmentID, DeviceID: d.ID}
	n, diags, err := svc.Ingest(ctx, src, []telemetry.Event{
		{Name: "screen_view", Time: now, Route: "/loans", Fields: []byte(`{"durationMs": 900}`)},
		{Name: "error", Time: now.Add(-time.Minute), DeviceID: app.ID},
		{Name: "error", Time: now.Add(2 * time.Hour)},
	})
	if err != nil || n != 1 || len(diags) != 2 || diags[0].Path != "/events/1" {
		t.Fatalf("Ingest: %d %v %v", n, diags, err)
	}
	if _, _, err := svc.Ingest(ctx, src, make([]telemetry.Event, 4)); code(err) != plxerr.LimitExceeded {
		t.Errorf("a batch over the limit: %v", err)
	}
	events, err := svc.List(ctx, owner, app.ID, d.EnvironmentID, "", time.Time{}, storage.Cursor{}, 10)
	if err != nil || len(events) != 1 || events[0].Route != "/loans" || strings.ReplaceAll(string(events[0].Fields), " ", "") != `{"durationMs":900}` {
		t.Fatalf("List: %+v %v", events, err)
	}
	if none, err := svc.List(ctx, owner, app.ID, d.EnvironmentID, "error", time.Time{}, storage.Cursor{}, 10); err != nil || len(none) != 0 {
		t.Errorf("List(error): %+v %v", none, err)
	}
	if _, err := svc.List(ctx, owner, app.ID, "nope", "", time.Time{}, storage.Cursor{}, 10); code(err) != plxerr.InvalidFormat {
		t.Errorf("List(bad environment): %v", err)
	}
	// The newest valid session_start says what the device runs now
	// (REL-080), whatever order the batch is in.
	if n, _, err := svc.Ingest(ctx, src, []telemetry.Event{
		{Name: "session_start", Time: now, Fields: []byte(`{"runtime_version":"1.2.0","host_build":"42","os_version":"18.1"}`)},
		{Name: "session_start", Time: now.Add(-time.Hour), Fields: []byte(`{"runtime_version":"1.1.0","host_build":"41","os_version":"18.0"}`)},
		{Name: "session_start", Time: now.Add(time.Minute), Fields: []byte(`{"runtime_version":"not a version"}`)},
	}); err != nil || n != 3 {
		t.Fatalf("Ingest(session_start): %d %v", n, err)
	}
	// Longer than registration accepts: stored, but not the device's.
	if n, _, err := svc.Ingest(ctx, src, []telemetry.Event{
		{Name: "session_start", Time: now.Add(2 * time.Minute), Fields: []byte(`{"runtime_version":"1.3.0","host_build":"43","os_version":"` + strings.Repeat("x", 65) + `"}`)},
	}); err != nil || n != 1 {
		t.Fatalf("Ingest(long session_start): %d %v", n, err)
	}
	var runtime, host, osVersion string
	if err := db.InTx(ctx, storage.Tenant{OrganizationID: o.ID}, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT runtime_version, host_build, os_version FROM devices WHERE id = $1", d.ID).Scan(&runtime, &host, &osVersion)
	}); err != nil || runtime != "1.2.0" || host != "42" || osVersion != "18.1" {
		t.Errorf("the device runs %q %q %q (%v)", runtime, host, osVersion, err)
	}
	now = now.Add(telemetry.Retention + time.Hour)
	if n, err := svc.Purge(ctx, o.ID); err != nil || n != 5 {
		t.Errorf("Purge: %d %v", n, err)
	}
}
