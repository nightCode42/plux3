// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package appattest

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	_ "embed" // For the Apple root certificate.
	"encoding/asn1"
	"encoding/binary"
	"encoding/pem"
	"time"

	"github.com/nightCode42/plux3/backend/internal/cbor"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// maxInput bounds every attestation object and assertion before it is
// parsed.
const maxInput = 64 << 10

// Layout of the authenticator data (WebAuthn §6.1, as Apple uses it).
const (
	rpIDHashLen    = 32
	counterOffset  = 33
	assertionLen   = 37
	aaguidOffset   = 37
	credIDLenOff   = 53
	credIDOffset   = 55
	aaguidLen      = 16
	attestedMinLen = credIDOffset
)

// nonceOID is the extension of the credential certificate that carries
// the nonce.
var nonceOID = asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 8, 2}

//go:embed roots/apple-app-attestation-root.pem
var appleRootPEM []byte

// Environment is the App Attest environment an app was built for. It
// selects the AAGUID the authenticator data must carry.
type Environment int

const (
	// Development is the sandbox environment of debug builds.
	Development Environment = iota
	// Production is the environment of App Store and TestFlight builds.
	Production
)

// aaguid returns the AAGUID Apple writes for the environment.
func (e Environment) aaguid() ([aaguidLen]byte, bool) {
	var g [aaguidLen]byte
	switch e {
	case Development:
		copy(g[:], "appattestdevelop")
	case Production:
		copy(g[:], "appattest")
	default:
		return g, false
	}
	return g, true
}

// Attestation is the outcome of a verified attestation.
type Attestation struct {
	// PublicKey is the attested key; assertions are verified against it.
	PublicKey *ecdsa.PublicKey
	// Receipt is Apple's receipt for the attestation, opaque to Plux and
	// kept for later fraud-metric queries. It is sensitive; never log it.
	Receipt []byte
	// Counter is the signature counter of the attestation; it is zero.
	Counter uint32
}

// Verifier verifies App Attest attestations. It is safe for concurrent
// use as long as Roots is not modified.
type Verifier struct {
	// Roots holds the trusted Apple root; DefaultRoots returns the
	// embedded one.
	Roots *x509.CertPool
	// Now returns the time at which certificates must be valid.
	Now func() time.Time
}

