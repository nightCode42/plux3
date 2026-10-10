// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package updatemeta

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"time"

	"github.com/nightCode42/plux3/backend/internal/schema/jcs"
)

// depth bounds the nesting of a document; the deepest is the root's.
const depth = 8

// Signature is one signature over a document's signed part
// (schema/update/common.schema.json).
type Signature struct {
	KeyID string `json:"keyid"`
	Alg   string `json:"alg"`
	Sig   string `json:"sig"`
}

// Document is a metadata file: the signed part and its signatures. Signed
// holds the canonical JSON the signatures cover.
type Document struct {
	Signed     json.RawMessage `json:"signed"`
	Signatures []Signature     `json:"signatures"`
}

// Key is a public key a root lists.
type Key struct {
	Alg string `json:"alg"`
	// Public is the key in standard base64: 32 raw bytes for Ed25519, the
	// 65-byte uncompressed point for ES256.
	Public string `json:"public"`
}

// RoleKeys names the keys that may sign a role and how many must.
type RoleKeys struct {
	KeyIDs    []string `json:"keyids"`
	Threshold int      `json:"threshold"`
}

// Roles holds every role's keys.
type Roles struct {
	Root      RoleKeys `json:"root"`
	Targets   RoleKeys `json:"targets"`
	Snapshot  RoleKeys `json:"snapshot"`
	Timestamp RoleKeys `json:"timestamp"`
}

// Named returns the keys of a role by its name.
func (r Roles) Named(role string) (RoleKeys, bool) {
	switch role {
	case RoleRoot:
		return r.Root, true
	case RoleTargets:
		return r.Targets, true
	case RoleSnapshot:
		return r.Snapshot, true
	case RoleTimestamp:
		return r.Timestamp, true
	}
	return RoleKeys{}, false
}

// Root is the signed part of root metadata: the keys and thresholds of
// every role.
type Root struct {
	Type        string         `json:"_type"`
	Version     int64          `json:"version"`
	Expires     string         `json:"expires"`
	SpecVersion string         `json:"specVersion"`
	Keys        map[string]Key `json:"keys"`
	Roles       Roles          `json:"roles"`
	// Pins are the certificate pins of the servers an app talks to, per
	// host name: at least two SPKI SHA-256 pins each, in the base64 of
	// RFC 7469 (SEC-041). They sit in the root so that only the offline
	// root keys can change them.
	Pins map[string][]string `json:"pins,omitempty"`
}

// Bounds of the pins a root carries; schema/update/root.schema.json
// says the same.
const (
	// MinPins is the fewest pins of a host: the key in use and a backup.
	MinPins = 2
	// MaxPins is the most pins of a host.
	MaxPins = 8
	// MaxPinHosts is the most hosts a root pins.
	MaxPinHosts = 16
)

// VersionRef names a version of another document.
type VersionRef struct {
	Version int64 `json:"version"`
}

// HashRef names a version of another document and its hash.
type HashRef struct {
	Version int64  `json:"version"`
	SHA256  string `json:"sha256"`
}

// SnapshotMeta is what a snapshot pins.
type SnapshotMeta struct {
	Targets VersionRef `json:"targets.json"`
}

// Snapshot is the signed part of snapshot metadata: the version of the
// targets.
type Snapshot struct {
	Type        string       `json:"_type"`
	Version     int64        `json:"version"`
	Expires     string       `json:"expires"`
	SpecVersion string       `json:"specVersion"`
	Meta        SnapshotMeta `json:"meta"`
}

// TimestampMeta is what a timestamp points to.
type TimestampMeta struct {
	Snapshot HashRef `json:"snapshot.json"`
}

// Timestamp is the signed part of timestamp metadata: the newest
// snapshot and its hash.
type Timestamp struct {
	Type        string        `json:"_type"`
	Version     int64         `json:"version"`
	Expires     string        `json:"expires"`
	SpecVersion string        `json:"specVersion"`
	Meta        TimestampMeta `json:"meta"`
}

// Targets is what a verifier reads of a manifest, the targets role: its
// version, which the snapshot pins, its role and its expiry.
type Targets struct {
	Role    string `json:"role"`
	Version int64  `json:"version"`
	Expires string `json:"expires"`
}

// NewSnapshot builds a snapshot that pins a targets version.
func NewSnapshot(version, targets int64, expires time.Time) Snapshot {
	return Snapshot{
		Type: RoleSnapshot, Version: version, Expires: FormatTime(expires), SpecVersion: SpecVersion,
		Meta: SnapshotMeta{Targets: VersionRef{Version: targets}},
	}
}

