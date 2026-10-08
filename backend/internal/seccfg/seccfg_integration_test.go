// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package seccfg_test

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/cache"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
	"github.com/nightCode42/plux3/backend/internal/schema/uuid7"
	"github.com/nightCode42/plux3/backend/internal/seccfg"
	"github.com/nightCode42/plux3/backend/internal/security/settings"
	"github.com/nightCode42/plux3/backend/internal/signing"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
	"github.com/nightCode42/plux3/backend/internal/storage/storagetest"
	"github.com/nightCode42/plux3/backend/internal/tenancy"
)

type ids struct{ g *uuid7.Generator }

func (i ids) New() (string, error) {
	u, err := i.g.New()
	return u.String(), err
}

// fixture is an organisation with an owner, a viewer and one app, against
// a fresh schema.
type fixture struct {
	db      *storage.DB
	log     *audit.Log
	tenancy *tenancy.Service
	svc     *seccfg.Service
	now     time.Time
	org     string
	owner   auth.Principal
	viewer  auth.Principal
	app     string
	envs    map[string]string
}

func newFixture(t *testing.T, limitSet limits.Set) *fixture {
	t.Helper()
	ctx := context.Background()
	db := storagetest.Open(t)
	backend, err := signing.NewFile(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gen := ids{g: uuid7.NewGenerator(time.Now, rand.Reader)}
	log := audit.NewLog(gen, nil)
	f := &fixture{db: db, log: log, now: time.Now(), envs: map[string]string{}}
	clock := func() time.Time { return f.now }
	authService, err := auth.NewService(auth.Options{DB: db, Audit: log, Cache: cache.NewMemory(nil), Crypter: backend, IDs: gen, VerificationURI: "https://p.example/device"})
	if err != nil {
		t.Fatal(err)
	}
	if f.tenancy, err = tenancy.NewService(tenancy.Options{DB: db, Audit: log, Auth: authService, Crypter: backend, IDs: gen, SigningKeyPrefix: "targets", Now: clock}); err != nil {
		t.Fatal(err)
	}
	if f.svc, err = seccfg.New(seccfg.Options{DB: db, Audit: log, Limits: limitSet, Now: clock}); err != nil {
		t.Fatal(err)
	}
	admin, invitation, err := authService.Bootstrap(ctx, "admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authService.AcceptInvitation(ctx, invitation, "Admin", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	adminID := auth.Identity{Kind: auth.KindUser, ID: admin.ID, UserID: admin.ID, Display: "Admin", SecondFactor: true, InstallationAdmin: true}
	o, err := f.tenancy.CreateOrganization(ctx, adminID, "acme", "Acme")
	if err != nil {
		t.Fatal(err)
	}
	f.org = o.ID
	if f.owner, err = authService.Resolve(ctx, adminID, f.org); err != nil {
		t.Fatal(err)
	}
	a, err := f.tenancy.CreateApp(ctx, f.owner, "demo", "Demo")
	if err != nil {
		t.Fatal(err)
	}
	f.app = a.ID
	envs, err := f.tenancy.ListEnvironments(ctx, f.owner, f.app, tenancy.Page{Size: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range envs {
		f.envs[e.Key] = e.ID
	}
	m, _, err := f.tenancy.AddMember(ctx, f.owner, "", "viewer@example.com", "viewer")
	if err != nil {
		t.Fatal(err)
	}
	if f.viewer, err = authService.Resolve(ctx, auth.Identity{Kind: auth.KindUser, ID: m.UserID, UserID: m.UserID, Display: "v", SecondFactor: true}, f.org); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) set(t *testing.T, env, profile, overrides string, expected int64) (int64, error) {
	t.Helper()
	return f.svc.Set(context.Background(), f.owner, seccfg.Change{
		AppID: f.app, EnvironmentID: f.envs[env], Profile: profile, Overrides: []byte(overrides), ExpectedVersion: expected,
	})
}

func (f *fixture) mustSet(t *testing.T, env, profile, overrides string, expected int64) int64 {
	t.Helper()
	v, err := f.set(t, env, profile, overrides, expected)
	if err != nil {
		t.Fatalf("Set(%s, %s, %s, %d): %v", env, profile, overrides, expected, err)
	}
	return v
}

func wantCode(t *testing.T, err error, want plxerr.Code, what string) {
	t.Helper()
	if code, ok := plxerr.CodeOf(err); !ok || code != want {
		t.Errorf("%s: %v, want code %d", what, err, want)
	}
}

// Verifies: SEC-180, SEC-182.
// An environment starts at version 0, the built-in standard profile; each
// change is the next version; a stale expected_version is refused and a
// refused change creates no version.
func TestSetAndGet(t *testing.T) {
	t.Parallel()
	f := newFixture(t, limits.Set{})
	ctx := context.Background()
	prod := f.envs["production"]

	c, err := f.svc.Get(ctx, f.owner, f.app, prod)
	if err != nil || c.Version != 0 || c.Values.Profile() != settings.Standard {
		t.Fatalf("a new environment: %+v %v", c, err)
	}
	if v := f.mustSet(t, "production", "strict", `{"inactivityLockTimeout": 60}`, 0); v != 1 {
		t.Fatalf("first version = %d", v)
	}
	if v := f.mustSet(t, "production", "maximum", ``, 1); v != 2 {
		t.Fatalf("second version = %d", v)
	}
	c, err = f.svc.Get(ctx, f.owner, f.app, prod)
	if err != nil || c.Version != 2 || c.Values.Profile() != settings.Maximum || c.Values.Overridden(settings.InactivityLockTimeout) {
		t.Fatalf("after two changes: %+v %v", c, err)
	}

	_, err = f.set(t, "production", "strict", ``, 1)
	wantCode(t, err, plxerr.RevisionConflict, "a stale expected version")
	_, err = f.set(t, "production", "strict", ``, 0)
	wantCode(t, err, plxerr.RevisionConflict, "expected version 0 for a configured environment")
	_, err = f.set(t, "production", "strict", `{"inactivityLock": false}`, 2)
	wantCode(t, err, plxerr.SecurityConfigLoosensPreset, "a loosening override")
	_, err = f.set(t, "production", "strict", `{"inactivityLockTimeout": 5}`, 2)
	wantCode(t, err, plxerr.SecurityConfigOutOfBounds, "an override out of bounds")
	_, err = f.set(t, "production", "strict", `{"nope": 1}`, 2)
	wantCode(t, err, plxerr.UnknownProperty, "an unknown key")
	if c, _ := f.svc.Get(ctx, f.owner, f.app, prod); c.Version != 2 {
		t.Errorf("a refused change made version %d", c.Version)
	}

	// The same configuration again changes nothing.
	if v := f.mustSet(t, "production", "maximum", `{}`, 2); v != 2 {
		t.Errorf("an unchanged configuration made version %d", v)
	}
	// Another environment is untouched.
	if c, err := f.svc.Get(ctx, f.owner, f.app, f.envs["development"]); err != nil || c.Version != 0 {
		t.Errorf("another environment: %+v %v", c, err)
	}
}

// Verifies: SEC-182.
// A concurrent writer that read the same version loses: exactly one of
// two changes with the same expected version succeeds.
func TestConcurrentSet(t *testing.T) {
	t.Parallel()
	f := newFixture(t, limits.Set{})
	results := make(chan error, 2)
	for _, profile := range []string{"strict", "maximum"} {
		go func() {
			_, err := f.set(t, "staging", profile, ``, 0)
			results <- err
		}()
	}
	var failed, ok int
	for range 2 {
		if err := <-results; err == nil {
			ok++
		} else if code, _ := plxerr.CodeOf(err); code == plxerr.RevisionConflict {
			failed++
		} else {
			t.Errorf("unexpected: %v", err)
		}
	}
	if ok != 1 || failed != 1 {
		t.Errorf("%d succeeded, %d conflicted", ok, failed)
	}
}

// Verifies: SEC-182.
// The server keeps the newest HistoryKept versions.
func TestHistoryIsPruned(t *testing.T) {
	t.Parallel()
	f := newFixture(t, limits.Set{})
	profiles := []string{"standard", "strict", "maximum"}
	total := seccfg.HistoryKept + 5
	for i := range total {
		f.mustSet(t, "development", profiles[i%3], ``, int64(i))
	}
	var kept []int64
	err := f.db.InTx(context.Background(), storage.Tenant{OrganizationID: f.org}, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		for v := int64(1); v <= int64(total); v++ {
			if _, err := q.GetSecurityConfig(ctx, dbgen.GetSecurityConfigParams{EnvironmentID: storage.MustUUID(f.envs["development"]), Version: v}); err == nil {
				kept = append(kept, v)
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != seccfg.HistoryKept || kept[0] != int64(total-seccfg.HistoryKept+1) || kept[len(kept)-1] != int64(total) {
		t.Errorf("kept versions %v", kept)
	}
}

// Verifies: SEC-182.
// Changing a configuration needs app.manage; reading it needs app.read; an
// environment of another app is not found.
func TestPermissions(t *testing.T) {
	t.Parallel()
	f := newFixture(t, limits.Set{})
	ctx := context.Background()
	_, err := f.svc.Set(ctx, f.viewer, seccfg.Change{AppID: f.app, EnvironmentID: f.envs["production"], Profile: "strict"})
	wantCode(t, err, plxerr.PermissionDenied, "a viewer setting the configuration")
	if _, err := f.svc.Get(ctx, f.viewer, f.app, f.envs["production"]); err != nil {
		t.Errorf("a viewer reading it: %v", err)
	}
	other, err := f.tenancy.CreateApp(ctx, f.owner, "other", "Other")
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.Set(ctx, f.owner, seccfg.Change{AppID: other.ID, EnvironmentID: f.envs["production"], Profile: "strict"})
	wantCode(t, err, plxerr.ResourceNotFound, "an environment of another app")
	_, err = f.svc.Get(ctx, f.owner, f.app, "not-an-id")
	wantCode(t, err, plxerr.InvalidFormat, "a malformed environment")
	_, err = f.svc.Set(ctx, f.owner, seccfg.Change{AppID: f.app, EnvironmentID: f.envs["production"], Profile: "strict", ExpectedVersion: -1})
	wantCode(t, err, plxerr.InvalidFormat, "a negative expected version")
}

// Verifies: SEC-182, SEC-140.
// A change is audited with the settings it names, never their values.
func TestChangeIsAudited(t *testing.T) {
	t.Parallel()
	f := newFixture(t, limits.Set{})
	f.mustSet(t, "production", "strict", `{"inactivityLockTimeout": 61}`, 0)
	f.mustSet(t, "production", "strict", `{"inactivityLockTimeout": 61}`, 1) // unchanged: not audited
	entries, err := f.tenancy.AuditEntries(context.Background(), f.owner, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var found []audit.Entry
	for _, e := range entries {
		if e.Action == audit.SecurityConfigSet {
			found = append(found, e)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%d audit entries for the change", len(found))
	}
	e := found[0]
	if e.TargetKind != "environment" || e.TargetID != f.envs["production"] || e.Actor.ID != f.owner.ID ||
		e.Detail != "version 1, profile strict, overrides [inactivityLockTimeout]" || strings.Contains(e.Detail, "61") {
		t.Errorf("audit entry %+v", e)
	}
}

// Verifies: SEC-182.
// The server's readers see a change at once in the process that made it,
// and another replica within the cache interval; an environment with no
// configuration is the standard profile.
func TestEffective(t *testing.T) {
	t.Parallel()
	f := newFixture(t, limits.Set{})
	ctx := context.Background()
	other, err := seccfg.New(seccfg.Options{DB: f.db, Audit: f.log, Now: func() time.Time { return f.now }})
	if err != nil {
		t.Fatal(err)
	}
	dev := f.envs["development"]
	v, version, err := f.svc.Effective(ctx, f.app, dev)
	if err != nil || version != 0 || v.Profile() != settings.Standard || v.Get(settings.AllowSoftwareKeys).Bool() != true {
		t.Fatalf("no configuration: %v %d %v", v.Profile(), version, err)
	}
	if _, _, err := other.Effective(ctx, f.app, dev); err != nil {
		t.Fatal(err)
	}
	f.mustSet(t, "development", "standard", `{"allowSoftwareKeys": false, "dpopIatWindow": 20}`, 0)
	v, version, err = f.svc.Effective(ctx, f.app, dev)
	if err != nil || version != 1 || v.Get(settings.AllowSoftwareKeys).Bool() || v.Get(settings.DPOPIatWindow).Int() != 20 {
		t.Errorf("after the change: version %d, %v", version, err)
	}
	if _, version, _ := other.Effective(ctx, f.app, dev); version != 0 {
		t.Errorf("another replica saw version %d before its cache expired", version)
	}
	f.now = f.now.Add(31 * time.Second)
	if _, version, _ := other.Effective(ctx, f.app, dev); version != 1 {
		t.Errorf("another replica saw version %d after its cache expired", version)
	}
	if _, _, err := f.svc.Effective(ctx, f.app, "not-an-id"); err == nil {
		t.Error("a malformed environment was read")
	}
	if _, _, err := f.svc.Effective(ctx, uuid7Zero, dev); err == nil {
		t.Error("an environment was read under another app")
	}
}

const uuid7Zero = "00000000-0000-7000-8000-000000000000"

// Verifies: SEC-182.
// The change hook runs in the transaction of the change: when it fails,
// the version is not stored.
func TestOnChange(t *testing.T) {
	t.Parallel()
	f := newFixture(t, limits.Set{})
	var seen []string
	f.svc.OnChange(func(_ context.Context, _ pgx.Tx, org, env string) error {
		seen = append(seen, org+"/"+env)
		return nil
	})
	f.mustSet(t, "production", "strict", ``, 0)
	if len(seen) != 1 || seen[0] != f.org+"/"+f.envs["production"] {
		t.Errorf("hook calls %v", seen)
	}
	f.mustSet(t, "production", "strict", ``, 1)
	if len(seen) != 1 {
		t.Errorf("the hook ran for an unchanged configuration: %v", seen)
	}
	failure := errors.New("no queue")
	f.svc.OnChange(func(context.Context, pgx.Tx, string, string) error { return failure })
	if _, err := f.set(t, "production", "maximum", ``, 1); !errors.Is(err, failure) {
		t.Fatalf("a failing hook: %v", err)
	}
	if c, _ := f.svc.Get(context.Background(), f.owner, f.app, f.envs["production"]); c.Version != 1 {
		t.Errorf("a failed hook left version %d", c.Version)
	}
}

// Verifies: SEC-182, LIM-001.
// securityConfig.bytes bounds a request.
func TestSizeLimit(t *testing.T) {
	t.Parallel()
	tight, err := limits.Defaults().Tighten(limits.SecurityConfigBytes, limits.ScopeApp, 40)
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture(t, tight)
	_, err = f.set(t, "production", "strict", `{"inactivityLockTimeout": 61, "dpopIatWindow": 20}`, 0)
	wantCode(t, err, plxerr.LimitExceeded, "an oversized request")
	f.mustSet(t, "production", "strict", `{"dpopIatWindow": 20}`, 0)
}

// Verifies: SEC-182.
// A device holding version from, with the manifest pinning version to,
// gets the merge patch between the two documents: nothing for the same
// version, from nothing for version 0 or a version no longer kept, and the
// whole document marked as such when the patch is too large.
func TestDeliver(t *testing.T) {
	t.Parallel()
	f := newFixture(t, limits.Set{})
	ctx := context.Background()
	sc := seccfg.Scope{OrganizationID: f.org, AppID: f.app, EnvironmentID: f.envs["production"]}
	f.mustSet(t, "production", "standard", `{"allowSoftwareKeys": false}`, 0)                         // 1
	f.mustSet(t, "production", "standard", `{"allowSoftwareKeys": false, "inactivityLock": true}`, 1) // 2
	f.mustSet(t, "production", "strict", `{"inactivityLockTimeout": 60}`, 2)                          // 3
	f.mustSet(t, "production", "strict", `{"inactivityLockTimeout": 60, "dpopIatWindow": 20}`, 3)     // 4: same document as 3

	tests := []struct {
		name     string
		from, to int64
		patch    string
		full     bool
	}{
		{"the same version", 3, 3, ``, false},
		{"nothing pinned", 2, 0, ``, false},
		{"one version behind", 2, 3, `{"overrides":{"allowSoftwareKeys":null,"inactivityLock":null,"inactivityLockTimeout":60},"profile":"strict"}`, false},
		{"a version with the same document", 3, 4, `{}`, false},
		{"version 0", 0, 3, `{"overrides":{"inactivityLockTimeout":60},"profile":"strict"}`, false},
		{"the device is ahead", 4, 1, `{"overrides":{"allowSoftwareKeys":false,"inactivityLockTimeout":null},"profile":"standard"}`, false},
		{"a version never stored", 99, 3, `{"overrides":{"inactivityLockTimeout":60},"profile":"strict"}`, false},
		{"the pinned version never stored", 3, 99, ``, true},
	}
	for _, tt := range tests {
		d, err := f.svc.Deliver(ctx, sc, tt.from, tt.to)
		if err != nil || string(d.Patch) != tt.patch || d.FullRequired != tt.full {
			t.Errorf("%s: %+v %v, want patch %q full %v", tt.name, d, err, tt.patch, tt.full)
		}
	}
}

// Verifies: SEC-182, LIM-001.
// A patch beyond securityConfig.patchBytes becomes the whole document.
func TestDeliverTooLarge(t *testing.T) {
	t.Parallel()
	tight, err := limits.Defaults().Tighten(limits.SecurityConfigPatchBytes, limits.ScopeApp, 40)
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture(t, tight)
	f.mustSet(t, "production", "standard", `{"allowSoftwareKeys": false}`, 0)
	f.mustSet(t, "production", "strict", `{"allowSoftwareKeys": false, "inactivityLock": true, "inactivityLockTimeout": 60, "screenshotBlockingDefault": true}`, 1)
	d, err := f.svc.Deliver(context.Background(), seccfg.Scope{OrganizationID: f.org, AppID: f.app, EnvironmentID: f.envs["production"]}, 1, 2)
	const whole = `{"overrides":{"allowSoftwareKeys":false,"inactivityLock":true,"inactivityLockTimeout":60,"screenshotBlockingDefault":true},"profile":"strict"}`
	if err != nil || !d.FullRequired || string(d.Patch) != whole {
		t.Errorf("a patch beyond the limit: %+v %v", d, err)
	}
}

// Verifies: SEC-182.
// Tenant isolation: another organisation cannot read an environment's
// configuration, and the device-read policy does not let a tenant write.
func TestTenantIsolation(t *testing.T) {
	t.Parallel()
	f := newFixture(t, limits.Set{})
	f.mustSet(t, "production", "strict", ``, 0)
	err := f.db.InTx(context.Background(), storage.Tenant{OrganizationID: uuid7Zero}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := dbgen.New(tx).LatestSecurityConfig(ctx, storage.MustUUID(f.envs["production"]))
		return err
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("another organisation read the configuration: %v", err)
	}
	err = f.db.InTx(context.Background(), storage.Tenant{Scope: storage.ScopeRegistration}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := dbgen.New(tx).InsertSecurityConfig(ctx, dbgen.InsertSecurityConfigParams{
			OrganizationID: storage.MustUUID(f.org), AppID: storage.MustUUID(f.app), EnvironmentID: storage.MustUUID(f.envs["production"]),
			Version: 9, Profile: "strict", Overrides: []byte(`{}`), CreatedByKind: "user", CreatedByID: "x", CreatedBy: "x",
		})
		return err
	})
	if err == nil {
		t.Error("the registration scope wrote a configuration")
	}
}
