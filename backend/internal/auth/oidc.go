// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/httpx"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/signing"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
)

// This file signs people in with an OpenID Connect provider (SEC-100):
// the authorization code flow with PKCE (RFC 7636, S256) and a nonce, as
// a confidential client. The provider authenticates the person; Plux
// decides who they are. An identity is linked to an account that already
// exists — created by invitation, like every account — on its first
// sign-in, by the address the provider says it verified. Provisioning,
// group mapping and SAML are GOV-004's, in P9.

// oidcLoginTTL is how long a sign-in may take at the provider.
const oidcLoginTTL = 10 * time.Minute

// OIDCConfig configures the installation's OpenID Connect provider.
type OIDCConfig struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	// RedirectURL is Studio's callback, registered with the provider.
	RedirectURL string
	// Client is the SSRF-safe HTTP client (SEC-105).
	Client *http.Client
}

// OIDCProvider is the installation's OpenID Connect provider.
type OIDCProvider struct {
	OIDCConfig

	verifier *Verifier
	mu       sync.Mutex
	metadata *oidcMetadata
}

// oidcMetadata is the part of the discovery document the flow uses.
type oidcMetadata struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
}

// NewOIDCProvider checks a provider's configuration.
func NewOIDCProvider(p OIDCConfig, now func() time.Time) (*OIDCProvider, error) {
	switch {
	case p.Issuer == "", p.ClientID == "", p.ClientSecret == "", p.RedirectURL == "":
		return nil, errors.New("auth: an OIDC provider needs an issuer, a client ID, a client secret and a redirect URL")
	case !strings.HasPrefix(p.Issuer, "https://"), !strings.HasPrefix(p.RedirectURL, "https://"):
		return nil, errors.New("auth: the OIDC issuer and redirect URL must be https")
	case p.Client == nil:
		return nil, errors.New("auth: an OIDC provider needs the SSRF-safe HTTP client")
	}
	v, err := NewVerifier(p.Issuer, p.Client, now)
	if err != nil {
		return nil, err
	}
	p.Issuer = strings.TrimSuffix(p.Issuer, "/")
	return &OIDCProvider{OIDCConfig: p, verifier: v}, nil
}

// discover reads the provider's endpoints once; both must belong to the
// issuer's host.
func (p *OIDCProvider) discover(ctx context.Context) (oidcMetadata, error) {
	p.mu.Lock()
	cached := p.metadata
	p.mu.Unlock()
	if cached != nil {
		return *cached, nil
	}
	var m oidcMetadata
	if err := p.verifier.getJSON(ctx, p.Issuer+"/.well-known/openid-configuration", &m); err != nil {
		return oidcMetadata{}, err
	}
	issuer, err := url.Parse(p.Issuer)
	if err != nil {
		return oidcMetadata{}, fmt.Errorf("auth: the OIDC issuer: %w", err)
	}
	if strings.TrimSuffix(m.Issuer, "/") != p.Issuer {
		return oidcMetadata{}, fmt.Errorf("auth: the discovery document names issuer %q", m.Issuer)
	}
	for _, endpoint := range []string{m.AuthorizationEndpoint, m.TokenEndpoint} {
		u, err := url.Parse(endpoint)
		if err != nil || u.Scheme != "https" || u.Host != issuer.Host {
			return oidcMetadata{}, fmt.Errorf("auth: the OIDC endpoint %q is not on the issuer's host", endpoint)
		}
	}
	p.mu.Lock()
	p.metadata = &m
	p.mu.Unlock()
	return m, nil
}

