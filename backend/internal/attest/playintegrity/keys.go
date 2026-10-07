// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package playintegrity

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/base64"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// aesKeyLen is the length in bytes of the A256KW key-encryption key.
const aesKeyLen = 32

// Keys holds the two Play Console keys needed to open a token.
//
// A Keys value is immutable after ParseKeys returns and is safe for
// concurrent use.
type Keys struct {
	// Decryption is the raw 32-byte AES key that unwraps the content key.
	Decryption []byte
	// Verification is the EC P-256 key that verifies the verdict signature.
	Verification *ecdsa.PublicKey
}

// ParseKeys decodes the Play Console keys: the decryption key as standard
// base64 of the raw 32-byte AES key, the verification key as standard base64
// of a DER SubjectPublicKeyInfo holding an EC P-256 key. Errors never echo
// the input.
func ParseKeys(decryptionB64, verificationB64 string) (Keys, error) {
	dec, err := base64.StdEncoding.DecodeString(decryptionB64)
	if err != nil {
		return Keys{}, plxerr.New(plxerr.AttestationFailed, "play integrity decryption key is not valid base64")
	}
	if len(dec) != aesKeyLen {
		return Keys{}, plxerr.New(plxerr.AttestationFailed, "play integrity decryption key must be %d bytes", aesKeyLen)
	}
	der, err := base64.StdEncoding.DecodeString(verificationB64)
	if err != nil {
		return Keys{}, plxerr.New(plxerr.AttestationFailed, "play integrity verification key is not valid base64")
	}
	parsed, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return Keys{}, plxerr.New(plxerr.AttestationFailed, "play integrity verification key is not a valid public key")
	}
	pub, ok := parsed.(*ecdsa.PublicKey)
	if !ok || pub.Curve != elliptic.P256() {
		return Keys{}, plxerr.New(plxerr.AttestationFailed, "play integrity verification key must be an EC P-256 key")
	}
	return Keys{Decryption: dec, Verification: pub}, nil
}

// valid reports whether both keys are present and well formed.
func (k Keys) valid() bool {
	return len(k.Decryption) == aesKeyLen && k.Verification != nil && k.Verification.Curve == elliptic.P256()
}
