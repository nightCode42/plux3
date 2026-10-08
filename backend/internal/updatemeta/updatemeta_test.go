// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package updatemeta_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/nightCode42/plux3/backend/internal/signing"
	"github.com/nightCode42/plux3/backend/internal/updatemeta"
)

// at is the clock of every test: metadata is built and checked against it.
var at = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

// key is a test key pair that signs in the algorithm's wire form.
type key struct {
	alg  string
	pub  []byte
	sign func(msg []byte) []byte
}

func (k key) id() string { return updatemeta.KeyID(k.pub) }

func (k key) listed() updatemeta.Key {
	return updatemeta.Key{Alg: k.alg, Public: base64.StdEncoding.EncodeToString(k.pub)}
}

func (k key) signature(msg []byte) updatemeta.Signature {
	return updatemeta.Signature{KeyID: k.id(), Alg: k.alg, Sig: base64.StdEncoding.EncodeToString(k.sign(msg))}
}

func newEd25519(t *testing.T) key {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key{alg: updatemeta.AlgEd25519, pub: pub, sign: func(m []byte) []byte { return ed25519.Sign(priv, m) }}
}

func newES256(t *testing.T) key {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := priv.PublicKey.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return key{alg: updatemeta.AlgES256, pub: pub, sign: func(m []byte) []byte {
		digest := sha256.Sum256(m)
		der, err := ecdsa.SignASN1(rand.Reader, priv, digest[:])
		if err != nil {
			t.Fatal(err)
		}
		var rs struct{ R, S *big.Int }
		if _, err := asn1.Unmarshal(der, &rs); err != nil {
			t.Fatal(err)
		}
		out := make([]byte, 64)
		rs.R.FillBytes(out[:32])
		rs.S.FillBytes(out[32:])
		return out
	}}
}

// offline makes the three offline root keys of a ceremony, one of them
// ECDSA, as a hardware module would hold.
func offline(t *testing.T) []key { t.Helper(); return []key{newEd25519(t), newES256(t), newEd25519(t)} }

// online makes the keys of the three online roles.
type online struct{ targets, snapshot, timestamp key }

func newOnline(t *testing.T) online {
	t.Helper()
	return online{newEd25519(t), newEd25519(t), newEd25519(t)}
}

// rootSigned builds the signed part of a root.
func rootSigned(version int64, expires time.Time, root []key, on online) updatemeta.Root {
	r := updatemeta.Root{
		Type: updatemeta.RoleRoot, Version: version, Expires: updatemeta.FormatTime(expires), SpecVersion: updatemeta.SpecVersion,
		Keys: map[string]updatemeta.Key{},
	}
	ids := func(ks ...key) []string {
		var out []string
		for _, k := range ks {
			r.Keys[k.id()] = k.listed()
			out = append(out, k.id())
		}
		return out
	}
	r.Roles = updatemeta.Roles{
		Root:      updatemeta.RoleKeys{KeyIDs: ids(root...), Threshold: 2},
		Targets:   updatemeta.RoleKeys{KeyIDs: ids(on.targets), Threshold: 1},
		Snapshot:  updatemeta.RoleKeys{KeyIDs: ids(on.snapshot), Threshold: 1},
		Timestamp: updatemeta.RoleKeys{KeyIDs: ids(on.timestamp), Threshold: 1},
	}
	return r
}

// signRoot signs a root with the given keys and returns the file.
func signRoot(t *testing.T, r updatemeta.Root, signers ...key) []byte {
	t.Helper()
	canonical, err := updatemeta.Canonical(r)
	if err != nil {
		t.Fatal(err)
	}
	var sigs []updatemeta.Signature
	for _, k := range signers {
		sigs = append(sigs, k.signature(canonical))
	}
	doc, err := updatemeta.Marshal(r, sigs)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

// chain is a complete, valid chain under a root.
type chain struct {
	root      updatemeta.Root
	timestamp []byte
	snapshot  []byte
	manifest  []byte
	sigs      []updatemeta.Signature
}

// manifestJSON is the part of a manifest the targets role reads.
func manifestJSON(t *testing.T, version int64, expires time.Time) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"type": "manifest", "specVersion": 1, "role": "targets", "version": version, "releaseSequence": 7,
		"expires": updatemeta.FormatTime(expires),
	})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := updatemeta.Canonical(json.RawMessage(b))
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

