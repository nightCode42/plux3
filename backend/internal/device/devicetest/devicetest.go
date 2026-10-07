// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

// Package devicetest provides fake attestation verifiers and keys for
// tests of device registration. The fakes verify nothing about real
// evidence; they return what a test sets and record what they were
// asked, so a test can check that the service binds evidence to the
// challenge and the key. The real verifiers have their own tests.
package devicetest

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"

	"github.com/nightCode42/plux3/backend/internal/attest/appattest"
	"github.com/nightCode42/plux3/backend/internal/attest/keyattest"
	"github.com/nightCode42/plux3/backend/internal/attest/playintegrity"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/signing"
)

// NewKey returns a fresh P-256 key and its public JWK.
func NewKey(t testing.TB) (*ecdsa.PrivateKey, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key, JWK(t, &key.PublicKey)
}

// JWK encodes a public key as a JSON Web Key.
func JWK(t testing.TB, pub *ecdsa.PublicKey) []byte {
	t.Helper()
	raw, err := json.Marshal(jose.JSONWebKey{Key: pub})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// KeyAttestor is a fake Android Key Attestation verifier. Like the real
// one, it refuses a key that is not hardware-backed when the policy
// demands it.
type KeyAttestor struct {
	// Result is returned for every chain.
	Result keyattest.Result
	// Err, when set, is returned instead of Result.
	Err error
	// Challenge and Policy are those of the last call.
	Challenge []byte
	Policy    keyattest.Policy
}

// Verify records the call and returns the configured outcome.
func (f *KeyAttestor) Verify(_ [][]byte, challenge []byte, p keyattest.Policy) (keyattest.Result, error) {
	f.Challenge, f.Policy = challenge, p
	if f.Err != nil {
		return keyattest.Result{}, f.Err
	}
	if p.RequireHardware && !f.Result.HardwareBacked() {
		return keyattest.Result{}, plxerr.New(plxerr.KeyNotHardwareBacked, "key is not protected by a TEE or StrongBox")
	}
	return f.Result, nil
}

// IntegrityChecker is a fake Play Integrity verifier.
type IntegrityChecker struct {
	// Verdict is returned for every token.
	Verdict playintegrity.Verdict
	// Err, when set, is returned instead of Verdict.
	Err error
	// Expect is the expectation of the last call.
	Expect playintegrity.Expect
}

// Verify records the call and returns the configured outcome.
func (f *IntegrityChecker) Verify(_ playintegrity.Keys, _ string, e playintegrity.Expect) (playintegrity.Verdict, error) {
	f.Expect = e
	return f.Verdict, f.Err
}

// AppAttestor is a fake App Attest verifier.
type AppAttestor struct {
	// Attestation is returned for every object.
	Attestation appattest.Attestation
	// Err, when set, is returned instead of Attestation.
	Err error
	// ClientDataHash, AppID and Environment are those of the last call.
	ClientDataHash [32]byte
	AppID          string
	Environment    appattest.Environment
}

// VerifyAttestation records the call and returns the configured outcome.
func (f *AppAttestor) VerifyAttestation(_, _ []byte, clientDataHash [32]byte, appID string, env appattest.Environment) (appattest.Attestation, error) {
	f.ClientDataHash, f.AppID, f.Environment = clientDataHash, appID, env
	return f.Attestation, f.Err
}

// AppAssertor is a fake App Attest assertion verifier. Like the real
// one, it refuses a counter that does not exceed the stored one.
type AppAssertor struct {
	// Counter is the counter every assertion carries.
	Counter uint32
	// Err, when set, is returned instead of the counter.
	Err error
	// ClientDataHash, AppID and LastCounter are those of the last call.
	ClientDataHash [32]byte
	AppID          string
	LastCounter    uint32
}

// VerifyAssertion records the call and returns the configured outcome.
func (f *AppAssertor) VerifyAssertion(_ []byte, _ *ecdsa.PublicKey, clientDataHash [32]byte, appID string, lastCounter uint32) (uint32, error) {
	f.ClientDataHash, f.AppID, f.LastCounter = clientDataHash, appID, lastCounter
	if f.Err != nil {
		return 0, f.Err
	}
	if f.Counter <= lastCounter {
		return 0, plxerr.New(plxerr.AttestationFailed, "the assertion counter did not advance")
	}
	return f.Counter, nil
}

// Proof describes a DPoP proof to build. Zero fields take usable values:
// a random identifier and the present time.
type Proof struct {
	// Method and URL are the htm and htu claims.
	Method, URL string
	// AccessToken, when set, is bound through the ath claim.
	AccessToken string
	// Nonce, when set, is the nonce claim.
	Nonce string
	// ID is the jti claim.
	ID string
	// IssuedAt is the iat claim.
	IssuedAt time.Time
}

// SignProof builds a DPoP proof signed with key, with the public key in
// its header (RFC 9449 section 4.2).
func SignProof(t testing.TB, key *ecdsa.PrivateKey, p Proof) string {
	t.Helper()
	if p.ID == "" {
		raw := make([]byte, 12)
		if _, err := rand.Read(raw); err != nil {
			t.Fatal(err)
		}
		p.ID = base64.RawURLEncoding.EncodeToString(raw)
	}
	if p.IssuedAt.IsZero() {
		p.IssuedAt = time.Now()
	}
	claims := map[string]any{"htm": p.Method, "htu": p.URL, "iat": p.IssuedAt.Unix(), "jti": p.ID}
	if p.AccessToken != "" {
		sum := sha256.Sum256([]byte(p.AccessToken))
		claims["ath"] = base64.RawURLEncoding.EncodeToString(sum[:])
	}
	if p.Nonce != "" {
		claims["nonce"] = p.Nonce
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: key}, (&jose.SignerOptions{}).
		WithType("dpop+jwt").WithHeader("jwk", jose.JSONWebKey{Key: &key.PublicKey}))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	jws, err := signer.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	compact, err := jws.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return compact
}

