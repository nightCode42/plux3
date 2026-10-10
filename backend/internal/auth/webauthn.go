// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"slices"

	"github.com/nightCode42/plux3/backend/internal/cbor"
)

// This file verifies WebAuthn (Web Authentication Level 2) responses: a
// registration's attestation object and a sign-in's assertion. Plux asks
// for no attestation ("none" conveyance), so the attestation statement is
// not verified and the authenticator's make is not trusted; what is
// verified is that the browser saw the right origin and challenge, that
// the authenticator scoped the credential to the relying party and the
// user was present, and — at sign-in — the signature and its counter.

// errWebAuthn is returned for a response that does not verify.
var errWebAuthn = errors.New("auth: the security key response does not verify")

// COSE algorithm identifiers Plux accepts (RFC 9053, RFC 8812).
const (
	coseES256 = -7
	coseEdDSA = -8
	coseRS256 = -257
)

// webAuthnAlgorithms are offered at registration, strongest first.
var webAuthnAlgorithms = []int64{coseEdDSA, coseES256, coseRS256}

// Authenticator data flags.
const (
	flagUserPresent  = 0x01
	flagUserVerified = 0x04
	flagAttested     = 0x40
)

// WebAuthnConfig names the relying party.
type WebAuthnConfig struct {
	// RPID is the relying party identifier: the registrable domain Studio
	// is served from, such as "plux.example.com".
	RPID string
	// RPName is shown by the authenticator.
	RPName string
	// Origins are the exact origins Studio is served from.
	Origins []string
}

// clientData is the browser's record of a ceremony.
type clientData struct {
	Type        string `json:"type"`
	Challenge   string `json:"challenge"`
	Origin      string `json:"origin"`
	CrossOrigin bool   `json:"crossOrigin"`
}

// checkClientData verifies the ceremony type, the challenge and the
// origin the browser recorded.
func (c WebAuthnConfig) checkClientData(raw []byte, ceremony string, challenge []byte) error {
	var cd clientData
	if err := json.Unmarshal(raw, &cd); err != nil {
		return fmt.Errorf("%w: client data is not JSON", errWebAuthn)
	}
	if cd.Type != ceremony {
		return fmt.Errorf("%w: a %q ceremony, not %q", errWebAuthn, cd.Type, ceremony)
	}
	got, err := base64.RawURLEncoding.DecodeString(cd.Challenge)
	if err != nil || subtle.ConstantTimeCompare(got, challenge) != 1 {
		return fmt.Errorf("%w: the challenge does not match", errWebAuthn)
	}
	if cd.CrossOrigin || !slices.Contains(c.Origins, cd.Origin) {
		return fmt.Errorf("%w: origin %q is not Studio's", errWebAuthn, cd.Origin)
	}
	return nil
}

// authenticatorData is the parsed authenticator data.
type authenticatorData struct {
	rpIDHash     []byte
	flags        byte
	signCount    uint32
	credentialID []byte
	publicKey    []byte
}

// parseAuthenticatorData reads authenticator data; attested credential
// data is read when the AT flag is set.
func parseAuthenticatorData(b []byte) (authenticatorData, error) {
	if len(b) < 37 {
		return authenticatorData{}, fmt.Errorf("%w: authenticator data is too short", errWebAuthn)
	}
	a := authenticatorData{rpIDHash: b[:32], flags: b[32], signCount: binary.BigEndian.Uint32(b[33:37])}
	if a.flags&flagAttested == 0 {
		return a, nil
	}
	rest := b[37:]
	if len(rest) < 18 {
		return authenticatorData{}, fmt.Errorf("%w: attested credential data is too short", errWebAuthn)
	}
	n := int(binary.BigEndian.Uint16(rest[16:18]))
	rest = rest[18:]
	if n == 0 || n > 1023 || len(rest) < n {
		return authenticatorData{}, fmt.Errorf("%w: the credential identifier is malformed", errWebAuthn)
	}
	a.credentialID = rest[:n]
	_, used, err := cbor.Decode(rest[n:])
	if err != nil {
		return authenticatorData{}, fmt.Errorf("%w: the public key: %w", errWebAuthn, err)
	}
	a.publicKey = rest[n : n+used]
	return a, nil
}

// checkAuthenticatorData verifies that the credential is scoped to this
// relying party and that the user was present.
func (c WebAuthnConfig) checkAuthenticatorData(a authenticatorData) error {
	want := sha256.Sum256([]byte(c.RPID))
	if !bytes.Equal(a.rpIDHash, want[:]) {
		return fmt.Errorf("%w: the credential belongs to another site", errWebAuthn)
	}
	if a.flags&flagUserPresent == 0 {
		return fmt.Errorf("%w: the user was not present", errWebAuthn)
	}
	return nil
}

// registered is a credential a registration created.
type registered struct {
	ID        []byte
	PublicKey []byte
	SignCount uint32
}

