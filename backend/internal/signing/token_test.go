// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package signing_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/signing"
)

// verifyES256 checks a JOSE ES256 signature over input.
func verifyES256(pub *ecdsa.PublicKey, input, sig []byte) bool {
	if len(sig) != signing.TokenSignatureSize {
		return false
	}
	digest := sha256.Sum256(input)
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	return ecdsa.Verify(pub, digest[:], r, s)
}

// Verifies: SEC-020.
func TestFileSignsTokens(t *testing.T) {
	t.Parallel()
	b := backend(t)
	ctx := context.Background()
	input := []byte("eyJhbGciOiJFUzI1NiJ9.eyJzdWIiOiJkZXYifQ")
	sig, keyID, err := b.SignToken(ctx, signing.TokenDevelopment, input)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}
	keys, err := b.TokenKeys(ctx)
	if err != nil || len(keys) != 1 {
		t.Fatalf("TokenKeys = %v, %v", keys, err)
	}
	k := keys[0]
	if k.ID != keyID || k.Class != signing.TokenDevelopment {
		t.Errorf("key = %+v, signed with %s", k, keyID)
	}
	if !verifyES256(k.Public, input, sig) {
		t.Error("the signature does not verify under the published key")
	}
	if verifyES256(k.Public, append(input, 'x'), sig) {
		t.Error("the signature verifies another input")
	}
	want, err := signing.TokenKeyID(k.Public)
	if err != nil || want != keyID || len(keyID) != 22 || strings.ContainsAny(keyID, "=+/") {
		t.Errorf("key ID %q, want %q (%v)", keyID, want, err)
	}
}

// Verifies: SEC-056.
func TestFileRefusesProductionTokens(t *testing.T) {
	t.Parallel()
	b := backend(t)
	_, _, err := b.SignToken(context.Background(), signing.TokenProduction, []byte("x"))
	if !errors.Is(err, signing.ErrProductionToken) {
		t.Errorf("production token: err = %v", err)
	}
	if _, _, err := b.SignToken(context.Background(), "staging", []byte("x")); err == nil {
		t.Error("an unknown class was accepted")
	}
	keys, err := b.TokenKeys(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if k.Class == signing.TokenProduction {
			t.Error("the file backend listed a production key")
		}
	}
}

