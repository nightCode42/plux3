// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package pkcs11helper

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/asn1"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/pkcs11pb"
	"github.com/nightCode42/plux3/backend/internal/signing"
)

// Verifies: SEC-120.
func TestDecodeEd25519Attributes(t *testing.T) {
	t.Parallel()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	wrapped, _ := asn1.Marshal([]byte(pub))
	for name, attrs := range map[string]PublicAttributes{
		"OID and wrapped point":  {ECParams: paramsEd25519OID, ECPoint: wrapped},
		"OID and bare point":     {ECParams: paramsEd25519OID, ECPoint: pub},
		"name and wrapped point": {ECParams: paramsEd25519Str, ECPoint: wrapped},
		"name and bare point":    {ECParams: paramsEd25519Str, ECPoint: pub},
	} {
		got, err := attrs.Decode()
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		parsed, err := x509.ParsePKIXPublicKey(got.DER)
		if err != nil || !pub.Equal(parsed) {
			t.Errorf("%s: DER does not hold the key (%v)", name, err)
		}
		if got.Algorithm != pkcs11pb.Algorithm_ALGORITHM_ED25519 || got.KeyID != signing.KeyID(pub) {
			t.Errorf("%s: algorithm %v, key ID %q", name, got.Algorithm, got.KeyID)
		}
	}
}

// Verifies: SEC-120.
func TestDecodeP256Attributes(t *testing.T) {
	t.Parallel()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	point, _ := key.PublicKey.Bytes()
	wrapped, _ := asn1.Marshal(point)
	for name, raw := range map[string][]byte{"wrapped": wrapped, "bare": point} {
		got, err := PublicAttributes{ECParams: paramsP256, ECPoint: raw}.Decode()
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		wantID, _ := signing.TokenKeyID(&key.PublicKey)
		if got.Algorithm != pkcs11pb.Algorithm_ALGORITHM_ECDSA_P256_SHA256 || got.KeyID != wantID {
			t.Errorf("%s: algorithm %v, key ID %q, want %q", name, got.Algorithm, got.KeyID, wantID)
		}
	}
}

// Verifies: SEC-120.
func TestDecodeRefusesWhatItCannotUse(t *testing.T) {
	t.Parallel()
	p384 := []byte{0x06, 0x05, 0x2b, 0x81, 0x04, 0x00, 0x22}
	offCurve := append([]byte{0x04}, make([]byte, 64)...)
	for name, attrs := range map[string]PublicAttributes{
		"no parameters":      {},
		"another curve":      {ECParams: p384, ECPoint: make([]byte, 97)},
		"ed25519 short":      {ECParams: paramsEd25519OID, ECPoint: make([]byte, 31)},
		"ed25519 trailing":   {ECParams: paramsEd25519OID, ECPoint: append([]byte{0x04, 0x20}, make([]byte, 34)...)},
		"p256 off the curve": {ECParams: paramsP256, ECPoint: offCurve},
		"p256 short":         {ECParams: paramsP256, ECPoint: make([]byte, 10)},
		"p256 no point":      {ECParams: paramsP256},
	} {
		if _, err := attrs.Decode(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
