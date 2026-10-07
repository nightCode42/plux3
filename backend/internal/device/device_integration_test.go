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
	"github.com/nightCode42/plux3/backend/internal/device/devicetest"
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
	log   *audit.Log
	ten   *tenancy.Service
}

// newFixtureWith builds a fixture with the device service's options adjusted
// by configure, which may read the fixture's clock.
func newFixtureWith(t *testing.T, configure func(f *fixture, o *device.Options)) *fixture {
	t.Helper()
	ctx := context.Background()
	db := storagetest.Open(t)
	backend, err := signing.NewFile(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gen := ids{g: uuid7.NewGenerator(time.Now, rand.Reader)}
	log := audit.NewLog(gen, nil)
	f := &fixture{db: db, now: time.Now(), envs: map[string]string{}, log: log}
	authService, err := auth.NewService(auth.Options{DB: db, Audit: log, Cache: cache.NewMemory(nil), Crypter: backend, IDs: gen, VerificationURI: "https://p.example/device"})
	if err != nil {
		t.Fatal(err)
	}
	ten, err := tenancy.NewService(tenancy.Options{DB: db, Audit: log, Auth: authService, Crypter: backend, IDs: gen, SigningKeyPrefix: "targets"})
	if err != nil {
		t.Fatal(err)
	}
	opts := device.Options{DB: db, IDs: gen, Now: func() time.Time { return f.now }, Audit: log}
	if configure != nil {
		configure(f, &opts)
	}
	f.ten = ten
	if f.svc, err = device.NewService(opts); err != nil {
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

// registerDevelopment registers a device with development evidence in a
// non-production environment, with the runtime version and host build
// given.
func (r *rig) registerDevelopment(t *testing.T, env, platform, runtime, build string) device.Device {
	t.Helper()
	_, jwk := devicetest.NewKey(t)
	d, err := r.svc.RegisterAttested(context.Background(), device.AttestedRegistration{
		AppID: r.app, Environment: env, Platform: platform, OSVersion: "18", RuntimeVersion: runtime, Build: build,
		Challenge: r.challenge(t, env), DPoPKeyJWK: jwk, KeyStorage: device.KeyStorageSoftware, Evidence: developmentEvidence(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// Verifies: GOV-010, SEC-001.
// A registration describes the device within bounds, and people read the
// devices of the apps they can read.
func TestRegistrationAndPeopleReadDevices(t *testing.T) {
	t.Parallel()
	f := newRig(t)
	ctx := context.Background()
	_, jwk := devicetest.NewKey(t)
	register := func(appID, env, platform, build string) error {
		challenge, _, err := f.svc.CreateChallenge(ctx, f.app, "development")
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.svc.RegisterAttested(ctx, device.AttestedRegistration{
			AppID: appID, Environment: env, Platform: platform, Build: build, Challenge: challenge,
			DPoPKeyJWK: jwk, KeyStorage: device.KeyStorageSoftware, Evidence: developmentEvidence(),
		})
		return err
	}
	for name, err := range map[string]error{
		"an app that is not an identifier": register("nope", "development", "ios", ""),
		"an unknown platform":              register(f.app, "development", "toaster", ""),
		"a long build":                     register(f.app, "development", "ios", strings.Repeat("x", 65)),
	} {
		if code(err) != plxerr.InvalidFormat {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := register(f.app, "moon", "ios", ""); code(err) != plxerr.ResourceNotFound {
		t.Errorf("an unknown environment: %v", err)
	}
	d := f.registerDevelopment(t, "development", "ios", "1.2.3-beta", "")
	if d.EnvironmentID != f.envs["development"] || d.AssuranceLevel != "AL0" || d.DPoPJKT == "" {
		t.Fatalf("RegisterAttested: %+v", d)
	}
	other := f.registerDevelopment(t, "staging", "web", "", "")
	// Only hashes of the old secrets remain, and an attested device has none.
	if err := f.db.InTx(ctx, storage.Tenant{OrganizationID: f.org}, func(ctx context.Context, tx pgx.Tx) error {
		var n int
		err := tx.QueryRow(ctx, `SELECT count(*) FROM devices WHERE length(secret_hash) > 0`).Scan(&n)
		if err == nil && n != 0 {
			t.Errorf("%d attested devices hold a secret hash", n)
		}
		return err
	}); err != nil {
		t.Fatal(err)
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
	prod, err := f.svc.List(ctx, f.owner, f.app, f.envs["development"], storage.Cursor{}, 10)
	if err != nil || len(prod) != 1 {
		t.Errorf("List(development): %+v %v", prod, err)
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
	f := newRig(t)
	ctx := context.Background()
	var ids []device.Identity
	builds := map[string]string{"0.9.0": "1.0.0+1", "2.0": "1.0.0+2"}
	for _, v := range []string{"0.9.0", "1.2.3-beta", "2.0", ""} {
		d := f.registerDevelopment(t, "development", "android", v, builds[v])
		if d.Build != builds[v] {
			t.Errorf("a device of build %q registered with build %q", builds[v], d.Build)
		}
		ids = append(ids, device.Identity{
			DeviceID: d.ID, OrganizationID: f.org, AppID: f.app, EnvironmentID: d.EnvironmentID, HostBuild: d.Build,
		})
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
			n, err := f.svc.Incompatible(ctx, tx, f.app, minRuntime, nil)
			if err != nil {
				return err
			}
			if n != want {
				t.Errorf("Incompatible(%q) = %d, want %d", minRuntime, n, want)
			}
		}
		// Devices of a host build that cannot run a release count too,
		// once each (REL-080).
		for _, c := range []struct {
			minRuntime string
			builds     []string
			want       int64
		}{{"", []string{"1.0.0+2"}, 1}, {"1.0.0", []string{"1.0.0+1"}, 1}, {"1.0.0", []string{"1.0.0+2"}, 2}, {"", []string{"7.0.0+1"}, 0}} {
			n, err := f.svc.Incompatible(ctx, tx, f.app, c.minRuntime, c.builds)
			if err != nil {
				return err
			}
			if n != c.want {
				t.Errorf("Incompatible(%q, %v) = %d, want %d", c.minRuntime, c.builds, n, c.want)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