// Verifies: SEC-020.
func TestFileTokenKeysSurviveReopen(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ctx := context.Background()
	first, err := signing.NewFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, id1, err := first.SignToken(ctx, signing.TokenDevelopment, []byte("a"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := signing.NewFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	sig, id2, err := second.SignToken(ctx, signing.TokenDevelopment, []byte("a"))
	if err != nil || id1 != id2 {
		t.Fatalf("reopened key %s, was %s (%v)", id2, id1, err)
	}
	keys, _ := first.TokenKeys(ctx)
	if !verifyES256(keys[0].Public, []byte("a"), sig) {
		t.Error("a reopened backend signs with another key")
	}
	info, err := os.Stat(filepath.Join(dir, "token-development.p256"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("key file: %v, %v", info, err)
	}
	// A damaged key file is an error that names no key material.
	if err := os.WriteFile(filepath.Join(dir, "token-development.p256"), []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	broken, err := signing.NewFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := broken.SignToken(ctx, signing.TokenDevelopment, nil); err == nil || strings.Contains(err.Error(), "short") {
		t.Errorf("damaged key: err = %v", err)
	}
}

// Verifies: SEC-020.
func TestTokenOnlyHidesTheBackend(t *testing.T) {
	t.Parallel()
	b := backend(t)
	narrow := signing.TokenOnly(b)
	if _, ok := narrow.(signing.Backend); ok {
		t.Error("TokenOnly exposes the backend")
	}
	if _, ok := narrow.(signing.Signer); ok {
		t.Error("TokenOnly exposes release signing")
	}
	if _, ok := narrow.(signing.Crypter); ok {
		t.Error("TokenOnly exposes envelope encryption")
	}
	if _, ok := narrow.(*signing.File); ok {
		t.Error("TokenOnly exposes the file backend")
	}
	sig, id, err := narrow.SignToken(context.Background(), signing.TokenDevelopment, []byte("x"))
	if err != nil || len(sig) != signing.TokenSignatureSize || id == "" {
		t.Errorf("SignToken through TokenOnly: %v", err)
	}
	if keys, err := narrow.TokenKeys(context.Background()); err != nil || len(keys) != 1 {
		t.Errorf("TokenKeys through TokenOnly: %v", err)
	}
}

// fakeTokenTransit is the part of Transit's ecdsa-p256 signing the token
// methods use, with Vault's request and response shapes.
type fakeTokenTransit struct {
	token string

	mu       sync.Mutex
	versions map[string][]*ecdsa.PrivateKey
	// signature, when set, replaces what sign answers with.
	signature string
	// status, when set, is answered to every sign request.
	status int
	// forge makes sign use another key than the described one.
	forge bool
	// kindOverride makes the description claim another key type.
	kindOverride string
	// requests records the last sign body.
	lastSign map[string]any
}

func newFakeTokenTransit(t *testing.T) (*fakeTokenTransit, *httptest.Server) {
	t.Helper()
	f := &fakeTokenTransit{token: "s.test-token", versions: map[string][]*ecdsa.PrivateKey{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeTokenTransit) rotate(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	f.versions[name] = append(f.versions[name], k)
}

func (f *fakeTokenTransit) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Vault-Token") != f.token {
		http.Error(w, `{"errors":["permission denied"]}`, http.StatusForbidden)
		return
	}
	path, ok := strings.CutPrefix(r.URL.Path, "/v1/transit/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	op, name, _ := strings.Cut(path, "/")
	var body map[string]any
	if r.Method == http.MethodPost {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case op == "keys" && r.Method == http.MethodGet:
		f.describe(w, name)
	case op == "keys" && r.Method == http.MethodPost:
		if len(f.versions[name]) == 0 {
			k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			f.versions[name] = []*ecdsa.PrivateKey{k}
		}
		w.WriteHeader(http.StatusNoContent)
	case op == "sign":
		f.sign(w, name, body)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeTokenTransit) describe(w http.ResponseWriter, name string) {
	versions := f.versions[name]
	if len(versions) == 0 {
		http.Error(w, `{"errors":[]}`, http.StatusNotFound)
		return
	}
	keys := map[string]any{}
	for i, k := range versions {
		der, _ := x509.MarshalPKIXPublicKey(&k.PublicKey)
		keys[fmt.Sprint(i+1)] = map[string]any{
			"public_key": string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})),
		}
	}
	kind := "ecdsa-p256"
	if f.kindOverride != "" {
		kind = f.kindOverride
	}
	reply(w, map[string]any{"data": map[string]any{
		"type": kind, "exportable": false, "latest_version": len(versions), "keys": keys,
	}})
}

func (f *fakeTokenTransit) sign(w http.ResponseWriter, name string, body map[string]any) {
	f.lastSign = body
	if f.status != 0 {
		http.Error(w, `{"errors":["boom"]}`, f.status)
		return
	}
	if f.signature != "" {
		reply(w, map[string]any{"data": map[string]any{"signature": f.signature}})
		return
	}
	versions := f.versions[name]
	input, _ := base64.StdEncoding.DecodeString(body["input"].(string))
	key := versions[len(versions)-1]
	if f.forge {
		key, _ = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	}
	digest := sha256.Sum256(input)
	r, s, _ := ecdsa.Sign(rand.Reader, key, digest[:])
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	reply(w, map[string]any{"data": map[string]any{
		"signature": fmt.Sprintf("vault:v%d:%s", len(versions), base64.RawURLEncoding.EncodeToString(sig)),
	}})
}

func newTokenVault(t *testing.T, srv *httptest.Server, f *fakeTokenTransit) *signing.Vault {
	t.Helper()
	v, err := signing.NewVault(signing.VaultOptions{Address: srv.URL, Token: f.token})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// Verifies: SEC-020.
// Verifies: SEC-056.
func TestVaultSignsTokensPerClass(t *testing.T) {
	t.Parallel()
	f, srv := newFakeTokenTransit(t)
	v := newTokenVault(t, srv, f)
	ctx := context.Background()
	input := []byte("header.payload")
	keys, err := v.TokenKeys(ctx)
	if err != nil || len(keys) != 2 {
		t.Fatalf("TokenKeys = %d keys, %v", len(keys), err)
	}
	for _, class := range []signing.TokenClass{signing.TokenProduction, signing.TokenDevelopment} {
		sig, id, err := v.SignToken(ctx, class, input)
		if err != nil {
			t.Fatalf("SignToken(%s): %v", class, err)
		}
		var matched bool
		for _, k := range keys {
			if k.ID == id {
				matched = k.Class == class && verifyES256(k.Public, input, sig)
			}
		}
		if !matched {
			t.Errorf("%s: key %s is not that class's, or the signature does not verify", class, id)
		}
	}
	if f.lastSign["hash_algorithm"] != "sha2-256" || f.lastSign["marshaling_algorithm"] != "jws" {
		t.Errorf("sign request = %v", f.lastSign)
	}
	if keys[0].ID == keys[1].ID {
		t.Error("the two classes share a key")
	}
	if _, ok := f.versions["plux-tokens-production"]; !ok {
		t.Error("the production key was not created under its name")
	}
}

// Verifies: SEC-020.
func TestVaultTokenRotationListsEveryVersion(t *testing.T) {
	t.Parallel()
	f, srv := newFakeTokenTransit(t)
	v := newTokenVault(t, srv, f)
	ctx := context.Background()
	_, before, err := v.SignToken(ctx, signing.TokenProduction, []byte("a"))
	if err != nil {
		t.Fatal(err)
	}
	f.rotate("plux-tokens-production")
	sig, after, err := v.SignToken(ctx, signing.TokenProduction, []byte("a"))
	if err != nil || after == before {
		t.Fatalf("after rotation: key %s, was %s (%v)", after, before, err)
	}
	keys, err := v.TokenKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var production []signing.TokenKey
	for _, k := range keys {
		if k.Class == signing.TokenProduction {
			production = append(production, k)
		}
	}
	if len(production) != 2 || production[0].ID != before || production[1].ID != after {
		t.Fatalf("production keys = %+v, want [%s %s]", production, before, after)
	}
	if !verifyES256(production[1].Public, []byte("a"), sig) {
		t.Error("the rotated signature does not verify")
	}
}

// Verifies: SEC-020.
func TestVaultTokenRefusesWhatItCannotTrust(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	good := base64.RawURLEncoding.EncodeToString(make([]byte, 64))
	cases := []struct {
		name  string
		setup func(*fakeTokenTransit)
	}{
		{"forged signature", func(f *fakeTokenTransit) { f.forge = true }},
		{"error status", func(f *fakeTokenTransit) { f.status = http.StatusInternalServerError }},
		{"short signature", func(f *fakeTokenTransit) {
			f.signature = "vault:v1:" + base64.RawURLEncoding.EncodeToString(make([]byte, 63))
		}},
		{"not base64url", func(f *fakeTokenTransit) { f.signature = "vault:v1:***" }},
		{"no vault prefix", func(f *fakeTokenTransit) { f.signature = "v1:" + good }},
		{"no version", func(f *fakeTokenTransit) { f.signature = "vault:vx:" + good }},
		{"unknown version", func(f *fakeTokenTransit) { f.signature = "vault:v9:" + good }},
		{"zero signature", func(f *fakeTokenTransit) { f.signature = "vault:v1:" + good }},
		{"wrong key type", func(f *fakeTokenTransit) { f.kindOverride = "ed25519" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, srv := newFakeTokenTransit(t)
			tc.setup(f)
			v := newTokenVault(t, srv, f)
			_, _, err := v.SignToken(ctx, signing.TokenProduction, []byte("x"))
			if err == nil {
				t.Fatal("SignToken succeeded")
			}
			if strings.Contains(err.Error(), f.token) {
				t.Errorf("the error names the Vault token: %v", err)
			}
		})
	}

	f, srv := newFakeTokenTransit(t)
	wrong, err := signing.NewVault(signing.VaultOptions{Address: srv.URL, Token: "s.wrong"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrong.TokenKeys(ctx); err == nil || strings.Contains(err.Error(), "s.wrong") {
		t.Errorf("a refused token: err = %v", err)
	}
	v := newTokenVault(t, srv, f)
	if _, _, err := v.SignToken(ctx, "staging", nil); err == nil {
		t.Error("an unknown class was accepted")
	}
	if _, err := signing.NewVault(signing.VaultOptions{Address: srv.URL, Token: "t", TokenKey: "Not Valid"}); err == nil {
		t.Error("an invalid token key prefix was accepted")
	}
}

// Verifies: SEC-020.
func TestVaultTokenOnlyHidesTheBackend(t *testing.T) {
	t.Parallel()
	f, srv := newFakeTokenTransit(t)
	narrow := signing.TokenOnly(newTokenVault(t, srv, f))
	if _, ok := narrow.(signing.Backend); ok {
		t.Error("TokenOnly exposes the Vault backend")
	}
	if _, ok := narrow.(*signing.Vault); ok {
		t.Error("TokenOnly exposes *Vault")
	}
}
