// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package devtoken_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"

	"github.com/nightCode42/plux3/backend/internal/devtoken"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/signing"
)

var epoch = time.Date(2026, time.October, 7, 12, 0, 0, 0, time.UTC)

const (
	testIssuer   = "https://plux.example"
	testAudience = "plux-api"
)

// fakeSigner is an in-process TokenSigner with a key list per class, so
// that production, rotation and foreign keys can be tested.
type fakeSigner struct {
	keys map[signing.TokenClass][]*ecdsa.PrivateKey
	// beforeSign runs at the start of every SignToken.
	beforeSign func(*fakeSigner)
	// keyCalls counts TokenKeys.
	keyCalls int
}

func newFakeSigner(t *testing.T) *fakeSigner {
	t.Helper()
	return &fakeSigner{keys: map[signing.TokenClass][]*ecdsa.PrivateKey{
		signing.TokenProduction:  {newKey(t)},
		signing.TokenDevelopment: {newKey(t)},
	}}
}

func newKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func keyID(t *testing.T, k *ecdsa.PrivateKey) string {
	t.Helper()
	id, err := signing.TokenKeyID(&k.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func rawSign(t *testing.T, k *ecdsa.PrivateKey, input []byte) []byte {
	t.Helper()
	digest := sha256.Sum256(input)
	r, s, err := ecdsa.Sign(rand.Reader, k, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return sig
}

func (f *fakeSigner) SignToken(_ context.Context, class signing.TokenClass, input []byte) ([]byte, string, error) {
	if f.beforeSign != nil {
		f.beforeSign(f)
	}
	list := f.keys[class]
	if len(list) == 0 {
		return nil, "", errors.New("no key")
	}
	k := list[len(list)-1]
	digest := sha256.Sum256(input)
	r, s, err := ecdsa.Sign(rand.Reader, k, digest[:])
	if err != nil {
		return nil, "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	id, _ := signing.TokenKeyID(&k.PublicKey)
	return sig, id, nil
}

func (f *fakeSigner) TokenKeys(context.Context) ([]signing.TokenKey, error) {
	f.keyCalls++
	var out []signing.TokenKey
	for _, class := range []signing.TokenClass{signing.TokenProduction, signing.TokenDevelopment} {
		for _, k := range f.keys[class] {
			id, _ := signing.TokenKeyID(&k.PublicKey)
			out = append(out, signing.TokenKey{ID: id, Class: class, Public: &k.PublicKey})
		}
	}
	return out, nil
}

func issuerFor(s signing.TokenSigner) *devtoken.Issuer {
	return &devtoken.Issuer{
		Signer: s, Issuer: testIssuer, Audience: testAudience, Lifetime: 10 * time.Minute,
		Now: func() time.Time { return epoch }, Random: bytes.NewReader(bytes.Repeat([]byte{7}, 4096)),
	}
}

func verifierFor(s signing.TokenSigner) *devtoken.Verifier {
	return &devtoken.Verifier{
		Keys: s.TokenKeys, Issuer: testIssuer, Audience: testAudience,
		Now: func() time.Time { return epoch.Add(time.Minute) },
	}
}

func baseClaims(production bool) devtoken.Claims {
	return devtoken.Claims{
		DeviceID: "dev_123", AppID: "app_456", Environment: "prod-eu", OrganizationID: "org_789", HostBuild: "42", Production: production,
		Assurance: "AL2", JKT: "0ZcOCORZNYy-DWpqq30jZyJGHTN0d2HglBV3uiguA4I",
	}
}

// payload returns a valid claim set to alter in a forged token.
func payload() map[string]any {
	return map[string]any{
		"iss": testIssuer, "aud": testAudience, "sub": "dev_123", "client_id": "app_456",
		"env": "prod-eu", "org": "org_789", "hb": "42", "al": "AL2", "cnf": map[string]any{"jkt": "thumb"},
		"iat": epoch.Unix(), "exp": epoch.Add(10 * time.Minute).Unix(), "jti": "abc",
	}
}

// forge builds a compact ES256 token under k with the given header and
// payload, signed correctly, so that only the altered field can fail.
func forge(t *testing.T, k *ecdsa.PrivateKey, header, claims map[string]any) string {
	t.Helper()
	h, _ := json.Marshal(header)
	p, _ := json.Marshal(claims)
	input := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(p)
	return input + "." + base64.RawURLEncoding.EncodeToString(rawSign(t, k, []byte(input)))
}

func header(t *testing.T, k *ecdsa.PrivateKey) map[string]any {
	t.Helper()
	return map[string]any{"alg": "ES256", "typ": "at+jwt", "kid": keyID(t, k)}
}

func requireInvalid(t *testing.T, err error, check string) {
	t.Helper()
	var perr *plxerr.Error
	if !errors.As(err, &perr) || perr.Code != plxerr.AccessTokenInvalid {
		t.Fatalf("err = %v, want ACCESS_TOKEN_INVALID", err)
	}
	if !strings.Contains(perr.Message, `"`+check+`"`) {
		t.Errorf("err = %v, want it to name check %q", err, check)
	}
}

// Verifies: SEC-020.
func TestRoundTripFileBackend(t *testing.T) {
	t.Parallel()
	file, err := signing.NewFile(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	narrow := signing.TokenOnly(file)
	token, expires, err := issuerFor(narrow).Issue(context.Background(), baseClaims(false))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if want := epoch.Add(10 * time.Minute); !expires.Equal(want) {
		t.Errorf("expires = %v, want %v", expires, want)
	}
	got, err := verifierFor(narrow).Verify(context.Background(), token, false)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	want := baseClaims(false)
	want.IssuedAt, want.ExpiresAt = epoch, expires
	want.ID = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 16))
	if got != want {
		t.Errorf("claims = %+v, want %+v", got, want)
	}
	parts := strings.Split(token, ".")
	raw, _ := base64.RawURLEncoding.DecodeString(parts[0])
	var h map[string]string
	if err := json.Unmarshal(raw, &h); err != nil {
		t.Fatal(err)
	}
	keys, _ := file.TokenKeys(context.Background())
	if h["alg"] != "ES256" || h["typ"] != "at+jwt" || h["kid"] != keys[0].ID || len(h) != 3 {
		t.Errorf("header = %v", h)
	}
}

// Verifies: SEC-020.
// Verifies: SEC-056.
func TestRoundTripProductionAndClassSeparation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	signer := newFakeSigner(t)
	prod, _, err := issuerFor(signer).Issue(ctx, baseClaims(true))
	if err != nil {
		t.Fatal(err)
	}
	dev, _, err := issuerFor(signer).Issue(ctx, baseClaims(false))
	if err != nil {
		t.Fatal(err)
	}
	v := verifierFor(signer)
	if c, err := v.Verify(ctx, prod, true); err != nil || !c.Production {
		t.Fatalf("production token in a production environment: %+v, %v", c, err)
	}
	if c, err := v.Verify(ctx, dev, false); err != nil || c.Production {
		t.Fatalf("development token in a development environment: %+v, %v", c, err)
	}
	_, err = v.Verify(ctx, dev, true)
	requireInvalid(t, err, "key class")
	_, err = v.Verify(ctx, prod, false)
	requireInvalid(t, err, "key class")
}

// Verifies: SEC-020.
func TestIssueRefusesBadInput(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	signer := newFakeSigner(t)
	for name, lifetime := range map[string]time.Duration{
		"zero": 0, "negative": -time.Minute, "sub-second": time.Millisecond, "too long": 15*time.Minute + time.Second,
	} {
		i := issuerFor(signer)
		i.Lifetime = lifetime
		if _, _, err := i.Issue(ctx, baseClaims(false)); err == nil {
			t.Errorf("lifetime %s was accepted", name)
		}
	}
	i := issuerFor(signer)
	i.Lifetime = 15 * time.Minute
	if _, _, err := i.Issue(ctx, baseClaims(false)); err != nil {
		t.Errorf("a lifetime of exactly fifteen minutes: %v", err)
	}
	for name, alter := range map[string]func(*devtoken.Claims){
		"device":    func(c *devtoken.Claims) { c.DeviceID = "" },
		"app":       func(c *devtoken.Claims) { c.AppID = "" },
		"env":       func(c *devtoken.Claims) { c.Environment = "" },
		"org":       func(c *devtoken.Claims) { c.OrganizationID = "" },
		"jkt":       func(c *devtoken.Claims) { c.JKT = "" },
		"assurance": func(c *devtoken.Claims) { c.Assurance = "AL4" },
	} {
		c := baseClaims(false)
		alter(&c)
		if _, _, err := issuerFor(signer).Issue(ctx, c); err == nil {
			t.Errorf("a token without a valid %s was issued", name)
		}
	}
	if _, _, err := (&devtoken.Issuer{Lifetime: time.Minute}).Issue(ctx, baseClaims(false)); err == nil {
		t.Error("an issuer without a signer issued a token")
	}
	empty := issuerFor(signer)
	empty.Random = bytes.NewReader(nil)
	if _, _, err := empty.Issue(ctx, baseClaims(false)); err == nil {
		t.Error("a token was issued without randomness")
	}
}

// Verifies: SEC-020.
func TestIssueFollowsRotation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	signer := newFakeSigner(t)
	i := issuerFor(signer)
	if _, _, err := i.Issue(ctx, baseClaims(true)); err != nil {
		t.Fatal(err)
	}
	calls := signer.keyCalls
	if _, _, err := i.Issue(ctx, baseClaims(true)); err != nil || signer.keyCalls != calls {
		t.Errorf("the key list was read again without a rotation: %d calls (%v)", signer.keyCalls-calls, err)
	}
	// A rotation between the cached read and the signature: the token
	// must still name the key that signed it.
	signer.keys[signing.TokenProduction] = append(signer.keys[signing.TokenProduction], newKey(t))
	token, _, err := i.Issue(ctx, baseClaims(true))
	if err != nil {
		t.Fatalf("Issue across a rotation: %v", err)
	}
	if _, err := verifierFor(signer).Verify(ctx, token, true); err != nil {
		t.Errorf("a token issued across a rotation does not verify: %v", err)
	}
	// A signer that rotates on every call never settles.
	signer.beforeSign = func(f *fakeSigner) {
		f.keys[signing.TokenProduction] = append(f.keys[signing.TokenProduction], newKey(t))
	}
	if _, _, err := i.Issue(ctx, baseClaims(true)); err == nil {
		t.Error("a token was issued with a kid its signer did not use")
	}
}

// Verifies: SEC-020.
func TestVerifyRefusesTimeViolations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	signer := newFakeSigner(t)
	k := signer.keys[signing.TokenDevelopment][0]
	v := verifierFor(signer)
	now := epoch.Add(time.Minute)
	cases := []struct {
		name  string
		alter func(map[string]any)
		check string
	}{
		{"expired", func(p map[string]any) {
			p["iat"], p["exp"] = now.Add(-10*time.Minute).Unix(), now.Add(-time.Second).Unix()
		}, "exp"},
		{"future iat", func(p map[string]any) {
			p["iat"], p["exp"] = now.Add(time.Hour).Unix(), now.Add(time.Hour+time.Minute).Unix()
		}, "iat"},
		{"too long", func(p map[string]any) {
			p["iat"], p["exp"] = now.Unix(), now.Add(15*time.Minute+time.Second).Unix()
		}, "lifetime"},
		{"exp before iat", func(p map[string]any) { p["iat"], p["exp"] = now.Unix(), now.Add(-time.Hour).Unix() }, "exp"},
		{"no exp", func(p map[string]any) { delete(p, "exp") }, "exp"},
		{"no iat", func(p map[string]any) { delete(p, "iat") }, "iat"},
		{"huge exp", func(p map[string]any) { p["exp"] = 1e300 }, "exp"},
	}
	for _, tc := range cases {
		p := payload()
		tc.alter(p)
		_, err := v.Verify(ctx, forge(t, k, header(t, k), p), false)
		requireInvalid(t, err, tc.check)
	}

	// Leeway tolerates skew in both directions.
	p := payload()
	p["iat"], p["exp"] = now.Add(20*time.Second).Unix(), now.Add(10*time.Minute).Unix()
	token := forge(t, k, header(t, k), p)
	if _, err := v.Verify(ctx, token, false); err == nil {
		t.Error("a future iat passed without leeway")
	}
	v.Leeway = 30 * time.Second
	if _, err := v.Verify(ctx, token, false); err != nil {
		t.Errorf("a future iat within the leeway: %v", err)
	}
	p["iat"], p["exp"] = now.Add(-10*time.Minute).Unix(), now.Add(-10*time.Second).Unix()
	if _, err := v.Verify(ctx, forge(t, k, header(t, k), p), false); err != nil {
		t.Errorf("an expiry within the leeway: %v", err)
	}
}

