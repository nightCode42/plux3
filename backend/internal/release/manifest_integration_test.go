// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package release_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/delta"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/release"
	"github.com/nightCode42/plux3/backend/internal/schema/uuid7"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/objects"
	"github.com/nightCode42/plux3/backend/internal/tenancy"
)

// Verifies: BND-002, SEC-056, REL-020, REL-021, REL-022, REL-023, REL-024, REL-030, REL-031, REL-032, REL-033, NFR-005, NFR-006.
// A promoted release gets a signed manifest; a device holding the old
// app bundle is told to fetch a small delta that rebuilds the new one;
// an unchanged device gets "not modified"; switches reach the manifest.
func TestManifestAndDeltas(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	prod := f.envs["production"]
	f.publish(t, "", false)
	f.publish(t, f.loans, false)
	first, _, err := f.rel.CreateRelease(ctx, f.owner, f.app, f.envs["development"].ID, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.rel.PromoteRelease(ctx, f.owner, f.app, first.Sequence, prod.ID, ""); err != nil {
		t.Fatal(err)
	}
	// The channel says when devices get the promotion: once the worker
	// has signed its manifest (what `plux publish --promote` waits for).
	signed := func() int64 {
		t.Helper()
		chs, err := f.tenancy.ListChannels(ctx, f.owner, prod.ID, tenancy.Page{Size: 10})
		if err != nil || len(chs) != 1 || chs[0].ReleaseSequence != first.Sequence {
			t.Fatalf("ListChannels: %+v %v", chs, err)
		}
		return chs[0].SignedReleaseSequence
	}
	if n := signed(); n != 0 {
		t.Errorf("signed before the worker ran: %d", n)
	}
	f.run(t)
	if n := signed(); n != first.Sequence {
		t.Errorf("signed after the worker ran: %d, want %d", n, first.Sequence)
	}
	req := release.ManifestRequest{OrganizationID: f.org, AppID: f.app, EnvironmentID: prod.ID}
	m1, err := f.rel.GetManifest(ctx, req)
	if err != nil {
		t.Fatalf("GetManifest: %v", err)
	}
	doc := m1.Document
	if doc.Type != "manifest" || doc.Role != "targets" || doc.App != f.app || doc.Environment != "production" ||
		doc.Channel != "production" || doc.ReleaseSequence != first.Sequence || len(doc.Plugins) != 1 ||
		doc.Plugins[0].Key != "loans" || doc.AppBundle.Hash != "sha256:"+first.AppBundleSHA256 || doc.Expires <= doc.IssuedAt {
		t.Fatalf("the manifest: %+v", doc)
	}
	for key, step := range m1.Plan {
		if step.Action != release.SyncFull || !strings.HasPrefix(step.URL, "https://plux.example.com/v1/objects/bundles/") {
			t.Errorf("a device with nothing installed, %q: %+v", key, step)
		}
	}
	// The signature verifies with the published key.
	keys, err := f.rel.RootKeys(ctx, f.org, prod.ID)
	if err != nil || len(keys) != 1 || keys[0].Algorithm != "ed25519" || len(m1.Signatures) != 1 || m1.Signatures[0].KeyID != keys[0].KeyID {
		t.Fatalf("keys %+v, signatures %+v, %v", keys, m1.Signatures, err)
	}
	sig, _ := base64.StdEncoding.DecodeString(m1.Signatures[0].Signature)
	if !ed25519.Verify(keys[0].PublicKey, m1.Signed, sig) {
		t.Fatal("the manifest signature does not verify")
	}
	// Change one translated message and ship it.
	if _, _, err := f.docs.AcquireLock(ctx, f.owner, f.app, "", "edit", false); err != nil {
		t.Fatal(err)
	}
	en, err := f.docs.GetDocument(ctx, f.owner, f.app, "", "translations/en.json")
	if err != nil {
		t.Fatal(err)
	}
	changed := bytes.Replace(en.Content, []byte("Your schedule"), []byte("Your repayment plan"), 1)
	if bytes.Equal(changed, en.Content) {
		t.Fatal("the message was not found")
	}
	if _, err := f.docs.PutDocument(ctx, f.owner, f.app, "", "edit", en.Path, changed, en.Revision); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", f.loans} {
		if j := f.publish(t, id, false); j.State != release.StateSucceeded {
			t.Fatalf("publish %q: %+v", id, j)
		}
	}
	second, _, err := f.rel.CreateRelease(ctx, f.owner, f.app, f.envs["development"].ID, map[string]int64{"": 2, "loans": 2}, "")
	if err != nil {
		t.Fatalf("the second release: %+v %v", second, err)
	}
	if _, err := f.rel.PromoteRelease(ctx, f.owner, f.app, second.Sequence, prod.ID, ""); err != nil {
		t.Fatal(err)
	}
	f.run(t)
	oldApp, _ := hex.DecodeString(first.AppBundleSHA256)
	oldPlugin := mustHex(t, strings.TrimPrefix(doc.Plugins[0].Hash, "sha256:"))
	if second.AppBundleSHA256 == first.AppBundleSHA256 {
		t.Fatal("the app bundle did not change")
	}
	req.InstalledSequence = first.Sequence
	req.Installed = map[string][]byte{"": oldApp, "loans": oldPlugin}
	m2, err := f.rel.GetManifest(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if m2.Plan["loans"].Action != release.SyncKeep {
		t.Errorf("the unchanged plugin bundle: %+v", m2.Plan["loans"])
	}
	step := m2.Plan[""]
	newApp := mustHex(t, second.AppBundleSHA256)
	if step.Action != release.SyncDelta || step.From != "sha256:"+first.AppBundleSHA256 || step.Size > 2048 ||
		!strings.HasPrefix(step.URL, "https://plux.example.com/v1/objects/deltas/") {
		t.Fatalf("the changed app bundle: %+v", step)
	}
	// The delta, precomputed at publish, rebuilds the new bundle.
	d, found, err := f.rel.Delta(ctx, f.org, oldApp, newApp)
	if err != nil || !found || !delta.Worthwhile(d.Size, d.FullSize) {
		t.Fatalf("Delta: %+v %v %v", d, found, err)
	}
	rebuilt, err := delta.Apply(get(t, f, objects.KindBundle, oldApp), get(t, f, objects.KindDelta, d.DeltaSha256), 1<<24)
	if err != nil || !bytes.Equal(rebuilt, get(t, f, objects.KindBundle, newApp)) {
		t.Fatalf("Apply: %v", err)
	}
	t.Logf("NFR-005: one translated message changed, delta %d B, full compressed bundle %d B", d.Size, d.FullSize)
	// The same inputs give the same answer; the ETag makes it cheap.
	again, err := f.rel.GetManifest(ctx, req)
	if err != nil || again.ETag != m2.ETag || !bytes.Equal(again.Signed, m2.Signed) {
		t.Errorf("the manifest is not deterministic: %v", err)
	}
	// The ETag names the manifest: a device that still holds the old
	// bundles (its download failed) gets the plan again; once it holds
	// the new ones, the ETag of the response it synced from is "not
	// modified" (REL-031).
	req.IfNoneMatch = m2.ETag
	stale, err := f.rel.GetManifest(ctx, req)
	if err != nil || stale.NotModified || stale.Plan[""].Action != release.SyncDelta {
		t.Errorf("a device without the new bundles: %+v %v", stale.Plan, err)
	}
	req.Installed = map[string][]byte{"": newApp, "loans": oldPlugin}
	nm, err := f.rel.GetManifest(ctx, req)
	if err != nil || !nm.NotModified || nm.Signed != nil || nm.ETag != m2.ETag {
		t.Errorf("not modified: %+v %v", nm, err)
	}
	req.Installed = map[string][]byte{"": newApp}
	if partial, err := f.rel.GetManifest(ctx, req); err != nil || partial.NotModified {
		t.Errorf("a device without a plugin: %+v %v", partial.NotModified, err)
	}
	// The digest of the installed bundles stands for them on an
	// up-to-date check (NFR-006); any other digest, or a changed
	// manifest, asks the device for its bundles, which the plan needs.
	req.Installed = nil
	req.InstalledDigest = release.InstalledDigest(map[string][]byte{"": newApp, "loans": oldPlugin})
	if nm, err := f.rel.GetManifest(ctx, req); err != nil || !nm.NotModified || nm.InstalledRequired {
		t.Errorf("not modified by digest: %+v %v", nm, err)
	}
	req.InstalledDigest = release.InstalledDigest(map[string][]byte{"": newApp})
	if ask, err := f.rel.GetManifest(ctx, req); err != nil || ask.NotModified || !ask.InstalledRequired || ask.Signed != nil {
		t.Errorf("a digest of other bundles: %+v %v", ask, err)
	}
	req.IfNoneMatch, req.InstalledDigest = "an older manifest", release.InstalledDigest(map[string][]byte{"": newApp, "loans": oldPlugin})
	if ask, err := f.rel.GetManifest(ctx, req); err != nil || !ask.InstalledRequired || ask.ETag != m2.ETag {
		t.Errorf("a digest with an older ETag: %+v %v", ask, err)
	}
	req.InstalledDigest = nil
	// A bundle the organisation never published gets the full bundle.
	req.IfNoneMatch = ""
	req.Installed = map[string][]byte{"": bytes.Repeat([]byte{7}, 32)}
	stranger, err := f.rel.GetManifest(ctx, req)
	if err != nil || stranger.Plan[""].Action != release.SyncFull {
		t.Errorf("an unknown installed bundle: %+v %v", stranger.Plan, err)
	}
	// Switches reach devices with the next manifest.
	if _, err := f.rel.SetControl(ctx, f.viewer, release.Control{AppID: f.app, EnvironmentID: prod.ID}); code(err) != plxerr.PermissionDenied {
		t.Errorf("a viewer set a switch: %v", err)
	}
	if _, err := f.rel.SetControl(ctx, f.owner, release.Control{AppID: f.app, EnvironmentID: prod.ID, KillSwitchPlugins: []string{"loans", "loans"}, MandatoryUpdate: true, Message: "Update"}); err != nil {
		t.Fatal(err)
	}
	f.run(t)
	c, err := f.rel.GetControl(ctx, f.viewer, f.app, prod.ID, "")
	if err != nil || len(c.KillSwitchPlugins) != 1 || !c.MandatoryUpdate {
		t.Errorf("GetControl: %+v %v", c, err)
	}
	m3, err := f.rel.GetManifest(ctx, req)
	if err != nil || len(m3.Document.Control.KillSwitches) != 1 || !m3.Document.Control.Mandatory || m3.ETag == stranger.ETag {
		t.Errorf("the switched manifest: %+v %v", m3.Document.Control, err)
	}
	// Refresh signs only channels near expiry.
	if n, err := f.rel.RefreshManifests(ctx, f.org); err != nil || n != 0 {
		t.Errorf("refresh of fresh manifests: %d %v", n, err)
	}
	f.now = f.now.Add(release.ManifestLifetime)
	if n, err := f.rel.RefreshManifests(ctx, f.org); err != nil || n != 1 {
		t.Errorf("refresh near expiry: %d %v", n, err)
	}
	if n, err := f.rel.PurgeManifests(ctx, f.org); err != nil || n == 0 {
		t.Errorf("purge: %d %v", n, err)
	}
	if _, err := f.rel.GetManifest(ctx, req); err != nil {
		t.Errorf("after purge: %v", err)
	}
	// A backend that keeps keys on disk never signs for production.
	onDisk, err := release.NewService(release.Options{
		DB: f.db, Audit: audit.NewLog(ids{g: uuid7.NewGenerator(time.Now, rand.Reader)}, nil), Tenancy: f.tenancy, Documents: f.docs,
		Objects: f.store, IDs: ids{g: uuid7.NewGenerator(time.Now, rand.Reader)}, Signer: f.signer,
	})
	if err != nil {
		t.Fatal(err)
	}
	channels, err := f.tenancy.ListChannels(ctx, f.owner, prod.ID, tenancy.Page{Size: 10})
	if err != nil || len(channels) != 1 {
		t.Fatalf("channels: %v", err)
	}
	if err := onDisk.SignManifest(ctx, release.ManifestJob{OrganizationID: f.org, ChannelID: channels[0].ID}); code(err) != plxerr.PermissionDenied {
		t.Errorf("SEC-056: the file backend signed a production manifest: %v", err)
	}
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func get(t *testing.T, f *fixture, kind objects.Kind, sum []byte) []byte {
	t.Helper()
	key, err := objects.Key(kind, hex.EncodeToString(sum))
	if err != nil {
		t.Fatal(err)
	}
	data, _, err := f.store.Get(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// Verifies: REL-031.
// The signing job locks the channel, so a job that starts while a
// promotion is in flight waits for it and signs the release it commits.
// Reading the channel without the lock, it would sign the release
// before, and store that manifest after the newer one's.
func TestSigningWaitsForAPromotionInFlight(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	prod := f.envs["production"]
	f.publish(t, "", false)
	f.publish(t, f.loans, false)
	dev := f.envs["development"].ID
	first, _, err := f.rel.CreateRelease(ctx, f.owner, f.app, dev, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := f.rel.CreateRelease(ctx, f.owner, f.app, dev, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.rel.PromoteRelease(ctx, f.owner, f.app, first.Sequence, prod.ID, ""); err != nil {
		t.Fatal(err)
	}
	f.q.mu.Lock()
	var job release.ManifestJob
	for _, w := range f.q.jobs {
		if j, ok := w.(release.ManifestJob); ok {
			job = j
		}
	}
	f.q.jobs = nil
	f.q.mu.Unlock()
	if job.ChannelID == "" {
		t.Fatal("the promotion queued no signing")
	}
	done := make(chan error, 1)
	// A promotion of the second release, holding the channel's lock
	// while the job starts.
	err = f.db.InTx(ctx, storage.Tenant{OrganizationID: f.org}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT 1 FROM channels WHERE id = $1 FOR UPDATE", job.ChannelID); err != nil {
			return err
		}
		go func() { done <- f.rel.SignManifest(ctx, job) }()
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			var waiting int
			err := f.db.InTx(ctx, storage.Tenant{OrganizationID: f.org}, func(ctx context.Context, q pgx.Tx) error {
				return q.QueryRow(ctx, "SELECT count(*) FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND datname = current_database() AND query LIKE '%GetChannelByIDForUpdate%'").Scan(&waiting)
			})
			if err != nil {
				return err
			}
			if waiting > 0 {
				break
			}
			if time.Now().After(deadline) {
				return errors.New("the signing job did not wait for the channel's lock")
			}
		}
		_, err := tx.Exec(ctx, "UPDATE channels SET release_sequence = $2 WHERE id = $1", job.ChannelID, second.Sequence)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("SignManifest: %v", err)
	}
	m, err := f.rel.GetManifest(ctx, release.ManifestRequest{OrganizationID: f.org, AppID: f.app, EnvironmentID: prod.ID})
	if err != nil || m.Document.ReleaseSequence != second.Sequence {
		t.Fatalf("the manifest names release %d, want %d (%v)", m.Document.ReleaseSequence, second.Sequence, err)
	}
}
