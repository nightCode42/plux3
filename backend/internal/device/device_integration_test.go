// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package device_test

import (
	"bytes"
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
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema/uuid7"
	"github.com/nightCode42/plux3/backend/internal/signing"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/storagetest"
	"github.com/nightCode42/plux3/backend/internal/tenancy"
)

type ids struct{ g *uuid7.Generator }

func (i ids) New() (string, error) {
	u, err := i.g.New()
	return u.String(), err
}

// fixture is an organisation with one app, its owner, and the device
// service on a movable clock.
type fixture struct {
	db    *storage.DB
	svc   *device.Service
	now   time.Time
	org   string
	app   string
	envs  map[string]string
	owner auth.Principal
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	db := storagetest.Open(t)
	backend, err := signing.NewFile(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gen := ids{g: uuid7.NewGenerator(time.Now, rand.Reader)}
	log := audit.NewLog(gen, nil)
	f := &fixture{db: db, now: time.Now(), envs: map[string]string{}}
	authService, err := auth.NewService(auth.Options{DB: db, Audit: log, Cache: cache.NewMemory(nil), Crypter: backend, IDs: gen, VerificationURI: "https://p.example/device"})
	if err != nil {
		t.Fatal(err)
	}
	ten, err := tenancy.NewService(tenancy.Options{DB: db, Audit: log, Auth: authService, Crypter: backend, IDs: gen, SigningKeyPrefix: "targets"})
	if err != nil {
		t.Fatal(err)
	}
	if f.svc, err = device.NewService(device.Options{DB: db, IDs: gen, Now: func() time.Time { return f.now }}); err != nil {
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
	f.org = o.ID
	if f.owner, err = authService.Resolve(ctx, id, f.org); err != nil {
		t.Fatal(err)
	}
	a, err := ten.CreateApp(ctx, f.owner, "demo", "Demo")
	if err != nil {
		t.Fatal(err)
	}
	f.app = a.ID
	envs, err := ten.ListEnvironments(ctx, f.owner, f.app, tenancy.Page{Size: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range envs {
		f.envs[e.Key] = e.ID
	}
	return f
}

func code(err error) plxerr.Code {
	c, _ := plxerr.CodeOf(err)
	return c
}

// Verifies: GOV-010.
// Registration, credentials and tokens: a secret works for its own
// device only, tokens expire, and nothing but the hash is stored.
func TestRegistrationAndTokens(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	for _, r := range []device.Registration{
		{AppID: "nope", Environment: "production", Platform: "ios"},
		{AppID: f.app, Environment: "production", Platform: "toaster"},
		{AppID: f.app, Environment: "production", Platform: "ios", Build: strings.Repeat("x", 65)},
	} {
		if _, _, err := f.svc.Register(ctx, r); code(err) != plxerr.InvalidFormat {
			t.Errorf("%+v: %v", r, err)
		}
	}
	if _, _, err := f.svc.Register(ctx, device.Registration{AppID: f.app, Environment: "moon", Platform: "ios"}); code(err) != plxerr.ResourceNotFound {
		t.Errorf("an unknown environment: %v", err)
	}
	d, secret, err := f.svc.Register(ctx, device.Registration{AppID: f.app, Environment: "production", Platform: "ios", OSVersion: "18", RuntimeVersion: "1.2.3-beta"})
	if err != nil || !strings.HasPrefix(secret, "plux_dsec_") || d.EnvironmentID != f.envs["production"] || d.AssuranceLevel != "none" {
		t.Fatalf("Register: %+v %v", d, err)
	}
	other, otherSecret, err := f.svc.Register(ctx, device.Registration{AppID: f.app, Environment: "staging", Platform: "web"})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ id, secret string }{
		{d.ID, otherSecret}, {d.ID, "plux_dsec_nope"}, {"nope", secret}, {d.ID, "nope"}, {"01a0c450-6c00-7002-8000-000000003dde", secret},
	} {
		if _, err := f.svc.IssueToken(ctx, c.id, c.secret); code(err) != plxerr.AuthenticationRequired {
			t.Errorf("IssueToken(%q): %v", c.id, err)
		}
	}
	tok, err := f.svc.IssueToken(ctx, d.ID, secret)
	if err != nil || !device.IsToken(tok.Value) {
		t.Fatalf("IssueToken: %v", err)
	}
	id, err := f.svc.Authenticate(ctx, tok.Value)
	if err != nil || id.DeviceID != d.ID || id.OrganizationID != f.org || id.AppID != f.app || id.EnvironmentID != f.envs["production"] {
		t.Fatalf("Authenticate: %+v %v", id, err)
	}
	for _, bad := range []string{"nope", "plux_dat_nope", secret} {
		if _, err := f.svc.Authenticate(ctx, bad); code(err) != plxerr.AuthenticationRequired {
			t.Errorf("Authenticate(%q): %v", bad, err)
		}
	}
	// Only hashes are stored.
	if err := f.db.InTx(ctx, storage.Tenant{OrganizationID: f.org}, func(ctx context.Context, tx pgx.Tx) error {
		var n int
		err := tx.QueryRow(ctx, `SELECT count(*) FROM devices WHERE position($1::bytea in secret_hash) > 0`, []byte(secret)).Scan(&n)
		if err == nil && n != 0 {
			t.Error("a device secret is stored in the clear")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// Tokens expire and are swept.
	f.now = f.now.Add(device.TokenTTL + time.Second)
	if _, err := f.svc.Authenticate(ctx, tok.Value); code(err) != plxerr.AuthenticationRequired {
		t.Errorf("an expired token: %v", err)
	}
	if n, err := f.svc.ExpireTokens(ctx, f.org); err != nil || n != 1 {
		t.Errorf("ExpireTokens: %d %v", n, err)
	}
	// People read devices of apps they can read.
	got, err := f.svc.Get(ctx, f.owner, other.ID)
	if err != nil || got.Platform != "web" {
		t.Errorf("Get: %+v %v", got, err)
	}
	if _, err := f.svc.Get(ctx, f.owner, "nope"); code(err) != plxerr.InvalidFormat {
		t.Errorf("Get(nope): %v", err)
	}
	if _, err := f.svc.Get(ctx, f.owner, f.app); code(err) != plxerr.ResourceNotFound {
		t.Errorf("Get(an app ID): %v", err)
	}
	all, err := f.svc.List(ctx, f.owner, f.app, "", storage.Cursor{}, 10)
	if err != nil || len(all) != 2 || all[0].ID != other.ID {
		t.Errorf("List: %+v %v", all, err)
	}
	page, err := f.svc.List(ctx, f.owner, f.app, "", storage.Cursor{Time: all[0].RegisteredAt, ID: all[0].ID}, 10)
	if err != nil || len(page) != 1 || page[0].ID != d.ID {
		t.Errorf("the second page: %+v %v", page, err)
	}
	prod, err := f.svc.List(ctx, f.owner, f.app, f.envs["production"], storage.Cursor{}, 10)
	if err != nil || len(prod) != 1 {
		t.Errorf("List(production): %+v %v", prod, err)
	}
	if _, err := f.svc.List(ctx, f.owner, f.app, "nope", storage.Cursor{}, 10); code(err) != plxerr.InvalidFormat {
		t.Errorf("List(bad environment): %v", err)
	}
	if _, err := f.svc.List(ctx, f.owner, "nope", "", storage.Cursor{}, 10); err == nil {
		t.Error("List(bad app) succeeded")
	}
}

// Verifies: REL-080.
// Installed reports record the release and bundles, and the runtime
// versions count against a release's minimum.
func TestInstalledAndCompatibility(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	var ids []device.Identity
	for _, v := range []string{"0.9.0", "1.2.3-beta", "2.0", ""} {
		d, secret, err := f.svc.Register(ctx, device.Registration{AppID: f.app, Environment: "production", Platform: "android", RuntimeVersion: v})
		if err != nil {
			t.Fatal(err)
		}
		tok, err := f.svc.IssueToken(ctx, d.ID, secret)
		if err != nil {
			t.Fatal(err)
		}
		id, err := f.svc.Authenticate(ctx, tok.Value)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	sum := bytes.Repeat([]byte{1}, 32)
	if err := f.svc.ReportInstalled(ctx, ids[0], 3, []device.Installed{{Key: "", SHA256: sum}, {Key: "loans", SHA256: sum}}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.ReportInstalled(ctx, ids[0], 4, []device.Installed{{Key: "loans", SHA256: sum}}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]device.Installed{{{Key: "x", SHA256: []byte{1}}}, make([]device.Installed, 1025)} {
		if err := f.svc.ReportInstalled(ctx, ids[0], 1, bad); code(err) != plxerr.InvalidFormat {
			t.Errorf("a bad report: %v", err)
		}
	}
	if err := f.svc.ReportInstalled(ctx, ids[0], -1, nil); code(err) != plxerr.InvalidFormat {
		t.Errorf("a negative sequence: %v", err)
	}
	d, err := f.svc.Get(ctx, f.owner, ids[0].DeviceID)
	if err != nil || d.InstalledSequence != 4 {
		t.Errorf("after reports: %+v %v", d, err)
	}
	err = f.db.InTx(ctx, storage.Tenant{OrganizationID: f.org}, func(ctx context.Context, tx pgx.Tx) error {
		var held int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM device_bundles WHERE device_id = $1`, storage.MustUUID(ids[0].DeviceID)).Scan(&held); err != nil {
			return err
		}
		if held != 1 {
			t.Errorf("the device holds %d bundles, want 1", held)
		}
		for minRuntime, want := range map[string]int64{"": 0, "1.0.0": 1, "1.2.3": 1, "1.2.4": 2, "3": 3} {
			n, err := f.svc.Incompatible(ctx, tx, f.app, minRuntime)
			if err != nil {
				return err
			}
			if n != want {
				t.Errorf("Incompatible(%q) = %d, want %d", minRuntime, n, want)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
