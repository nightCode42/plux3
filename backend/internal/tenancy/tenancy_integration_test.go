// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package tenancy_test

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

// fixture is the identity and tenancy services against a fresh schema,
// with an installation administrator who owns one organisation.
type fixture struct {
	db     *storage.DB
	auth   *auth.Service
	svc    *tenancy.Service
	log    *audit.Log
	now    time.Time
	admin  auth.Identity
	org    string
	owner  auth.Principal
	ids    ids
	crypto signing.Crypter
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
	f := &fixture{db: db, log: log, ids: gen, crypto: backend, now: time.Now()}
	f.auth, err = auth.NewService(auth.Options{
		DB: db, Audit: log, Cache: cache.NewMemory(nil), Crypter: backend, IDs: gen, VerificationURI: "https://p.example/device",
	})
	if err != nil {
		t.Fatal(err)
	}
	f.svc, err = tenancy.NewService(tenancy.Options{
		DB: db, Audit: log, Auth: f.auth, Crypter: backend, IDs: gen,
		SigningKeyPrefix: "targets", Now: func() time.Time { return f.now },
	})
	if err != nil {
		t.Fatal(err)
	}
	admin, invitation, err := f.auth.Bootstrap(ctx, "admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.auth.AcceptInvitation(ctx, invitation, "Admin", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	f.admin = person(admin.ID, true)
	o, err := f.svc.CreateOrganization(ctx, f.admin, "acme", "Acme")
	if err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}
	f.org = o.ID
	f.owner = f.principal(t, f.admin, f.org)
	return f
}

// person is a signed-in user who has presented a second factor.
func person(userID string, admin bool) auth.Identity {
	return auth.Identity{Kind: auth.KindUser, ID: userID, UserID: userID, Display: "user", SecondFactor: true, InstallationAdmin: admin}
}

// principal resolves an identity in an organisation.
func (f *fixture) principal(t *testing.T, id auth.Identity, org string) auth.Principal {
	t.Helper()
	p, err := f.auth.Resolve(context.Background(), id, org)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return p
}

// member adds a person to the organisation with a role and returns
// their identity.
func (f *fixture) member(t *testing.T, email, role string) auth.Identity {
	t.Helper()
	m, _, err := f.svc.AddMember(context.Background(), f.owner, "", email, role)
	if err != nil {
		t.Fatalf("AddMember: %v", err)
	}
	return person(m.UserID, false)
}

func code(err error) plxerr.Code {
	var e *plxerr.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}

