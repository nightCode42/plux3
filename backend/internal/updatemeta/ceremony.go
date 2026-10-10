// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package updatemeta

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"encoding/base64"
	"fmt"
	"slices"
	"time"
)

// RoleSpec names the keys of one role and how many must sign.
type RoleSpec struct {
	Keys      []Key
	Threshold int
}

// RootSpec is what an offline root ceremony decides (SEC-051): the
// version, the expiry and the keys of every role. The root role's keys
// are the offline holders'; the other roles' are the online keys the
// worker signs with.
type RootSpec struct {
	Version                            int64
	Expires                            time.Time
	Root, Targets, Snapshot, Timestamp RoleSpec
	// Pins are the certificate pins the root carries, per host: at least
	// two each (SEC-041). Nil pins nothing.
	Pins map[string][]string
}

// NewRoot builds the unsigned root a spec describes. Key identifiers are
// derived from the public keys, listed once and sorted, so the same spec
// always yields the same document (CMP-002). The result passes the
// checks ParseRoot makes of any root.
func NewRoot(spec RootSpec) (Root, error) {
	r := Root{
		Type: RoleRoot, Version: spec.Version, Expires: FormatTime(spec.Expires), SpecVersion: SpecVersion,
		Keys: map[string]Key{}, Pins: spec.Pins,
	}
	for _, role := range []struct {
		name string
		spec RoleSpec
		dst  *RoleKeys
	}{
		{RoleRoot, spec.Root, &r.Roles.Root},
		{RoleTargets, spec.Targets, &r.Roles.Targets},
		{RoleSnapshot, spec.Snapshot, &r.Roles.Snapshot},
		{RoleTimestamp, spec.Timestamp, &r.Roles.Timestamp},
	} {
		ids, err := r.addKeys(role.spec.Keys)
		if err != nil {
			return Root{}, fmt.Errorf("role %s: %w", role.name, err)
		}
		*role.dst = RoleKeys{KeyIDs: ids, Threshold: role.spec.Threshold}
	}
	signed, err := Canonical(r)
	if err != nil {
		return Root{}, err
	}
	if _, err := ParseRoot(signed); err != nil {
		return Root{}, err
	}
	return r, nil
}

// addKeys lists keys in the root and returns their sorted identifiers. A
// key given twice for one role is an error: it would count once towards
// a threshold that its holder thinks it counted for twice.
func (r Root) addKeys(keys []Key) ([]string, error) {
	ids := make([]string, 0, len(keys))
	for _, k := range keys {
		k.Alg = CanonicalAlg(k.Alg)
		pub, err := base64.StdEncoding.DecodeString(k.Public)
		if err != nil || !validPublic(k.Alg, pub) {
			return nil, fmt.Errorf("%w: a %s public key is malformed", ErrFormat, k.Alg)
		}
		id := KeyID(pub)
		if slices.Contains(ids, id) {
			return nil, fmt.Errorf("%w: key %s is listed twice", ErrFormat, id)
		}
		ids = append(ids, id)
		r.Keys[id] = k
	}
	slices.Sort(ids)
	return ids, nil
}

// validPublic reports whether pub has the shape of a public key of alg.
func validPublic(alg string, pub []byte) bool {
	switch alg {
	case AlgEd25519:
		return len(pub) == ed25519.PublicKeySize
	case AlgES256:
		_, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), pub)
		return err == nil
	}
	return false
}

// AddSignature returns the file with one more signature, in canonical
// form. A signature already there by the same key is replaced, so a
// holder who signs twice is counted once.
func AddSignature(raw []byte, sig Signature) ([]byte, error) {
	d, err := ParseDocument(raw)
	if err != nil {
		return nil, err
	}
	sigs := make([]Signature, 0, len(d.Signatures)+1)
	for _, s := range d.Signatures {
		if s.KeyID != sig.KeyID {
			sigs = append(sigs, s)
		}
	}
	return Marshal(d.Signed, append(sigs, sig))
}

// RootReport is what CheckRoot found in a root that passed.
type RootReport struct {
	Root Root
	// Signers are the root keys that signed validly under the root's own
	// keys; Previous are those under the previous root's, set only when a
	// previous root was given. Both are sorted.
	Signers, Previous []string
}

// CheckRoot verifies a root file at an instant without any server state.
// The root must be signed by its own root threshold and unexpired. Given
// the file of the root it replaces, it must also be that root's
// successor and signed by the previous root's threshold (SEC-051). The
// previous root's own signatures are checked, its expiry is not: a
// rotation may follow a lapse.
func CheckRoot(raw, previous []byte, now time.Time) (RootReport, error) {
	var rep RootReport
	d, err := ParseDocument(raw)
	if err != nil {
		return RootReport{}, err
	}
	if previous != nil {
		if rep, err = checkRotation(raw, previous); err != nil {
			return RootReport{}, err
		}
	} else if rep.Root, err = ParseRoot(d.Signed); err != nil {
		return RootReport{}, err
	}
	if rep.Signers, err = validSigners(d.Signed, d.Signatures, rep.Root.Keys, rep.Root.Roles.Root); err != nil {
		return RootReport{}, fmt.Errorf("root %d: %w", rep.Root.Version, err)
	}
	exp, err := time.Parse(time.RFC3339, rep.Root.Expires)
	if err != nil {
		return RootReport{}, fmt.Errorf("%w: expires: %w", ErrFormat, err)
	}
	if !now.Before(exp) {
		return RootReport{}, fmt.Errorf("root %d: expired at %s: %w", rep.Root.Version, rep.Root.Expires, ErrExpired)
	}
	return rep, nil
}

// checkRotation verifies raw as the successor of previous and reports
// the signers under the previous root's keys.
func checkRotation(raw, previous []byte) (RootReport, error) {
	prev, err := checkedPrevious(previous)
	if err != nil {
		return RootReport{}, err
	}
	root, d, err := NextRoot(prev, raw)
	if err != nil {
		return RootReport{}, err
	}
	signers, err := validSigners(d.Signed, d.Signatures, prev.Keys, prev.Roles.Root)
	if err != nil {
		return RootReport{}, fmt.Errorf("root %d, previous keys: %w", root.Version, err)
	}
	return RootReport{Root: root, Previous: signers}, nil
}

// checkedPrevious parses the previous root and checks that its own
// threshold signed it.
func checkedPrevious(raw []byte) (Root, error) {
	d, err := ParseDocument(raw)
	if err != nil {
		return Root{}, fmt.Errorf("previous root: %w", err)
	}
	r, err := ParseRoot(d.Signed)
	if err != nil {
		return Root{}, fmt.Errorf("previous root: %w", err)
	}
	if _, err := validSigners(d.Signed, d.Signatures, r.Keys, r.Roles.Root); err != nil {
		return Root{}, fmt.Errorf("previous root %d: %w", r.Version, err)
	}
	return r, nil
}
