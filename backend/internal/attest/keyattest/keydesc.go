// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package keyattest

import (
	"golang.org/x/crypto/cryptobyte"
	cbasn1 "golang.org/x/crypto/cryptobyte/asn1"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// SecurityLevel is where a key, or its attestation, is protected.
type SecurityLevel int

// The security levels of the Android KeyMint SecurityLevel enumeration. The
// zero value is the weakest.
const (
	// SecuritySoftware means the Android operating system itself.
	SecuritySoftware SecurityLevel = 0
	// SecurityTrustedEnvironment means a trusted execution environment.
	SecurityTrustedEnvironment SecurityLevel = 1
	// SecurityStrongBox means a dedicated secure element.
	SecurityStrongBox SecurityLevel = 2
)

// BootState is the verified boot state reported in the root of trust. The
// zero value means the key description reported none.
type BootState int

// The verified boot states of the Android VerifiedBootState enumeration.
const (
	// BootUnknown means the hardware-enforced list carried no root of trust.
	BootUnknown BootState = iota
	// BootVerified means the boot chain is fully verified.
	BootVerified
	// BootSelfSigned means the device boots an image signed with a user key.
	BootSelfSigned
	// BootUnverified means the device boots an unverified image.
	BootUnverified
	// BootFailed means verification failed.
	BootFailed
)

// Authorisation list tags (EXPLICIT, context-specific).
const (
	tagRootOfTrust  = 704
	tagOSPatchLevel = 706
	tagAppID        = 709
)

const maxBootStateEnum = 3

// rootOfTrust is the part of the RootOfTrust sequence the policy needs.
type rootOfTrust struct {
	deviceLocked bool
	state        BootState
}

// appID is the decoded attestationApplicationId.
type appID struct {
	packages []string
	digests  [][]byte
}

// authList is the subset of an AuthorizationList that is read; every other
// tag is skipped.
type authList struct {
	rootOfTrust  *rootOfTrust
	osPatchLevel *int64
	appID        *appID
}

// keyDescription is the decoded Android KeyDescription extension.
type keyDescription struct {
	attestationVersion int64
	attestationLevel   SecurityLevel
	keyMintVersion     int64
	keyMintLevel       SecurityLevel
	challenge          []byte
	uniqueID           []byte
	software           authList
	hardware           authList
}

func malformed(what string) error {
	return plxerr.New(plxerr.AttestationFailed, "key description: malformed %s", what)
}

// parseKeyDescription decodes the value of the key attestation extension.
func parseKeyDescription(der []byte) (*keyDescription, error) {
	in := cryptobyte.String(der)
	var seq cryptobyte.String
	if !in.ReadASN1(&seq, cbasn1.SEQUENCE) || !in.Empty() {
		return nil, malformed("not a single sequence")
	}
	kd := &keyDescription{}
	var attLevel, kmLevel int
	var swList, hwList cryptobyte.String
	var challenge, uniqueID cryptobyte.String
	switch {
	case !seq.ReadASN1Integer(&kd.attestationVersion):
		return nil, malformed("attestationVersion")
	case !seq.ReadASN1Enum(&attLevel):
		return nil, malformed("attestationSecurityLevel")
	case !seq.ReadASN1Integer(&kd.keyMintVersion):
		return nil, malformed("keyMintVersion")
	case !seq.ReadASN1Enum(&kmLevel):
		return nil, malformed("keyMintSecurityLevel")
	case !seq.ReadASN1(&challenge, cbasn1.OCTET_STRING):
		return nil, malformed("attestationChallenge")
	case !seq.ReadASN1(&uniqueID, cbasn1.OCTET_STRING):
		return nil, malformed("uniqueId")
	case !seq.ReadASN1(&swList, cbasn1.SEQUENCE):
		return nil, malformed("softwareEnforced")
	case !seq.ReadASN1(&hwList, cbasn1.SEQUENCE):
		return nil, malformed("hardwareEnforced")
	}
	var err error
	if kd.attestationLevel, err = securityLevel(attLevel); err != nil {
		return nil, err
	}
	if kd.keyMintLevel, err = securityLevel(kmLevel); err != nil {
		return nil, err
	}
	kd.challenge = append([]byte(nil), challenge...)
	kd.uniqueID = append([]byte(nil), uniqueID...)
	if kd.software, err = parseAuthList(swList); err != nil {
		return nil, err
	}
	if kd.hardware, err = parseAuthList(hwList); err != nil {
		return nil, err
	}
	return kd, nil
}

func securityLevel(v int) (SecurityLevel, error) {
	switch SecurityLevel(v) {
	case SecuritySoftware, SecurityTrustedEnvironment, SecurityStrongBox:
		return SecurityLevel(v), nil
	}
	return SecuritySoftware, malformed("unknown security level")
}

// parseAuthList reads the tags the policy needs from an AuthorizationList
// and skips all others. A tag of interest that occurs twice is rejected.
func parseAuthList(list []byte) (authList, error) {
	var l authList
	for len(list) > 0 {
		t, rest, err := readTLV(list)
		if err != nil {
			return authList{}, malformed("authorisation list")
		}
		list = rest
		if !t.isContextConstructed() {
			continue
		}
		if err := l.readTag(t); err != nil {
			return authList{}, err
		}
	}
	return l, nil
}

// readTag stores the element if its tag is one of interest.
func (l *authList) readTag(t tlv) error {
	switch t.number {
	case tagRootOfTrust:
		if l.rootOfTrust != nil {
			return malformed("duplicate rootOfTrust")
		}
		r, err := parseRootOfTrust(t.content)
		l.rootOfTrust = r
		return err
	case tagOSPatchLevel:
		if l.osPatchLevel != nil {
			return malformed("duplicate osPatchLevel")
		}
		v, err := parsePatchLevel(t.content)
		l.osPatchLevel = &v
		return err
	case tagAppID:
		if l.appID != nil {
			return malformed("duplicate attestationApplicationId")
		}
		a, err := parseAppID(t.content)
		l.appID = a
		return err
	}
	return nil
}

// explicit unwraps the single element of an EXPLICIT tag's content.
func explicit(content []byte, tag cbasn1.Tag) (cryptobyte.String, bool) {
	in := cryptobyte.String(content)
	var out cryptobyte.String
	if !in.ReadASN1(&out, tag) || !in.Empty() {
		return nil, false
	}
	return out, true
}

func parseRootOfTrust(content []byte) (*rootOfTrust, error) {
	seq, ok := explicit(content, cbasn1.SEQUENCE)
	if !ok {
		return nil, malformed("rootOfTrust")
	}
	var key cryptobyte.String
	var locked bool
	var state int
	switch {
	case !seq.ReadASN1(&key, cbasn1.OCTET_STRING):
		return nil, malformed("verifiedBootKey")
	case !seq.ReadASN1Boolean(&locked):
		return nil, malformed("deviceLocked")
	case !seq.ReadASN1Enum(&state) || state < 0 || state > maxBootStateEnum:
		return nil, malformed("verifiedBootState")
	}
	if !seq.Empty() {
		var hash cryptobyte.String
		if !seq.ReadASN1(&hash, cbasn1.OCTET_STRING) {
			return nil, malformed("verifiedBootHash")
		}
	}
	return &rootOfTrust{deviceLocked: locked, state: BootState(state) + BootVerified}, nil
}

func parsePatchLevel(content []byte) (int64, error) {
	in := cryptobyte.String(content)
	var v int64
	var elem cryptobyte.String
	if !in.ReadASN1Element(&elem, cbasn1.INTEGER) || !in.Empty() || !elem.ReadASN1Integer(&v) || v < 0 {
		return 0, malformed("osPatchLevel")
	}
	return v, nil
}

// parseAppID decodes the attestationApplicationId OCTET STRING and the DER
// structure it wraps.
func parseAppID(content []byte) (*appID, error) {
	octets, ok := explicit(content, cbasn1.OCTET_STRING)
	if !ok {
		return nil, malformed("attestationApplicationId")
	}
	in := octets
	var seq, pkgs, digests cryptobyte.String
	if !in.ReadASN1(&seq, cbasn1.SEQUENCE) || !in.Empty() ||
		!seq.ReadASN1(&pkgs, cbasn1.SET) || !seq.ReadASN1(&digests, cbasn1.SET) {
		return nil, malformed("attestationApplicationId content")
	}
	a := &appID{}
	for !pkgs.Empty() {
		var info, name cryptobyte.String
		var version int64
		if !pkgs.ReadASN1(&info, cbasn1.SEQUENCE) || !info.ReadASN1(&name, cbasn1.OCTET_STRING) ||
			!info.ReadASN1Integer(&version) {
			return nil, malformed("package info")
		}
		a.packages = append(a.packages, string(name))
	}
	for !digests.Empty() {
		var d cryptobyte.String
		if !digests.ReadASN1(&d, cbasn1.OCTET_STRING) {
			return nil, malformed("signature digest")
		}
		a.digests = append(a.digests, append([]byte(nil), d...))
	}
	return a, nil
}
