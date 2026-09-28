// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package auth_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// idp is an OpenID provider for sign-in: discovery, keys and a token
// endpoint that checks the PKCE verifier and returns an ID token with the
// claims the test sets for the code.
type idp struct {
	srv *httptest.Server
	key *rsa.PrivateKey

	mu     sync.Mutex
	claims map[string]map[string]any // by code
	pkce   map[string]string         // code challenge by code
}

func newIDP(t *testing.T) *idp {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &idp{key: key, claims: map[string]map[string]any{}, pkce: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer": p.srv.URL, "jwks_uri": p.srv.URL + "/keys",
			"authorization_endpoint": p.srv.URL + "/authorize", "token_endpoint": p.srv.URL + "/token",
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "k1", "use": "sig",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		user, pass, _ := r.BasicAuth()
		code, verifier := r.PostFormValue("code"), r.PostFormValue("code_verifier")
		sum := sha256.Sum256([]byte(verifier))
		p.mu.Lock()
		claims, ok := p.claims[code]
		want := p.pkce[code]
		delete(p.claims, code)
		p.mu.Unlock()
		if !ok || user != "studio" || pass != "s3cret" || base64.RawURLEncoding.EncodeToString(sum[:]) != want {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"id_token": p.sign(t, claims), "token_type": "Bearer"})
	})
	p.srv = httptest.NewTLSServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

func (p *idp) sign(t *testing.T, claims map[string]any) string {
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	signed := enc(map[string]string{"alg": "RS256", "kid": "k1"}) + "." + enc(claims)
	sum := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, sum[:])
	if err != nil {
		t.Error(err)
	}
	return signed + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// authorize plays the browser at the provider: it reads the sign-in URL
// and issues a code for a subject and address.
func (p *idp) authorize(t *testing.T, login auth.OIDCLogin, subject, email string, verified bool, nonce string) string {
	t.Helper()
	u, err := url.Parse(login.URL)
	if err != nil || !strings.HasPrefix(login.URL, p.srv.URL+"/authorize?") {
		t.Fatalf("sign-in URL %q", login.URL)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("state") != login.State || q.Get("client_id") != "studio" || !strings.Contains(q.Get("scope"), "openid") {
		t.Errorf("sign-in parameters %v", q)
	}
	if nonce == "" {
		nonce = q.Get("nonce")
	}
	code := base64.RawURLEncoding.EncodeToString([]byte(subject + time.Now().String()))
	p.mu.Lock()
	p.claims[code] = map[string]any{
		"iss": p.srv.URL, "sub": subject, "aud": "studio", "exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Unix(), "nonce": nonce, "email": email, "email_verified": verified,
	}
	p.pkce[code] = q.Get("code_challenge")
	p.mu.Unlock()
	return code
}

// Verifies: SEC-100.
// A person with an account signs in through the provider: the code is
// redeemed with PKCE, the ID token's signature, audience and nonce are
// checked, and the identity is linked by its verified address. An
// unknown or unverified address, a replayed state and a token for
// another sign-in are refused; a second factor is still asked for.
func TestOIDCSignIn(t *testing.T) {
	t.Parallel()
	p := newIDP(t)
	provider, err := auth.NewOIDCProvider(auth.OIDCConfig{
		Issuer: p.srv.URL, ClientID: "studio", ClientSecret: "s3cret",
		RedirectURL: "https://plux.example/auth/callback", Client: p.srv.Client(),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f := newFixtureWith(t, nil, failedSignIns, func(o *auth.Options) { o.OIDC = provider })
	ctx := context.Background()
	f.person(t, "ada@example.com")

	login, err := f.svc.StartOIDCLogin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	authCode := p.authorize(t, login, "sub-ada", "Ada@Example.com", true, "")
	session, challenge, err := f.svc.CompleteOIDCLogin(ctx, login.State, authCode)
	if err != nil || session.Secret == "" || challenge.Secret != "" {
		t.Fatalf("CompleteOIDCLogin = %+v, %+v, %v", session, challenge, err)
	}
	if _, _, err := f.svc.CompleteOIDCLogin(ctx, login.State, authCode); code(err) != plxerr.AuthenticationRequired {
		t.Errorf("a replayed state: %v", err)
	}
	// The link holds even when the provider's address changes.
	login, _ = f.svc.StartOIDCLogin(ctx)
	if _, _, err := f.svc.CompleteOIDCLogin(ctx, login.State, p.authorize(t, login, "sub-ada", "new@example.com", false, "")); err != nil {
		t.Errorf("a linked identity: %v", err)
	}
	for name, c := range map[string]struct {
		subject, email string
		verified       bool
		nonce          string
	}{
		"an unknown address":    {"sub-bob", "bob@example.com", true, ""},
		"an unverified address": {"sub-eve", "ada@example.com", false, ""},
		"another sign-in":       {"sub-ada", "ada@example.com", true, "someone-elses-nonce"},
	} {
		login, _ := f.svc.StartOIDCLogin(ctx)
		if _, _, err := f.svc.CompleteOIDCLogin(ctx, login.State, p.authorize(t, login, c.subject, c.email, c.verified, c.nonce)); code(err) != plxerr.AuthenticationRequired {
			t.Errorf("%s: %v", name, err)
		}
	}
	login, _ = f.svc.StartOIDCLogin(ctx)
	if _, _, err := f.svc.CompleteOIDCLogin(ctx, login.State, "a-code-the-provider-never-issued"); code(err) != plxerr.AuthenticationRequired {
		t.Errorf("an unknown code: %v", err)
	}
	// With a second factor, the provider's sign-in yields a challenge.
	id := f.signIn(t, "ada@example.com")
	f.enrol(t, id)
	login, _ = f.svc.StartOIDCLogin(ctx)
	_, challenge, err = f.svc.CompleteOIDCLogin(ctx, login.State, p.authorize(t, login, "sub-ada", "ada@example.com", true, ""))
	if err != nil || challenge.Secret == "" {
		t.Errorf("with a second factor = %+v, %v", challenge, err)
	}
	// An expired sign-in is refused.
	login, _ = f.svc.StartOIDCLogin(ctx)
	f.clock.advance(11 * time.Minute)
	if _, _, err := f.svc.CompleteOIDCLogin(ctx, login.State, p.authorize(t, login, "sub-ada", "ada@example.com", true, "")); code(err) != plxerr.AuthenticationRequired {
		t.Errorf("an expired sign-in: %v", err)
	}
}

// Verifies: SEC-100.
// A provider is refused without https or credentials, and sign-in is
// refused when none is configured.
func TestOIDCConfiguration(t *testing.T) {
	t.Parallel()
	for _, c := range []auth.OIDCConfig{
		{Issuer: "http://idp.example", ClientID: "a", ClientSecret: "b", RedirectURL: "https://p/cb", Client: http.DefaultClient},
		{Issuer: "https://idp.example", ClientID: "a", RedirectURL: "https://p/cb", Client: http.DefaultClient},
		{Issuer: "https://idp.example", ClientID: "a", ClientSecret: "b", RedirectURL: "https://p/cb"},
	} {
		if _, err := auth.NewOIDCProvider(c, nil); err == nil {
			t.Errorf("accepted %+v", c)
		}
	}
	f := newFixture(t, nil)
	if _, err := f.svc.StartOIDCLogin(context.Background()); code(err) != plxerr.PreconditionFailed {
		t.Errorf("without a provider: %v", err)
	}
}