// newChain builds root, targets, snapshot and timestamp with the lifetimes
// of the specification, the online files signed through the file signing
// backend as the worker does.
func newChain(t *testing.T, on online, targets, timestamp int64) chain {
	t.Helper()
	const snapshot int64 = 3
	root := rootSigned(1, at.Add(updatemeta.DefaultExpiry().Root), offline(t), on)
	exp := updatemeta.DefaultExpiry()
	manifest := manifestJSON(t, targets, at.Add(exp.Targets))
	snapDoc, err := updatemeta.Marshal(updatemeta.NewSnapshot(snapshot, targets, at.Add(exp.Snapshot)),
		[]updatemeta.Signature{on.snapshot.signature(mustCanonical(t, updatemeta.NewSnapshot(snapshot, targets, at.Add(exp.Snapshot))))})
	if err != nil {
		t.Fatal(err)
	}
	ts := updatemeta.NewTimestamp(timestamp, snapshot, snapDoc, at.Add(exp.Timestamp))
	tsDoc, err := updatemeta.Marshal(ts, []updatemeta.Signature{on.timestamp.signature(mustCanonical(t, ts))})
	if err != nil {
		t.Fatal(err)
	}
	return chain{
		root: root, timestamp: tsDoc, snapshot: snapDoc, manifest: manifest,
		sigs: []updatemeta.Signature{{KeyID: on.targets.id(), Alg: "ed25519", Sig: base64.StdEncoding.EncodeToString(on.targets.sign(manifest))}},
	}
}