// exchange redeems an authorization code for an ID token.
func (p *OIDCProvider) exchange(ctx context.Context, endpoint, code, verifier string) (string, error) {
	form := url.Values{
		"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {p.RedirectURL}, "code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("auth: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.SetBasicAuth(url.QueryEscape(p.ClientID), url.QueryEscape(p.ClientSecret))
	resp, err := p.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("auth: redeem the code: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := httpx.ReadAll(resp.Body, 1<<20)
	if err != nil {
		return "", fmt.Errorf("auth: read the token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("auth: the provider refused the code: status %d", resp.StatusCode)
	}
	var out struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.IDToken == "" {
		return "", fmt.Errorf("auth: the token response carries no ID token (%s)", path.Base(endpoint))
	}
	return out.IDToken, nil
}

// OIDCLogin is a sign-in handed to the provider.
type OIDCLogin struct {
	// URL is where the browser goes to sign in.
	URL string
	// State comes back with the callback; Studio keeps it to finish.
	State     string
	ExpiresAt time.Time
}

// oidcRequired refuses when no provider is configured.
func (s *Service) oidcRequired() error {
	if s.o.OIDC == nil {
		return plxerr.New(plxerr.PreconditionFailed, "single sign-on is not configured on this installation")
	}
	return nil
}

// oidcBinding ties a sealed PKCE verifier to its sign-in.
func oidcBinding(stateHash []byte) []byte {
	return append([]byte("oidc-login:"), stateHash...)
}

// StartOIDCLogin begins a sign-in with the provider and returns where to
// send the browser.
func (s *Service) StartOIDCLogin(ctx context.Context) (OIDCLogin, error) {
	if err := s.oidcRequired(); err != nil {
		return OIDCLogin{}, err
	}
	m, err := s.o.OIDC.discover(ctx)
	if err != nil {
		return OIDCLogin{}, plxerr.Wrap(plxerr.UpstreamUnavailable, err, "the sign-in provider cannot be reached")
	}
	state, err := NewSecret(PrefixOIDCState, 32)
	if err != nil {
		return OIDCLogin{}, err
	}
	nonce, verifier := randomToken(), randomToken()
	sealed, err := signing.Seal(ctx, s.o.Crypter, []byte(verifier), oidcBinding(state.Hash))
	if err != nil {
		return OIDCLogin{}, fmt.Errorf("auth: %w", err)
	}
	expires := s.now().Add(oidcLoginTTL)
	if err := s.inTx(ctx, storage.Tenant{Scope: storage.ScopeInstallation}, func(ctx context.Context, tx pgx.Tx) error {
		return dbgen.New(tx).CreateOIDCLogin(ctx, dbgen.CreateOIDCLoginParams{ //nolint:wrapcheck // wrapped below
			StateHash: state.Hash, Nonce: nonce, Verifier: sealed.Encode(), ExpiresAt: storage.Timestamp(expires),
		})
	}); err != nil {
		return OIDCLogin{}, fmt.Errorf("auth: record the sign-in: %w", err)
	}
	challenge := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"response_type": {"code"}, "client_id": {s.o.OIDC.ClientID}, "redirect_uri": {s.o.OIDC.RedirectURL},
		"scope": {"openid email profile"}, "state": {state.Value}, "nonce": {nonce},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}, "code_challenge_method": {"S256"},
	}
	sep := "?"
	if strings.Contains(m.AuthorizationEndpoint, "?") {
		sep = "&"
	}
	return OIDCLogin{URL: m.AuthorizationEndpoint + sep + q.Encode(), State: state.Value, ExpiresAt: expires}, nil
}

// CompleteOIDCLogin ends a sign-in with the code the provider returned.
// Like a password, it yields a session, or a challenge when the account
// has a confirmed second factor.
func (s *Service) CompleteOIDCLogin(ctx context.Context, state, code string) (Session, Challenge, error) {
	if err := s.oidcRequired(); err != nil {
		return Session{}, Challenge{}, err
	}
	var login dbgen.OidcLogin
	err := s.inTx(ctx, storage.Tenant{Scope: storage.ScopeInstallation}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		login, err = dbgen.New(tx).TakeOIDCLogin(ctx, HashSecret(state))
		return err //nolint:wrapcheck // examined below
	})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && s.now().After(storage.Time(login.ExpiresAt))) {
		return Session{}, Challenge{}, plxerr.New(plxerr.AuthenticationRequired, "the sign-in has expired; sign in again")
	}
	if err != nil {
		return Session{}, Challenge{}, fmt.Errorf("auth: read the sign-in: %w", err)
	}
	claims, err := s.oidcClaims(ctx, login, code)
	if err != nil {
		return Session{}, Challenge{}, err
	}
	var (
		session   Session
		challenge Challenge
		refused   bool
	)
	err = s.inTx(ctx, storage.Tenant{Scope: storage.ScopeInstallation}, func(ctx context.Context, tx pgx.Tx) error {
		row, ok, err := s.linkedUser(ctx, tx, claims)
		if err != nil || !ok {
			refused = !ok
			return err
		}
		if row.DisabledAt.Valid {
			refused = true
			return s.refusal(ctx, tx, row)
		}
		session, challenge, err = s.signedIn(ctx, tx, row)
		return err
	})
	if err != nil {
		return Session{}, Challenge{}, err
	}
	if refused {
		return Session{}, Challenge{}, signInRefused()
	}
	return session, challenge, nil
}

