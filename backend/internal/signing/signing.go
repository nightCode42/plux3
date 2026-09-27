// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

// Package signing is the only place that touches private keys, and it
// touches them only through a backend (L-3, SEC-120, ADR-0004).
//
// Two capabilities are offered and nothing else: signing a message with
// a named key, and wrapping and unwrapping a data key for envelope
// encryption (SEC-106). No backend can export a private key, so no
// caller can hold one.
//
// P2 ships the file backend, which is refused for production
// environments (SEC-056); the PKCS#11, AWS KMS, Google Cloud KMS, Azure
// Key Vault and HashiCorp Vault backends arrive in P6 behind the same
// interfaces.
package signing

import (
	"context"
	"crypto/ed25519"
	"errors"
)

// Algorithm names a signature algorithm in the metadata, so that an
// implementation refuses what it does not know rather than guessing
// (SEC-122).
const Algorithm = "ed25519"

// ErrNoKey is returned when a key reference names nothing.
var ErrNoKey = errors.New("signing: no such key")

// Signer signs with a named key. Signing is the only operation: nothing
// here returns a private key.
type Signer interface {
	// Sign returns the signature of message under the key and the key's
	// stable identifier, which is stored with what was signed.
	Sign(ctx context.Context, keyRef string, message []byte) (signature []byte, keyID string, err error)
	// PublicKey returns the public half and the key's identifier, for
	// publishing to devices (SEC-051).
	PublicKey(ctx context.Context, keyRef string) (key ed25519.PublicKey, keyID string, err error)
}

// Crypter wraps and unwraps the data keys of envelope encryption. A
// secret is encrypted with a fresh data key, and only that key is sent
// to the backend, so the secret itself never leaves the process
// (SEC-106).
type Crypter interface {
	// Wrap encrypts a data key and returns it with the identifier of the
	// key that wrapped it.
	Wrap(ctx context.Context, dataKey []byte) (wrapped []byte, keyID string, err error)
	// Unwrap decrypts a data key that Wrap produced.
	Unwrap(ctx context.Context, wrapped []byte, keyID string) ([]byte, error)
}

// Backend is a signing backend: both capabilities from one place.
type Backend interface {
	Signer
	Crypter
	// Name identifies the backend in logs and in the configuration.
	Name() string
	// AllowedInProduction reports whether an environment marked
	// production may use it. Only the file backend says no (SEC-056).
	AllowedInProduction() bool
}

// CrypterOnly narrows a backend to envelope encryption. The api role is
// given this and never the backend itself, so no handler can sign, even
// through a type assertion (L-3, ADR-0006); only the worker role holds a
// Signer.
func CrypterOnly(c Crypter) Crypter { return crypterOnly{c: c} }

// crypterOnly hides everything of a backend but Wrap and Unwrap.
type crypterOnly struct{ c Crypter }

// Wrap encrypts a data key.
func (o crypterOnly) Wrap(ctx context.Context, dataKey []byte) ([]byte, string, error) {
	return o.c.Wrap(ctx, dataKey) //nolint:wrapcheck // a transparent narrowing
}

// Unwrap decrypts a data key.
func (o crypterOnly) Unwrap(ctx context.Context, wrapped []byte, keyID string) ([]byte, error) {
	return o.c.Unwrap(ctx, wrapped, keyID) //nolint:wrapcheck // a transparent narrowing
}