// verifyRegistration checks a registration response and returns the new
// credential.
func (c WebAuthnConfig) verifyRegistration(challenge, clientDataJSON, attestationObject []byte) (registered, error) {
	if err := c.checkClientData(clientDataJSON, "webauthn.create", challenge); err != nil {
		return registered{}, err
	}
	v, _, err := cbor.Decode(attestationObject)
	if err != nil {
		return registered{}, fmt.Errorf("%w: the attestation object: %w", errWebAuthn, err)
	}
	obj, ok := v.(map[any]any)
	if !ok {
		return registered{}, fmt.Errorf("%w: the attestation object is not a map", errWebAuthn)
	}
	raw, ok := obj["authData"].([]byte)
	if !ok {
		return registered{}, fmt.Errorf("%w: the attestation object has no authenticator data", errWebAuthn)
	}
	a, err := parseAuthenticatorData(raw)
	if err != nil {
		return registered{}, err
	}
	if err := c.checkAuthenticatorData(a); err != nil {
		return registered{}, err
	}
	if a.credentialID == nil {
		return registered{}, fmt.Errorf("%w: the response carries no credential", errWebAuthn)
	}
	if _, _, err := parseCOSEKey(a.publicKey); err != nil {
		return registered{}, err
	}
	return registered{ID: a.credentialID, PublicKey: a.publicKey, SignCount: a.signCount}, nil
}

// verifyAssertion checks a sign-in response against a stored credential
// and returns the authenticator's new signature counter. A counter that
// does not advance, where the authenticator keeps one, means the
// credential may have been cloned and is refused.
func (c WebAuthnConfig) verifyAssertion(challenge, publicKey []byte, storedCount uint32, clientDataJSON, authData, signature []byte) (uint32, error) {
	if err := c.checkClientData(clientDataJSON, "webauthn.get", challenge); err != nil {
		return 0, err
	}
	a, err := parseAuthenticatorData(authData)
	if err != nil {
		return 0, err
	}
	if err := c.checkAuthenticatorData(a); err != nil {
		return 0, err
	}
	key, alg, err := parseCOSEKey(publicKey)
	if err != nil {
		return 0, err
	}
	sum := sha256.Sum256(clientDataJSON)
	signed := append(append([]byte{}, authData...), sum[:]...)
	if !verifySignature(key, alg, signed, signature) {
		return 0, fmt.Errorf("%w: the signature does not verify", errWebAuthn)
	}
	if (a.signCount != 0 || storedCount != 0) && a.signCount <= storedCount {
		return 0, fmt.Errorf("%w: the signature counter went backwards; the key may be cloned", errWebAuthn)
	}
	return a.signCount, nil
}

// parseCOSEKey reads a COSE_Key (RFC 9052) of an accepted algorithm.
func parseCOSEKey(b []byte) (crypto.PublicKey, int64, error) {
	v, _, err := cbor.Decode(b)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: the public key: %w", errWebAuthn, err)
	}
	m, ok := v.(map[any]any)
	if !ok {
		return nil, 0, fmt.Errorf("%w: the public key is not a map", errWebAuthn)
	}
	kty, _ := m[int64(1)].(int64)
	alg, _ := m[int64(3)].(int64)
	crv, _ := m[int64(-1)].(int64)
	x, _ := m[int64(-2)].([]byte)
	y, _ := m[int64(-3)].([]byte)
	switch {
	case kty == 2 && alg == coseES256 && crv == 1 && len(x) == 32 && len(y) == 32:
		pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), append(append([]byte{4}, x...), y...))
		if err != nil {
			return nil, 0, fmt.Errorf("%w: the P-256 key is not on the curve", errWebAuthn)
		}
		return pub, alg, nil
	case kty == 1 && alg == coseEdDSA && crv == 6 && len(x) == ed25519.PublicKeySize:
		return ed25519.PublicKey(x), alg, nil
	case kty == 3 && alg == coseRS256:
		n, _ := m[int64(-1)].([]byte)
		e, _ := m[int64(-2)].([]byte)
		if len(n) < 256 || len(e) == 0 || len(e) > 4 {
			return nil, 0, fmt.Errorf("%w: the RSA key is weaker than 2048 bits or malformed", errWebAuthn)
		}
		var exponent [4]byte
		copy(exponent[4-len(e):], e)
		return &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(binary.BigEndian.Uint32(exponent[:]))}, alg, nil
	}
	return nil, 0, fmt.Errorf("%w: an unsupported key type %d or algorithm %d", errWebAuthn, kty, alg)
}

// verifySignature checks a WebAuthn signature with the key's algorithm.
func verifySignature(key crypto.PublicKey, alg int64, signed, signature []byte) bool {
	switch k := key.(type) {
	case *ecdsa.PublicKey:
		sum := sha256.Sum256(signed)
		return alg == coseES256 && ecdsa.VerifyASN1(k, sum[:], signature)
	case ed25519.PublicKey:
		return alg == coseEdDSA && ed25519.Verify(k, signed, signature)
	case *rsa.PublicKey:
		sum := sha256.Sum256(signed)
		return alg == coseRS256 && rsa.VerifyPKCS1v15(k, crypto.SHA256, sum[:], signature) == nil
	}
	return false
}