// Verifies: GOV-001, SEC-102.
// Only an installation administrator creates organisations; the creator
// owns it; members are invited, teams group them, and the last owner
// cannot be removed or demoted.
func TestOrganisationsTeamsAndMembers(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.svc.CreateOrganization(ctx, person(f.admin.UserID, false), "other", "Other"); code(err) != plxerr.PermissionDenied {
		t.Errorf("a non-administrator created an organisation: %v", err)
	}
	if _, err := f.svc.CreateOrganization(ctx, f.admin, "acme", "Duplicate"); code(err) != plxerr.ResourceExists {
		t.Errorf("a duplicate key: %v", err)
	}
	if _, err := f.svc.CreateOrganization(ctx, f.admin, "Not Valid", "x"); code(err) != plxerr.InvalidFormat {
		t.Errorf("an invalid key: %v", err)
	}
	orgs, err := f.svc.ListOrganizations(ctx, f.admin, tenancy.Page{Size: 10})
	if err != nil || len(orgs) != 1 || orgs[0].Key != "acme" {
		t.Errorf("ListOrganizations = %+v, %v", orgs, err)
	}
	renamed, err := f.svc.UpdateOrganization(ctx, f.owner, "Acme Ltd")
	if err != nil || renamed.Name != "Acme Ltd" {
		t.Errorf("UpdateOrganization = %+v, %v", renamed, err)
	}

	m, invitation, err := f.svc.AddMember(ctx, f.owner, "", "bob@example.com", "developer")
	if err != nil || invitation == "" || m.Role != "developer" {
		t.Fatalf("AddMember = %+v, %q, %v", m, invitation, err)
	}
	if _, again, err := f.svc.AddMember(ctx, f.owner, "", "bob@example.com", "viewer"); err != nil || again != "" {
		t.Errorf("re-adding an account = %q, %v; want no invitation", again, err)
	}
	if _, _, err := f.svc.AddMember(ctx, f.owner, "", "carol@example.com", "root"); code(err) != plxerr.InvalidEnumValue {
		t.Errorf("an unknown role: %v", err)
	}
	bob := f.principal(t, person(m.UserID, false), f.org)
	if _, _, err := f.svc.AddMember(ctx, bob, "", "carol@example.com", "viewer"); code(err) != plxerr.PermissionDenied {
		t.Errorf("a viewer added a member: %v", err)
	}

	team, err := f.svc.CreateTeam(ctx, f.owner, "mobile", "Mobile")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CreateTeam(ctx, f.owner, "mobile", "Again"); code(err) != plxerr.ResourceExists {
		t.Errorf("a duplicate team: %v", err)
	}
	if _, _, err := f.svc.AddMember(ctx, f.owner, team.ID, "bob@example.com", "owner"); code(err) != plxerr.InvalidEnumValue {
		t.Errorf("a team membership with an organisation role: %v", err)
	}
	tm, _, err := f.svc.AddMember(ctx, f.owner, team.ID, "bob@example.com", "")
	if err != nil || tm.Role != "member" || tm.TeamID != team.ID {
		t.Errorf("team membership = %+v, %v", tm, err)
	}
	teams, err := f.svc.ListTeams(ctx, f.owner, tenancy.Page{Size: 10})
	if err != nil || len(teams) != 1 {
		t.Errorf("ListTeams = %+v, %v", teams, err)
	}
	if _, err := f.svc.UpdateTeam(ctx, f.owner, team.ID, "Mobile apps"); err != nil {
		t.Error(err)
	}
	members, err := f.svc.ListMembers(ctx, f.owner, "", tenancy.Page{Size: 1})
	if err != nil || len(members) != 1 {
		t.Fatalf("ListMembers page 1 = %+v, %v", members, err)
	}
	next, err := f.svc.ListMembers(ctx, f.owner, "", tenancy.Page{Size: 10, After: storage.Cursor{ID: members[0].UserID, Key: members[0].MembershipID}})
	if err != nil || len(next) != 1 || next[0].UserID == members[0].UserID {
		t.Errorf("ListMembers page 2 = %+v, %v", next, err)
	}

	if err := f.svc.RemoveMember(ctx, f.owner, "", f.admin.UserID); code(err) != plxerr.PreconditionFailed {
		t.Errorf("removing the last owner: %v", err)
	}
	if _, _, err := f.svc.AddMember(ctx, f.owner, "", "admin@example.com", "admin"); code(err) != plxerr.PreconditionFailed {
		t.Errorf("demoting the last owner: %v", err)
	}
	if err := f.svc.RemoveMember(ctx, f.owner, team.ID, m.UserID); err != nil {
		t.Errorf("RemoveMember from a team: %v", err)
	}
	if err := f.svc.RemoveMember(ctx, f.owner, team.ID, m.UserID); code(err) != plxerr.ResourceNotFound {
		t.Errorf("removing twice: %v", err)
	}
	if err := f.svc.DeleteTeam(ctx, f.owner, team.ID); err != nil {
		t.Error(err)
	}
	if err := f.svc.DeleteTeam(ctx, f.owner, team.ID); code(err) != plxerr.ResourceNotFound {
		t.Errorf("deleting a team twice: %v", err)
	}
}

