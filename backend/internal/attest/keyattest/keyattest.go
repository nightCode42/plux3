// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package keyattest

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/subtle"
	"crypto/x509"
	"encoding/asn1"
	"slices"
	"time"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

const (
	minChainLength = 2
	maxChainLength = 10
)

// attestationOID identifies the Android key attestation extension.
var attestationOID = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 11129, 2, 1, 17}

// Policy states what a device's attestation must prove. The zero Policy
// accepts no package, so it rejects every chain.
type Policy struct {
	// PackageNames lists the application identifiers the attested key may
	// belong to. The attested package must be one of them.
	PackageNames []string
	// SigningCertDigests lists accepted SHA-256 digests of the application's
	// signing certificates. When non-empty, the attestation must carry at
	// least one digest and every digest it carries must be listed.
	SigningCertDigests [][]byte
	// RequireHardware demands that both the attestation and the key are
	// protected by a TEE or a StrongBox.
	RequireHardware bool
	// RequireVerifiedBoot demands a locked bootloader and a fully verified
	// boot state.
	RequireVerifiedBoot bool
}

// Result is the verified content of an attestation. It is only returned for a
// chain that passed every check.
type Result struct {
	// AttestationLevel is where the attestation itself was produced.
	AttestationLevel SecurityLevel
	// KeyMintLevel is where the attested key is protected.
	KeyMintLevel SecurityLevel
	// VerifiedBootState is the boot state from the hardware-enforced root of
	// trust, or BootUnknown when it carried none.
	VerifiedBootState BootState
	// DeviceLocked reports a locked bootloader; false when no root of trust
	// was present.
	DeviceLocked bool
	// PackageName is the attested application identifier that matched the
	// policy.
	PackageName string
	// SigningCertDigests are the SHA-256 digests of the application's signing
	// certificates as attested.
	SigningCertDigests [][]byte
	// OSPatchLevel is the attested operating system patch level (YYYYMM, or
	// YYYYMMDD from attestation version 4), or 0 when absent.
	OSPatchLevel int
	// PublicKey is the attested P-256 key, the leaf certificate's key.
	PublicKey *ecdsa.PublicKey
}

// HardwareBacked reports whether both the attestation and the key are
// protected by a TEE or a StrongBox.
func (r Result) HardwareBacked() bool {
	return r.AttestationLevel >= SecurityTrustedEnvironment &&
		r.KeyMintLevel >= SecurityTrustedEnvironment
}

// Verifier verifies Android Key Attestation chains. Its fields must not be
// modified after first use; a Verifier is then safe for concurrent use.
type Verifier struct {
	// Roots holds the trusted attestation roots.
	Roots *x509.CertPool
	// Revocations is the parsed status list; nil lists nothing.
	Revocations *Revocations
	// Now returns the time at which certificate validity is judged.
	Now func() time.Time
}

// NewVerifier returns a Verifier. roots and now are required; revocations
// may be nil.
func NewVerifier(roots *x509.CertPool, revocations *Revocations, now func() time.Time) (*Verifier, error) {
	if roots == nil {
		return nil, plxerr.New(plxerr.AttestationFailed, "verifier needs trusted roots")
	}
	if now == nil {
		return nil, plxerr.New(plxerr.AttestationFailed, "verifier needs a clock")
	}
	return &Verifier{Roots: roots, Revocations: revocations, Now: now}, nil
}

// Verify checks an attestation chain (DER certificates, leaf first) against
// the challenge the server issued and the policy. On any failure it returns a
// zero Result and a plxerr error naming the failed check.
func (v *Verifier) Verify(chain [][]byte, challenge []byte, p Policy) (Result, error) {
	if v == nil || v.Roots == nil || v.Now == nil {
		return Result{}, plxerr.New(plxerr.AttestationFailed, "verifier is not configured")
	}
	if len(chain) < minChainLength || len(chain) > maxChainLength {
		return Result{}, plxerr.New(plxerr.AttestationFailed, "certificate chain length is not accepted")
	}
	certs, err := parseChain(chain)
	if err != nil {
		return Result{}, err
	}
	if err := v.verifyChain(certs); err != nil {
		return Result{}, err
	}
	key, err := leafKey(certs[0])
	if err != nil {
		return Result{}, err
	}
	kd, err := keyDescriptionOf(certs)
	if err != nil {
		return Result{}, err
	}
	res, err := checkPolicy(kd, challenge, p)
	if err != nil {
		return Result{}, err
	}
	res.PublicKey = key
	return res, nil
}

func parseChain(chain [][]byte) ([]*x509.Certificate, error) {
	certs := make([]*x509.Certificate, len(chain))
	for i, der := range chain {
		c, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, plxerr.Wrap(plxerr.AttestationFailed, err, "certificate %d does not parse", i)
		}
		certs[i] = c
	}
	return certs, nil
}

