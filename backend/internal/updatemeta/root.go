// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package updatemeta

import (
	"fmt"
	"time"
)

// FirstRoot verifies the first root of an environment: it must be version
// 1 and signed by its own root threshold. It is trusted because an
// operator uploaded it after the offline ceremony, as an app embeds it at
// build time (SEC-051).
func FirstRoot(raw []byte) (Root, Document, error) {
	d, err := ParseDocument(raw)
	if err != nil {
		return Root{}, Document{}, err
	}
	r, err := ParseRoot(d.Signed)
	if err != nil {
		return Root{}, Document{}, err
	}
	if r.Version != 1 {
		return Root{}, Document{}, fmt.Errorf("%w: the first root has version %d, not 1", ErrRollback, r.Version)
	}
	if _, err := validSigners(d.Signed, d.Signatures, r.Keys, r.Roles.Root); err != nil {
		return Root{}, Document{}, fmt.Errorf("root: %w", err)
	}
	return r, d, nil
}

// NextRoot verifies a root that replaces prev: the next version, signed by
// prev's root threshold and by its own, so that neither a stolen old key
// nor a freshly made one can rotate alone (SEC-051).
func NextRoot(prev Root, raw []byte) (Root, Document, error) {
	d, err := ParseDocument(raw)
	if err != nil {
		return Root{}, Document{}, err
	}
	r, err := ParseRoot(d.Signed)
	if err != nil {
		return Root{}, Document{}, err
	}
	if r.Version != prev.Version+1 {
		return Root{}, Document{}, fmt.Errorf("%w: root version %d follows %d", ErrRollback, r.Version, prev.Version)
	}
	if _, err := validSigners(d.Signed, d.Signatures, prev.Keys, prev.Roles.Root); err != nil {
		return Root{}, Document{}, fmt.Errorf("root %d, previous keys: %w", r.Version, err)
	}
	if _, err := validSigners(d.Signed, d.Signatures, r.Keys, r.Roles.Root); err != nil {
		return Root{}, Document{}, fmt.Errorf("root %d, new keys: %w", r.Version, err)
	}
	return r, d, nil
}

// RootChain follows a chain of root files from a trusted root, in order,
// and returns the last. Intermediate roots may have expired, as the
// device was offline while they were current; the last must not have.
func RootChain(trusted Root, chain [][]byte, now time.Time) (Root, error) {
	cur := trusted
	for _, raw := range chain {
		next, _, err := NextRoot(cur, raw)
		if err != nil {
			return Root{}, err
		}
		cur = next
	}
	exp, err := time.Parse(time.RFC3339, cur.Expires)
	if err != nil {
		return Root{}, fmt.Errorf("%w: expires: %w", ErrFormat, err)
	}
	if !now.Before(exp) {
		return Root{}, fmt.Errorf("root %d: expired at %s: %w", cur.Version, cur.Expires, ErrExpired)
	}
	return cur, nil
}
