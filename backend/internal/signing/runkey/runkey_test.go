// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package runkey_test

import (
	"bytes"
	"crypto/ed25519"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/signing"
	"github.com/nightCode42/plux3/backend/internal/signing/runkey"
)

// Verifies: TST-002.
func TestKeySignsAndAgreesWithSigningKeyID(t *testing.T) {
	t.Parallel()
	k, err := runkey.New(bytes.NewReader(bytes.Repeat([]byte{7}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("bundle hash")
	if !ed25519.Verify(k.Public(), msg, k.Sign(msg)) {
		t.Error("the signature does not verify with the public key")
	}
	if got, want := k.ID(), signing.KeyID(k.Public()); got != want {
		t.Errorf("ID = %s, signing.KeyID = %s", got, want)
	}
	if runkey.Algorithm != signing.Algorithm {
		t.Errorf("Algorithm = %s, want %s", runkey.Algorithm, signing.Algorithm)
	}
}

func TestNewReportsShortEntropy(t *testing.T) {
	t.Parallel()
	if _, err := runkey.New(bytes.NewReader([]byte{1})); err == nil {
		t.Error("a reader with too little entropy was accepted")
	}
}
