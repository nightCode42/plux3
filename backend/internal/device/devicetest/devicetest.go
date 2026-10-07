// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

// Package devicetest provides fake attestation verifiers and keys for
// tests of device registration. The fakes verify nothing about real
// evidence; they return what a test sets and record what they were
// asked, so a test can check that the service binds evidence to the
// challenge and the key. The real verifiers have their own tests.
package devicetest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"testing"

	"github.com/go-jose/go-jose/v4"

	"github.com/nightCode42/plux3/backend/internal/attest/appattest"
	"github.com/nightCode42/plux3/backend/internal/attest/keyattest"
	"github.com/nightCode42/plux3/backend/internal/attest/playintegrity"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
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
