// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

// Package updatemeta is the TUF-style update metadata of ADR-0004 and
// ADR-0054: the formats of the root, snapshot and timestamp roles
// (schema/update/), their signing through a [signing.Signer], and the
// checks a verifier makes. The targets role is the manifest of package
// release, which stamps the version this package pins.
//
// The chain a device follows is timestamp, which names the snapshot and
// its hash; snapshot, which names the targets version; targets, the
// manifest of that version. Each link must meet its role's signature
// threshold under the keys of the current root, be unexpired, and not go
// back in version (SEC-050). A new root is accepted only when the
// previous root's threshold and its own both signed it (SEC-051).
//
// The package signs only through the Signer interface and never holds
// key material (L-3). The verifier in this package is the server's own,
// used to validate an uploaded root and by tests; the runtime has its own.
package updatemeta

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// SpecVersion is the version of the metadata format these documents carry.
const SpecVersion = "1.0.0"

// The roles of the update metadata (SEC-050).
const (
	RoleRoot      = "root"
	RoleTargets   = "targets"
	RoleSnapshot  = "snapshot"
	RoleTimestamp = "timestamp"
)

// Algorithm identifiers (SEC-122). The names are those of
// schema/update/common.schema.json; the field is an open string so that a
// post-quantum algorithm needs no change of format, and a verifier
// refuses what it does not know. AlgMLDSA65 is reserved: it can be named
// in a root but nothing here signs or verifies with it.
const (
	AlgEd25519 = "Ed25519"
	AlgES256   = "ES256"
	AlgMLDSA65 = "ML-DSA-65"
)

// The environment types a key belongs to (SEC-056).
const (
	EnvProduction  = "production"
	EnvDevelopment = "development"
)

// Errors a verifier returns; each is wrapped with detail.
var (
	// ErrFormat means a document is malformed or breaks its schema.
	ErrFormat = errors.New("updatemeta: malformed document")
	// ErrThreshold means too few valid signatures from the role's keys.
	ErrThreshold = errors.New("updatemeta: signature threshold not met")
	// ErrExpired means a role's metadata is past its expiry (PLX-6022).
	ErrExpired = errors.New("updatemeta: metadata expired")
	// ErrRollback means a version below the one already trusted, or not
	// the successor a root update must be.
	ErrRollback = errors.New("updatemeta: version is not newer")
	// ErrMismatch means the chain does not hold together: a hash or a
	// version that the link above did not name.
	ErrMismatch = errors.New("updatemeta: chain mismatch")
	// ErrDevelopmentKey means production metadata signed by a key that is
	// not a production key (PLX-6021, SEC-056).
	ErrDevelopmentKey = errors.New("updatemeta: signed with a development key")
	// ErrAlgorithm means an algorithm this implementation does not know.
	ErrAlgorithm = errors.New("updatemeta: unsupported algorithm")
)

// Expiry is how long each role's metadata is valid (SEC-050).
type Expiry struct {
	Timestamp, Snapshot, Targets, Root time.Duration
}

// DefaultExpiry is the specification's: timestamp 24 hours, snapshot 7
// days, targets 30 days, root one year.
func DefaultExpiry() Expiry {
	const day = 24 * time.Hour
	return Expiry{Timestamp: day, Snapshot: 7 * day, Targets: 30 * day, Root: 365 * day}
}

// CanonicalAlg maps the spellings of an algorithm to the identifier of
// the schema, so that the manifest's historical "ed25519" and the
// "ecdsa-p256-sha256" form read as Ed25519 and ES256. An unknown name is
// returned unchanged.
func CanonicalAlg(name string) string {
	switch strings.ToLower(name) {
	case "ed25519":
		return AlgEd25519
	case "es256", "ecdsa-p256-sha256":
		return AlgES256
	case "ml-dsa-65":
		return AlgMLDSA65
	}
	return name
}

// Supported reports whether this implementation signs or verifies with an
// algorithm.
func Supported(alg string) bool {
	switch CanonicalAlg(alg) {
	case AlgEd25519, AlgES256:
		return true
	}
	return false
}

// KeyID is the identifier of a public key: the first sixteen bytes of its
// SHA-256 in lower-case hexadecimal, as package signing derives it.
func KeyID(public []byte) string {
	sum := sha256.Sum256(public)
	return hex.EncodeToString(sum[:16])
}

// FormatTime renders an instant as the documents carry it: UTC, whole
// seconds, RFC 3339.
func FormatTime(t time.Time) string { return t.UTC().Truncate(time.Second).Format(time.RFC3339) }