func mustCanonical(t *testing.T, v any) []byte {
	t.Helper()
	b, err := updatemeta.Canonical(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (c chain) verifier(production bool, envs map[string]string) updatemeta.Verifier {
	return updatemeta.Verifier{Root: c.root, Production: production, KeyEnvironments: envs, Now: at.Add(time.Hour)}
}

func (c chain) verify(v updatemeta.Verifier) error {
	_, err := v.Chain(c.timestamp, c.snapshot, c.manifest, c.sigs)
	return err
}

// Verifies: SEC-050, SEC-122.
// Every role signs and verifies: the chain timestamp, snapshot, targets
// holds under the keys of the root, each role by its own key.
func TestEveryRoleSignsAndVerifies_SEC_050(t *testing.T) {
	t.Parallel()
	c := newChain(t, newOnline(t), 4, 9)
	floor, err := c.verifier(false, nil).Chain(c.timestamp, c.snapshot, c.manifest, c.sigs)
	if err != nil {
		t.Fatalf("Chain: %v", err)
	}
	if floor != (updatemeta.Floor{Timestamp: 9, Snapshot: 3, Targets: 4}) {
		t.Errorf("trusted versions: %+v", floor)
	}
}

// Verifies: SEC-050, SEC-122.
// The worker's path: roles signed through a signing backend, as the file
// backend names its keys, carry the algorithm identifier and a key
// identifier that the root lists.
func TestSignThroughTheSigningBackend_SEC_122(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend, err := signing.NewFile(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pub, id, err := backend.PublicKey(ctx, "snapshot-env")
	if err != nil {
		t.Fatal(err)
	}
	snap := updatemeta.NewSnapshot(2, 5, at.Add(24*time.Hour))
	doc, sig, err := updatemeta.Sign(ctx, backend, "snapshot-env", snap)
	if err != nil {
		t.Fatal(err)
	}
	if sig.Alg != updatemeta.AlgEd25519 || sig.KeyID != id || sig.KeyID != updatemeta.KeyID(pub) {
		t.Errorf("signature: %+v", sig)
	}
	d, err := updatemeta.ParseDocument(doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Signatures) != 1 || d.Signatures[0] != sig {
		t.Errorf("signatures: %+v", d.Signatures)
	}
	if !bytes.Equal(d.Signed, mustCanonical(t, snap)) {
		t.Errorf("the signed part is not the canonical form: %s", d.Signed)
	}
	if !ed25519.Verify(pub, d.Signed, mustBase64(t, sig.Sig)) {
		t.Error("the signature does not verify under the public key")
	}
	again, _, err := updatemeta.Sign(ctx, backend, "snapshot-env", snap)
	if err != nil || !bytes.Equal(again, doc) {
		t.Errorf("signing the same content twice differs (Ed25519 is deterministic): %v", err)
	}
}

func mustBase64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Verifies: SEC-050.
// The chain is checked link by link: the timestamp's hash and version of
// the snapshot, the snapshot's version of the targets.
func TestChainLinksAreChecked_SEC_050(t *testing.T) {
	t.Parallel()
	on := newOnline(t)
	good := newChain(t, on, 4, 9)
	other := newChain(t, on, 4, 10)
	other.root = good.root

	t.Run("a snapshot the timestamp does not hash", func(t *testing.T) {
		t.Parallel()
		c := good
		c.snapshot = append(bytes.Clone(good.snapshot), ' ')
		if err := c.verify(c.verifier(false, nil)); !errors.Is(err, updatemeta.ErrMismatch) {
			t.Errorf("got %v", err)
		}
	})
	t.Run("a different but valid snapshot", func(t *testing.T) {
		t.Parallel()
		c := good
		c.snapshot = newChain(t, on, 5, 9).snapshot // another targets version, same snapshot version
		if err := c.verify(c.verifier(false, nil)); !errors.Is(err, updatemeta.ErrMismatch) {
			t.Errorf("got %v", err)
		}
	})
	t.Run("a manifest of another targets version", func(t *testing.T) {
		t.Parallel()
		c := good
		c.manifest = manifestJSON(t, 3, at.Add(30*24*time.Hour))
		c.sigs = []updatemeta.Signature{{KeyID: on.targets.id(), Alg: "ed25519", Sig: base64.StdEncoding.EncodeToString(on.targets.sign(c.manifest))}}
		if err := c.verify(c.verifier(false, nil)); !errors.Is(err, updatemeta.ErrMismatch) {
			t.Errorf("got %v", err)
		}
	})
	t.Run("a manifest without a version", func(t *testing.T) {
		t.Parallel()
		c := good
		c.manifest = manifestJSON(t, 0, at.Add(30*24*time.Hour))
		c.sigs = []updatemeta.Signature{{KeyID: on.targets.id(), Alg: "ed25519", Sig: base64.StdEncoding.EncodeToString(on.targets.sign(c.manifest))}}
		if err := c.verify(c.verifier(false, nil)); !errors.Is(err, updatemeta.ErrMismatch) {
			t.Errorf("got %v", err)
		}
	})
	t.Run("a manifest signed by a key of another role", func(t *testing.T) {
		t.Parallel()
		c := good
		c.sigs = []updatemeta.Signature{{KeyID: on.snapshot.id(), Alg: "ed25519", Sig: base64.StdEncoding.EncodeToString(on.snapshot.sign(c.manifest))}}
		if err := c.verify(c.verifier(false, nil)); !errors.Is(err, updatemeta.ErrThreshold) {
			t.Errorf("got %v", err)
		}
	})
	t.Run("a tampered manifest", func(t *testing.T) {
		t.Parallel()
		c := good
		c.manifest = bytes.Replace(good.manifest, []byte(`"releaseSequence":7`), []byte(`"releaseSequence":8`), 1)
		if err := c.verify(c.verifier(false, nil)); !errors.Is(err, updatemeta.ErrThreshold) {
			t.Errorf("got %v", err)
		}
	})
	t.Run("a newer timestamp over the same snapshot", func(t *testing.T) {
		t.Parallel()
		c := good
		c.timestamp = other.timestamp
		if err := c.verify(c.verifier(false, nil)); err != nil {
			t.Errorf("a newer timestamp that hashes the same snapshot is fine: %v", err)
		}
	})
}

// Verifies: SEC-050.
// Expiry is enforced for every role when the device syncs, at the second
// the file says; a file one second short of it is still good.
func TestExpiryIsEnforcedPerRole_SEC_050(t *testing.T) {
	t.Parallel()
	c := newChain(t, newOnline(t), 4, 9)
	exp := updatemeta.DefaultExpiry()
	for _, tc := range []struct {
		name string
		now  time.Time
		ok   bool
	}{
		{"fresh", at, true},
		{"timestamp one second left", at.Add(exp.Timestamp - time.Second), true},
		{"timestamp expired", at.Add(exp.Timestamp), false},
		{"snapshot expired", at.Add(exp.Snapshot), false},
		{"targets expired", at.Add(exp.Targets), false},
	} {
		v := c.verifier(false, nil)
		v.Now = tc.now
		err := c.verify(v)
		if tc.ok && err != nil {
			t.Errorf("%s: %v", tc.name, err)
		}
		if !tc.ok && !errors.Is(err, updatemeta.ErrExpired) {
			t.Errorf("%s: got %v, want expiry", tc.name, err)
		}
	}
	// A frozen timestamp is the freeze attack: only the expired role
	// fails, so an attacker cannot keep serving it past 24 hours.
	v := c.verifier(false, nil)
	v.Now = at.Add(exp.Timestamp + time.Hour)
	if _, err := v.Timestamp(c.timestamp); !errors.Is(err, updatemeta.ErrExpired) {
		t.Errorf("a frozen timestamp: %v", err)
	}
	v.Now = at.Add(exp.Timestamp - time.Hour)
	if _, err := v.Timestamp(c.timestamp); err != nil {
		t.Errorf("the same timestamp inside its lifetime: %v", err)
	}
}

// Verifies: SEC-050.
// A version below the one already trusted is refused for each role.
func TestVersionsNeverGoBack_SEC_050(t *testing.T) {
	t.Parallel()
	c := newChain(t, newOnline(t), 4, 9)
	for _, f := range []updatemeta.Floor{{Timestamp: 10}, {Snapshot: 4}, {Targets: 5}} {
		v := c.verifier(false, nil)
		v.Floor = f
		if err := c.verify(v); !errors.Is(err, updatemeta.ErrRollback) {
			t.Errorf("floor %+v: got %v", f, err)
		}
	}
	v := c.verifier(false, nil)
	v.Floor = updatemeta.Floor{Timestamp: 9, Snapshot: 3, Targets: 4}
	if err := c.verify(v); err != nil {
		t.Errorf("the same versions again: %v", err)
	}
}

// Verifies: SEC-050.
// The root role's threshold is 2 of 3: one signature, a repeated
// signature or a signature by a stranger do not meet it.
func TestRootThreshold_SEC_050(t *testing.T) {
	t.Parallel()
	on := newOnline(t)
	keys := offline(t)
	r := rootSigned(1, at.Add(time.Hour*24*365), keys, on)
	stranger := newEd25519(t)
	for _, tc := range []struct {
		name    string
		signers []key
		ok      bool
	}{
		{"two of three", []key{keys[0], keys[2]}, true},
		{"including the ECDSA key", []key{keys[1], keys[2]}, true},
		{"all three", keys, true},
		{"one", []key{keys[0]}, false},
		{"the same key twice", []key{keys[0], keys[0]}, false},
		{"one and a stranger", []key{keys[0], stranger}, false},
		{"an online key", []key{keys[0], on.targets}, false},
		{"none", nil, false},
	} {
		_, _, err := updatemeta.FirstRoot(signRoot(t, r, tc.signers...))
		if tc.ok && err != nil {
			t.Errorf("%s: %v", tc.name, err)
		}
		if !tc.ok && !errors.Is(err, updatemeta.ErrThreshold) {
			t.Errorf("%s: got %v, want the threshold error", tc.name, err)
		}
	}
	if _, _, err := updatemeta.FirstRoot(signRoot(t, rootSigned(2, at.Add(time.Hour), keys, on), keys...)); !errors.Is(err, updatemeta.ErrRollback) {
		t.Errorf("a first root of version 2: %v", err)
	}
}

// Verifies: SEC-051.
// A new root is accepted only when the previous root's threshold and its
// own both signed it, and only as the next version.
func TestRootRotation_SEC_051(t *testing.T) {
	t.Parallel()
	on := newOnline(t)
	old := offline(t)
	fresh := offline(t)
	v1 := rootSigned(1, at.Add(time.Hour*24*365), old, on)
	if _, _, err := updatemeta.FirstRoot(signRoot(t, v1, old[0], old[1])); err != nil {
		t.Fatal(err)
	}
	// Rotation of one root key: key 2 replaced by a new one.
	rotated := []key{old[0], old[1], fresh[0]}
	v2 := rootSigned(2, at.Add(time.Hour*24*365), rotated, on)
	// A key rotation of the online roles in the same step.
	onNew := newOnline(t)
	v2online := rootSigned(2, at.Add(time.Hour*24*365), rotated, onNew)

	for _, tc := range []struct {
		name    string
		next    updatemeta.Root
		signers []key
		wantErr error
	}{
		{"signed by the old and the new threshold", v2, []key{old[0], old[1]}, nil},
		{"new online keys, signed by the old threshold and the new", v2online, []key{old[0], old[1]}, nil},
		{"signed by one old key and one new key", v2, []key{old[0], fresh[0]}, updatemeta.ErrThreshold}, // fresh[0] is new, old[0] only one old
		{"signed by one old key", v2, []key{old[0]}, updatemeta.ErrThreshold},
		{"signed by an unrelated set", v2, []key{fresh[1], fresh[2]}, updatemeta.ErrThreshold},
		{"a skipped version", rootSigned(3, at.Add(time.Hour), rotated, on), []key{old[0], old[1]}, updatemeta.ErrRollback},
		{"the same version again", rootSigned(1, at.Add(time.Hour), rotated, on), []key{old[0], old[1]}, updatemeta.ErrRollback},
	} {
		_, _, err := updatemeta.NextRoot(v1, signRoot(t, tc.next, tc.signers...))
		if !errors.Is(err, tc.wantErr) && (tc.wantErr != nil || err != nil) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.wantErr)
		}
	}

	// A root that the new keys alone signed (the old threshold stolen
	// none) and one that only the old keys signed (a root whose new
	// keys never agreed): neither is accepted.
	allNew := []key{fresh[0], fresh[1], fresh[2]}
	vNew := rootSigned(2, at.Add(time.Hour*24*365), allNew, on)
	if _, _, err := updatemeta.NextRoot(v1, signRoot(t, vNew, fresh[0], fresh[1])); !errors.Is(err, updatemeta.ErrThreshold) {
		t.Errorf("signed by the new keys only: %v", err)
	}
	if _, _, err := updatemeta.NextRoot(v1, signRoot(t, vNew, old[0], old[1])); !errors.Is(err, updatemeta.ErrThreshold) {
		t.Errorf("signed by the old keys only: %v", err)
	}
	if _, _, err := updatemeta.NextRoot(v1, signRoot(t, vNew, old[0], old[1], fresh[0], fresh[1])); err != nil {
		t.Errorf("signed by both: %v", err)
	}
}

// Verifies: SEC-051.
// A device that trusts the first root follows the chain of roots in
// order to the newest, and refuses a chain with a gap, a reorder or a
// bad link, or one whose last root has expired.
func TestRootChain_SEC_051(t *testing.T) {
	t.Parallel()
	on := newOnline(t)
	k1 := offline(t)
	k2 := []key{k1[0], k1[1], newEd25519(t)}
	k3 := []key{k1[0], k2[2], newEd25519(t)}
	year := time.Hour * 24 * 365
	v1 := rootSigned(1, at.Add(year), k1, on)
	v2 := rootSigned(2, at.Add(year), k2, on)
	v3 := rootSigned(3, at.Add(year), k3, on)
	d2 := signRoot(t, v2, k1[0], k1[1])
	d3 := signRoot(t, v3, k2[0], k2[2], k3[0], k3[1])
	now := at.Add(time.Hour)

	got, err := updatemeta.RootChain(v1, [][]byte{d2, d3}, now)
	if err != nil || got.Version != 3 {
		t.Fatalf("RootChain: %+v, %v", got, err)
	}
	if got, err := updatemeta.RootChain(v1, nil, now); err != nil || got.Version != 1 {
		t.Errorf("an empty chain keeps the trusted root: %+v %v", got, err)
	}
	if _, err := updatemeta.RootChain(v1, [][]byte{d3, d2}, now); err == nil {
		t.Error("a reordered chain was accepted")
	}
	if _, err := updatemeta.RootChain(v1, [][]byte{d3}, now); !errors.Is(err, updatemeta.ErrRollback) {
		t.Errorf("a chain with a gap: %v", err)
	}
	if _, err := updatemeta.RootChain(v1, [][]byte{d2, d3}, at.Add(year+time.Hour)); !errors.Is(err, updatemeta.ErrExpired) {
		t.Errorf("an expired last root: %v", err)
	}
	// An intermediate root may be expired: the device was offline.
	short := rootSigned(2, at.Add(time.Minute), k2, on)
	d2short := signRoot(t, short, k1[0], k1[1])
	v3b := rootSigned(3, at.Add(year), k3, on)
	d3b := signRoot(t, v3b, k2[0], k2[2], k3[0], k3[1])
	if got, err := updatemeta.RootChain(v1, [][]byte{d2short, d3b}, now); err != nil || got.Version != 3 {
		t.Errorf("an expired intermediate root: %+v %v", got, err)
	}
}

// Verifies: SEC-056.
// A production verifier refuses metadata signed by a development key, or
// by a key whose environment is not known; a development verifier trusts
// both.
func TestDevelopmentKeysAreRefusedInProduction_SEC_056(t *testing.T) {
	t.Parallel()
	on := newOnline(t)
	c := newChain(t, on, 4, 9)
	allKeys := func(env string) map[string]string {
		m := map[string]string{}
		for _, k := range []key{on.targets, on.snapshot, on.timestamp} {
			m[k.id()] = env
		}
		return m
	}
	if err := c.verify(c.verifier(true, allKeys(updatemeta.EnvProduction))); err != nil {
		t.Errorf("production keys in production: %v", err)
	}
	if err := c.verify(c.verifier(true, allKeys(updatemeta.EnvDevelopment))); !errors.Is(err, updatemeta.ErrDevelopmentKey) {
		t.Errorf("development keys in production: %v", err)
	}
	if err := c.verify(c.verifier(true, nil)); !errors.Is(err, updatemeta.ErrDevelopmentKey) {
		t.Errorf("keys of unknown type in production: %v", err)
	}
	// Each role is checked: one development key among production keys.
	for _, dev := range []key{on.targets, on.snapshot, on.timestamp} {
		m := allKeys(updatemeta.EnvProduction)
		m[dev.id()] = updatemeta.EnvDevelopment
		if err := c.verify(c.verifier(true, m)); !errors.Is(err, updatemeta.ErrDevelopmentKey) {
			t.Errorf("one development key: %v", err)
		}
	}
	if err := c.verify(c.verifier(false, allKeys(updatemeta.EnvDevelopment))); err != nil {
		t.Errorf("development keys in development: %v", err)
	}
}

// Verifies: SEC-122.
// Signatures and keys name their algorithm. ES256 is verified beside
// Ed25519; the historical spellings read as the same algorithm; an
// unknown or reserved algorithm signs nothing and is not guessed at; a
// signature whose algorithm differs from its key's counts for nothing.
func TestAlgorithmIdentifiers_SEC_122(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"ed25519": updatemeta.AlgEd25519, "Ed25519": updatemeta.AlgEd25519, "ES256": updatemeta.AlgES256,
		"ecdsa-p256-sha256": updatemeta.AlgES256, "ml-dsa-65": updatemeta.AlgMLDSA65, "ML-DSA-65": updatemeta.AlgMLDSA65,
		"rsa": "rsa",
	} {
		if got := updatemeta.CanonicalAlg(in); got != want {
			t.Errorf("CanonicalAlg(%q) = %q, want %q", in, got, want)
		}
	}
	if !updatemeta.Supported("ed25519") || !updatemeta.Supported("ES256") || updatemeta.Supported("ML-DSA-65") || updatemeta.Supported("rsa") {
		t.Error("Supported does not match what can be verified")
	}

	on := newOnline(t)
	es := newES256(t)
	// An ES256 online timestamp key, beside Ed25519 snapshot and targets keys.
	on.timestamp = es
	c := newChain(t, on, 4, 9)
	if err := c.verify(c.verifier(false, nil)); err != nil {
		t.Errorf("an ES256 timestamp: %v", err)
	}
	d, _ := updatemeta.ParseDocument(c.timestamp)
	if d.Signatures[0].Alg != updatemeta.AlgES256 {
		t.Errorf("the signature names %q", d.Signatures[0].Alg)
	}

	// A signature that names another algorithm than its key is not counted.
	d.Signatures[0].Alg = updatemeta.AlgEd25519
	forged, err := updatemeta.Marshal(d.Signed, d.Signatures)
	if err != nil {
		t.Fatal(err)
	}
	cc := c
	cc.timestamp = forged
	if err := cc.verify(cc.verifier(false, nil)); !errors.Is(err, updatemeta.ErrThreshold) {
		t.Errorf("a signature with the wrong algorithm: %v", err)
	}
	d.Signatures[0].Alg = "ML-DSA-65"
	reserved, _ := updatemeta.Marshal(d.Signed, d.Signatures)
	cc.timestamp = reserved
	if err := cc.verify(cc.verifier(false, nil)); !errors.Is(err, updatemeta.ErrThreshold) {
		t.Errorf("a signature with a reserved algorithm: %v", err)
	}

	// A root may name a post-quantum key for the future; it cannot meet a
	// threshold today, and a root that needs it is refused.
	pq := updatemeta.Key{Alg: updatemeta.AlgMLDSA65, Public: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 1952))}
	pqID := updatemeta.KeyID(bytes.Repeat([]byte{7}, 1952))
	good := rootSigned(1, at.Add(time.Hour*24*365), offline(t), newOnline(t))
	good.Keys[pqID] = pq
	good.Roles.Root.KeyIDs = append(good.Roles.Root.KeyIDs, pqID)
	canonical := mustCanonical(t, good)
	if _, err := updatemeta.ParseRoot(canonical); err != nil {
		t.Errorf("a root that lists a reserved algorithm beside enough usable keys: %v", err)
	}
	good.Roles.Root.Threshold = 4
	if _, err := updatemeta.ParseRoot(mustCanonical(t, good)); !errors.Is(err, updatemeta.ErrFormat) {
		t.Errorf("a threshold only a reserved key could help meet: %v", err)
	}
}

