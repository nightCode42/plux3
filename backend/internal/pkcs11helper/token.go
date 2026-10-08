// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package pkcs11helper

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"

	"github.com/nightCode42/plux3/backend/internal/pkcs11pb"
	"github.com/nightCode42/plux3/backend/internal/signing"
)

var (
	// ErrKeyNotFound is returned by a Token for a selector that matches
	// no key.
	ErrKeyNotFound = errors.New("pkcs11helper: no such key")
	// ErrAmbiguousKey is returned by a Token for a selector that matches
	// more than one key.
	ErrAmbiguousKey = errors.New("pkcs11helper: the key reference matches more than one key")
)

// Token is a PKCS#11 token as the server needs it: two operations, and
// no way to read a private key. An implementation is safe for concurrent
// use. Errors must not contain a PIN or key material.
type Token interface {
	// PublicKey returns the attributes of the public key the selector
	// names, or ErrKeyNotFound or ErrAmbiguousKey.
	PublicKey(sel Selector) (PublicAttributes, error)
	// Sign signs data with the private key the selector names, using
	// CKM_EDDSA for Ed25519 (data is the message) and CKM_ECDSA for
	// ECDSA P-256 (data is the SHA-256 digest). The result is the
	// token's raw signature.
	Sign(sel Selector, alg pkcs11pb.Algorithm, data []byte) ([]byte, error)
}

// PublicAttributes are the PKCS#11 attributes of an elliptic-curve
// public key.
type PublicAttributes struct {
	// ECParams is CKA_EC_PARAMS, the DER curve identifier.
	ECParams []byte
	// ECPoint is CKA_EC_POINT: the public point, DER-wrapped in an OCTET
	// STRING as PKCS#11 specifies, or bare as some modules return it.
	ECPoint []byte
}

// DER encodings of the curve identifiers a module may report: the OIDs
// of Ed25519 and P-256, and the PrintableString name PKCS#11 3.0 allows
// for Ed25519.
var (
	paramsEd25519OID = []byte{0x06, 0x03, 0x2b, 0x65, 0x70}
	paramsEd25519Str = append([]byte{0x13, 0x0c}, "edwards25519"...)
	paramsP256       = []byte{0x06, 0x08, 0x2a, 0x86, 0x48, 0xce, 0x3d, 0x03, 0x01, 0x07}
)

// Public is a decoded public key.
type Public struct {
	// Algorithm is the algorithm the key signs with.
	Algorithm pkcs11pb.Algorithm
	// DER is the PKIX encoding.
	DER []byte
	// KeyID is the identifier the server stores with signatures.
	KeyID string
}

// Decode turns the attributes into a public key, refusing a curve that
// is neither Ed25519 nor P-256 and a point that is not on the curve.
func (a PublicAttributes) Decode() (Public, error) {
	switch {
	case bytes.Equal(a.ECParams, paramsEd25519OID), bytes.Equal(a.ECParams, paramsEd25519Str):
		point, err := unwrapPoint(a.ECPoint, ed25519.PublicKeySize)
		if err != nil {
			return Public{}, err
		}
		pub := ed25519.PublicKey(point)
		der, err := x509.MarshalPKIXPublicKey(pub)
		if err != nil {
			return Public{}, fmt.Errorf("pkcs11helper: encode an Ed25519 key: %w", err)
		}
		return Public{Algorithm: pkcs11pb.Algorithm_ALGORITHM_ED25519, DER: der, KeyID: signing.KeyID(pub)}, nil
	case bytes.Equal(a.ECParams, paramsP256):
		point, err := unwrapPoint(a.ECPoint, 1+2*32)
		if err != nil {
			return Public{}, err
		}
		pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), point)
		if err != nil {
			return Public{}, errors.New("pkcs11helper: the public point is not on P-256")
		}
		der, err := x509.MarshalPKIXPublicKey(pub)
		if err != nil {
			return Public{}, fmt.Errorf("pkcs11helper: encode a P-256 key: %w", err)
		}
		id, err := signing.TokenKeyID(pub)
		if err != nil {
			return Public{}, fmt.Errorf("pkcs11helper: %w", err)
		}
		return Public{Algorithm: pkcs11pb.Algorithm_ALGORITHM_ECDSA_P256_SHA256, DER: der, KeyID: id}, nil
	default:
		return Public{}, errors.New("pkcs11helper: the key is on a curve other than Ed25519 or P-256")
	}
}

// unwrapPoint returns the public point of a CKA_EC_POINT of the given
// length, removing the OCTET STRING wrapper when there is one. The two
// forms differ in length, so they cannot be confused.
func unwrapPoint(raw []byte, size int) ([]byte, error) {
	if len(raw) == size {
		return raw, nil
	}
	var point []byte
	if rest, err := asn1.Unmarshal(raw, &point); err != nil || len(rest) != 0 || len(point) != size {
		return nil, errors.New("pkcs11helper: the public point has an unexpected encoding")
	}
	return point, nil
}