// Verifies: SEC-020.
func TestVerifyRefusesBadHeadersAndAlgorithms(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	signer := newFakeSigner(t)
	k := signer.keys[signing.TokenDevelopment][0]
	v := verifierFor(signer)
	good := header(t, k)
	with := func(key string, value any) map[string]any {
		h := map[string]any{}
		for name, val := range good {
			h[name] = val
		}
		if value == nil {
			delete(h, key)
		} else {
			h[key] = value
		}
		return h
	}

	for _, tc := range []struct {
		name   string
		header map[string]any
		check  string
	}{
		{"wrong typ", with("typ", "JWT"), "typ"},
		{"application typ", with("typ", "application/at+jwt"), "typ"},
		{"no typ", with("typ", nil), "typ"},
		{"no kid", with("kid", nil), "kid"},
		{"unknown kid", with("kid", "AAAAAAAAAAAAAAAAAAAAAA"), "kid"},
	} {
		_, err := v.Verify(ctx, forge(t, k, tc.header, payload()), false)
		requireInvalid(t, err, tc.check)
	}

	p, _ := json.Marshal(payload())
	segment := base64.RawURLEncoding.EncodeToString(p)
	hs256, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.HS256, Key: bytes.Repeat([]byte{1}, 32)},
		(&jose.SignerOptions{}).WithType("at+jwt").WithHeader("kid", keyID(t, k)))
	if err != nil {
		t.Fatal(err)
	}
	signed, err := hs256.Sign(p)
	if err != nil {
		t.Fatal(err)
	}
	hsToken, _ := signed.CompactSerialize()
	k384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	es384, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES384, Key: k384},
		(&jose.SignerOptions{}).WithType("at+jwt").WithHeader("kid", keyID(t, k)))
	if err != nil {
		t.Fatal(err)
	}
	signed, err = es384.Sign(p)
	if err != nil {
		t.Fatal(err)
	}
	esToken, _ := signed.CompactSerialize()
	noneHeader := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"at+jwt","kid":"` + keyID(t, k) + `"}`))
	for name, token := range map[string]string{
		"HS256":        hsToken,
		"ES384":        esToken,
		"none":         noneHeader + "." + segment + ".",
		"none, no dot": noneHeader + "." + segment,
		"empty":        "",
		"garbage":      "not a token",
		"two parts":    segment + "." + segment,
	} {
		if _, err := v.Verify(ctx, token, false); err == nil {
			t.Errorf("%s was accepted", name)
		} else {
			requireInvalid(t, err, "format")
		}
	}
}

// Verifies: SEC-020.
func TestVerifyRefusesTamperingAndForeignKeys(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	signer := newFakeSigner(t)
	v := verifierFor(signer)
	token, _, err := issuerFor(signer).Issue(ctx, baseClaims(false))
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")

	// The payload of another token under this token's signature.
	p := payload()
	p["sub"] = "someone-else"
	other, _ := json.Marshal(p)
	swapped := parts[0] + "." + base64.RawURLEncoding.EncodeToString(other) + "." + parts[2]
	_, err = v.Verify(ctx, swapped, false)
	requireInvalid(t, err, "signature")

	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	sig[10] ^= 0x01
	flipped := parts[0] + "." + parts[1] + "." + base64.RawURLEncoding.EncodeToString(sig)
	_, err = v.Verify(ctx, flipped, false)
	requireInvalid(t, err, "signature")

	// A correctly signed token under a key the verifier does not know,
	// naming a known key's identifier.
	stranger := newKey(t)
	h := header(t, signer.keys[signing.TokenDevelopment][0])
	_, err = v.Verify(ctx, forge(t, stranger, h, payload()), false)
	requireInvalid(t, err, "signature")
}

// Verifies: SEC-020.
func TestVerifyChecksIssuerAudienceAndRequiredClaims(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	signer := newFakeSigner(t)
	k := signer.keys[signing.TokenDevelopment][0]
	v := verifierFor(signer)
	for _, tc := range []struct {
		name  string
		alter func(map[string]any)
		check string
	}{
		{"wrong iss", func(p map[string]any) { p["iss"] = "https://other.example" }, "iss"},
		{"no iss", func(p map[string]any) { delete(p, "iss") }, "iss"},
		{"iss prefix", func(p map[string]any) { p["iss"] = testIssuer + "/" }, "iss"},
		{"wrong aud", func(p map[string]any) { p["aud"] = "other" }, "aud"},
		{"aud array", func(p map[string]any) { p["aud"] = []string{testAudience} }, "claims"},
		{"no sub", func(p map[string]any) { delete(p, "sub") }, "sub"},
		{"no client_id", func(p map[string]any) { delete(p, "client_id") }, "client_id"},
		{"no env", func(p map[string]any) { delete(p, "env") }, "env"},
		{"no org", func(p map[string]any) { delete(p, "org") }, "org"},
		{"no al", func(p map[string]any) { delete(p, "al") }, "al"},
		{"bad al", func(p map[string]any) { p["al"] = "AL9" }, "al"},
		{"no cnf", func(p map[string]any) { delete(p, "cnf") }, "cnf"},
		{"empty jkt", func(p map[string]any) { p["cnf"] = map[string]any{"jkt": ""} }, "cnf"},
		{"cnf without jkt", func(p map[string]any) { p["cnf"] = map[string]any{"x5t#S256": "x"} }, "cnf"},
		{"no jti", func(p map[string]any) { delete(p, "jti") }, "jti"},
	} {
		p := payload()
		tc.alter(p)
		_, err := v.Verify(ctx, forge(t, k, header(t, k), p), false)
		requireInvalid(t, err, tc.check)
	}
	if _, err := v.Verify(ctx, forge(t, k, header(t, k), payload()), false); err != nil {
		t.Errorf("the unaltered forged token: %v", err)
	}
}

// Verifies: SEC-020.
func TestVerifySizeKeysAndConfiguration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	signer := newFakeSigner(t)
	v := verifierFor(signer)
	k := signer.keys[signing.TokenDevelopment][0]

	p := payload()
	p["pad"] = strings.Repeat("x", devtoken.MaxTokenSize)
	_, err := v.Verify(ctx, forge(t, k, header(t, k), p), false)
	requireInvalid(t, err, "size")

	outage := errors.New("vault is down")
	broken := *v
	broken.Keys = func(context.Context) ([]signing.TokenKey, error) { return nil, outage }
	token, _, err := issuerFor(signer).Issue(ctx, baseClaims(false))
	if err != nil {
		t.Fatal(err)
	}
	_, err = broken.Verify(ctx, token, false)
	var perr *plxerr.Error
	if !errors.Is(err, outage) || errors.As(err, &perr) {
		t.Errorf("a key outage: err = %v, want a plain error wrapping the cause", err)
	}
	for _, bad := range []devtoken.Verifier{{Issuer: testIssuer, Audience: testAudience}, {Keys: signer.TokenKeys}, {Keys: signer.TokenKeys, Issuer: testIssuer}} {
		if _, err := bad.Verify(ctx, token, false); err == nil {
			t.Errorf("a verifier configured as %+v accepted a token", bad.Issuer)
		}
	}
	// A key of an unknown class never verifies.
	odd := *v
	odd.Keys = func(ctx context.Context) ([]signing.TokenKey, error) {
		keys, _ := signer.TokenKeys(ctx)
		for i := range keys {
			keys[i].Class = "staging"
		}
		return keys, nil
	}
	for _, production := range []bool{true, false} {
		if _, err := odd.Verify(ctx, token, production); err == nil {
			t.Errorf("a key of an unknown class verified (production=%v)", production)
		}
	}
}

// Verifies: SEC-020.
// VerifyFor takes the key class from the environment the token names and
// still refuses a token signed with the other class's key.
func TestVerifyForSelectsTheClassByEnvironment(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	signer := newFakeSigner(t)
	prod, _, err := issuerFor(signer).Issue(ctx, baseClaims(true))
	if err != nil {
		t.Fatal(err)
	}
	dev, _, err := issuerFor(signer).Issue(ctx, baseClaims(false))
	if err != nil {
		t.Fatal(err)
	}
	v := verifierFor(signer)
	classOf := func(production bool) func(context.Context, string) (bool, error) {
		return func(_ context.Context, env string) (bool, error) {
			if env != "prod-eu" {
				t.Errorf("the environment looked up = %q", env)
			}
			return production, nil
		}
	}
	if c, err := v.VerifyFor(ctx, prod, classOf(true)); err != nil || !c.Production || c.OrganizationID != "org_789" || c.HostBuild != "42" {
		t.Errorf("production token: %+v, %v", c, err)
	}
	if c, err := v.VerifyFor(ctx, dev, classOf(false)); err != nil || c.Production {
		t.Errorf("development token: %+v, %v", c, err)
	}
	_, err = v.VerifyFor(ctx, dev, classOf(true))
	requireInvalid(t, err, "key class")
	lookup := errors.New("the lookup failed")
	if _, err := v.VerifyFor(ctx, prod, func(context.Context, string) (bool, error) { return false, lookup }); !errors.Is(err, lookup) {
		t.Errorf("a failed lookup: %v", err)
	}
	for bad, check := range map[string]string{"": "format", "a.b.c": "format", strings.Repeat("x", devtoken.MaxTokenSize+1): "size"} {
		_, err := v.VerifyFor(ctx, bad, classOf(false))
		requireInvalid(t, err, check)
	}
	k := signer.keys[signing.TokenDevelopment][0]
	p := payload()
	delete(p, "env")
	_, err = v.VerifyFor(ctx, forge(t, k, header(t, k), p), classOf(false))
	requireInvalid(t, err, "env")
}

// Verifies: SEC-020.
// A claim set may set its own lifetime, within the maximum.
func TestIssueHonoursAClaimLifetime(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	signer := newFakeSigner(t)
	c := baseClaims(false)
	c.Lifetime = 5 * time.Minute
	_, expires, err := issuerFor(signer).Issue(ctx, c)
	if err != nil || !expires.Equal(epoch.Add(5*time.Minute)) {
		t.Errorf("expires = %v, %v", expires, err)
	}
	c.Lifetime = devtoken.MaxLifetime + time.Second
	if _, _, err := issuerFor(signer).Issue(ctx, c); err == nil {
		t.Error("a lifetime past the maximum was accepted")
	}
}

// FuzzVerify checks that no input makes the verifier panic.
//
// Verifies: SEC-020.
func FuzzVerify(f *testing.F) {
	signer := &fakeSigner{keys: map[signing.TokenClass][]*ecdsa.PrivateKey{}}
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		f.Fatal(err)
	}
	signer.keys[signing.TokenDevelopment] = []*ecdsa.PrivateKey{k}
	token, _, err := issuerFor(signer).Issue(context.Background(), baseClaims(false))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(token)
	f.Add("")
	f.Add("a.b.c")
	f.Add(strings.Repeat(".", 100))
	v := verifierFor(signer)
	f.Fuzz(func(_ *testing.T, in string) {
		_, _ = v.Verify(context.Background(), in, false)
		_, _ = v.Verify(context.Background(), in, true)
	})
}
