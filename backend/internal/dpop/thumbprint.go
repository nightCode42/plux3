// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package dpop

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/subtle"
	"encoding/base64"

	"github.com/go-jose/go-jose/v4"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// Thumbprint returns the RFC 7638 SHA-256 JWK thumbprint of a P-256 public
// key, base64url-encoded without padding. It is the identifier a token is
// bound to (jkt).
func Thumbprint(pub *ecdsa.PublicKey) (string, error) {
	if pub == nil || pub.Curve != elliptic.P256() {
		return "", plxerr.New(plxerr.DPoPProofInvalid, "dpop key is not a P-256 public key")
	}
	sum, err := (&jose.JSONWebKey{Key: pub}).Thumbprint(crypto.SHA256)
	if err != nil {
		return "", plxerr.Wrap(plxerr.DPoPProofInvalid, err, "dpop key thumbprint failed")
	}
	return base64.RawURLEncoding.EncodeToString(sum), nil
}

// CheckBinding reports TokenBindingMismatch unless the thumbprint an access
// token is bound to equals the thumbprint of the key that signed the proof.
// The comparison takes the same time whatever the inputs share.
func CheckBinding(tokenJKT, proofJKT string) error {
	if tokenJKT == "" || subtle.ConstantTimeCompare([]byte(tokenJKT), []byte(proofJKT)) != 1 {
		return plxerr.New(plxerr.TokenBindingMismatch, "access token is bound to another device key")
	}
	return nil
}