// Verifies: SEC-050, SEC-122.
// The files are canonical: the same content has the same bytes whatever
// the order the signatures were added in, so the timestamp's hash of the
// snapshot is stable; a hand-formatted file reads the same.
func TestFilesAreCanonical_SEC_050(t *testing.T) {
	t.Parallel()
	on := newOnline(t)
	snap := updatemeta.NewSnapshot(2, 3, at.Add(time.Hour))
	canonical := mustCanonical(t, snap)
	s1, s2 := on.snapshot.signature(canonical), on.timestamp.signature(canonical)
	a, err := updatemeta.Marshal(snap, []updatemeta.Signature{s1, s2})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := updatemeta.Marshal(snap, []updatemeta.Signature{s2, s1})
	if !bytes.Equal(a, b) {
		t.Errorf("signature order changes the bytes:\n%s\n%s", a, b)
	}
	var generic any
	if err := json.Unmarshal(a, &generic); err != nil {
		t.Fatal(err)
	}
	pretty, _ := json.MarshalIndent(generic, "", "    ")
	d, err := updatemeta.ParseDocument(pretty)
	if err != nil || !bytes.Equal(d.Signed, canonical) {
		t.Errorf("a re-formatted file: %v %s", err, d.Signed)
	}
	for _, bad := range []string{`{}`, `{"signed":{},"signatures":[],"extra":1}`, `{"signed":{}} x`, `{"signed":{},"signatures":[{"keyid":"zz","alg":"Ed25519","sig":"AA=="}]}`} {
		if _, err := updatemeta.ParseDocument([]byte(bad)); !errors.Is(err, updatemeta.ErrFormat) {
			t.Errorf("%s: %v", bad, err)
		}
	}
}