// TokenSigner is an in-process signing.TokenSigner with a key for each
// token class, so that tests can issue tokens for production environments,
// which the file signing backend refuses (SEC-056).
type TokenSigner struct {
	keys map[signing.TokenClass]*ecdsa.PrivateKey
}

// NewTokenSigner returns a signer with a fresh key for each class.
func NewTokenSigner(t testing.TB) *TokenSigner {
	t.Helper()
	s := &TokenSigner{keys: map[signing.TokenClass]*ecdsa.PrivateKey{}}
	for _, class := range []signing.TokenClass{signing.TokenProduction, signing.TokenDevelopment} {
		key, _ := NewKey(t)
		s.keys[class] = key
	}
	return s
}

// SignToken signs a JWS signing input in the JOSE ES256 form.
func (s *TokenSigner) SignToken(_ context.Context, class signing.TokenClass, input []byte) ([]byte, string, error) {
	key, ok := s.keys[class]
	if !ok {
		return nil, "", errors.New("devicetest: no key for the token class")
	}
	id, err := signing.TokenKeyID(&key.PublicKey)
	if err != nil {
		return nil, "", fmt.Errorf("devicetest: %w", err)
	}
	digest := sha256.Sum256(input)
	r, sv, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		return nil, "", fmt.Errorf("devicetest: %w", err)
	}
	sig := make([]byte, signing.TokenSignatureSize)
	r.FillBytes(sig[:32])
	sv.FillBytes(sig[32:])
	return sig, id, nil
}

// TokenKeys lists the public keys, production first.
func (s *TokenSigner) TokenKeys(context.Context) ([]signing.TokenKey, error) {
	var out []signing.TokenKey
	for _, class := range []signing.TokenClass{signing.TokenProduction, signing.TokenDevelopment} {
		key := s.keys[class]
		id, err := signing.TokenKeyID(&key.PublicKey)
		if err != nil {
			return nil, fmt.Errorf("devicetest: %w", err)
		}
		out = append(out, signing.TokenKey{ID: id, Class: class, Public: &key.PublicKey})
	}
	return out, nil
}
