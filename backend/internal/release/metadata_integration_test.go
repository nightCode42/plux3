// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package release_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nightCode42/plux3/backend/internal/schema/limits"

	"github.com/jackc/pgx/v5"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/release"
	"github.com/nightCode42/plux3/backend/internal/schema/uuid7"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
	"github.com/nightCode42/plux3/backend/internal/updatemeta"
)

// offlineKey is an offline root key of a ceremony.
type offlineKey struct {
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
}

func newOfflineKeys(t *testing.T, n int) []offlineKey {
	t.Helper()
	out := make([]offlineKey, n)
	for i := range out {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		out[i] = offlineKey{pub, priv}
	}
	return out
}

func (k offlineKey) id() string { return updatemeta.KeyID(k.pub) }

// ceremony builds root files the way an offline ceremony would.
type ceremony struct {
	t      *testing.T
	online []release.OnlineKey
	expiry time.Time
	// pins are the certificate pins the root carries, when set (SEC-041).
	pins map[string][]string
}

// root builds and signs a root of the given version with the root keys
// named, signed by the signers.
func (c ceremony) root(version int64, rootKeys []offlineKey, threshold int, signers ...offlineKey) []byte {
	c.t.Helper()
	r := updatemeta.Root{
		Type: updatemeta.RoleRoot, Version: version, Expires: updatemeta.FormatTime(c.expiry), SpecVersion: updatemeta.SpecVersion,
		Keys: map[string]updatemeta.Key{},
	}
	r.Pins = c.pins
	var rootIDs []string
	for _, k := range rootKeys {
		r.Keys[k.id()] = updatemeta.Key{Alg: updatemeta.AlgEd25519, Public: base64.StdEncoding.EncodeToString(k.pub)}
		rootIDs = append(rootIDs, k.id())
	}
	r.Roles.Root = updatemeta.RoleKeys{KeyIDs: rootIDs, Threshold: threshold}
	for _, k := range c.online {
		r.Keys[k.KeyID] = updatemeta.Key{Alg: k.Algorithm, Public: base64.StdEncoding.EncodeToString(k.PublicKey)}
		keys := updatemeta.RoleKeys{KeyIDs: []string{k.KeyID}, Threshold: 1}
		switch k.Role {
		case updatemeta.RoleTargets:
			r.Roles.Targets = keys
		case updatemeta.RoleSnapshot:
			r.Roles.Snapshot = keys
		case updatemeta.RoleTimestamp:
			r.Roles.Timestamp = keys
		}
	}
	canonical, err := updatemeta.Canonical(r)
	if err != nil {
		c.t.Fatal(err)
	}
	var sigs []updatemeta.Signature
	for _, k := range signers {
		sigs = append(sigs, updatemeta.Signature{
			KeyID: k.id(), Alg: updatemeta.AlgEd25519, Sig: base64.StdEncoding.EncodeToString(ed25519.Sign(k.priv, canonical)),
		})
	}
	doc, err := updatemeta.Marshal(r, sigs)
	if err != nil {
		c.t.Fatal(err)
	}
	return doc
}

// metadataFixture is a fixture with a promoted production release and
// the ceremony for its environment.
type metadataFixture struct {
	*fixture
	prod     string
	ceremony ceremony
}

func newMetadataFixture(t *testing.T) *metadataFixture {
	t.Helper()
	return newMetadataFixtureWith(t, limits.Set{})
}