// DefaultRoots returns a pool holding the embedded Apple App Attestation
// Root CA.
func DefaultRoots() (*x509.CertPool, error) {
	block, _ := pem.Decode(appleRootPEM)
	if block == nil {
		return nil, plxerr.New(plxerr.AttestationFailed, "app attest: the embedded root is not PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, plxerr.Wrap(plxerr.AttestationFailed, err, "app attest: the embedded root does not parse")
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return pool, nil
}

// ClientDataHash binds an attestation or assertion to the server's
// challenge and the device's DPoP key thumbprint: SHA-256(challenge ‖
// jkt). The device passes it as clientDataHash to attestKey and
// generateAssertion.
func ClientDataHash(challenge []byte, jkt string) [32]byte {
	h := sha256.New()
	h.Write(challenge)
	h.Write([]byte(jkt))
	var out [32]byte
	h.Sum(out[:0])
	return out
}

// fail returns the error for a failed check.
func fail(check string) error {
	return plxerr.New(plxerr.AttestationFailed, "app attest: %s", check)
}

// VerifyAttestation verifies an attestation object for the key keyID (the
// raw 32 bytes of the key identifier). appID is the team and bundle
// identifier, "TEAMID.com.example.app".
func (v *Verifier) VerifyAttestation(object, keyID []byte, clientDataHash [32]byte, appID string, env Environment) (Attestation, error) {
	if len(object) == 0 || len(object) > maxInput {
		return Attestation{}, fail("the attestation object has an invalid size")
	}
	want, ok := env.aaguid()
	if !ok {
		return Attestation{}, fail("the environment is unknown")
	}
	cred, inter, authData, receipt, err := parseObject(object)
	if err != nil {
		return Attestation{}, err
	}
	if err := v.verifyChain(cred, inter); err != nil {
		return Attestation{}, err
	}
	nonce := nonceOf(authData, clientDataHash)
	if err := checkNonce(cred, nonce[:]); err != nil {
		return Attestation{}, err
	}
	pub, err := checkKeyID(cred, keyID)
	if err != nil {
		return Attestation{}, err
	}
	counter, err := checkAuthData(authData, keyID, appID, want)
	if err != nil {
		return Attestation{}, err
	}
	return Attestation{PublicKey: pub, Receipt: bytes.Clone(receipt), Counter: counter}, nil
}

// parseObject reads the CBOR attestation object.
func parseObject(object []byte) (cred, inter *x509.Certificate, authData, receipt []byte, err error) {
	v, used, err := cbor.Decode(object)
	if err != nil || used != len(object) {
		return nil, nil, nil, nil, fail("the attestation object is not valid CBOR")
	}
	m, ok := v.(map[any]any)
	if !ok {
		return nil, nil, nil, nil, fail("the attestation object is not a map")
	}
	if f, _ := m["fmt"].(string); f != "apple-appattest" {
		return nil, nil, nil, nil, fail("the attestation format is not apple-appattest")
	}
	authData, ok = m["authData"].([]byte)
	if !ok {
		return nil, nil, nil, nil, fail("authData is missing")
	}
	stmt, ok := m["attStmt"].(map[any]any)
	if !ok {
		return nil, nil, nil, nil, fail("attStmt is missing")
	}
	receipt, ok = stmt["receipt"].([]byte)
	if !ok {
		return nil, nil, nil, nil, fail("the receipt is missing")
	}
	chain, ok := stmt["x5c"].([]any)
	if !ok || len(chain) != 2 {
		return nil, nil, nil, nil, fail("x5c does not hold two certificates")
	}
	certs := make([]*x509.Certificate, 2)
	for i, item := range chain {
		der, ok := item.([]byte)
		if !ok {
			return nil, nil, nil, nil, fail("x5c holds a value that is not a certificate")
		}
		if certs[i], err = x509.ParseCertificate(der); err != nil {
			return nil, nil, nil, nil, fail("x5c holds a certificate that does not parse")
		}
	}
	return certs[0], certs[1], authData, receipt, nil
}

// verifyChain verifies the credential certificate through the
// intermediate to the trusted root.
func (v *Verifier) verifyChain(cred, inter *x509.Certificate) error {
	if v.Roots == nil || v.Now == nil {
		return fail("the verifier is not configured")
	}
	pool := x509.NewCertPool()
	pool.AddCert(inter)
	_, err := cred.Verify(x509.VerifyOptions{
		Roots:         v.Roots,
		Intermediates: pool,
		CurrentTime:   v.Now(),
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	})
	if err != nil {
		return plxerr.Wrap(plxerr.AttestationFailed, err, "app attest: the certificate chain does not verify")
	}
	return nil
}

// nonceOf returns SHA-256(authData ‖ clientDataHash).
func nonceOf(authData []byte, clientDataHash [32]byte) [32]byte {
	h := sha256.New()
	h.Write(authData)
	h.Write(clientDataHash[:])
	var out [32]byte
	h.Sum(out[:0])
	return out
}

// nonceExtension is the DER layout of the nonce extension value.
type nonceExtension struct {
	Nonce []byte `asn1:"explicit,tag:1"`
}

// checkNonce compares the nonce in the credential certificate with the
// expected one.
func checkNonce(cred *x509.Certificate, nonce []byte) error {
	for _, ext := range cred.Extensions {
		if !ext.Id.Equal(nonceOID) {
			continue
		}
		var parsed nonceExtension
		rest, err := asn1.Unmarshal(ext.Value, &parsed)
		if err != nil || len(rest) != 0 {
			return fail("the nonce extension is malformed")
		}
		if subtle.ConstantTimeCompare(parsed.Nonce, nonce) != 1 {
			return fail("the nonce does not match")
		}
		return nil
	}
	return fail("the nonce extension is missing")
}

// checkKeyID checks that the certificate key is a P-256 key whose
// uncompressed point hashes to keyID, and returns it.
func checkKeyID(cred *x509.Certificate, keyID []byte) (*ecdsa.PublicKey, error) {
	pub, ok := cred.PublicKey.(*ecdsa.PublicKey)
	if !ok || pub.Curve != elliptic.P256() {
		return nil, fail("the attested key is not an ECDSA P-256 key")
	}
	ecdhKey, err := pub.ECDH()
	if err != nil {
		return nil, fail("the attested key is not a valid point")
	}
	sum := sha256.Sum256(ecdhKey.Bytes())
	if subtle.ConstantTimeCompare(sum[:], keyID) != 1 {
		return nil, fail("the key identifier does not match the attested key")
	}
	return pub, nil
}

// checkAuthData checks the attestation's authenticator data and returns
// its counter.
func checkAuthData(authData, keyID []byte, appID string, aaguid [aaguidLen]byte) (uint32, error) {
	if len(authData) < attestedMinLen {
		return 0, fail("authData is too short")
	}
	if err := checkRPID(authData, appID); err != nil {
		return 0, err
	}
	counter := binary.BigEndian.Uint32(authData[counterOffset:])
	if counter != 0 {
		return 0, fail("the attestation counter is not zero")
	}
	if !bytes.Equal(authData[aaguidOffset:aaguidOffset+aaguidLen], aaguid[:]) {
		return 0, fail("the AAGUID does not match the environment")
	}
	n := int(binary.BigEndian.Uint16(authData[credIDLenOff:]))
	if len(authData)-credIDOffset < n {
		return 0, fail("the credential identifier is truncated")
	}
	if !bytes.Equal(authData[credIDOffset:credIDOffset+n], keyID) {
		return 0, fail("the credential identifier does not match the key identifier")
	}
	return counter, nil
}

// checkRPID checks the relying-party hash against the application
// identifier.
func checkRPID(authData []byte, appID string) error {
	sum := sha256.Sum256([]byte(appID))
	if subtle.ConstantTimeCompare(authData[:rpIDHashLen], sum[:]) != 1 {
		return fail("the application identifier does not match")
	}
	return nil
}

// VerifyAssertion verifies an assertion made with the attested key pub
// and returns its counter. The counter must exceed lastCounter; the
// caller must store the returned value with a compare-and-set so a
// replayed assertion is refused.
func VerifyAssertion(assertion []byte, pub *ecdsa.PublicKey, clientDataHash [32]byte, appID string, lastCounter uint32) (uint32, error) {
	if pub == nil {
		return 0, fail("the public key is missing")
	}
	if len(assertion) == 0 || len(assertion) > maxInput {
		return 0, fail("the assertion has an invalid size")
	}
	v, used, err := cbor.Decode(assertion)
	if err != nil || used != len(assertion) {
		return 0, fail("the assertion is not valid CBOR")
	}
	m, ok := v.(map[any]any)
	if !ok {
		return 0, fail("the assertion is not a map")
	}
	sig, ok := m["signature"].([]byte)
	if !ok {
		return 0, fail("the assertion signature is missing")
	}
	authData, ok := m["authenticatorData"].([]byte)
	if !ok || len(authData) < assertionLen {
		return 0, fail("the authenticator data is missing or too short")
	}
	nonce := nonceOf(authData, clientDataHash)
	digest := sha256.Sum256(nonce[:])
	if !ecdsa.VerifyASN1(pub, digest[:], sig) {
		return 0, fail("the assertion signature does not verify")
	}
	if err := checkRPID(authData, appID); err != nil {
		return 0, err
	}
	counter := binary.BigEndian.Uint32(authData[counterOffset:])
	if counter <= lastCounter {
		return 0, fail("the assertion counter did not increase")
	}
	return counter, nil
}
