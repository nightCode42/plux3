// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package auth_test

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"sync"
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
)

// clock is a settable clock, so that TOTP time steps can be crossed
// without waiting.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type ids struct{ g *uuid7.Generator }

func (i ids) New() (string, error) {
	u, err := i.g.New()
	return u.String(), err
}

// fixture is a service against a fresh schema.
type fixture struct {
	db     *storage.DB
	svc    *auth.Service
	clock  *clock
	log    *audit.Log
	ids    ids
	issuer map[string]auth.TrustedIssuer
}

// failedSignIns is the tightened allowance most tests run with.
const failedSignIns = 3

func newFixture(t *testing.T, issuers map[string]auth.TrustedIssuer) *fixture {
	t.Helper()
	return newFixtureWith(t, issuers, failedSignIns)
}

func newFixtureWith(t *testing.T, issuers map[string]auth.TrustedIssuer, failed int64, configure ...func(*auth.Options)) *fixture {
	t.Helper()
	db := storagetest.Open(t)
	backend, err := signing.NewFile(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	set, err := limits.Defaults().Tighten(limits.AuthFailedSignIns, limits.ScopeInstallation, failed)
	if err != nil {
		t.Fatal(err)
	}
	c := &clock{t: time.Now().UTC().Truncate(time.Second)}
	gen := ids{g: uuid7.NewGenerator(time.Now, rand.Reader)}
	log := audit.NewLog(gen, nil)
	o := auth.Options{
		DB: db, Audit: log, Cache: cache.NewMemory(nil), Limits: set, Crypter: backend, IDs: gen,
		Now: c.now, VerificationURI: "https://plux.example/device", Issuers: issuers,
	}
	for _, f := range configure {
		f(&o)
	}
	svc, err := auth.NewService(o)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{db: db, svc: svc, clock: c, log: log, ids: gen, issuer: issuers}
}

const password = "correct horse battery"

// person creates an account with a password and returns its user ID.
func (f *fixture) person(t *testing.T, email string) string {
	t.Helper()
	ctx := context.Background()
	var invitation string
	if err := f.db.InTx(ctx, storage.Tenant{Scope: storage.ScopeInstallation}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		_, invitation, err = f.svc.Invite(ctx, tx, email)
		return err
	}); err != nil {
		t.Fatalf("Invite: %v", err)
	}
	user, err := f.svc.AcceptInvitation(ctx, invitation, "Ada Lovelace", password)
	if err != nil {
		t.Fatalf("AcceptInvitation: %v", err)
	}
	return user.ID
}