// NewTimestamp builds a timestamp that names a snapshot document, whose
// exact bytes it hashes.
func NewTimestamp(version, snapshot int64, snapshotDocument []byte, expires time.Time) Timestamp {
	sum := sha256.Sum256(snapshotDocument)
	return Timestamp{
		Type: RoleTimestamp, Version: version, Expires: FormatTime(expires), SpecVersion: SpecVersion,
		Meta: TimestampMeta{Snapshot: HashRef{Version: snapshot, SHA256: hex.EncodeToString(sum[:])}},
	}
}

// Canonical returns the RFC 8785 canonical JSON of a signed part: the
// bytes every signature covers.
func Canonical(signed any) ([]byte, error) {
	plain, err := json.Marshal(signed)
	if err != nil {
		return nil, fmt.Errorf("updatemeta: %w", err)
	}
	out, err := jcs.Canonicalize(plain, depth)
	if err != nil {
		return nil, fmt.Errorf("updatemeta: %w", err)
	}
	return out, nil
}

// Marshal returns the file for a signed part and its signatures, in
// canonical form, so that the same content always has the same bytes and
// hash. Signatures are ordered by key identifier.
func Marshal(signed any, signatures []Signature) ([]byte, error) {
	canonical, err := Canonical(signed)
	if err != nil {
		return nil, err
	}
	sigs := slices.Clone(signatures)
	if sigs == nil {
		sigs = []Signature{}
	}
	slices.SortFunc(sigs, func(a, b Signature) int {
		if a.KeyID != b.KeyID {
			return cmpString(a.KeyID, b.KeyID)
		}
		return cmpString(a.Alg, b.Alg)
	})
	plain, err := json.Marshal(Document{Signed: canonical, Signatures: sigs})
	if err != nil {
		return nil, fmt.Errorf("updatemeta: %w", err)
	}
	out, err := jcs.Canonicalize(plain, depth+1)
	if err != nil {
		return nil, fmt.Errorf("updatemeta: %w", err)
	}
	return out, nil
}

func cmpString(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// Bytes returns the file in canonical form, the signed part unchanged.
func (d Document) Bytes() ([]byte, error) { return Marshal(d.Signed, d.Signatures) }

// ParseDocument reads a metadata file. The signed part is returned in
// canonical form, which is what the signatures cover whatever the file's
// own formatting.
func ParseDocument(raw []byte) (Document, error) {
	var d Document
	if err := decodeStrict(raw, &d); err != nil {
		return Document{}, err
	}
	if len(d.Signed) == 0 {
		return Document{}, fmt.Errorf("%w: no signed part", ErrFormat)
	}
	canonical, err := jcs.Canonicalize(d.Signed, depth)
	if err != nil {
		return Document{}, fmt.Errorf("%w: %w", ErrFormat, err)
	}
	d.Signed = canonical
	for _, s := range d.Signatures {
		if !keyIDPattern.MatchString(s.KeyID) || s.Alg == "" || !base64Pattern.MatchString(s.Sig) {
			return Document{}, fmt.Errorf("%w: a signature is malformed", ErrFormat)
		}
	}
	return d, nil
}

// decodeStrict decodes JSON, refusing fields the type does not have: the
// schemas close every object.
func decodeStrict(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: %w", ErrFormat, err)
	}
	if dec.More() {
		return fmt.Errorf("%w: trailing data", ErrFormat)
	}
	return nil
}