// verifyChain builds the chain to a trusted root at the verifier's time and
// rejects every listed serial number.
func (v *Verifier) verifyChain(certs []*x509.Certificate) error {
	intermediates := x509.NewCertPool()
	for _, c := range certs[1:] {
		intermediates.AddCert(c)
	}
	_, err := certs[0].Verify(x509.VerifyOptions{
		Roots:         v.Roots,
		Intermediates: intermediates,
		CurrentTime:   v.Now(),
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	})
	if err != nil {
		return plxerr.Wrap(plxerr.AttestationFailed, err, "certificate chain does not lead to a trusted root")
	}
	for i, c := range certs {
		if status, listed := v.Revocations.status(c.SerialNumber); listed {
			return plxerr.New(plxerr.AttestationFailed, "certificate %d is on the revocation list (%s)", i, status)
		}
	}
	return nil
}

func leafKey(leaf *x509.Certificate) (*ecdsa.PublicKey, error) {
	key, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok || key.Curve != elliptic.P256() {
		return nil, plxerr.New(plxerr.AttestationFailed, "attested key is not an ECDSA P-256 key")
	}
	return key, nil
}

// keyDescriptionOf returns the key description of the leaf, after checking
// that no other certificate carries the extension.
func keyDescriptionOf(certs []*x509.Certificate) (*keyDescription, error) {
	for i, c := range certs[1:] {
		if _, found := extensionValue(c); found {
			return nil, plxerr.New(plxerr.AttestationFailed, "certificate %d carries the attestation extension but is not the leaf", i+1)
		}
	}
	der, found := extensionValue(certs[0])
	if !found {
		return nil, plxerr.New(plxerr.AttestationFailed, "leaf has no attestation extension")
	}
	return parseKeyDescription(der)
}

// extensionValue returns the attestation extension value of a certificate.
func extensionValue(c *x509.Certificate) ([]byte, bool) {
	for _, e := range c.Extensions {
		if e.Id.Equal(attestationOID) {
			return e.Value, true
		}
	}
	return nil, false
}

// checkPolicy applies the challenge and the policy to a key description.
func checkPolicy(kd *keyDescription, challenge []byte, p Policy) (Result, error) {
	if len(challenge) == 0 || subtle.ConstantTimeCompare(kd.challenge, challenge) != 1 {
		return Result{}, plxerr.New(plxerr.AttestationFailed, "attestation challenge does not match")
	}
	res := Result{AttestationLevel: kd.attestationLevel, KeyMintLevel: kd.keyMintLevel}
	if p.RequireHardware && !res.HardwareBacked() {
		return Result{}, plxerr.New(plxerr.KeyNotHardwareBacked, "key is not protected by a TEE or StrongBox")
	}
	if err := applyBootState(&res, kd.hardware.rootOfTrust, p.RequireVerifiedBoot); err != nil {
		return Result{}, err
	}
	if err := applyApplication(&res, kd, p); err != nil {
		return Result{}, err
	}
	if kd.hardware.osPatchLevel != nil {
		res.OSPatchLevel = int(*kd.hardware.osPatchLevel)
	}
	return res, nil
}

func applyBootState(res *Result, rot *rootOfTrust, require bool) error {
	if rot != nil {
		res.VerifiedBootState = rot.state
		res.DeviceLocked = rot.deviceLocked
	}
	if require && (rot == nil || rot.state != BootVerified || !rot.deviceLocked) {
		return plxerr.New(plxerr.AttestationFailed, "device is not locked with a verified boot")
	}
	return nil
}

func applyApplication(res *Result, kd *keyDescription, p Policy) error {
	app := kd.software.appID
	if app == nil {
		app = kd.hardware.appID
	}
	if app == nil {
		return plxerr.New(plxerr.AttestationFailed, "attestation carries no application identity")
	}
	pkg := ""
	for _, name := range app.packages {
		if slices.Contains(p.PackageNames, name) {
			pkg = name
			break
		}
	}
	if pkg == "" {
		return plxerr.New(plxerr.AttestationFailed, "attested package is not allowed")
	}
	if len(p.SigningCertDigests) > 0 {
		if err := checkDigests(app.digests, p.SigningCertDigests); err != nil {
			return err
		}
	}
	res.PackageName = pkg
	res.SigningCertDigests = app.digests
	return nil
}

func checkDigests(attested, allowed [][]byte) error {
	if len(attested) == 0 {
		return plxerr.New(plxerr.AttestationFailed, "attestation carries no signing certificate digest")
	}
	for _, d := range attested {
		if !slices.ContainsFunc(allowed, func(a []byte) bool { return bytes.Equal(a, d) }) {
			return plxerr.New(plxerr.AttestationFailed, "signing certificate digest is not allowed")
		}
	}
	return nil
}