// organisation creates an organisation with members and their roles.
func (f *fixture) organisation(t *testing.T, key string, members map[string]auth.Role) string {
	t.Helper()
	ctx := context.Background()
	org, _ := f.ids.New()
	if _, err := f.db.Pool().Exec(ctx, `INSERT INTO organizations (id, key, name) VALUES ($1, $2, $2)`, org, key); err != nil {
		t.Fatal(err)
	}
	if err := f.db.InTx(ctx, storage.Tenant{OrganizationID: org}, func(ctx context.Context, tx pgx.Tx) error {
		for user, role := range members {
			id, _ := f.ids.New()
			if _, err := tx.Exec(ctx, `INSERT INTO memberships (id, organization_id, user_id, role) VALUES ($1, $2, $3, $4)`,
				id, org, user, string(role)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return org
}

// signIn signs in without a second factor and returns the identity the
// session cookie resolves to.
func (f *fixture) signIn(t *testing.T, email string) auth.Identity {
	t.Helper()
	ctx := context.Background()
	session, challenge, err := f.svc.StartPasswordLogin(ctx, email, password)
	if err != nil || challenge.Secret != "" {
		t.Fatalf("StartPasswordLogin = %+v, %+v, %v", session, challenge, err)
	}
	id, err := f.svc.AuthenticateSession(ctx, session.Secret)
	if err != nil {
		t.Fatalf("AuthenticateSession: %v", err)
	}
	return id
}

// enrol adds and confirms a TOTP factor and returns its secret and the
// session, which now counts as having presented a second factor.
func (f *fixture) enrol(t *testing.T, id auth.Identity) (string, auth.Identity) {
	t.Helper()
	ctx := context.Background()
	e, err := f.svc.EnrollTOTP(ctx, id, "phone")
	if err != nil {
		t.Fatalf("EnrollTOTP: %v", err)
	}
	if !strings.HasPrefix(e.OTPAuthURL, "otpauth://totp/") || e.Factor.Confirmed {
		t.Errorf("enrolment = %+v", e)
	}
	code, _ := auth.TOTPCode(e.Secret, f.clock.now())
	if _, err := f.svc.ConfirmFactor(ctx, id, e.Factor.ID, code); err != nil {
		t.Fatalf("ConfirmFactor: %v", err)
	}
	f.clock.advance(30 * time.Second)
	return e.Secret, f.reload(t, id)
}

// reload re-reads whether a session has presented a second factor. The
// session's secret is not kept by the fixture, so the row is read.
func (f *fixture) reload(t *testing.T, id auth.Identity) auth.Identity {
	t.Helper()
	if err := f.db.Pool().QueryRow(context.Background(),
		`SELECT mfa_at IS NOT NULL FROM sessions WHERE id = $1`, id.SessionID).Scan(&id.SecondFactor); err != nil {
		t.Fatal(err)
	}
	return id
}

// code returns an error's Plux code, or 0.
func code(err error) plxerr.Code {
	var e *plxerr.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}

// Verifies: SEC-100.
// The first administrator is created once, sets a password through the
// invitation, and signs in; a second bootstrap is refused.
func TestBootstrapInviteAndSignIn(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	ctx := context.Background()
	admin, invitation, err := f.svc.Bootstrap(ctx, "admin@example.com")
	if err != nil || !admin.InstallationAdmin || !strings.HasPrefix(invitation, auth.PrefixInvitation+"_") {
		t.Fatalf("Bootstrap = %+v, %q, %v", admin, invitation, err)
	}
	if _, _, err := f.svc.Bootstrap(ctx, "second@example.com"); code(err) != plxerr.PreconditionFailed {
		t.Errorf("a second bootstrap: %v", err)
	}
	if _, _, err := f.svc.StartPasswordLogin(ctx, "admin@example.com", password); code(err) != plxerr.AuthenticationRequired {
		t.Errorf("signing in before accepting: %v", err)
	}
	for _, bad := range []struct{ name, password string }{
		{"Ada", "short"},
		{"", password},
		{"ada@example.com", password},
	} {
		if _, err := f.svc.AcceptInvitation(ctx, invitation, bad.name, bad.password); code(err) != plxerr.InvalidFormat {
			t.Errorf("AcceptInvitation(%q, %q): %v", bad.name, bad.password, err)
		}
	}
	if _, err := f.svc.AcceptInvitation(ctx, invitation, "Ada", password); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.AcceptInvitation(ctx, invitation, "Ada", password); code(err) != plxerr.PreconditionFailed {
		t.Errorf("an invitation was accepted twice: %v", err)
	}
	id := f.signIn(t, "ADMIN@example.com")
	if id.Kind != auth.KindUser || !id.InstallationAdmin || id.SecondFactor || id.CSRFToken == "" {
		t.Errorf("identity = %+v", id)
	}
}

// Verifies: SEC-100.
// A wrong password and an unknown address are refused alike, and an
// account is locked for the window after auth.failedSignIns failures.
func TestFailedSignInsAreRefusedAlikeAndThrottled(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	ctx := context.Background()
	f.person(t, "ada@example.com")
	_, _, unknown := f.svc.StartPasswordLogin(ctx, "nobody@example.com", password)
	_, _, wrong := f.svc.StartPasswordLogin(ctx, "ada@example.com", "incorrect password")
	if unknown == nil || wrong == nil || unknown.Error() != wrong.Error() {
		t.Errorf("refusals differ: %v / %v", unknown, wrong)
	}
	for range failedSignIns - 1 {
		_, _, _ = f.svc.StartPasswordLogin(ctx, "ada@example.com", "incorrect password")
	}
	if _, _, err := f.svc.StartPasswordLogin(ctx, "ada@example.com", password); code(err) != plxerr.RateLimited {
		t.Errorf("the right password after %d failures: %v; want RATE_LIMITED", failedSignIns, err)
	}
}

// Verifies: SEC-100, SEC-106.
// With a confirmed factor, sign-in needs a code; a code works once; a
// challenge closes after a few wrong codes.
func TestSecondFactorAtSignIn(t *testing.T) {
	t.Parallel()
	// A generous allowance, so that the challenge's own bound is what
	// closes it rather than the account's throttle.
	f := newFixtureWith(t, nil, 10)
	ctx := context.Background()
	f.person(t, "ada@example.com")
	secret, _ := f.enrol(t, f.signIn(t, "ada@example.com"))

	var stored []byte
	if err := f.db.Pool().QueryRow(ctx, `SELECT secret FROM mfa_factors`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored), secret) {
		t.Error("the TOTP secret is stored in the clear")
	}

	session, challenge, err := f.svc.StartPasswordLogin(ctx, "ada@example.com", password)
	if err != nil || session.ID != "" || challenge.Secret == "" || challenge.Kinds[0] != "totp" {
		t.Fatalf("StartPasswordLogin = %+v, %+v, %v", session, challenge, err)
	}
	now, _ := auth.TOTPCode(secret, f.clock.now())
	session, err = f.svc.CompleteMFA(ctx, challenge.Secret, now)
	if err != nil || !session.SecondFactor {
		t.Fatalf("CompleteMFA = %+v, %v", session, err)
	}
	if _, err := f.svc.CompleteMFA(ctx, challenge.Secret, now); code(err) != plxerr.AuthenticationRequired {
		t.Errorf("a used challenge answered again: %v", err)
	}

	// The same code in a new sign-in is a replay.
	_, again, err := f.svc.StartPasswordLogin(ctx, "ada@example.com", password)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CompleteMFA(ctx, again.Secret, now); err == nil {
		t.Error("a TOTP code was accepted twice")
	}
	// Wrong codes close the challenge.
	for range 5 {
		_, _ = f.svc.CompleteMFA(ctx, again.Secret, "000000")
	}
	f.clock.advance(30 * time.Second)
	next, _ := auth.TOTPCode(secret, f.clock.now())
	if _, err := f.svc.CompleteMFA(ctx, again.Secret, next); err == nil {
		t.Error("a challenge stayed open after five wrong codes")
	}
}

// Verifies: SEC-100.
// A session presents a second factor on demand; removing a factor needs
// one; changing the password ends the other sessions.
func TestSessionSecurity(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	ctx := context.Background()
	f.person(t, "ada@example.com")
	first := f.signIn(t, "ada@example.com")
	secret, first := f.enrol(t, first)

	other, challenge, err := f.svc.StartPasswordLogin(ctx, "ada@example.com", password)
	if err != nil || other.ID != "" {
		t.Fatal(err)
	}
	c, _ := auth.TOTPCode(secret, f.clock.now())
	second, err := f.svc.CompleteMFA(ctx, challenge.Secret, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.AuthenticateSession(ctx, second.Secret); err != nil {
		t.Fatal(err)
	}

	factors, err := f.svc.ListFactors(ctx, first)
	if err != nil || len(factors) != 1 || !factors[0].Confirmed {
		t.Fatalf("ListFactors = %+v, %v", factors, err)
	}
	withoutFactor := first
	withoutFactor.SecondFactor = false
	if err := f.svc.DeleteFactor(ctx, withoutFactor, factors[0].ID); code(err) != plxerr.MultiFactorRequired {
		t.Errorf("DeleteFactor without a second factor: %v", err)
	}

	f.clock.advance(30 * time.Second)
	c, _ = auth.TOTPCode(secret, f.clock.now())
	verified, err := f.svc.VerifySecondFactor(ctx, first, c)
	if err != nil || !verified.SecondFactor {
		t.Errorf("VerifySecondFactor = %+v, %v", verified, err)
	}

	if err := f.svc.ChangePassword(ctx, first, "not the password", "a brand new passphrase"); code(err) != plxerr.AuthenticationRequired {
		t.Errorf("ChangePassword with a wrong current password: %v", err)
	}
	if err := f.svc.ChangePassword(ctx, first, password, "a brand new passphrase"); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if _, err := f.svc.AuthenticateSession(ctx, second.Secret); err == nil {
		t.Error("another session survived a password change")
	}
	if err := f.svc.Logout(ctx, first); err != nil {
		t.Fatal(err)
	}
	var revoked bool
	if err := f.db.Pool().QueryRow(ctx, `SELECT revoked_at IS NOT NULL FROM sessions WHERE id = $1`, first.SessionID).Scan(&revoked); err != nil || !revoked {
		t.Errorf("Logout left the session open: %v", err)
	}
	if err := f.svc.DeleteFactor(ctx, withSecondFactor(first), factors[0].ID); err != nil {
		t.Errorf("DeleteFactor with a second factor: %v", err)
	}
}

// withSecondFactor marks an identity as having presented a second
// factor.
func withSecondFactor(id auth.Identity) auth.Identity {
	id.SecondFactor = true
	return id
}

// Verifies: SEC-102, SRV-064.
// A personal access token needs a second factor to create, carries no
// more than its creator holds, is bound to its organisation, and stops
// working when revoked.
func TestPersonalAccessTokens(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	ctx := context.Background()
	ada := f.person(t, "ada@example.com")
	org := f.organisation(t, "acme", map[string]auth.Role{ada: auth.RoleDeveloper})
	other := f.organisation(t, "other", map[string]auth.Role{ada: auth.RoleOwner})
	id := f.signIn(t, "ada@example.com")

	p, err := f.svc.Resolve(ctx, id, org)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CreateAccessToken(ctx, p, "ci", nil, time.Hour); code(err) != plxerr.MultiFactorRequired {
		t.Errorf("a token without a second factor: %v", err)
	}
	_, id = f.enrol(t, id)
	p, _ = f.svc.Resolve(ctx, id, org)
	if _, err := f.svc.CreateAccessToken(ctx, p, "ci", []string{string(auth.MembersManage)}, time.Hour); code(err) != plxerr.PermissionDenied {
		t.Errorf("a token wider than its creator: %v", err)
	}
	for _, ttl := range []time.Duration{0, 400 * 24 * time.Hour} {
		if _, err := f.svc.CreateAccessToken(ctx, p, "ci", nil, ttl); code(err) != plxerr.OutOfRange {
			t.Errorf("ttl %s: %v", ttl, err)
		}
	}
	minted, err := f.svc.CreateAccessToken(ctx, p, "ci", []string{string(auth.PluginRead)}, time.Hour)
	if err != nil || !strings.HasPrefix(minted.Secret, auth.PrefixToken+"_") {
		t.Fatalf("CreateAccessToken = %+v, %v", minted, err)
	}
	tokenID, err := f.svc.AuthenticateToken(ctx, minted.Secret)
	if err != nil || tokenID.Kind != auth.KindToken || tokenID.OrganizationID != org {
		t.Fatalf("AuthenticateToken = %+v, %v", tokenID, err)
	}
	tp, err := f.svc.Resolve(ctx, tokenID, "")
	if err != nil {
		t.Fatal(err)
	}
	if !tp.Holds(auth.PluginRead) || tp.Holds(auth.PluginEdit) {
		t.Errorf("token permissions = %v; want only %s", tp.Permissions, auth.PluginRead)
	}
	if _, err := f.svc.Resolve(ctx, tokenID, other); code(err) != plxerr.PermissionDenied {
		t.Errorf("a token acted in another organisation: %v", err)
	}
	tokens, err := f.svc.ListAccessTokens(ctx, p, storage.Cursor{}, 10)
	if err != nil || len(tokens) != 1 || tokens[0].Prefix == "" {
		t.Errorf("ListAccessTokens = %+v, %v", tokens, err)
	}
	if err := f.svc.RevokeAccessToken(ctx, p, minted.Token.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.AuthenticateToken(ctx, minted.Secret); code(err) != plxerr.AuthenticationRequired {
		t.Errorf("a revoked token authenticated: %v", err)
	}
	for _, bad := range []string{"", "plux_pat_nope", "plux_ses_x"} {
		if _, err := f.svc.AuthenticateToken(ctx, bad); err == nil {
			t.Errorf("AuthenticateToken(%q) succeeded", bad)
		}
	}
}

// Verifies: SEC-102.
// A person with no part in an organisation is told it does not exist; a
// grant on one app gives permissions on that app only.
func TestResolveIsDenyByDefault(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	ctx := context.Background()
	ada := f.person(t, "ada@example.com")
	f.person(t, "bob@example.com")
	org := f.organisation(t, "acme", map[string]auth.Role{ada: auth.RoleOwner})
	bob := f.signIn(t, "bob@example.com")
	if _, err := f.svc.Resolve(ctx, bob, org); code(err) != plxerr.ResourceNotFound {
		t.Errorf("an outsider resolved: %v", err)
	}
	if _, err := f.svc.Resolve(ctx, bob, ""); code(err) != plxerr.InvalidFormat {
		t.Errorf("no organisation named: %v", err)
	}
	app, _ := f.ids.New()
	grant, _ := f.ids.New()
	if err := f.db.InTx(ctx, storage.Tenant{OrganizationID: org}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO apps (id, organization_id, key, name) VALUES ($1, $2, 'shop', 'Shop')`, app, org); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO app_access (id, organization_id, app_id, user_id, role) VALUES ($1, $2, $3, $4, 'developer')`,
			grant, org, app, bob.UserID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	p, err := f.svc.Resolve(ctx, bob, org)
	if err != nil {
		t.Fatal(err)
	}
	if p.Authorize(auth.AppRead) == nil || p.AuthorizeApp(auth.PluginEdit, app) != nil || p.AuthorizeApp(auth.PluginEdit, "other") == nil {
		t.Errorf("app grant permissions = %v, %v", p.Permissions, p.AppPermissions)
	}
	if got := p.AllowedApps(); len(got) != 1 || got[0] != app {
		t.Errorf("AllowedApps = %v", got)
	}
	// Publishing needs a second factor even with the permission.
	if err := p.AuthorizeApp(auth.ReleasePublish, app); code(err) != plxerr.MultiFactorRequired {
		t.Errorf("publishing without a second factor: %v", err)
	}
}

// Verifies: CLI-002.
// The device authorization grant: pending, slow down, approved by a
// person with a second factor, the token handed out exactly once.
func TestDeviceAuthorizationGrant(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	ctx := context.Background()
	ada := f.person(t, "ada@example.com")
	org := f.organisation(t, "acme", map[string]auth.Role{ada: auth.RoleDeveloper})
	grant, err := f.svc.StartDeviceAuthorization(ctx, "plux-cli", nil)
	if err != nil || len(grant.UserCode) != 9 || !strings.Contains(grant.VerificationURIComplete, grant.UserCode) {
		t.Fatalf("StartDeviceAuthorization = %+v, %v", grant, err)
	}
	if _, err := f.svc.StartDeviceAuthorization(ctx, "plux-cli", []string{"nonsense"}); err == nil {
		t.Error("an unknown scope was accepted")
	}
	status, _, err := f.svc.PollDeviceAuthorization(ctx, grant.DeviceCode)
	if err != nil || status != auth.PollPending {
		t.Errorf("first poll = %s, %v", status, err)
	}
	status, _, _ = f.svc.PollDeviceAuthorization(ctx, grant.DeviceCode)
	if status != auth.PollSlowDown {
		t.Errorf("an immediate second poll = %s; want slow_down", status)
	}

	id := f.signIn(t, "ada@example.com")
	p, _ := f.svc.Resolve(ctx, id, org)
	if err := f.svc.ApproveDeviceAuthorization(ctx, p, grant.UserCode); code(err) != plxerr.MultiFactorRequired {
		t.Errorf("approval without a second factor: %v", err)
	}
	_, id = f.enrol(t, id)
	p, _ = f.svc.Resolve(ctx, id, org)
	if err := f.svc.ApproveDeviceAuthorization(ctx, p, strings.ToLower(strings.ReplaceAll(grant.UserCode, "-", ""))); err != nil {
		t.Fatalf("ApproveDeviceAuthorization: %v", err)
	}
	status, minted, err := f.svc.PollDeviceAuthorization(ctx, grant.DeviceCode)
	if err != nil || status != auth.PollApproved || minted.Secret == "" || minted.Token.Source != "cli" {
		t.Fatalf("poll after approval = %s, %+v, %v", status, minted, err)
	}
	if _, _, err := f.svc.PollDeviceAuthorization(ctx, grant.DeviceCode); code(err) != plxerr.AuthenticationRequired {
		t.Errorf("the token was handed out twice: %v", err)
	}
	tokenID, err := f.svc.AuthenticateToken(ctx, minted.Secret)
	if err != nil {
		t.Fatal(err)
	}
	tp, _ := f.svc.Resolve(ctx, tokenID, "")
	if !tp.Holds(auth.ReleasePublish) || tp.Holds(auth.MembersManage) {
		t.Errorf("a login token carries %v; want the developer's permissions", tp.Permissions)
	}

	denied, _ := f.svc.StartDeviceAuthorization(ctx, "plux-cli", nil)
	if err := f.svc.DenyDeviceAuthorization(ctx, id, denied.UserCode); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := f.svc.PollDeviceAuthorization(ctx, denied.DeviceCode); status != auth.PollDenied {
		t.Errorf("poll after denial = %s", status)
	}
	if _, _, err := f.svc.PollDeviceAuthorization(ctx, "plux_dev_unknown"); code(err) != plxerr.AuthenticationRequired {
		t.Errorf("an unknown device code: %v", err)
	}
}

// Verifies: SEC-140.
// Sign-ins land in the installation's chain; organisation actions in
// the organisation's.
func TestIdentityEventsAreAudited(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	ctx := context.Background()
	f.person(t, "ada@example.com")
	f.signIn(t, "ada@example.com")
	_, _, _ = f.svc.StartPasswordLogin(ctx, "ada@example.com", "wrong password!")
	var actions []string
	if err := f.db.InTx(ctx, storage.Tenant{Scope: storage.ScopeInstallation}, func(ctx context.Context, tx pgx.Tx) error {
		entries, err := f.log.List(ctx, tx, "", 0, 100)
		for _, e := range entries {
			actions = append(actions, string(e.Action))
		}
		if _, err := f.log.VerifyChain(ctx, tx, ""); err != nil {
			t.Errorf("VerifyChain: %v", err)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{"user.invitation_accepted", "user.signed_in", "user.sign_in_refused"}
	if strings.Join(actions, ",") != strings.Join(want, ",") {
		t.Errorf("installation chain = %v; want %v", actions, want)
	}
	if n, err := f.svc.Expire(ctx); err != nil || n != 0 {
		t.Errorf("Expire = %d, %v", n, err)
	}
}

// Verifies: SEC-100, SEC-102.
// The edges of the identity service: who a call may come from, and what
// it refuses before touching anything.
func TestIdentityRefusals(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	ctx := context.Background()
	ada := f.person(t, "ada@example.com")
	org := f.organisation(t, "acme", map[string]auth.Role{ada: auth.RoleViewer})
	id := f.signIn(t, "ada@example.com")

	user, memberships, err := f.svc.CurrentUser(ctx, id)
	if err != nil || user.Email != "ada@example.com" || user.MFAEnrolled || len(memberships) != 1 {
		t.Errorf("CurrentUser = %+v, %+v, %v", user, memberships, err)
	}
	if _, _, err := f.svc.CurrentUser(ctx, auth.Identity{Kind: auth.KindCI}); code(err) != plxerr.PreconditionFailed {
		t.Errorf("CurrentUser for CI: %v", err)
	}

	token := auth.Identity{Kind: auth.KindToken, ID: "t", UserID: id.UserID, OrganizationID: org, Scopes: []auth.Permission{}}
	if _, err := f.svc.EnrollTOTP(ctx, token, "x"); code(err) != plxerr.PermissionDenied {
		t.Errorf("a token enrolled a factor: %v", err)
	}
	if _, err := f.svc.EnrollTOTP(ctx, id, strings.Repeat("x", 101)); code(err) != plxerr.InvalidFormat {
		t.Errorf("an oversized label: %v", err)
	}
	if _, err := f.svc.ConfirmFactor(ctx, id, "not-an-id", "123456"); code(err) != plxerr.InvalidFormat {
		t.Errorf("a malformed factor ID: %v", err)
	}
	e, err := f.svc.EnrollTOTP(ctx, id, "phone")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ConfirmFactor(ctx, id, e.Factor.ID, "000000"); code(err) != plxerr.AuthenticationRequired {
		t.Errorf("a wrong confirmation code: %v", err)
	}
	if _, err := f.svc.VerifySecondFactor(ctx, token, "000000"); code(err) != plxerr.PreconditionFailed {
		t.Errorf("a token verified a second factor: %v", err)
	}
	if _, err := f.svc.VerifySecondFactor(ctx, id, "000000"); code(err) != plxerr.AuthenticationRequired {
		t.Errorf("a wrong second-factor code: %v", err)
	}
	if err := f.svc.ChangePassword(ctx, token, password, "another good passphrase"); code(err) != plxerr.PreconditionFailed {
		t.Errorf("a token changed a password: %v", err)
	}
	if err := f.svc.ChangePassword(ctx, id, password, "short"); code(err) != plxerr.InvalidFormat {
		t.Errorf("a weak new password: %v", err)
	}
	if err := f.svc.Logout(ctx, token); err != nil {
		t.Errorf("Logout without a session: %v", err)
	}

	if err := f.db.InTx(ctx, storage.Tenant{Scope: storage.ScopeInstallation}, func(ctx context.Context, tx pgx.Tx) error {
		existing, invitation, err := f.svc.Invite(ctx, tx, "ada@example.com")
		if err != nil || existing.ID != ada || invitation != "" {
			t.Errorf("inviting an existing account = %+v, %q, %v", existing, invitation, err)
		}
		if _, _, err := f.svc.Invite(ctx, tx, "not an address"); code(err) != plxerr.InvalidFormat {
			t.Errorf("inviting a non-address: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	p, err := f.svc.Resolve(ctx, id, org)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.RevokeAccessToken(ctx, p, "not-an-id"); code(err) != plxerr.InvalidFormat {
		t.Errorf("revoking a malformed ID: %v", err)
	}
	missing, _ := f.ids.New()
	if err := f.svc.RevokeAccessToken(ctx, p, missing); code(err) != plxerr.ResourceNotFound {
		t.Errorf("revoking a missing token: %v", err)
	}
	if err := f.svc.DeleteWorkloadIdentity(ctx, p, missing); code(err) != plxerr.PermissionDenied {
		t.Errorf("a viewer deleted a workload identity: %v", err)
	}
	if _, err := f.svc.CreateAccessToken(ctx, auth.Principal{Identity: token, OrganizationID: org}, "x", nil, time.Hour); code(err) != plxerr.PermissionDenied {
		t.Errorf("a token minted a token: %v", err)
	}
	if err := f.svc.ApproveDeviceAuthorization(ctx, auth.Principal{Identity: token, OrganizationID: org}, "BCDF-GHJK"); code(err) != plxerr.PermissionDenied {
		t.Errorf("a token approved a login: %v", err)
	}
	if err := f.svc.DenyDeviceAuthorization(ctx, token, "BCDF-GHJK"); code(err) != plxerr.PermissionDenied {
		t.Errorf("a token denied a login: %v", err)
	}
	if err := f.svc.DenyDeviceAuthorization(ctx, id, "BCDF-GHJK"); code(err) != plxerr.ResourceNotFound {
		t.Errorf("denying an unknown code: %v", err)
	}
	if _, err := f.svc.StartDeviceAuthorization(ctx, "", nil); code(err) != plxerr.InvalidFormat {
		t.Errorf("a grant with no client: %v", err)
	}
	if _, err := f.svc.AuthenticateSession(ctx, "plux_ses_unknown"); code(err) != plxerr.AuthenticationRequired {
		t.Errorf("an unknown session: %v", err)
	}
	if _, err := f.svc.AuthenticateSession(ctx, "garbage"); code(err) != plxerr.AuthenticationRequired {
		t.Errorf("a malformed session: %v", err)
	}
}

// Verifies: SEC-106.
// The service refuses to start without what keeps it safe.
func TestNewServiceNeedsItsDependencies(t *testing.T) {
	t.Parallel()
	if _, err := auth.NewService(auth.Options{}); err == nil {
		t.Error("a service with no dependencies was built")
	}
}