var (
	keyIDPattern   = regexp.MustCompile(`^[0-9a-f]{32}$`)
	base64Pattern  = regexp.MustCompile(`^[A-Za-z0-9+/]+={0,2}$`)
	specPattern    = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	sha256Pattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	algNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$`)
	hostPattern    = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)
)

// header checks the properties every signed part carries.
func header(role, typ string, version int64, expires, spec string) error {
	switch {
	case typ != role:
		return fmt.Errorf("%w: _type is %q, want %q", ErrFormat, typ, role)
	case version < 1:
		return fmt.Errorf("%w: version %d", ErrFormat, version)
	case !specPattern.MatchString(spec):
		return fmt.Errorf("%w: specVersion %q", ErrFormat, spec)
	}
	if _, err := time.Parse(time.RFC3339, expires); err != nil {
		return fmt.Errorf("%w: expires: %w", ErrFormat, err)
	}
	return nil
}

// ParseRoot reads and checks the signed part of a root.
func ParseRoot(signed []byte) (Root, error) {
	var r Root
	if err := decodeStrict(signed, &r); err != nil {
		return Root{}, err
	}
	if err := header(RoleRoot, r.Type, r.Version, r.Expires, r.SpecVersion); err != nil {
		return Root{}, err
	}
	for id, k := range r.Keys {
		pub, err := base64.StdEncoding.DecodeString(k.Public)
		switch {
		case !keyIDPattern.MatchString(id) || !algNamePattern.MatchString(k.Alg) || err != nil || len(pub) == 0:
			return Root{}, fmt.Errorf("%w: key %q is malformed", ErrFormat, id)
		case Supported(k.Alg) && KeyID(pub) != id:
			return Root{}, fmt.Errorf("%w: key %q is not the hash of its public key", ErrFormat, id)
		}
	}
	for _, role := range []string{RoleRoot, RoleTargets, RoleSnapshot, RoleTimestamp} {
		keys, _ := r.Roles.Named(role)
		if err := r.checkRole(role, keys); err != nil {
			return Root{}, err
		}
	}
	if err := r.checkPins(); err != nil {
		return Root{}, err
	}
	return r, nil
}

// checkPins checks the pins of a root: a host name each, and for each
// between MinPins and MaxPins distinct, canonical base64 SHA-256 values.
func (r Root) checkPins() error {
	if len(r.Pins) > MaxPinHosts {
		return fmt.Errorf("%w: %d pinned hosts, at most %d", ErrFormat, len(r.Pins), MaxPinHosts)
	}
	for host, pins := range r.Pins {
		if len(host) > 253 || !hostPattern.MatchString(host) {
			return fmt.Errorf("%w: %q is not a host name to pin", ErrFormat, host)
		}
		if len(pins) < MinPins || len(pins) > MaxPins {
			return fmt.Errorf("%w: host %s has %d pins, between %d and %d are needed", ErrFormat, host, len(pins), MinPins, MaxPins)
		}
		seen := map[string]bool{}
		for _, p := range pins {
			raw, err := base64.StdEncoding.DecodeString(p)
			if err != nil || len(raw) != sha256.Size || base64.StdEncoding.EncodeToString(raw) != p {
				return fmt.Errorf("%w: host %s has a pin that is not the base64 of a SHA-256 hash", ErrFormat, host)
			}
			if seen[p] {
				return fmt.Errorf("%w: host %s repeats a pin", ErrFormat, host)
			}
			seen[p] = true
		}
	}
	return nil
}

// checkRole checks a role's keys against the root's key list: every key
// listed, unique, and enough of them able to meet the threshold with an
// algorithm this implementation can verify.
func (r Root) checkRole(role string, keys RoleKeys) error {
	usable := 0
	seen := map[string]bool{}
	for _, id := range keys.KeyIDs {
		k, ok := r.Keys[id]
		switch {
		case !ok:
			return fmt.Errorf("%w: role %s names key %s, which the root does not list", ErrFormat, role, id)
		case seen[id]:
			return fmt.Errorf("%w: role %s names key %s twice", ErrFormat, role, id)
		}
		seen[id] = true
		if Supported(k.Alg) {
			usable++
		}
	}
	if keys.Threshold < 1 || keys.Threshold > usable {
		return fmt.Errorf("%w: role %s has threshold %d with %d usable keys", ErrFormat, role, keys.Threshold, usable)
	}
	return nil
}

// ParseTimestamp reads and checks the signed part of a timestamp.
func ParseTimestamp(signed []byte) (Timestamp, error) {
	var t Timestamp
	if err := decodeStrict(signed, &t); err != nil {
		return Timestamp{}, err
	}
	if err := header(RoleTimestamp, t.Type, t.Version, t.Expires, t.SpecVersion); err != nil {
		return Timestamp{}, err
	}
	if m := t.Meta.Snapshot; m.Version < 1 || !sha256Pattern.MatchString(m.SHA256) {
		return Timestamp{}, fmt.Errorf("%w: the snapshot reference is malformed", ErrFormat)
	}
	return t, nil
}

// ParseSnapshot reads and checks the signed part of a snapshot.
func ParseSnapshot(signed []byte) (Snapshot, error) {
	var s Snapshot
	if err := decodeStrict(signed, &s); err != nil {
		return Snapshot{}, err
	}
	if err := header(RoleSnapshot, s.Type, s.Version, s.Expires, s.SpecVersion); err != nil {
		return Snapshot{}, err
	}
	if s.Meta.Targets.Version < 1 {
		return Snapshot{}, fmt.Errorf("%w: the targets version is malformed", ErrFormat)
	}
	return s, nil
}

// ParseTargets reads the targets fields of a signed manifest. The
// manifest has many other fields, which are not this package's business.
func ParseTargets(signed []byte) (Targets, error) {
	var t Targets
	if err := json.Unmarshal(signed, &t); err != nil {
		return Targets{}, fmt.Errorf("%w: %w", ErrFormat, err)
	}
	if t.Role != RoleTargets {
		return Targets{}, fmt.Errorf("%w: the manifest's role is %q", ErrFormat, t.Role)
	}
	if _, err := time.Parse(time.RFC3339, t.Expires); err != nil {
		return Targets{}, fmt.Errorf("%w: expires: %w", ErrFormat, err)
	}
	return t, nil
}
