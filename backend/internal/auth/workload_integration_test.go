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
	"strings"
	"testing"
	"time"

	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// provider is an OpenID provider that mints CI identity tokens, the way
// GitHub Actions does: a discovery document, a key set, RS256 tokens.
type provider struct {
	srv *httptest.Server
	key *rsa.PrivateKey
}

func newProvider(t *testing.T) *provider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &provider{key: key}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": p.srv.URL, "jwks_uri": p.srv.URL + "/keys"})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, _ *http.Request) {
		e := big.NewInt(int64(key.E)).Bytes()
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "k1", "use": "sig",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(e),
		}}})
	})
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

// token mints an identity token with the given claims.
func (p *provider) token(t *testing.T, claims map[string]any) string {
	t.Helper()
	enc := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	signed := enc(map[string]string{"alg": "RS256", "kid": "k1", "typ": "JWT"}) + "." + enc(claims)
	sum := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return signed + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// Verifies: SRV-064.
// A pipeline trades its provider's identity token for a short-lived
// token of an organisation, with no long-lived secret; a token for the
// wrong audience, subject or issuer, or with a forged signature, gets
// nothing.
func TestWorkloadIdentityFederation(t *testing.T) {
	t.Parallel()
	p := newProvider(t)
	// The provider runs on loopback, which the SSRF-safe client refuses,
	// so the test's verifier uses a plain client (SEC-105 is tested in
	// httpx).
	v, err := auth.NewVerifier(p.srv.URL, p.srv.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	issuers := map[string]auth.TrustedIssuer{p.srv.URL: {
		Verifier: v, Audience: "https://plux.example", SubjectPattern: "repo:acme/*",
	}}
	f := newFixture(t, issuers)
	ctx := context.Background()
	ada := f.person(t, "ada@example.com")
	org := f.organisation(t, "acme", map[string]auth.Role{ada: auth.RoleOwner})
	id := f.signIn(t, "ada@example.com")
	_, id = f.enrol(t, id)
	principal, err := f.svc.Resolve(ctx, id, org)
	if err != nil {
		t.Fatal(err)
	}

	for _, bad := range []auth.WorkloadIdentity{
		{Issuer: "https://elsewhere.example", Audience: "https://plux.example", SubjectPattern: "repo:acme/app:*"},
		{Issuer: p.srv.URL, Audience: "https://other.example", SubjectPattern: "repo:acme/app:*"},
		{Issuer: p.srv.URL, Audience: "https://plux.example", SubjectPattern: "repo:*"},
	} {
		if _, err := f.svc.CreateWorkloadIdentity(ctx, principal, bad, []string{"plugin.read"}); err == nil {
			t.Errorf("CreateWorkloadIdentity(%+v) was accepted", bad)
		}
	}
	w, err := f.svc.CreateWorkloadIdentity(ctx, principal, auth.WorkloadIdentity{
		Issuer: p.srv.URL, Audience: "https://plux.example", SubjectPattern: "repo:acme/app",
	}, []string{"plugin.read", "release.publish"})
	if err != nil {
		t.Fatalf("CreateWorkloadIdentity: %v", err)
	}
	list, err := f.svc.ListWorkloadIdentities(ctx, principal)
	if err != nil || len(list) != 1 || list[0].ID != w.ID {
		t.Errorf("ListWorkloadIdentities = %+v, %v", list, err)
	}

	now := time.Now()
	claims := func(sub, aud string) map[string]any {
		return map[string]any{"iss": p.srv.URL, "sub": sub, "aud": aud, "exp": now.Add(5 * time.Minute).Unix(), "iat": now.Unix()}
	}
	minted, err := f.svc.ExchangeWorkloadIdentity(ctx, p.token(t, claims("repo:acme/app", "https://plux.example")), org)
	if err != nil {
		t.Fatalf("ExchangeWorkloadIdentity: %v", err)
	}
	ci, err := f.svc.AuthenticateToken(ctx, minted.Secret)
	if err != nil || ci.Kind != auth.KindCI || ci.UserID != "" {
		t.Fatalf("AuthenticateToken = %+v, %v", ci, err)
	}
	cp, err := f.svc.Resolve(ctx, ci, "")
	if err != nil || !cp.Holds(auth.ReleasePublish) || cp.Holds(auth.PluginEdit) || cp.Authorize(auth.ReleasePublish) != nil {
		t.Errorf("CI principal = %+v, %v", cp, err)
	}

	forged := p.token(t, claims("repo:acme/app", "https://plux.example"))
	forged = forged[:strings.LastIndex(forged, ".")+1] + base64.RawURLEncoding.EncodeToString([]byte("not a signature"))
	expired := claims("repo:acme/app", "https://plux.example")
	expired["exp"] = now.Add(-time.Hour).Unix()
	for name, tok := range map[string]string{
		"wrong subject":  p.token(t, claims("repo:acme/other", "https://plux.example")),
		"wrong audience": p.token(t, claims("repo:acme/app", "https://other.example")),
		"expired":        p.token(t, expired),
		"forged":         forged,
		"not a token":    "a.b",
	} {
		if _, err := f.svc.ExchangeWorkloadIdentity(ctx, tok, org); code(err) != plxerr.AuthenticationRequired {
			t.Errorf("%s: %v; want AUTHENTICATION_REQUIRED", name, err)
		}
	}
	if err := f.svc.DeleteWorkloadIdentity(ctx, principal, w.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ExchangeWorkloadIdentity(ctx, p.token(t, claims("repo:acme/app", "https://plux.example")), org); err == nil {
		t.Error("a deleted workload identity still exchanged")
	}
}

// Verifies: SRV-064.
// The verifier accepts only what an OpenID provider must support and
// only from the issuer it was built for.
func TestVerifierRefusals(t *testing.T) {
	t.Parallel()
	p := newProvider(t)
	ctx := context.Background()
	now := time.Now()
	v, err := auth.NewVerifier(p.srv.URL+"/", p.srv.Client(), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.NewVerifier("", nil, nil); err == nil {
		t.Error("a verifier with no issuer was built")
	}
	base := map[string]any{"iss": p.srv.URL, "sub": "repo:acme/app", "aud": []string{"a", "b"}, "exp": now.Add(time.Minute).Unix()}
	if claims, err := v.Verify(ctx, p.token(t, base), "b"); err != nil || claims.Subject != "repo:acme/app" {
		t.Errorf("a token with a list audience: %+v, %v", claims, err)
	}
	with := func(k string, val any) map[string]any {
		c := map[string]any{}
		for key, value := range base {
			c[key] = value
		}
		if val == nil {
			delete(c, k)
		} else {
			c[k] = val
		}
		return c
	}
	for name, claims := range map[string]map[string]any{
		"another issuer": with("iss", "https://evil.example"),
		"not yet valid":  with("nbf", now.Add(time.Hour).Unix()),
		"no subject":     with("sub", nil),
		"no expiry":      with("exp", nil),
	} {
		if _, err := v.Verify(ctx, p.token(t, claims), "a"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := v.Verify(ctx, p.token(t, base), "c"); err == nil {
		t.Error("a token for another audience was accepted")
	}
	none := base64Segment(t, map[string]string{"alg": "none", "kid": "k1"}) + "." + base64Segment(t, base) + "."
	if _, err := v.Verify(ctx, none, "a"); err == nil {
		t.Error("an unsigned token was accepted")
	}
	unknownKey := base64Segment(t, map[string]string{"alg": "RS512", "kid": "k9"}) + "." + base64Segment(t, base) + ".c2ln"
	if _, err := v.Verify(ctx, unknownKey, "a"); err == nil {
		t.Error("a token signed by an unpublished key was accepted")
	}
	for _, malformed := range []string{"x.y.z", "a.b", base64Segment(t, map[string]string{"alg": "RS256"}) + ".!!.x"} {
		if _, err := v.Verify(ctx, malformed, "a"); err == nil {
			t.Errorf("%q was accepted", malformed)
		}
	}
	// A provider whose discovery document names another issuer is not
	// trusted, and neither is one that serves nothing.
	liar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": "https://evil.example", "jwks_uri": "https://evil.example/keys"})
	}))
	t.Cleanup(liar.Close)
	lv, _ := auth.NewVerifier(liar.URL, liar.Client(), nil)
	if _, err := lv.Verify(ctx, p.token(t, base), "a"); err == nil {
		t.Error("a lying discovery document was trusted")
	}
	gone := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(gone.Close)
	gv, _ := auth.NewVerifier(gone.URL, gone.Client(), nil)
	if _, err := gv.Verify(ctx, p.token(t, base), "a"); err == nil {
		t.Error("a provider with no discovery document was trusted")
	}
}

// base64Segment encodes one JWS segment.
func base64Segment(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