// oidcClaims redeems the code and verifies the ID token and its nonce.
func (s *Service) oidcClaims(ctx context.Context, login dbgen.OidcLogin, code string) (Claims, error) {
	sealed, err := signing.DecodeSealed(login.Verifier)
	if err != nil {
		return Claims{}, fmt.Errorf("auth: %w", err)
	}
	verifier, err := signing.Open(ctx, s.o.Crypter, sealed, oidcBinding(login.StateHash))
	if err != nil {
		return Claims{}, fmt.Errorf("auth: %w", err)
	}
	m, err := s.o.OIDC.discover(ctx)
	if err != nil {
		return Claims{}, plxerr.Wrap(plxerr.UpstreamUnavailable, err, "the sign-in provider cannot be reached")
	}
	token, err := s.o.OIDC.exchange(ctx, m.TokenEndpoint, code, string(verifier))
	if err != nil {
		return Claims{}, plxerr.Wrap(plxerr.AuthenticationRequired, err, "the sign-in provider did not confirm the sign-in")
	}
	claims, err := s.o.OIDC.verifier.Verify(ctx, token, s.o.OIDC.ClientID)
	if err != nil {
		return Claims{}, plxerr.Wrap(plxerr.AuthenticationRequired, err, "the sign-in provider's token does not verify")
	}
	if claims.Nonce == "" || claims.Nonce != login.Nonce {
		return Claims{}, plxerr.New(plxerr.AuthenticationRequired, "the sign-in provider's token belongs to another sign-in")
	}
	return claims, nil
}

// linkedUser finds the account of a provider identity, linking it by
// verified address on its first sign-in. It reports false when no
// account may be used.
func (s *Service) linkedUser(ctx context.Context, tx pgx.Tx, c Claims) (dbgen.User, bool, error) {
	q := dbgen.New(tx)
	link, err := q.GetUserIdentity(ctx, dbgen.GetUserIdentityParams{Issuer: s.o.OIDC.Issuer, Subject: c.Subject})
	switch {
	case err == nil:
		row, err := q.GetUser(ctx, link.UserID)
		if err != nil {
			return dbgen.User{}, false, fmt.Errorf("auth: read a user: %w", err)
		}
		return row, true, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return dbgen.User{}, false, fmt.Errorf("auth: read an identity: %w", err)
	}
	if c.Email == "" || !c.emailVerified() {
		return dbgen.User{}, false, nil
	}
	row, err := q.GetUserByEmail(ctx, strings.ToLower(strings.TrimSpace(c.Email)))
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.User{}, false, nil
	}
	if err != nil {
		return dbgen.User{}, false, fmt.Errorf("auth: read a user: %w", err)
	}
	id, err := s.newID()
	if err != nil {
		return dbgen.User{}, false, err
	}
	if _, err := q.LinkUserIdentity(ctx, dbgen.LinkUserIdentityParams{
		ID: storage.MustUUID(id), UserID: row.ID, Issuer: s.o.OIDC.Issuer, Subject: c.Subject,
	}); err != nil {
		return dbgen.User{}, false, fmt.Errorf("auth: link an identity: %w", err)
	}
	if row.InvitationHash != nil {
		// Signing in through the provider accepts the invitation: the
		// provider has proved the address it was sent to.
		if row, err = q.AcceptInvitationExternally(ctx, row.ID); err != nil {
			return dbgen.User{}, false, fmt.Errorf("auth: accept the invitation: %w", err)
		}
	}
	return row, true, s.record(ctx, tx, audit.Entry{
		Actor:  audit.Actor{Kind: KindUser, ID: storage.ID(row.ID), Display: row.DisplayName},
		Action: audit.IdentityLinked, TargetKind: "user", TargetID: storage.ID(row.ID), Detail: s.o.OIDC.Issuer,
	})
}

// signedIn finishes a first-factor sign-in: a challenge when the account
// has a confirmed second factor, a session otherwise.
func (s *Service) signedIn(ctx context.Context, tx pgx.Tx, row dbgen.User) (Session, Challenge, error) {
	factors, err := dbgen.New(tx).CountConfirmedFactors(ctx, row.ID)
	if err != nil {
		return Session{}, Challenge{}, fmt.Errorf("auth: read the factors: %w", err)
	}
	if factors > 0 {
		c, err := s.challenge(ctx, tx, row.ID)
		return Session{}, c, err
	}
	session, err := s.issueSession(ctx, tx, row, false)
	return session, Challenge{}, err
}

// randomToken is 32 random bytes, base64url-encoded.
func randomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // crypto/rand.Read never fails (Go 1.24)
	return base64.RawURLEncoding.EncodeToString(b)
}