// Verifies: GOV-010, REL-005, SEC-106, DAT-003.
// An app comes with development, staging and production, each with its
// own signing key and a production channel; variables are plain and
// secrets are sealed and never listed with their value.
func TestAppsEnvironmentsAndSecrets(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	app, err := f.svc.CreateApp(ctx, f.owner, "shop", "Shop")
	if err != nil {
		t.Fatal(err)
	}
	envs, err := f.svc.ListEnvironments(ctx, f.owner, app.ID, tenancy.Page{Size: 10})
	if err != nil || len(envs) != 3 {
		t.Fatalf("ListEnvironments = %+v, %v", envs, err)
	}
	refs := map[string]bool{}
	var prod, dev tenancy.Environment
	for _, e := range envs {
		refs[e.SigningKeyRef] = true
		if !strings.HasPrefix(e.SigningKeyRef, "targets-") {
			t.Errorf("signing key ref %q", e.SigningKeyRef)
		}
		switch e.Key {
		case "production":
			prod = e
		case "development":
			dev = e
		}
	}
	if len(refs) != 3 || !prod.Production || dev.Production {
		t.Errorf("environments = %+v", envs)
	}
	channels, err := f.svc.ListChannels(ctx, f.owner, prod.ID, tenancy.Page{Size: 10})
	if err != nil || len(channels) != 1 || channels[0].Key != "production" {
		t.Errorf("ListChannels = %+v, %v", channels, err)
	}
	if err := f.svc.DeleteChannel(ctx, f.owner, channels[0].ID); code(err) != plxerr.PreconditionFailed {
		t.Errorf("deleting the default channel: %v", err)
	}
	beta, err := f.svc.CreateChannel(ctx, f.owner, prod.ID, "beta")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.DeleteChannel(ctx, f.owner, beta.ID); err != nil {
		t.Error(err)
	}
	if err := f.svc.DeleteEnvironment(ctx, f.owner, prod.ID); code(err) != plxerr.PreconditionFailed {
		t.Errorf("deleting production: %v", err)
	}
	qa, err := f.svc.CreateEnvironment(ctx, f.owner, app.ID, "qa", "QA", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.UpdateEnvironment(ctx, f.owner, qa.ID, "Quality"); err != nil {
		t.Error(err)
	}
	if err := f.svc.DeleteEnvironment(ctx, f.owner, qa.ID); err != nil {
		t.Error(err)
	}

	if _, err := f.svc.SetVariable(ctx, f.owner, dev.ID, "API_BASE", "https://api.example"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SetVariable(ctx, f.owner, dev.ID, "not a key", "x"); code(err) != plxerr.InvalidFormat {
		t.Errorf("an invalid variable key: %v", err)
	}
	vars, err := f.svc.ListVariables(ctx, f.owner, dev.ID, tenancy.Page{Size: 10})
	if err != nil || len(vars) != 1 || vars[0].Value != "https://api.example" {
		t.Errorf("ListVariables = %+v, %v", vars, err)
	}

	const value = "demo-value-0123456789abcdef"
	sec, err := f.svc.SetSecret(ctx, f.owner, dev.ID, "STRIPE_KEY", value)
	if err != nil || sec.Hint != "…cdef" {
		t.Fatalf("SetSecret = %+v, %v", sec, err)
	}
	var stored []byte
	if err := f.db.InTx(ctx, storage.Tenant{OrganizationID: f.org}, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT ciphertext FROM environment_secrets`).Scan(&stored)
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored), value) {
		t.Error("a secret is stored in the clear")
	}
	got, err := f.svc.OpenSecret(ctx, f.owner, dev.ID, "STRIPE_KEY")
	if err != nil || string(got) != value {
		t.Errorf("OpenSecret = %q, %v", got, err)
	}
	// Copying one environment's ciphertext onto another's secret does not
	// make it readable there.
	if _, err := f.svc.SetSecret(ctx, f.owner, prod.ID, "STRIPE_KEY", "other"); err != nil {
		t.Fatal(err)
	}
	if err := f.db.InTx(ctx, storage.Tenant{OrganizationID: f.org}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE environment_secrets p SET ciphertext = d.ciphertext, wrapped_key = d.wrapped_key
			FROM environment_secrets d WHERE p.environment_id = $1 AND d.environment_id = $2`, prod.ID, dev.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.OpenSecret(ctx, f.owner, prod.ID, "STRIPE_KEY"); err == nil {
		t.Error("a secret moved to another environment was opened")
	}
	secrets, err := f.svc.ListSecrets(ctx, f.owner, dev.ID, tenancy.Page{Size: 10})
	if err != nil || len(secrets) != 1 || secrets[0].Hint != "…cdef" {
		t.Errorf("ListSecrets = %+v, %v", secrets, err)
	}
	if _, err := f.svc.SetSecret(ctx, f.owner, dev.ID, "EMPTY", ""); code(err) != plxerr.MissingProperty {
		t.Errorf("an empty secret: %v", err)
	}
	if err := f.svc.DeleteSecret(ctx, f.owner, dev.ID, "STRIPE_KEY"); err != nil {
		t.Error(err)
	}
	if err := f.svc.DeleteSecret(ctx, f.owner, dev.ID, "STRIPE_KEY"); code(err) != plxerr.ResourceNotFound {
		t.Errorf("deleting a secret twice: %v", err)
	}

	withoutFactor := f.owner
	withoutFactor.SecondFactor = false
	if _, err := f.svc.SetSecret(ctx, withoutFactor, dev.ID, "K", "v"); code(err) != plxerr.MultiFactorRequired {
		t.Errorf("setting a secret without a second factor: %v", err)
	}
	updated, err := f.svc.UpdateApp(ctx, f.owner, app.ID, "Shop 2", "checkout")
	if err != nil || updated.DefaultPluginKey != "checkout" {
		t.Errorf("UpdateApp = %+v, %v", updated, err)
	}
}

// Verifies: GOV-001, SEC-102, SRV-022.
// Access is granted per app to users and teams; a person with a grant
// sees that app only; nobody sees another organisation's apps.
func TestAppAccessAndIsolation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	shop, _ := f.svc.CreateApp(ctx, f.owner, "shop", "Shop")
	blog, _ := f.svc.CreateApp(ctx, f.owner, "blog", "Blog")
	team, _ := f.svc.CreateTeam(ctx, f.owner, "web", "Web")

	carol := f.member(t, "carol@example.com", "viewer")
	if err := f.svc.RemoveMember(ctx, f.owner, "", carol.UserID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.svc.AddMember(ctx, f.owner, team.ID, "carol@example.com", ""); err != nil {
		t.Fatal(err)
	}
	grant, err := f.svc.GrantAccess(ctx, f.owner, shop.ID, team.ID, "", "developer")
	if err != nil {
		t.Fatalf("GrantAccess: %v", err)
	}
	if _, err := f.svc.GrantAccess(ctx, f.owner, shop.ID, team.ID, "", "viewer"); err != nil {
		t.Errorf("re-granting: %v", err)
	}
	if _, err := f.svc.GrantAccess(ctx, f.owner, shop.ID, team.ID, carol.UserID, "viewer"); code(err) != plxerr.InvalidStructure {
		t.Errorf("a grant to a team and a user: %v", err)
	}
	stranger, _ := f.ids.New()
	if _, err := f.svc.GrantAccess(ctx, f.owner, shop.ID, "", stranger, "viewer"); code(err) != plxerr.ResourceNotFound {
		t.Errorf("a grant to a non-member: %v", err)
	}
	grants, err := f.svc.ListAccess(ctx, f.owner, shop.ID, tenancy.Page{Size: 10})
	if err != nil || len(grants) != 1 || grants[0].Role != "viewer" {
		t.Errorf("ListAccess = %+v, %v", grants, err)
	}

	cp := f.principal(t, carol, f.org)
	apps, err := f.svc.ListApps(ctx, cp, tenancy.Page{Size: 10})
	if err != nil || len(apps) != 1 || apps[0].ID != shop.ID {
		t.Errorf("a team member's apps = %+v, %v", apps, err)
	}
	if _, err := f.svc.GetApp(ctx, cp, blog.ID); code(err) != plxerr.ResourceNotFound {
		t.Errorf("an app without a grant: %v", err)
	}
	if _, err := f.svc.UpdateApp(ctx, cp, shop.ID, "Renamed", ""); code(err) != plxerr.PermissionDenied {
		t.Errorf("a viewer renamed an app: %v", err)
	}
	if err := f.svc.RevokeAccess(ctx, f.owner, grant.ID); err != nil {
		t.Fatal(err)
	}
	cp = f.principal(t, carol, f.org)
	if _, err := f.svc.ListApps(ctx, cp, tenancy.Page{Size: 10}); code(err) != plxerr.PermissionDenied {
		t.Errorf("listing apps after the grant was revoked: %v", err)
	}

	// Another organisation's owner sees nothing of this one, even by ID.
	other, err := f.svc.CreateOrganization(ctx, f.admin, "other", "Other")
	if err != nil {
		t.Fatal(err)
	}
	op := f.principal(t, f.admin, other.ID)
	if _, err := f.svc.GetApp(ctx, op, shop.ID); code(err) != plxerr.ResourceNotFound {
		t.Errorf("another organisation read an app: %v", err)
	}
	if apps, err := f.svc.ListApps(ctx, op, tenancy.Page{Size: 10}); err != nil || len(apps) != 0 {
		t.Errorf("another organisation listed %d apps: %v", len(apps), err)
	}
	if err := f.svc.DeleteTeam(ctx, op, team.ID); code(err) != plxerr.ResourceNotFound {
		t.Errorf("another organisation deleted a team: %v", err)
	}
}

// Verifies: GOV-031.
// A deleted app waits in the trash, can be restored while its key is
// free, and is purged when its retention ends or on request.
func TestTrash(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	shop, _ := f.svc.CreateApp(ctx, f.owner, "shop", "Shop")
	item, err := f.svc.DeleteApp(ctx, f.owner, shop.ID)
	if err != nil || item.Kind != "app" || item.PurgeAfter.Sub(item.DeletedAt) < 29*24*time.Hour {
		t.Fatalf("DeleteApp = %+v, %v", item, err)
	}
	if _, err := f.svc.GetApp(ctx, f.owner, shop.ID); code(err) != plxerr.ResourceNotFound {
		t.Errorf("a deleted app is readable: %v", err)
	}
	items, err := f.svc.ListTrash(ctx, f.owner, "", tenancy.Page{Size: 10})
	if err != nil || len(items) != 1 {
		t.Fatalf("ListTrash = %+v, %v", items, err)
	}
	if byApp, err := f.svc.ListTrash(ctx, f.owner, shop.ID, tenancy.Page{Size: 10}); err != nil || len(byApp) != 1 {
		t.Errorf("ListTrash for the app = %+v, %v", byApp, err)
	}
	// While it is in the trash, its key is taken by a new app, so it
	// cannot come back under it.
	newer, err := f.svc.CreateApp(ctx, f.owner, "shop", "Newer shop")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.RestoreFromTrash(ctx, f.owner, item.ID); code(err) != plxerr.ResourceExists {
		t.Errorf("restoring over a reused key: %v", err)
	}
	if _, err := f.svc.DeleteApp(ctx, f.owner, newer.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.RestoreFromTrash(ctx, f.owner, item.ID); err != nil {
		t.Fatalf("RestoreFromTrash: %v", err)
	}
	if _, err := f.svc.GetApp(ctx, f.owner, shop.ID); err != nil {
		t.Errorf("a restored app is not readable: %v", err)
	}
	if err := f.svc.RestoreFromTrash(ctx, f.owner, item.ID); code(err) != plxerr.ResourceNotFound {
		t.Errorf("restoring twice: %v", err)
	}
	items, _ = f.svc.ListTrash(ctx, f.owner, "", tenancy.Page{Size: 10})
	if len(items) != 1 {
		t.Fatalf("trash holds %d items; want the newer app", len(items))
	}
	if err := f.svc.PurgeFromTrash(ctx, f.owner, items[0].ID); err != nil {
		t.Fatalf("PurgeFromTrash: %v", err)
	}

	// Retention: an item past its purge time goes in the sweep.
	if _, err := f.svc.DeleteApp(ctx, f.owner, shop.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := f.svc.PurgeExpired(ctx, 10); err != nil || n != 0 {
		t.Errorf("sweep before retention ends = %d, %v", n, err)
	}
	f.now = f.now.AddDate(0, 0, -31)
	if _, err := f.svc.CreateApp(ctx, f.owner, "old", "Old"); err != nil {
		t.Fatal(err)
	}
	apps, _ := f.svc.ListApps(ctx, f.owner, tenancy.Page{Size: 10})
	for _, a := range apps {
		if a.Key == "old" {
			if _, err := f.svc.DeleteApp(ctx, f.owner, a.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if n, err := f.svc.PurgeExpired(ctx, 10); err != nil || n != 1 {
		t.Errorf("sweep after retention = %d, %v; want 1", n, err)
	}
	viewer := f.principal(t, f.member(t, "v@example.com", "viewer"), f.org)
	if _, err := f.svc.ListTrash(ctx, viewer, "", tenancy.Page{Size: 10}); code(err) != plxerr.PermissionDenied {
		t.Errorf("a viewer listed the trash: %v", err)
	}
}

// Verifies: LIM-002, LIM-005.
// An organisation and an app can tighten a limit but never raise it
// above the scope above.
func TestLimitsTightenDownwards(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	shop, _ := f.svc.CreateApp(ctx, f.owner, "shop", "Shop")
	def := limits.Defaults().Get(limits.AppPlugins)
	if _, err := f.svc.SetOrganizationLimit(ctx, f.owner, string(limits.AppPlugins), def+1); code(err) != plxerr.OutOfRange {
		t.Errorf("raising a limit: %v", err)
	}
	if _, err := f.svc.SetOrganizationLimit(ctx, f.owner, "no.such.limit", 1); code(err) != plxerr.InvalidEnumValue {
		t.Errorf("an unknown limit: %v", err)
	}
	u, err := f.svc.SetOrganizationLimit(ctx, f.owner, string(limits.AppPlugins), 50)
	if err != nil || u.Effective != 50 || u.Scope != "organization" {
		t.Fatalf("SetOrganizationLimit = %+v, %v", u, err)
	}
	if _, err := f.svc.SetAppLimit(ctx, f.owner, shop.ID, string(limits.AppPlugins), 60); code(err) != plxerr.OutOfRange {
		t.Errorf("an app above its organisation: %v", err)
	}
	if u, err := f.svc.SetAppLimit(ctx, f.owner, shop.ID, string(limits.AppPlugins), 20); err != nil || u.Effective != 20 {
		t.Errorf("SetAppLimit = %+v, %v", u, err)
	}
	orgLimits, err := f.svc.ListOrganizationLimits(ctx, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	appLimits, err := f.svc.ListAppLimits(ctx, f.owner, shop.ID)
	if err != nil {
		t.Fatal(err)
	}
	find := func(us []tenancy.LimitUsage) int64 {
		for _, u := range us {
			if u.Key == limits.AppPlugins {
				return u.Effective
			}
		}
		return -1
	}
	if find(orgLimits) != 50 || find(appLimits) != 20 {
		t.Errorf("effective limits: organisation %d, app %d", find(orgLimits), find(appLimits))
	}
	set, err := f.svc.OrganizationLimits(ctx, f.org)
	if err != nil || set.Get(limits.AppPlugins) != 50 {
		t.Errorf("OrganizationLimits = %d, %v", set.Get(limits.AppPlugins), err)
	}
}

// Verifies: SEC-140.
// Every change above is in the organisation's audit log, in a chain
// that verifies; only an auditor may read it, and only an installation
// administrator reads the installation's.
func TestTenancyChangesAreAudited(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.svc.CreateApp(ctx, f.owner, "shop", "Shop"); err != nil {
		t.Fatal(err)
	}
	entries, err := f.svc.AuditEntries(ctx, f.owner, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var actions []string
	for _, e := range entries {
		actions = append(actions, string(e.Action))
		if e.Actor.ID != f.admin.UserID {
			t.Errorf("entry %d actor = %+v", e.Sequence, e.Actor)
		}
	}
	joined := strings.Join(actions, ",")
	for _, want := range []string{"organization.created", "app.created", "environment.created", "channel.created"} {
		if !strings.Contains(joined, want) {
			t.Errorf("audit log %v lacks %s", actions, want)
		}
	}
	if err := audit.Verify(entries, ""); err != nil {
		t.Errorf("Verify: %v", err)
	}
	viewer := f.principal(t, f.member(t, "v@example.com", "viewer"), f.org)
	if _, err := f.svc.AuditEntries(ctx, viewer, 0, 10); code(err) != plxerr.PermissionDenied {
		t.Errorf("a viewer read the audit log: %v", err)
	}
	installation, err := f.svc.InstallationAuditEntries(ctx, f.admin, 0, 100)
	if err != nil || len(installation) == 0 {
		t.Errorf("InstallationAuditEntries = %d entries, %v", len(installation), err)
	}
	if _, err := f.svc.InstallationAuditEntries(ctx, person(f.admin.UserID, false), 0, 10); code(err) != plxerr.PermissionDenied {
		t.Errorf("a non-administrator read the installation's log: %v", err)
	}
}

// Verifies: SEC-104.
// Malformed input is refused before anything is read or written.
func TestTenancyRefusesMalformedInput(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	if _, err := tenancy.NewService(tenancy.Options{}); err == nil {
		t.Error("a service with no dependencies was built")
	}
	for name, err := range map[string]error{
		"team key":        errOf(f.svc.CreateTeam(ctx, f.owner, "Bad Key", "x")),
		"team name":       errOf(f.svc.CreateTeam(ctx, f.owner, "ok", "")),
		"team ID":         errOf(f.svc.UpdateTeam(ctx, f.owner, "nope", "x")),
		"delete team ID":  f.svc.DeleteTeam(ctx, f.owner, "nope"),
		"member team ID":  errOf3(f.svc.AddMember(ctx, f.owner, "nope", "a@example.com", "")),
		"remove user ID":  f.svc.RemoveMember(ctx, f.owner, "", "nope"),
		"remove team ID":  f.svc.RemoveMember(ctx, f.owner, "nope", f.admin.UserID),
		"list team ID":    errOf(f.svc.ListMembers(ctx, f.owner, "nope", tenancy.Page{Size: 1})),
		"app key":         errOf(f.svc.CreateApp(ctx, f.owner, "", "x")),
		"app name":        errOf(f.svc.CreateApp(ctx, f.owner, "ok", strings.Repeat("x", 201))),
		"app ID":          errOf(f.svc.GetApp(ctx, f.owner, "nope")),
		"plugin key":      errOf(f.svc.UpdateApp(ctx, f.owner, "nope", "x", "Bad")),
		"environment key": errOf(f.svc.CreateEnvironment(ctx, f.owner, "nope", "Bad", "x", false)),
		"environment ID":  errOf(f.svc.ListVariables(ctx, f.owner, "nope", tenancy.Page{Size: 1})),
		"channel key":     errOf(f.svc.CreateChannel(ctx, f.owner, "nope", "Bad")),
		"channel ID":      f.svc.DeleteChannel(ctx, f.owner, "nope"),
		"grant role":      errOf(f.svc.GrantAccess(ctx, f.owner, "nope", "", "u", "root")),
		"grant ID":        f.svc.RevokeAccess(ctx, f.owner, "nope"),
		"trash app ID":    errOf(f.svc.ListTrash(ctx, f.owner, "nope", tenancy.Page{Size: 1})),
		"trash item ID":   f.svc.RestoreFromTrash(ctx, f.owner, "nope"),
		"limit app ID":    errOf(f.svc.SetAppLimit(ctx, f.owner, "nope", "app.plugins", 1)),
		"limits org ID":   errOf(f.svc.OrganizationLimits(ctx, "nope")),
		"secret key":      errOf(f.svc.SetSecret(ctx, f.owner, "nope", "1bad", "v")),
		"org name":        errOf(f.svc.UpdateOrganization(ctx, f.owner, "")),
	} {
		c := code(err)
		if c != plxerr.InvalidFormat && c != plxerr.InvalidEnumValue {
			t.Errorf("%s: %v; want INVALID_FORMAT or INVALID_ENUM_VALUE", name, err)
		}
	}
	missing, _ := f.ids.New()
	for name, err := range map[string]error{
		"app":         errOf(f.svc.GetApp(ctx, f.owner, missing)),
		"environment": errOf(f.svc.ListChannels(ctx, f.owner, missing, tenancy.Page{Size: 1})),
		"channel":     f.svc.DeleteChannel(ctx, f.owner, missing),
		"grant":       f.svc.RevokeAccess(ctx, f.owner, missing),
		"trash":       f.svc.PurgeFromTrash(ctx, f.owner, missing),
		"team":        errOf(f.svc.UpdateTeam(ctx, f.owner, missing, "x")),
	} {
		if code(err) != plxerr.ResourceNotFound {
			t.Errorf("%s: %v; want RESOURCE_NOT_FOUND", name, err)
		}
	}
	if _, err := f.svc.ListOrganizations(ctx, auth.Identity{Kind: auth.KindCI}, tenancy.Page{Size: 1}); code(err) != plxerr.PreconditionFailed {
		t.Errorf("CI listed organisations: %v", err)
	}
}

// errOf keeps a call's error.
func errOf[T any](_ T, err error) error { return err }

// errOf3 keeps a three-result call's error.
func errOf3[T, U any](_ T, _ U, err error) error { return err }
