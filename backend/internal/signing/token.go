// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package signing

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
)

// ErrProductionToken is returned when a backend that is refused for
// production is asked for a production token key (SEC-056).
var ErrProductionToken = errors.New("signing: the file backend holds no production token key")

// TokenClass names the environment class a device access token key
// serves. Production and development tokens are signed by different
// keys, so a token minted for one class never verifies in the other
// (SEC-056).
type TokenClass string

const (
	// TokenProduction is the class of production environments. The file
	// backend refuses it.
	TokenProduction TokenClass = "production"
	// TokenDevelopment is the class of every other environment.
	TokenDevelopment TokenClass = "development"
)

// valid reports whether the class is one of the two defined.
func (c TokenClass) valid() bool { return c == TokenProduction || c == TokenDevelopment }

// TokenSignatureSize is the length of a JOSE ES256 signature: r then s,
// 32 bytes each (RFC 7518 section 3.4).
const TokenSignatureSize = 64

// TokenKey is the public half of a token signing key, for verifying
// device access tokens.
type TokenKey struct {
	// ID is the stable identifier, which tokens carry as their kid.
	ID string
	// Class is the environment class the key signs for.
	Class TokenClass
	// Public is the verification key.
	Public *ecdsa.PublicKey
}

// TokenSigner signs device access tokens (SEC-020). Like Crypter it is
// the narrow capability the api role holds: it can sign a token but
// nothing else, and no private key is reachable through it (L-3).
type TokenSigner interface {
	// SignToken signs a JWS signing input with the key of the class. The
	// signature is in JOSE ES256 form, 64 bytes r followed by s, and the
	// key identifier is that of the key that made it.
	SignToken(ctx context.Context, class TokenClass, signingInput []byte) (signature []byte, keyID string, err error)
	// TokenKeys returns every public key that may have signed a token,
	// all classes, each class ordered oldest first, so that the last key
	// of a class is the one that signs now.
	TokenKeys(ctx context.Context) ([]TokenKey, error)
}

// TokenKeyID is the stable identifier of a token key: the first sixteen
// bytes of the SHA-256 of its PKIX encoding, in unpadded base64url.
func TokenKeyID(pub *ecdsa.PublicKey) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", fmt.Errorf("signing: encode a token public key: %w", err)
	}
	sum := sha256.Sum256(der)
	return base64.RawURLEncoding.EncodeToString(sum[:16]), nil
}

// TokenOnly narrows a backend to token signing. The api role is given
// this and never the backend itself, so no handler can reach release
// signing or a private key, even through a type assertion (L-3).
func TokenOnly(t TokenSigner) TokenSigner { return tokenOnly{t: t} }

// tokenOnly hides everything of a backend but SignToken and TokenKeys.
type tokenOnly struct{ t TokenSigner }

// SignToken signs a token's signing input.
func (o tokenOnly) SignToken(ctx context.Context, class TokenClass, signingInput []byte) ([]byte, string, error) {
	return o.t.SignToken(ctx, class, signingInput) //nolint:wrapcheck // a transparent narrowing
}

// TokenKeys returns the token verification keys.
func (o tokenOnly) TokenKeys(ctx context.Context) ([]TokenKey, error) {
	return o.t.TokenKeys(ctx) //nolint:wrapcheck // a transparent narrowing
}
