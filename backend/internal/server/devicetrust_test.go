// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"testing"
	"time"

	"github.com/nightCode42/plux3/backend/internal/config"
	"github.com/nightCode42/plux3/backend/internal/signing"
)

const trustedApp = "0190a1b2-0000-7000-8000-000000000001"

// Verifies: SEC-003.
// The attestation trust of an app comes from the configuration; an app
// the configuration does not list has none, and a key that cannot be
// decoded stops the server from starting.
func TestAppTrustFromConfiguration(t *testing.T) {
	t.Parallel()
	pub, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&pub.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(t, base)
	cfg.Attestation.Apps = map[string]config.AppAttestation{trustedApp: {
		AndroidPackages: []string{"com.example.app"}, AndroidCertDigests: []string{"abcd0123456789abcdef0123456789abcdef0123456789abcdef0123456789ab"},
		PlayIntegrityDecryptionKey:   config.Secret(base64.StdEncoding.EncodeToString(make([]byte, 32))),
		PlayIntegrityVerificationKey: config.Secret(base64.StdEncoding.EncodeToString(der)),
		IOSAppID:                     "TEAMID.com.example.app", AppAttestProduction: true,
	}}
	trust, err := appTrust(cfg)
	if err != nil {
		t.Fatal(err)
	}
	got, err := trust(context.Background(), trustedApp)
	if err != nil || len(got.AndroidPackages) != 1 || len(got.AndroidCertDigests) != 1 || len(got.AndroidCertDigests[0]) != 32 ||
		got.PlayIntegrity == nil || got.IOSAppID != "TEAMID.com.example.app" || !got.AppAttestProduction {
		t.Errorf("the trust of a configured app = %+v, %v", got, err)
	}
	none, err := trust(context.Background(), "0190a1b2-0000-7000-8000-000000000002")
	if err != nil || len(none.AndroidPackages) != 0 || none.PlayIntegrity != nil || none.IOSAppID != "" {
		t.Errorf("the trust of an unlisted app = %+v, %v", none, err)
	}

	cfg.Attestation.Apps = map[string]config.AppAttestation{trustedApp: {
		AndroidPackages: []string{"com.example.app"}, PlayIntegrityDecryptionKey: "not base64!", PlayIntegrityVerificationKey: "x",
	}}
	if _, err := appTrust(cfg); err == nil {
		t.Error("an undecodable Play Integrity key was accepted")
	}
	cfg.Attestation.Apps = map[string]config.AppAttestation{trustedApp: {AndroidCertDigests: []string{"zz"}}}
	if _, err := appTrust(cfg); err == nil {
		t.Error("a certificate digest that is not hexadecimal was accepted")
	}
}

// countingSigner counts how often the public keys are read.
type countingSigner struct {
	signing.TokenSigner
	reads int
}

func (c *countingSigner) TokenKeys(context.Context) ([]signing.TokenKey, error) {
	c.reads++
	return []signing.TokenKey{{ID: "k"}}, nil
}

// Verifies: NFR-025.
// The public token keys are read once in a while, not on every request.
func TestTokenKeysAreCached(t *testing.T) {
	t.Parallel()
	now := time.Now()
	signer := &countingSigner{}
	c := &tokenKeys{signer: signer, now: func() time.Time { return now }}
	for range 5 {
		if keys, err := c.Keys(context.Background()); err != nil || len(keys) != 1 {
			t.Fatalf("Keys: %v, %v", keys, err)
		}
	}
	if signer.reads != 1 {
		t.Errorf("the keys were read %d times", signer.reads)
	}
	now = now.Add(tokenKeysTTL + time.Second)
	if _, err := c.Keys(context.Background()); err != nil || signer.reads != 2 {
		t.Errorf("after the lifetime: %d reads, %v", signer.reads, err)
	}
}

// crypterOnly is a backend that cannot sign tokens.
type crypterOnly struct{ signing.Crypter }

// Verifies: SEC-020.
// Only the api role holds a token signer, narrowed to signing tokens; a
// backend that cannot sign them stops that role from starting.
func TestTokenSignerIsForTheAPIRoleOnly(t *testing.T) {
	t.Parallel()
	backend, err := signing.NewFile(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(t, base)
	signer, err := tokenSignerFor(cfg, backend)
	if err != nil || signer == nil {
		t.Fatalf("the api role: %v, %v", signer, err)
	}
	if _, ok := signer.(signing.Backend); ok {
		t.Error("the api role's signer reaches the whole backend")
	}
	if _, ok := signer.(signing.Signer); ok {
		t.Error("the api role's signer can sign releases")
	}
	cfg.Server.Roles = []config.Role{config.RoleWorker}
	if signer, err := tokenSignerFor(cfg, backend); err != nil || signer != nil {
		t.Errorf("the worker role: %v, %v", signer, err)
	}
	cfg.Server.Roles = []config.Role{config.RoleAPI}
	if _, err := tokenSignerFor(cfg, crypterOnly{backend}); err == nil {
		t.Error("a backend that cannot sign tokens was accepted")
	}
}
