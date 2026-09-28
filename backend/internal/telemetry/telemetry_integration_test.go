// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package telemetry_test

import (
	"context"
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/cache"
	"github.com/nightCode42/plux3/backend/internal/device"
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

// Verifies: SCH-012.
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
	devices, err := device.NewService(device.Options{DB: db, IDs: gen})
	if err != nil {
		t.Fatal(err)
	}
	d, _, err := devices.Register(ctx, device.Registration{AppID: app.ID, Environment: "production", Platform: "ios"})
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
	now = now.Add(telemetry.Retention + time.Hour)
	if n, err := svc.Purge(ctx, o.ID); err != nil || n != 1 {
		t.Errorf("Purge: %d %v", n, err)
	}
}
