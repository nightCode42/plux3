// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Package runkey makes the signing key of one `plux test` run: generated
// from the caller's entropy, held in memory, never written anywhere, and
// trusted by nothing but the test harness that embeds its public half
// (TST-002). It lives beside the signing package, the only place that
// touches private keys, and depends on the standard library alone so that
// the CLI does not link the server's storage.
package runkey

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
)

// Algorithm is the signature algorithm, as in release metadata.
const Algorithm = "ed25519"

// Key is a signing key that exists for one run.
type Key struct {
	priv ed25519.PrivateKey
}

// New generates a key from entropy; tests pass a fixed reader, the CLI
// the system's.
func New(entropy io.Reader) (*Key, error) {
	_, priv, err := ed25519.GenerateKey(entropy)
	if err != nil {
		return nil, fmt.Errorf("runkey: generate: %w", err)
	}
	return &Key{priv: priv}, nil
}

// Sign signs message.
func (k *Key) Sign(message []byte) []byte { return ed25519.Sign(k.priv, message) }

// Public is the public key.
func (k *Key) Public() ed25519.PublicKey {
	pub, _ := k.priv.Public().(ed25519.PublicKey)
	return pub
}

// ID is the identifier of the public key: the first sixteen bytes of its
// SHA-256 in hexadecimal, as signing.KeyID computes it.
func (k *Key) ID() string {
	sum := sha256.Sum256(k.Public())
	return hex.EncodeToString(sum[:16])
}