// Verifies: SEC-122.
// What the package writes validates against the JSON Schema of its kind.
func TestDocumentsMatchTheSchemas_SEC_122(t *testing.T) {
	t.Parallel()
	dir := filepath.Join("..", "..", "..", "schema", "update")
	commonRaw, err := os.ReadFile(filepath.Join(dir, "common.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	common, err := jsonschema.UnmarshalJSON(bytes.NewReader(commonRaw))
	if err != nil {
		t.Fatal(err)
	}
	on := newOnline(t)
	c := newChain(t, on, 4, 9)
	rootDoc := signRoot(t, c.root, offline(t)[0])
	for kind, doc := range map[string][]byte{"root": rootDoc, "snapshot": c.snapshot, "timestamp": c.timestamp} {
		abs, err := filepath.Abs(filepath.Join(dir, kind+".schema.json"))
		if err != nil {
			t.Fatal(err)
		}
		comp := jsonschema.NewCompiler()
		comp.AssertFormat()
		if err := comp.AddResource("https://plux.dev/schema/v1/update/common.schema.json", common); err != nil {
			t.Fatal(err)
		}
		s, err := comp.Compile(abs)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc))
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Validate(inst); err != nil {
			t.Errorf("%s does not match its schema: %v\n%s", kind, err, doc)
		}
	}
}
