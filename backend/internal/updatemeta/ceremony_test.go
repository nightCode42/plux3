// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package updatemeta_test

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/nightCode42/plux3/backend/internal/updatemeta"
)

// ceremonySpec is a root of three offline keys, two of which must sign,
// and one online key per other role.
func ceremonySpec(offline []key, online [3]key) updatemeta.RootSpec {
	var root []updatemeta.Key
	for _, k := range offline {
		root = append(root, k.listed())
	}
	one := func(k key) updatemeta.RoleSpec {
		return updatemeta.RoleSpec{Keys: []updatemeta.Key{k.listed()}, Threshold: 1}
	}
	return updatemeta.RootSpec{
		Version: 1, Expires: at.Add(365 * 24 * time.Hour),
		Root:    updatemeta.RoleSpec{Keys: root, Threshold: 2},
		Targets: one(online[0]), Snapshot: one(online[1]), Timestamp: one(online[2]),
	}
}

// signedBy returns the root's file with a signature from each key.
func signedBy(t *testing.T, root updatemeta.Root, keys ...key) []byte {
	t.Helper()
	canonical, err := updatemeta.Canonical(root)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := updatemeta.Marshal(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if raw, err = updatemeta.AddSignature(raw, k.signature(canonical)); err != nil {
			t.Fatal(err)
		}
	}
	return raw
}

// Verifies: SEC-051, CMP-002.
// A root is built from a spec deterministically, in whatever order the
// keys come, and a key listed twice is refused.
func TestNewRoot_SEC_051(t *testing.T) {
	a, b, c := newEd25519(t), newEd25519(t), newEd25519(t)
	online := [3]key{newEd25519(t), newEd25519(t), newEd25519(t)}
	r1, err := updatemeta.NewRoot(ceremonySpec([]key{a, b, c}, online))
	if err != nil {
		t.Fatal(err)
	}
	r2, err := updatemeta.NewRoot(ceremonySpec([]key{c, a, b}, online))
	if err != nil {
		t.Fatal(err)
	}
	d1, _ := updatemeta.Marshal(r1, nil)
	d2, _ := updatemeta.Marshal(r2, nil)
	if !bytes.Equal(d1, d2) || r1.Roles.Root.Threshold != 2 || len(r1.Roles.Root.KeyIDs) != 3 || len(r1.Keys) != 6 {
		t.Errorf("the same keys in another order gave %s and %s", d1, d2)
	}
	if _, err := updatemeta.NewRoot(ceremonySpec([]key{a, a, b}, online)); !errors.Is(err, updatemeta.ErrFormat) {
		t.Errorf("a key twice: %v", err)
	}
	if _, err := updatemeta.NewRoot(ceremonySpec([]key{a}, online)); !errors.Is(err, updatemeta.ErrFormat) {
		t.Errorf("a threshold of 2 with one key: %v", err)
	}
	bad := ceremonySpec([]key{a, b, c}, online)
	bad.Root.Keys[0].Public = "AAAA"
	if _, err := updatemeta.NewRoot(bad); !errors.Is(err, updatemeta.ErrFormat) {
		t.Errorf("a short key: %v", err)
	}
}

// Verifies: SEC-051.
// CheckRoot enforces the threshold, the expiry and, for a rotation, both
// the previous and the new threshold; a repeated signature counts once.
func TestCheckRoot_SEC_051(t *testing.T) {
	old := []key{newEd25519(t), newEd25519(t), newEd25519(t)}
	next := []key{newEd25519(t), newEd25519(t), newEd25519(t)}
	online := [3]key{newEd25519(t), newEd25519(t), newEd25519(t)}
	r1, err := updatemeta.NewRoot(ceremonySpec(old, online))
	if err != nil {
		t.Fatal(err)
	}
	spec2 := ceremonySpec(next, online)
	spec2.Version = 2
	r2, err := updatemeta.NewRoot(spec2)
	if err != nil {
		t.Fatal(err)
	}
	first := signedBy(t, r1, old[0], old[2])

	rep, err := updatemeta.CheckRoot(first, nil, at)
	if err != nil || len(rep.Signers) != 2 || rep.Previous != nil {
		t.Fatalf("root 1: %+v, %v", rep, err)
	}
	if _, err := updatemeta.CheckRoot(signedBy(t, r1, old[0]), nil, at); !errors.Is(err, updatemeta.ErrThreshold) {
		t.Errorf("one signature: %v", err)
	}
	if _, err := updatemeta.CheckRoot(signedBy(t, r1, old[0], old[0]), nil, at); !errors.Is(err, updatemeta.ErrThreshold) {
		t.Errorf("one holder twice: %v", err)
	}
	if _, err := updatemeta.CheckRoot(signedBy(t, r1, old[0], online[0]), nil, at); !errors.Is(err, updatemeta.ErrThreshold) {
		t.Errorf("an online key as a holder: %v", err)
	}
	if _, err := updatemeta.CheckRoot(first, nil, at.Add(366*24*time.Hour)); !errors.Is(err, updatemeta.ErrExpired) {
		t.Errorf("an expired root: %v", err)
	}

	both := signedBy(t, r2, old[0], old[1], next[0], next[2])
	rep, err = updatemeta.CheckRoot(both, first, at)
	if err != nil || len(rep.Signers) != 2 || len(rep.Previous) != 2 {
		t.Fatalf("rotation: %+v, %v", rep, err)
	}
	for name, doc := range map[string][]byte{
		"only the new keys": signedBy(t, r2, next[0], next[1]),
		"only the old keys": signedBy(t, r2, old[0], old[1]),
		"one old, two new":  signedBy(t, r2, old[0], next[0], next[1]),
	} {
		if _, err := updatemeta.CheckRoot(doc, first, at); !errors.Is(err, updatemeta.ErrThreshold) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := updatemeta.CheckRoot(both, signedBy(t, r1, old[0]), at); !errors.Is(err, updatemeta.ErrThreshold) {
		t.Errorf("a previous root that is not itself signed: %v", err)
	}
	if _, err := updatemeta.CheckRoot(signedBy(t, r1, old[0], old[1]), first, at); !errors.Is(err, updatemeta.ErrRollback) {
		t.Errorf("a root that is not the successor: %v", err)
	}
	if _, err := updatemeta.CheckRoot(both, first, at.Add(366*24*time.Hour)); !errors.Is(err, updatemeta.ErrExpired) {
		t.Errorf("an expired rotation: %v", err)
	}
}

// Verifies: SEC-051.
// AddSignature keeps a file canonical and replaces a holder's earlier
// signature instead of adding a second.
func TestAddSignature_SEC_051(t *testing.T) {
	a, b, c := newEd25519(t), newEd25519(t), newEd25519(t)
	online := [3]key{newEd25519(t), newEd25519(t), newEd25519(t)}
	r, err := updatemeta.NewRoot(ceremonySpec([]key{a, b, c}, online))
	if err != nil {
		t.Fatal(err)
	}
	raw := signedBy(t, r, a, b, a)
	d, err := updatemeta.ParseDocument(raw)
	if err != nil || len(d.Signatures) != 2 {
		t.Fatalf("%d signatures, %v", len(d.Signatures), err)
	}
	again, err := d.Bytes()
	if err != nil || !bytes.Equal(again, raw) {
		t.Errorf("the file is not canonical: %v", err)
	}
	if _, err := updatemeta.AddSignature([]byte("{"), updatemeta.Signature{}); !errors.Is(err, updatemeta.ErrFormat) {
		t.Errorf("garbage: %v", err)
	}
}
