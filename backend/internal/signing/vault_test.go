// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package signing_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/signing"
)

// fakeTransit is the part of Vault's Transit engine the backend uses,
// with the same request and response shapes, so the backend is tested
// against the protocol rather than against itself.
type fakeTransit struct {
	t     *testing.T
	token string

	mu      sync.Mutex
	signing map[string][]ed25519.PrivateKey // versions, oldest first
	aes     map[string]bool
	kinds   map[string]string
	// forge, when set, makes sign return a signature by another key.
	forge bool
	// exportable marks keys as exportable in their description.
	exportable bool
}

func newFakeTransit(t *testing.T) (*fakeTransit, *httptest.Server) {
	t.Helper()
	f := &fakeTransit{
		t: t, token: "s.test-token",
		signing: map[string][]ed25519.PrivateKey{}, aes: map[string]bool{}, kinds: map[string]string{},
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeTransit) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	if r.Body != nil && r.Method == http.MethodPost {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case op == "keys" && r.Method == http.MethodGet:
		kind, ok := f.kinds[name]
		if !ok {
			http.Error(w, `{"errors":[]}`, http.StatusNotFound)
			return
		}
		keys := map[string]any{}
		latest := 1
		for i, k := range f.signing[name] {
			pub, _ := k.Public().(ed25519.PublicKey)
			keys[fmt.Sprint(i+1)] = map[string]any{"public_key": base64.StdEncoding.EncodeToString(pub)}
			latest = i + 1
		}
		if kind != "ed25519" {
			keys["1"] = 1700000000
		}
		reply(w, map[string]any{"data": map[string]any{
			"type": kind, "exportable": f.exportable, "latest_version": latest, "keys": keys,
		}})
	case op == "keys" && r.Method == http.MethodPost:
		kind, _ := body["type"].(string)
		if _, ok := f.kinds[name]; !ok {
			f.kinds[name] = kind
			if kind == "ed25519" {
				_, k, _ := ed25519.GenerateKey(rand.Reader)
				f.signing[name] = []ed25519.PrivateKey{k}
			} else {
				f.aes[name] = true
			}
		}
		w.WriteHeader(http.StatusNoContent)
	case op == "sign":
		versions := f.signing[name]
		if len(versions) == 0 {
			http.Error(w, `{"errors":[]}`, http.StatusBadRequest)
			return
		}
		input, _ := base64.StdEncoding.DecodeString(body["input"].(string))
		key := versions[len(versions)-1]
		if f.forge {
			_, key, _ = ed25519.GenerateKey(rand.Reader)
		}
		sig := ed25519.Sign(key, input)
		reply(w, map[string]any{"data": map[string]any{
			"signature": fmt.Sprintf("vault:v%d:%s", len(versions), base64.StdEncoding.EncodeToString(sig)),
		}})
	case op == "encrypt":
		// Reversing the bytes is enough to show that the value was
		// transformed and restored by the engine, not by the backend.
		plain, _ := base64.StdEncoding.DecodeString(body["plaintext"].(string))
		reply(w, map[string]any{"data": map[string]any{
			"ciphertext": "vault:v1:" + base64.StdEncoding.EncodeToString(reverse(plain)),
		}})
	case op == "decrypt":
		c, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(body["ciphertext"].(string), "vault:v1:"))
		reply(w, map[string]any{"data": map[string]any{"plaintext": base64.StdEncoding.EncodeToString(reverse(c))}})
	default:
		http.NotFound(w, r)
	}
}

// rotate adds a version to a signing key, as `vault write -f
// transit/keys/<name>/rotate` does.
func (f *fakeTransit) rotate(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, k, _ := ed25519.GenerateKey(rand.Reader)
	f.signing[name] = append(f.signing[name], k)
}

func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func reverse(b []byte) []byte {
	out := make([]byte, len(b))
	for i, c := range b {
		out[len(b)-1-i] = c
	}
	return out
}

// Verifies: SEC-120.
func TestVaultSignsWithKeysThatStayInVault(t *testing.T) {
	t.Parallel()
	fake, srv := newFakeTransit(t)
	v, err := signing.NewVault(signing.VaultOptions{Address: srv.URL, Token: fake.token})
	if err != nil {
		t.Fatal(err)
	}
	if v.Name() != "vault" || !v.AllowedInProduction() {
		t.Error("the Vault backend must be usable in production")
	}
	ctx := context.Background()
	msg := []byte("bundle hash")
	sig, keyID, err := v.Sign(ctx, "targets-env", msg)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	pub, pubID, err := v.PublicKey(ctx, "targets-env")
	if err != nil {
		t.Fatalf("PublicKey: %v", err)
	}
	if keyID != pubID || keyID != signing.KeyID(pub) {
		t.Errorf("key IDs disagree: %s, %s", keyID, pubID)
	}
	if !ed25519.Verify(pub, msg, sig) {
		t.Error("the signature does not verify under the published key")
	}
	// After a rotation, signatures name the new key.
	fake.rotate("targets-env")
	sig2, keyID2, err := v.Sign(ctx, "targets-env", msg)
	if err != nil {
		t.Fatalf("Sign after rotation: %v", err)
	}
	if keyID2 == keyID {
		t.Error("a rotated key kept its identifier")
	}
	pub2, _, err := v.PublicKey(ctx, "targets-env")
	if err != nil || !ed25519.Verify(pub2, msg, sig2) {
		t.Errorf("the rotated signature does not verify: %v", err)
	}
}