func newMetadataFixtureWith(t *testing.T, set limits.Set) *metadataFixture {
	t.Helper()
	f := newFixtureWith(t, "loan-calculator", limits.Set{}, func(o *release.Options) {
		o.Limits = set
		o.Metadata = release.MetadataOptions{Expiry: updatemeta.DefaultExpiry(), RootThreshold: 2}
	})
	ctx := context.Background()
	f.publish(t, "", false)
	f.publish(t, f.loans, false)
	first, _, err := f.rel.CreateRelease(ctx, f.owner, f.app, f.envs["development"].ID, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	prod := f.envs["production"].ID
	if _, err := f.rel.PromoteRelease(ctx, f.owner, f.app, first.Sequence, prod, ""); err != nil {
		t.Fatal(err)
	}
	f.run(t)
	online, err := f.rel.OnlineKeys(ctx, f.org, prod)
	if err != nil {
		t.Fatal(err)
	}
	return &metadataFixture{fixture: f, prod: prod, ceremony: ceremony{t: t, online: online, expiry: f.now.Add(365 * 24 * time.Hour)}}
}

// chain fetches the newest files and the manifest and verifies the chain
// as a production device would at the fixture's time.
func (f *metadataFixture) chain(t *testing.T, floor updatemeta.Floor) (updatemeta.Floor, error) {
	t.Helper()
	ctx := context.Background()
	rootFile, err := f.rel.Metadata(ctx, f.prod, "1.root.json")
	if err != nil {
		t.Fatal(err)
	}
	chain, err := f.rel.RootChain(ctx, f.org, f.prod, 1)
	if err != nil {
		t.Fatal(err)
	}
	d, err := updatemeta.ParseDocument(rootFile.Document)
	if err != nil {
		t.Fatal(err)
	}
	trusted, err := updatemeta.ParseRoot(d.Signed)
	if err != nil {
		t.Fatal(err)
	}
	root, err := updatemeta.RootChain(trusted, chain, f.now)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := f.rel.RootKeys(ctx, f.org, f.prod)
	if err != nil {
		t.Fatal(err)
	}
	envs := map[string]string{}
	for _, k := range keys {
		envs[k.KeyID] = k.EnvironmentType
	}
	ts, err := f.rel.Metadata(ctx, f.prod, "timestamp.json")
	if err != nil {
		t.Fatal(err)
	}
	snap, err := f.rel.Metadata(ctx, f.prod, "snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	served, err := f.rel.GetManifest(ctx, release.ManifestRequest{OrganizationID: f.org, AppID: f.app, EnvironmentID: f.prod})
	if err != nil {
		t.Fatal(err)
	}
	var sigs []updatemeta.Signature
	for _, s := range served.Signatures {
		sigs = append(sigs, updatemeta.Signature{KeyID: s.KeyID, Alg: s.Algorithm, Sig: s.Signature})
	}
	v := updatemeta.Verifier{Root: root, Production: true, KeyEnvironments: envs, Floor: floor, Now: f.now}
	return v.Chain(ts.Document, snap.Document, served.Signed, sigs)
}

// Verifies: SEC-050, SEC-051, SEC-056, SEC-122.
// An environment with an uploaded root gets the whole chain on each
// publish: a manifest stamped with the new targets version, the snapshot
// that pins it, the timestamp that names the snapshot; a device that
// follows the chain from the root verifies it, and versions only grow.
func TestPublishWritesTheMetadataChain_SEC_050(t *testing.T) {
	t.Parallel()
	f := newMetadataFixture(t)
	ctx := context.Background()
	ref := release.ManifestRequest{OrganizationID: f.org, AppID: f.app, EnvironmentID: f.prod}

	// Before a root exists the environment signs manifests as before.
	before, err := f.rel.GetManifest(ctx, ref)
	if err != nil || before.Document.Version != 0 || before.Metadata != (release.MetadataRef{}) {
		t.Fatalf("an environment without a root: %+v %v", before.Metadata, err)
	}

	keys := newOfflineKeys(t, 3)
	if _, err := f.rel.UploadRoot(ctx, f.org, f.prod, f.ceremony.root(1, keys, 2, keys[0], keys[1])); err != nil {
		t.Fatalf("UploadRoot: %v", err)
	}
	// The sweep that signs the first metadata for a root that arrived
	// after the release.
	if n, err := f.rel.RefreshMetadata(ctx, f.org); err != nil || n == 0 {
		t.Fatalf("RefreshMetadata: %d %v", n, err)
	}
	floor, err := f.chain(t, updatemeta.Floor{})
	if err != nil {
		t.Fatalf("the chain: %v", err)
	}
	if floor != (updatemeta.Floor{Timestamp: 1, Snapshot: 1, Targets: 1}) {
		t.Errorf("first versions: %+v", floor)
	}
	m, err := f.rel.GetManifest(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if m.Metadata != (release.MetadataRef{Root: 1, Snapshot: 1, Timestamp: 1}) || m.Document.Version != 1 {
		t.Errorf("the manifest's references: %+v, version %d", m.Metadata, m.Document.Version)
	}
	issued, _ := time.Parse(time.RFC3339, m.Document.IssuedAt)
	expires, _ := time.Parse(time.RFC3339, m.Document.Expires)
	if expires.Sub(issued) != 30*24*time.Hour {
		t.Errorf("targets expiry: %v", expires.Sub(issued))
	}

	// Each change signs everything again, at the next versions.
	for want := int64(2); want <= 4; want++ {
		if _, err := f.rel.SetControl(ctx, f.owner, release.Control{AppID: f.app, EnvironmentID: f.prod, Message: string(rune('a' + want))}); err != nil {
			t.Fatal(err)
		}
		f.run(t)
		got, err := f.chain(t, floor)
		if err != nil {
			t.Fatalf("version %d: %v", want, err)
		}
		if got != (updatemeta.Floor{Timestamp: want, Snapshot: want, Targets: want}) {
			t.Errorf("versions after change %d: %+v", want, got)
		}
		floor = got
	}
	// An older timestamp is still served by version, and is refused as a
	// rollback by a device that has seen a newer one.
	old, err := f.rel.Metadata(ctx, f.prod, "1.timestamp.json")
	if err != nil || !old.Versioned || old.Version != 1 {
		t.Fatalf("1.timestamp.json: %+v %v", old, err)
	}
	if _, err := f.chain(t, updatemeta.Floor{Timestamp: 5}); !errors.Is(err, updatemeta.ErrRollback) {
		t.Errorf("a device that saw timestamp 5: %v", err)
	}
}

// Verifies: SEC-050.
// The worker's sweep keeps the timestamp ahead of its expiry hour by
// hour with a fixed clock: it signs once half the lifetime has passed and
// the snapshot once three quarters have, and a chain a device fetches is
// never expired.
func TestSweepResignsBeforeExpiry_SEC_050(t *testing.T) {
	t.Parallel()
	f := newMetadataFixture(t)
	ctx := context.Background()
	keys := newOfflineKeys(t, 3)
	if _, err := f.rel.UploadRoot(ctx, f.org, f.prod, f.ceremony.root(1, keys, 2, keys[0], keys[1])); err != nil {
		t.Fatal(err)
	}
	if _, err := f.rel.RefreshMetadata(ctx, f.org); err != nil {
		t.Fatal(err)
	}
	version := func(role string) int64 {
		t.Helper()
		file, err := f.rel.Metadata(ctx, f.prod, role+".json")
		if err != nil {
			t.Fatal(err)
		}
		return file.Version
	}
	start := f.now
	var resignedAt []time.Duration
	last := version("timestamp")
	for hour := 1; hour <= 8*24; hour++ {
		f.now = start.Add(time.Duration(hour) * time.Hour)
		if _, err := f.rel.RefreshMetadata(ctx, f.org); err != nil {
			t.Fatalf("hour %d: %v", hour, err)
		}
		if v := version("timestamp"); v != last {
			if v != last+1 {
				t.Fatalf("hour %d: the timestamp went from %d to %d", hour, last, v)
			}
			resignedAt = append(resignedAt, time.Duration(hour)*time.Hour)
			last = v
		}
		// The manifest's own refresh (25% of 30 days) is not under test.
		if _, err := f.chain(t, updatemeta.Floor{}); err != nil && !errors.Is(err, updatemeta.ErrExpired) {
			t.Fatalf("hour %d: %v", hour, err)
		} else if errors.Is(err, updatemeta.ErrExpired) {
			t.Fatalf("hour %d: the chain a device fetches has expired: %v", hour, err)
		}
	}
	// First re-signing at 12 hours (half of 24); every 12 hours after.
	if len(resignedAt) < 16 || resignedAt[0] != 12*time.Hour || resignedAt[1] != 24*time.Hour {
		t.Errorf("the timestamp was re-signed at %v", resignedAt)
	}
	// The snapshot was signed again once: when a quarter of its seven days
	// remained, 126 hours in.
	if v := version("snapshot"); v != 2 {
		t.Errorf("snapshot version after eight days: %d, want 2", v)
	}
	snap, err := f.rel.Metadata(ctx, f.prod, "2.snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	d, _ := updatemeta.ParseDocument(snap.Document)
	s, _ := updatemeta.ParseSnapshot(d.Signed)
	issuedAt := start.Add(126 * time.Hour)
	if got, _ := time.Parse(time.RFC3339, s.Expires); !got.Equal(issuedAt.Add(7 * 24 * time.Hour).Truncate(time.Second)) {
		t.Errorf("the second snapshot expires %s, signed at %s", s.Expires, issuedAt)
	}
	// Within the fresh half of its life nothing is signed.
	n, err := f.rel.RefreshMetadata(ctx, f.org)
	if err != nil || n != 0 {
		t.Errorf("an immediate second sweep signed %d files: %v", n, err)
	}
	// Purge keeps the newest of each role and every root.
	f.now = f.now.Add(10 * 24 * time.Hour)
	if _, err := f.rel.PurgeMetadata(ctx, f.org); err != nil {
		t.Fatal(err)
	}
	if _, err := f.rel.Metadata(ctx, f.prod, "1.timestamp.json"); code(err) != plxerr.ResourceNotFound {
		t.Errorf("an expired timestamp survived the purge: %v", err)
	}
	if _, err := f.rel.Metadata(ctx, f.prod, "1.root.json"); err != nil {
		t.Errorf("the purge removed a root: %v", err)
	}
	if _, err := f.rel.Metadata(ctx, f.prod, "timestamp.json"); err != nil {
		t.Errorf("the purge removed the newest timestamp: %v", err)
	}
}

// Verifies: SEC-050.
// A root that has expired stops the worker from signing under it, with
// the metadata error, rather than signing metadata a device would refuse.
func TestExpiredRootStopsSigning_SEC_050(t *testing.T) {
	t.Parallel()
	f := newMetadataFixture(t)
	ctx := context.Background()
	keys := newOfflineKeys(t, 3)
	if _, err := f.rel.UploadRoot(ctx, f.org, f.prod, f.ceremony.root(1, keys, 2, keys[0], keys[1])); err != nil {
		t.Fatal(err)
	}
	if _, err := f.rel.RefreshMetadata(ctx, f.org); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(366 * 24 * time.Hour)
	if _, err := f.rel.RefreshMetadata(ctx, f.org); code(err) != plxerr.UpdateMetadataInvalid {
		t.Errorf("RefreshMetadata under an expired root: %v", err)
	}
}

// Verifies: SEC-051, SEC-140.
// An uploaded root must be verifiable: the first is version 1 and signed
// by its own threshold; a later one is the next version, signed by the
// previous root's threshold and its own. Anything else is refused and
// stores nothing. A device asks for the chain after the root it trusts.
func TestRootUploadAndRotation_SEC_051(t *testing.T) {
	t.Parallel()
	f := newMetadataFixture(t)
	ctx := context.Background()
	old := newOfflineKeys(t, 3)
	fresh := newOfflineKeys(t, 3)
	c := f.ceremony
	upload := func(doc []byte) error { _, err := f.rel.UploadRoot(ctx, f.org, f.prod, doc); return err }

	for name, doc := range map[string][]byte{
		"one signature of two required":    c.root(1, old, 2, old[0]),
		"signed by strangers":              c.root(1, old, 2, fresh[0], fresh[1]),
		"a first root of version 2":        c.root(2, old, 2, old[0], old[1]),
		"a production root of threshold 1": c.root(1, old[:1], 1, old[0]),
		"garbage":                          []byte(`{"signed":1}`),
	} {
		if err := upload(doc); code(err) != plxerr.UpdateMetadataInvalid {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A root that omits the environment's online keys cannot be signed under.
	stranger := newOfflineKeys(t, 1)[0]
	wrong := append(append([]release.OnlineKey{}, c.online[:2]...), release.OnlineKey{
		Role: updatemeta.RoleTimestamp, KeyID: stranger.id(), Algorithm: updatemeta.AlgEd25519, PublicKey: stranger.pub,
	})
	missing := ceremony{t: t, online: wrong, expiry: c.expiry}
	if err := upload(missing.root(1, old, 2, old[0], old[1])); code(err) != plxerr.PreconditionFailed {
		t.Errorf("a root without the timestamp key: %v", err)
	}
	if got, _ := f.rel.RootChain(ctx, f.org, f.prod, 0); len(got) != 0 {
		t.Fatalf("a refused root was stored: %d", len(got))
	}
	if err := upload(c.root(1, old, 2, old[0], old[2])); err != nil {
		t.Fatalf("the first root: %v", err)
	}
	if err := upload(c.root(1, old, 2, old[0], old[2])); code(err) != plxerr.UpdateMetadataInvalid {
		t.Errorf("the same version twice: %v", err)
	}
	rotated := []offlineKey{old[0], old[1], fresh[0]}
	for name, doc := range map[string][]byte{
		"signed by the new keys only": c.root(2, rotated, 2, fresh[0]),
		"signed by one old key":       c.root(2, rotated, 2, old[0]),
		"signed by the old keys only": c.root(2, fresh, 2, old[0], old[1]),
		"a skipped version":           c.root(3, rotated, 2, old[0], old[1]),
	} {
		if err := upload(doc); code(err) != plxerr.UpdateMetadataInvalid {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := upload(c.root(2, rotated, 2, old[0], old[1])); err != nil {
		t.Fatalf("a rotation signed by the old threshold and the new: %v", err)
	}
	// Root 3 replaces the key that was new in 2; its signers are root 2's.
	third := []offlineKey{old[0], fresh[0], fresh[1]}
	if err := upload(c.root(3, third, 2, old[0], fresh[0], fresh[1])); err != nil {
		t.Fatalf("a second rotation: %v", err)
	}

	// Every stored root is audited with its version, threshold and key
	// identifiers, and no key material (SEC-140); refused roots are not.
	var entries []dbgen.AuditLog
	err := f.db.InTx(ctx, storage.Tenant{OrganizationID: f.org}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		entries, err = dbgen.New(tx).ListAuditEntries(ctx, dbgen.ListAuditEntriesParams{OrganizationID: storage.MustUUID(f.org), PageSize: 1000})
		return err //nolint:wrapcheck // a test
	})
	if err != nil {
		t.Fatal(err)
	}
	var uploads []dbgen.AuditLog
	for _, e := range entries {
		if e.Action == string(audit.UpdateMetadataRootUploaded) {
			uploads = append(uploads, e)
		}
	}
	if len(uploads) != 3 {
		t.Fatalf("%d root uploads audited, want 3", len(uploads))
	}
	last := uploads[2]
	wantDetail := "version=3 threshold=2 keys=" + strings.Join(sortedIDs(third), ",")
	if last.TargetKind != "environment" || last.TargetID != f.prod || last.ActorKind != "system" || last.AfterHash == "" ||
		strings.Split(last.Detail, " keys=")[0] != "version=3 threshold=2" || !sameKeys(last.Detail, third) {
		t.Errorf("audit entry: %+v (want detail like %q)", last, wantDetail)
	}
	for _, k := range third {
		if strings.Contains(last.Detail, base64.StdEncoding.EncodeToString(k.pub)) {
			t.Error("the audit entry carries key material")
		}
	}

	for since, want := range map[int64]int{0: 3, 1: 2, 2: 1, 3: 0} {
		got, err := f.rel.RootChain(ctx, f.org, f.prod, since)
		if err != nil || len(got) != want {
			t.Errorf("RootChain since %d: %d files, %v; want %d", since, len(got), err, want)
		}
	}
	// A device that embeds root 1 follows the served chain to root 3.
	all, _ := f.rel.RootChain(ctx, f.org, f.prod, 0)
	first, err := f.rel.Metadata(ctx, f.prod, "1.root.json")
	if err != nil || !bytes.Equal(first.Document, all[0]) {
		t.Fatalf("1.root.json: %v", err)
	}
	d, _ := updatemeta.ParseDocument(all[0])
	trusted, _ := updatemeta.ParseRoot(d.Signed)
	got, err := updatemeta.RootChain(trusted, all[1:], f.now)
	if err != nil || got.Version != 3 {
		t.Errorf("following the chain: %+v %v", got, err)
	}
	latest, err := f.rel.Metadata(ctx, f.prod, "root.json")
	if err != nil || latest.Version != 3 || latest.Versioned {
		t.Errorf("root.json: %+v %v", latest, err)
	}
}

// Verifies: SEC-056, SEC-122.
// GetRootKeys reports each key with its role, its algorithm identifier
// and the type of its environment; the development environment's keys are
// development keys, the production environment's production keys, and a
// development environment may use a root of threshold 1.
func TestKeysCarryAlgorithmAndEnvironmentType_SEC_056(t *testing.T) {
	t.Parallel()
	f := newMetadataFixture(t)
	ctx := context.Background()
	dev := f.envs["development"].ID
	devOnline, err := f.rel.OnlineKeys(ctx, f.org, dev)
	if err != nil {
		t.Fatal(err)
	}
	prodOnline := f.ceremony.online
	seenKeys := map[string]bool{}
	for _, k := range append(append([]release.OnlineKey{}, devOnline...), prodOnline...) {
		if seenKeys[k.KeyID] {
			t.Errorf("key %s serves two roles or environments", k.KeyID)
		}
		seenKeys[k.KeyID] = true
		if k.Algorithm != updatemeta.AlgEd25519 {
			t.Errorf("%s key names algorithm %q", k.Role, k.Algorithm)
		}
	}
	root := newOfflineKeys(t, 1)
	if _, err := f.rel.UploadRoot(ctx, f.org, dev, ceremony{t: t, online: devOnline, expiry: f.ceremony.expiry}.root(1, root, 1, root[0])); err != nil {
		t.Fatalf("a development root of threshold 1: %v", err)
	}
	prodKeys := newOfflineKeys(t, 3)
	if _, err := f.rel.UploadRoot(ctx, f.org, f.prod, f.ceremony.root(1, prodKeys, 2, prodKeys[0], prodKeys[1])); err != nil {
		t.Fatal(err)
	}
	if _, err := f.rel.RefreshMetadata(ctx, f.org); err != nil {
		t.Fatal(err)
	}
	check := func(env, wantType string, roles ...string) {
		t.Helper()
		keys, err := f.rel.RootKeys(ctx, f.org, env)
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for _, k := range keys {
			if k.EnvironmentType != wantType {
				t.Errorf("%s key %s of %s is a %q key, want %q", k.Role, k.KeyID, env, k.EnvironmentType, wantType)
			}
			if !updatemeta.Supported(k.Algorithm) || k.Algorithm == "" {
				t.Errorf("key %s names algorithm %q", k.KeyID, k.Algorithm)
			}
			seen[k.Role] = true
		}
		for _, r := range roles {
			if !seen[r] {
				t.Errorf("%s: no key for the %s role", env, r)
			}
		}
	}
	check(f.prod, updatemeta.EnvProduction, updatemeta.RoleRoot, updatemeta.RoleTargets, updatemeta.RoleSnapshot, updatemeta.RoleTimestamp)
	check(dev, updatemeta.EnvDevelopment, updatemeta.RoleRoot, updatemeta.RoleTargets, updatemeta.RoleSnapshot, updatemeta.RoleTimestamp)

	// A backend that keeps keys on disk never signs production metadata.
	gen := ids{g: uuid7.NewGenerator(time.Now, rand.Reader)}
	onDisk, err := release.NewService(release.Options{
		DB: f.db, Audit: audit.NewLog(gen, nil), Tenancy: f.tenancy, Documents: f.docs, Objects: f.store, IDs: gen, Signer: f.signer,
		Now: func() time.Time { return f.now }, Metadata: release.MetadataOptions{Expiry: updatemeta.DefaultExpiry()},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(13 * time.Hour)
	if _, err := onDisk.RefreshMetadata(ctx, f.org); code(err) != plxerr.PermissionDenied {
		t.Errorf("SEC-056: the file backend signed production metadata: %v", err)
	}
}

// Verifies: SEC-050.
// Only the three stored roles are served, by name or by version, and
// anything else is not found.
func TestMetadataNames_SEC_050(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]struct {
		role    string
		version int64
		ok      bool
	}{
		"root.json": {"root", 0, true}, "timestamp.json": {"timestamp", 0, true}, "snapshot.json": {"snapshot", 0, true},
		"3.root.json": {"root", 3, true}, "12.snapshot.json": {"snapshot", 12, true},
		"targets.json": {}, "0.root.json": {}, "01.root.json": {}, "-1.root.json": {}, "root": {}, "root.json.bak": {},
		"../root.json": {}, "1.2.root.json": {}, ".root.json": {}, "x.root.json": {},
	} {
		role, version, err := release.ParseMetadataName(name)
		if (err == nil) != want.ok || role != want.role || version != want.version {
			t.Errorf("%q: %q %d %v", name, role, version, err)
		}
	}
}

// sortedIDs are the key identifiers of keys.
func sortedIDs(keys []offlineKey) []string {
	ids := make([]string, len(keys))
	for i, k := range keys {
		ids[i] = k.id()
	}
	return ids
}

// sameKeys reports whether a detail line lists exactly keys' identifiers.
func sameKeys(detail string, keys []offlineKey) bool {
	_, list, _ := strings.Cut(detail, " keys=")
	got := strings.Split(list, ",")
	want := sortedIDs(keys)
	slices.Sort(got)
	slices.Sort(want)
	return slices.Equal(got, want)
}

// Verifies: SEC-041, SEC-050, LIM-001.
// A root carries the certificate pins to the device, unchanged; one with
// too few pins is refused; and no metadata file is accepted or served beyond
// the updateMetadata.bytes limit.
func TestRootPinsAndTheSizeLimit_SEC_041(t *testing.T) {
	t.Parallel()
	f := newMetadataFixture(t)
	ctx := context.Background()
	keys := newOfflineKeys(t, 3)
	pinA := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	pinB := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32))

	few := f.ceremony
	few.pins = map[string][]string{"plux.example.com": {pinA}}
	if _, err := f.rel.UploadRoot(ctx, f.org, f.prod, few.root(1, keys, 2, keys[0], keys[1])); code(err) != plxerr.UpdateMetadataInvalid {
		t.Errorf("a root with one pin: %v", err)
	}
	c := f.ceremony
	c.pins = map[string][]string{"plux.example.com": {pinA, pinB}}
	if _, err := f.rel.UploadRoot(ctx, f.org, f.prod, c.root(1, keys, 2, keys[0], keys[1])); err != nil {
		t.Fatalf("a root with pins: %v", err)
	}
	chain, err := f.rel.RootChain(ctx, f.org, f.prod, 0)
	if err != nil || len(chain) != 1 {
		t.Fatalf("the root chain: %d %v", len(chain), err)
	}
	d, err := updatemeta.ParseDocument(chain[0])
	if err != nil {
		t.Fatal(err)
	}
	root, err := updatemeta.ParseRoot(d.Signed)
	if err != nil || len(root.Pins["plux.example.com"]) != 2 {
		t.Errorf("the stored root's pins: %v %v", root.Pins, err)
	}

	small, err := limits.Defaults().Tighten(limits.UpdateMetadataBytes, limits.ScopeInstallation, 500)
	if err != nil {
		t.Fatal(err)
	}
	g := newMetadataFixtureWith(t, small)
	if _, err := g.rel.UploadRoot(ctx, g.org, g.prod, g.ceremony.root(1, keys, 2, keys[0], keys[1])); code(err) != plxerr.UpdateMetadataInvalid {
		t.Errorf("a root over the limit: %v", err)
	}
}
