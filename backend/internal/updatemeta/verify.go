// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package updatemeta

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
	"slices"
	"time"

	"github.com/nightCode42/plux3/backend/internal/schema/jcs"
)

// Verifier checks the metadata chain against a trusted root (SEC-050).
type Verifier struct {
	// Root is the trusted root.
	Root Root
	// Production makes the verifier refuse metadata signed by a key that
	// is not known to be a production key (SEC-056).
	Production bool
	// KeyEnvironments maps a key identifier to EnvProduction or
	// EnvDevelopment, as GetRootKeys reports it.
	KeyEnvironments map[string]string
	// Floor holds the versions already trusted; a document below its
	// role's floor is refused (anti-rollback).
	Floor Floor
	// Now is the verification time; the zero value is the present.
	Now time.Time
}

// Floor is the highest version trusted so far, per role.
type Floor struct {
	Timestamp, Snapshot, Targets int64
}

func (v Verifier) now() time.Time {
	if v.Now.IsZero() {
		return time.Now()
	}
	return v.Now
}

// Timestamp verifies a timestamp file: its signatures meet the root's
// timestamp threshold, it has not expired and it is not older than one
// already trusted.
func (v Verifier) Timestamp(raw []byte) (Timestamp, error) {
	d, err := ParseDocument(raw)
	if err != nil {
		return Timestamp{}, err
	}
	t, err := ParseTimestamp(d.Signed)
	if err != nil {
		return Timestamp{}, err
	}
	if err := v.checkRole(RoleTimestamp, d, t.Expires, t.Version, v.Floor.Timestamp); err != nil {
		return Timestamp{}, err
	}
	return t, nil
}

// Snapshot verifies a snapshot file against the timestamp that names it:
// the hash and version it announced, then the same checks as any role.
func (v Verifier) Snapshot(raw []byte, ts Timestamp) (Snapshot, error) {
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != ts.Meta.Snapshot.SHA256 {
		return Snapshot{}, fmt.Errorf("%w: the snapshot is not the one the timestamp hashes", ErrMismatch)
	}
	d, err := ParseDocument(raw)
	if err != nil {
		return Snapshot{}, err
	}
	s, err := ParseSnapshot(d.Signed)
	if err != nil {
		return Snapshot{}, err
	}
	if s.Version != ts.Meta.Snapshot.Version {
		return Snapshot{}, fmt.Errorf("%w: snapshot version %d, the timestamp names %d", ErrMismatch, s.Version, ts.Meta.Snapshot.Version)
	}
	if err := v.checkRole(RoleSnapshot, d, s.Expires, s.Version, v.Floor.Snapshot); err != nil {
		return Snapshot{}, err
	}
	return s, nil
}

// Targets verifies a manifest, the targets role, against the snapshot
// that pins its version. The signatures are the manifest's own.
func (v Verifier) Targets(signed []byte, signatures []Signature, snap Snapshot) (Targets, error) {
	t, err := ParseTargets(signed)
	if err != nil {
		return Targets{}, err
	}
	if t.Version != snap.Meta.Targets.Version {
		return Targets{}, fmt.Errorf("%w: manifest version %d, the snapshot pins %d", ErrMismatch, t.Version, snap.Meta.Targets.Version)
	}
	canonical, err := canonicalBytes(signed)
	if err != nil {
		return Targets{}, err
	}
	if err := v.check(RoleTargets, canonical, signatures, t.Expires, t.Version, v.Floor.Targets); err != nil {
		return Targets{}, err
	}
	return t, nil
}

// Chain verifies the whole chain, timestamp to manifest, and returns the
// versions that are now trusted.
func (v Verifier) Chain(timestamp, snapshot, manifest []byte, manifestSignatures []Signature) (Floor, error) {
	ts, err := v.Timestamp(timestamp)
	if err != nil {
		return Floor{}, fmt.Errorf("timestamp: %w", err)
	}
	snap, err := v.Snapshot(snapshot, ts)
	if err != nil {
		return Floor{}, fmt.Errorf("snapshot: %w", err)
	}
	t, err := v.Targets(manifest, manifestSignatures, snap)
	if err != nil {
		return Floor{}, fmt.Errorf("targets: %w", err)
	}
	return Floor{Timestamp: ts.Version, Snapshot: snap.Version, Targets: t.Version}, nil
}

func (v Verifier) checkRole(role string, d Document, expires string, version, floor int64) error {
	return v.check(role, d.Signed, d.Signatures, expires, version, floor)
}

// check applies the rules every role shares.
func (v Verifier) check(role string, signed []byte, sigs []Signature, expires string, version, floor int64) error {
	keys, _ := v.Root.Roles.Named(role)
	signers, err := validSigners(signed, sigs, v.Root.Keys, keys)
	if err != nil {
		return fmt.Errorf("%s: %w", role, err)
	}
	if v.Production {
		for _, id := range signers {
			if v.KeyEnvironments[id] != EnvProduction {
				return fmt.Errorf("%s: key %s: %w", role, id, ErrDevelopmentKey)
			}
		}
	}
	exp, err := time.Parse(time.RFC3339, expires)
	if err != nil {
		return fmt.Errorf("%w: expires: %w", ErrFormat, err)
	}
	if !v.now().Before(exp) {
		return fmt.Errorf("%s: expired at %s: %w", role, expires, ErrExpired)
	}
	if version < floor {
		return fmt.Errorf("%s: version %d is below the trusted %d: %w", role, version, floor, ErrRollback)
	}
	return nil
}

// validSigners returns the keys of a role that signed message validly,
// each counted once, and fails unless they reach the threshold. A
// signature by a key outside the role, with an algorithm other than its
// key's, or with one this implementation does not know, counts for
// nothing.
func validSigners(message []byte, sigs []Signature, listed map[string]Key, role RoleKeys) ([]string, error) {
	var signers []string
	for _, s := range sigs {
		if !slices.Contains(role.KeyIDs, s.KeyID) || slices.Contains(signers, s.KeyID) {
			continue
		}
		k := listed[s.KeyID]
		if CanonicalAlg(s.Alg) != CanonicalAlg(k.Alg) || !Supported(s.Alg) {
			continue
		}
		if verify(CanonicalAlg(k.Alg), k.Public, message, s.Sig) {
			signers = append(signers, s.KeyID)
		}
	}
	if len(signers) < role.Threshold {
		return nil, fmt.Errorf("%w: %d of the %d required", ErrThreshold, len(signers), role.Threshold)
	}
	slices.Sort(signers)
	return signers, nil
}

// verify checks one signature. An ES256 signature is the 64 bytes r||s
// of RFC 7518 and its key the 65-byte uncompressed point.
func verify(alg, public string, message []byte, signature string) bool {
	pub, err := base64.StdEncoding.DecodeString(public)
	if err != nil {
		return false
	}
	sig, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return false
	}
	switch alg {
	case AlgEd25519:
		return len(pub) == ed25519.PublicKeySize && ed25519.Verify(pub, message, sig)
	case AlgES256:
		key, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), pub)
		if err != nil || len(sig) != 64 {
			return false
		}
		der, err := asn1.Marshal(struct{ R, S *big.Int }{new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])})
		if err != nil {
			return false
		}
		digest := sha256.Sum256(message)
		return ecdsa.VerifyASN1(key, digest[:], der)
	}
	return false
}

// canonicalBytes returns the canonical form of a signed JSON part.
func canonicalBytes(signed []byte) ([]byte, error) {
	out, err := jcs.Canonicalize(signed, depth)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrFormat, err)
	}
	return out, nil
}