// Verifies: SEC-120.
func TestVaultRefusesWhatItCannotTrust(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	fake, srv := newFakeTransit(t)
	v, err := signing.NewVault(signing.VaultOptions{Address: srv.URL, Token: fake.token})
	if err != nil {
		t.Fatal(err)
	}
	fake.forge = true
	if _, _, err := v.Sign(ctx, "forged", []byte("x")); err == nil {
		t.Error("a signature that does not verify was returned")
	}

	fake2, srv2 := newFakeTransit(t)
	fake2.exportable = true
	v2, err := signing.NewVault(signing.VaultOptions{Address: srv2.URL, Token: fake2.token})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := v2.Sign(ctx, "exportable", []byte("x")); err == nil {
		t.Error("an exportable key was used to sign")
	}

	wrongToken, err := signing.NewVault(signing.VaultOptions{Address: srv.URL, Token: "s.wrong"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = wrongToken.Sign(ctx, "targets", []byte("x"))
	if err == nil || strings.Contains(err.Error(), "s.wrong") {
		t.Errorf("a refused token: err = %v", err)
	}
	if _, _, err := v.Sign(ctx, "../escape", nil); err == nil {
		t.Error("a key reference outside the pattern was used")
	}
	for _, opts := range []signing.VaultOptions{
		{Address: "", Token: "t"},
		{Address: "ftp://vault", Token: "t"},
		{Address: srv.URL},
		{Address: srv.URL, Token: "t", WrapKey: "Not Valid"},
	} {
		if _, err := signing.NewVault(opts); err == nil {
			t.Errorf("NewVault(%+v) was accepted", opts)
		}
	}
}

// Verifies: SEC-106.
// Verifies: SRV-007.
// Ping answers for readiness: Vault reachable with a valid token is
// healthy, before or after the wrapping key exists; a refused token or an
// unreachable Vault is not.
func TestVaultPing(t *testing.T) {
	t.Parallel()
	fake, srv := newFakeTransit(t)
	ctx := context.Background()
	v, err := signing.NewVault(signing.VaultOptions{Address: srv.URL, Token: fake.token})
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Ping(ctx); err != nil {
		t.Errorf("Ping before the wrapping key exists: %v", err)
	}
	if _, err := signing.Seal(ctx, v, []byte("x"), bindA); err != nil {
		t.Fatal(err)
	}
	if err := v.Ping(ctx); err != nil {
		t.Errorf("Ping: %v", err)
	}
	wrong, err := signing.NewVault(signing.VaultOptions{Address: srv.URL, Token: "s.wrong"})
	if err != nil {
		t.Fatal(err)
	}
	if err := wrong.Ping(ctx); err == nil {
		t.Error("Ping with a refused token reported healthy")
	}
	down, err := signing.NewVault(signing.VaultOptions{Address: "http://127.0.0.1:1", Token: fake.token})
	if err != nil {
		t.Fatal(err)
	}
	if err := down.Ping(ctx); err == nil {
		t.Error("Ping of an unreachable Vault reported healthy")
	}
}

func TestVaultWrapsDataKeys(t *testing.T) {
	t.Parallel()
	fake, srv := newFakeTransit(t)
	v, err := signing.NewVault(signing.VaultOptions{Address: srv.URL + "/", Token: fake.token})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	value := []byte("demo-value-abcdef0123456789")
	sealed, err := signing.Seal(ctx, v, value, bindA)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Contains(sealed.WrappedKey, value) || sealed.KeyID != "vault:plux-secrets" {
		t.Errorf("sealed = %+v", sealed)
	}
	got, err := signing.Open(ctx, v, sealed, bindA)
	if err != nil || !bytes.Equal(got, value) {
		t.Fatalf("Open = %q, %v", got, err)
	}
	sealed.KeyID = "file:0000"
	if _, err := signing.Open(ctx, v, sealed, bindA); err == nil {
		t.Error("a secret wrapped by another backend was unwrapped")
	}
}
