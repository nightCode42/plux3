// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package release_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nightCode42/plux3/backend/internal/icons"
	"github.com/nightCode42/plux3/backend/internal/icons/fonts"

	"github.com/jackc/pgx/v5"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/cache"
	"github.com/nightCode42/plux3/backend/internal/compiler/media"
	"github.com/nightCode42/plux3/backend/internal/document"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/release"
	"github.com/nightCode42/plux3/backend/internal/schema/uuid7"
	"github.com/nightCode42/plux3/backend/internal/signing"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/objects"
	"github.com/nightCode42/plux3/backend/internal/storage/storagetest"
	"github.com/nightCode42/plux3/backend/internal/tenancy"
)

type ids struct{ g *uuid7.Generator }

func (i ids) New() (string, error) {
	u, err := i.g.New()
	return u.String(), err
}

// queue records the publish jobs and asset jobs a write enqueues.
type queue struct {
	mu   sync.Mutex
	jobs []release.Work
}

func (q *queue) Enqueue(_ context.Context, _ pgx.Tx, j release.Work) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.jobs = append(q.jobs, j)
	return nil
}

func (*queue) EnqueueAsset(context.Context, pgx.Tx, document.AssetJob) error { return nil }

// assetQueue records the asset jobs an upload or import enqueues.
type assetQueue struct {
	mu   sync.Mutex
	jobs []document.AssetJob
}

func (q *assetQueue) Enqueue(_ context.Context, _ pgx.Tx, j document.AssetJob) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.jobs = append(q.jobs, j)
	return nil
}

func (q *assetQueue) take() []document.AssetJob {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := q.jobs
	q.jobs = nil
	return out
}

// sharedCodecs are the image codecs the worker transcodes assets with,
// compiled once for the package's tests: compiling costs seconds.
var sharedCodecs = sync.OnceValues(func() (*media.Codecs, error) { return media.NewCodecs(context.Background()) })

// fixture is the release service with everything it stands on.
type fixture struct {
	db      *storage.DB
	tenancy *tenancy.Service
	docs    *document.Service
	rel     *release.Service
	signer  *signing.File
	store   *objects.Filesystem
	q       *queue
	assets  *assetQueue
	// holdAssets keeps asset jobs queued, as a worker that has not run
	// them yet would.
	holdAssets bool
	now        time.Time
	org        string
	app        string
	envs       map[string]tenancy.Environment
	owner      auth.Principal
	viewer     auth.Principal
	loans      string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	db := storagetest.Open(t)
	backend, err := signing.NewFile(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := objects.NewFilesystem(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	gen := ids{g: uuid7.NewGenerator(time.Now, rand.Reader)}
	log := audit.NewLog(gen, nil)
	codecs, err := sharedCodecs()
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{db: db, signer: backend, store: store, q: &queue{}, assets: &assetQueue{}, now: time.Now(), envs: map[string]tenancy.Environment{}}
	clock := func() time.Time { return f.now }
	authService, err := auth.NewService(auth.Options{DB: db, Audit: log, Cache: cache.NewMemory(nil), Crypter: backend, IDs: gen, VerificationURI: "https://p.example/device"})
	if err != nil {
		t.Fatal(err)
	}
	if f.tenancy, err = tenancy.NewService(tenancy.Options{DB: db, Audit: log, Auth: authService, Crypter: backend, IDs: gen, SigningKeyPrefix: "targets", Now: clock}); err != nil {
		t.Fatal(err)
	}
	if f.docs, err = document.NewService(document.Options{DB: db, Audit: log, Tenancy: f.tenancy, IDs: gen, Objects: store, Jobs: f.assets, Codecs: codecs, Now: clock}); err != nil {
		t.Fatal(err)
	}
	if f.rel, err = release.NewService(release.Options{
		DB: db, Audit: log, Tenancy: f.tenancy, Documents: f.docs, Objects: store, IDs: gen,
		Jobs: f.q, Signer: backend, ProductionSigning: true, Now: clock, DevelopmentDays: 90,
		PublicBaseURL: "https://plux.example.com",
	}); err != nil {
		t.Fatal(err)
	}
	admin, invitation, err := authService.Bootstrap(ctx, "admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authService.AcceptInvitation(ctx, invitation, "Admin", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	adminID := auth.Identity{Kind: auth.KindUser, ID: admin.ID, UserID: admin.ID, Display: "Admin", SecondFactor: true, InstallationAdmin: true}
	o, err := f.tenancy.CreateOrganization(ctx, adminID, "acme", "Acme")
	if err != nil {
		t.Fatal(err)
	}
	f.org = o.ID
	if f.owner, err = authService.Resolve(ctx, adminID, f.org); err != nil {
		t.Fatal(err)
	}
	a, err := f.tenancy.CreateApp(ctx, f.owner, "demo", "Demo")
	if err != nil {
		t.Fatal(err)
	}
	f.app = a.ID
	envs, err := f.tenancy.ListEnvironments(ctx, f.owner, f.app, tenancy.Page{Size: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range envs {
		f.envs[e.Key] = e
	}
	m, _, err := f.tenancy.AddMember(ctx, f.owner, "", "viewer@example.com", "viewer")
	if err != nil {
		t.Fatal(err)
	}
	if f.viewer, err = authService.Resolve(ctx, auth.Identity{Kind: auth.KindUser, ID: m.UserID, UserID: m.UserID, Display: "v", SecondFactor: true}, f.org); err != nil {
		t.Fatal(err)
	}
	files := project(t)
	if _, err := f.docs.Import(ctx, f.owner, f.app, "", "s", files); err != nil {
		t.Fatalf("Import: %v", err)
	}
	plugins, err := f.docs.ListPlugins(ctx, f.owner, f.app, storage.Cursor{}, 10)
	if err != nil || len(plugins) != 1 {
		t.Fatalf("plugins: %v %v", plugins, err)
	}
	f.loans = plugins[0].ID
	return f
}

// project reads the loan calculator, with its assets.
func project(t *testing.T) []document.File {
	t.Helper()
	root := filepath.Join("..", "..", "..", "schema", "testdata", "documents", "loan-calculator")
	var out []document.File
	err := fs.WalkDir(os.DirFS(root), ".", func(path string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		data, err := os.ReadFile(filepath.Join(root, path))
		out = append(out, document.File{Path: path, Content: data})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// publish publishes a draft and runs the job the worker would.
func (f *fixture) publish(t *testing.T, pluginID string, ack bool) release.PublishJob {
	t.Helper()
	ctx := context.Background()
	job, err := f.rel.Publish(ctx, f.owner, release.PublishRequest{
		AppID: f.app, PluginID: pluginID, EnvironmentID: f.envs["development"].ID, AcknowledgeWarnings: ack, Label: "l",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if job.State != release.StateQueued || job.Revision == 0 {
		t.Errorf("queued %+v", job)
	}
	f.run(t)
	got, err := f.rel.GetPublishJob(ctx, f.viewer, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// run runs every queued job the way the worker would: asset jobs first,
// unless they are held, and a publish waiting for assets stays queued.
func (f *fixture) run(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	for {
		if !f.holdAssets {
			for _, j := range f.assets.take() {
				if err := f.docs.ProcessAsset(ctx, j); err != nil {
					t.Fatalf("ProcessAsset: %v", err)
				}
			}
		}
		f.q.mu.Lock()
		jobs := f.q.jobs
		f.q.jobs = nil
		f.q.mu.Unlock()
		if len(jobs) == 0 {
			return
		}
		var snoozed []release.Work
		for _, w := range jobs {
			var err error
			switch j := w.(type) {
			case release.Job:
				err = f.rel.RunPublish(ctx, j)
			case release.ManifestJob:
				err = f.rel.SignManifest(ctx, j)
			case release.DeltaJob:
				err = f.rel.PrecomputeDeltas(ctx, j)
			}
			if _, ok := errors.AsType[*release.AssetsPendingError](err); ok && f.holdAssets {
				snoozed = append(snoozed, w)
				continue
			}
			if err != nil {
				t.Fatalf("%T: %v", w, err)
			}
		}
		if len(snoozed) > 0 {
			f.q.mu.Lock()
			f.q.jobs = append(f.q.jobs, snoozed...)
			f.q.mu.Unlock()
			return
		}
	}
}

func code(err error) plxerr.Code {
	var e *plxerr.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}

func hasCode(ds plxerr.Diagnostics, c plxerr.Code) bool {
	for _, d := range ds {
		if d.Code == c {
			return true
		}
	}
	return false
}

// Verifies: SRV-050, SRV-051, SRV-052, REL-001.
// A publish freezes a draft, compiles it in a job, signs the bundle hash
// with the environment's key and records an immutable, numbered version;
// errors, and warnings nobody acknowledged, fail it with nothing recorded;
// a cancelled job records nothing.
func TestPublish(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	appJob := f.publish(t, "", false)
	if appJob.State != release.StateSucceeded || appJob.Version != 1 || appJob.Percent != 100 {
		t.Fatalf("the app publish: %+v", appJob)
	}
	job := f.publish(t, f.loans, false)
	if job.State != release.StateSucceeded || job.Version != 1 {
		t.Fatalf("the plugin publish: %+v", job)
	}
	v, err := f.rel.GetVersion(ctx, f.viewer, f.loans, 1)
	if err != nil {
		t.Fatal(err)
	}
	pub, keyID, err := f.signer.PublicKey(ctx, f.envs["development"].SigningKeyRef)
	if err != nil {
		t.Fatal(err)
	}
	sum, _ := hex.DecodeString(v.BundleSHA256)
	if v.KeyID != keyID || v.Algorithm != "ed25519" || !ed25519.Verify(pub, sum, v.Signature) {
		t.Errorf("the signature does not verify: %+v", v)
	}
	if _, _, err := f.store.Get(ctx, "bundles/"+v.BundleSHA256[:2]+"/"+v.BundleSHA256); err != nil {
		t.Errorf("the bundle is not stored: %v", err)
	}
	// A warning stops a publish unless acknowledged.
	if _, err := f.tenancy.SetPluginLimit(ctx, f.owner, f.app, f.loans, "bundle.pluginSize", v.BundleSize*100/85); err != nil {
		t.Fatal(err)
	}
	warned := f.publish(t, f.loans, false)
	if warned.State != release.StateFailed || !hasCode(warned.Diagnostics, plxerr.WarningsNotAcknowledged) {
		t.Errorf("an unacknowledged warning: %+v", warned)
	}
	if acked := f.publish(t, f.loans, true); acked.State != release.StateSucceeded || acked.Version != 2 {
		t.Errorf("an acknowledged warning: %+v", acked)
	}
	// An error fails the publish; nothing is recorded.
	if _, _, err := f.docs.AcquireLock(ctx, f.owner, f.app, f.loans, "edit", false); err != nil {
		t.Fatal(err)
	}
	page, err := f.docs.GetDocument(ctx, f.owner, f.app, f.loans, "plugins/loans/pages/result.page.json")
	if err != nil {
		t.Fatal(err)
	}
	broken := strings.Replace(string(page.Content), `"type":"Scaffold"`, `"type":"NoSuchWidget"`, 1)
	if _, err := f.docs.PutDocument(ctx, f.owner, f.app, f.loans, "edit", page.Path, []byte(broken), page.Revision); err != nil {
		t.Fatal(err)
	}
	failed := f.publish(t, f.loans, true)
	if failed.State != release.StateFailed || !failed.Diagnostics.HasErrors() || failed.Version != 0 {
		t.Errorf("a publish with errors: %+v", failed)
	}
	versions, err := f.rel.ListVersions(ctx, f.viewer, f.loans, 0, 10)
	if err != nil || len(versions) != 2 || versions[0].Version != 2 {
		t.Errorf("versions: %+v %v", versions, err)
	}
	// Cancelled before the worker ran: nothing happens.
	queued, err := f.rel.Publish(ctx, f.owner, release.PublishRequest{AppID: f.app, EnvironmentID: f.envs["development"].ID})
	if err != nil {
		t.Fatal(err)
	}
	if c, err := f.rel.CancelPublishJob(ctx, f.owner, queued.ID); err != nil || c.State != release.StateCancelled {
		t.Errorf("cancel: %+v %v", c, err)
	}
	f.run(t)
	if _, err := f.rel.CancelPublishJob(ctx, f.owner, queued.ID); code(err) != plxerr.PreconditionFailed {
		t.Errorf("cancelling a finished job: %v", err)
	}
	jobs, err := f.rel.ListPublishJobs(ctx, f.viewer, f.app, "", storage.Cursor{}, 20)
	if err != nil || len(jobs) != 6 || jobs[0].ID != queued.ID {
		t.Errorf("jobs: %d %v", len(jobs), err)
	}
	if _, err := f.rel.Publish(ctx, f.viewer, release.PublishRequest{AppID: f.app, EnvironmentID: f.envs["development"].ID}); code(err) != plxerr.PermissionDenied {
		t.Errorf("a viewer published: %v", err)
	}
	if _, err := f.rel.Publish(ctx, f.owner, release.PublishRequest{AppID: f.app, EnvironmentID: f.app}); code(err) != plxerr.ResourceNotFound {
		t.Errorf("a publish to no environment: %v", err)
	}
	if _, err := f.rel.Publish(ctx, f.owner, release.PublishRequest{AppID: f.app, EnvironmentID: f.envs["development"].ID, Revision: 1 << 40}); code(err) != plxerr.OutOfRange {
		t.Errorf("a revision from the future: %v", err)
	}
}

// Verifies: THM-005, CMP-032.
// A publish subsets the icon fonts of the icons a bundle uses and stores
// them as assets, where devices and baselines fetch the files the bundle
// lists.
func TestPublishStoresIconFonts(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	if _, _, err := f.docs.AcquireLock(ctx, f.owner, f.app, f.loans, "edit", false); err != nil {
		t.Fatal(err)
	}
	page, err := f.docs.GetDocument(ctx, f.owner, f.app, f.loans, "plugins/loans/pages/result.page.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(page.Content, &doc); err != nil {
		t.Fatal(err)
	}
	root := doc["root"].(map[string]any)
	slots, _ := root["slots"].(map[string]any)
	slots["floatingActionButton"] = map[string]any{
		"id": "01a0c450-6c00-7fff-8000-0000000000f1", "type": "Icon",
		"props": map[string]any{"icon": map[string]any{"name": "home"}},
	}
	edited, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.docs.PutDocument(ctx, f.owner, f.app, f.loans, "edit", page.Path, edited, page.Revision); err != nil {
		t.Fatal(err)
	}
	if job := f.publish(t, f.loans, true); job.State != release.StateSucceeded {
		t.Fatalf("the publish: %+v", job)
	}
	font, err := fonts.Build(icons.Material, []string{"home"})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(font)
	hash := hex.EncodeToString(sum[:])
	stored, _, err := f.store.Get(ctx, "assets/"+hash[:2]+"/"+hash)
	if err != nil {
		t.Fatalf("the icon font is not stored: %v", err)
	}
	if !bytes.Equal(stored, font) {
		t.Error("the stored icon font differs")
	}
}

// Verifies: CMP-030, REL-003, SRV-050.
// A publish waits for the app's image assets to have their variants
// instead of compiling without them, so the release built from it
// recompiles to the same bundles; it fails with PLX-8053 once
// publish.assetWait has passed since it was queued.
func TestPublishWaitsForAssets(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	f.holdAssets = true
	job := f.publish(t, "", false)
	if job.State != release.StateRunning || job.Stage != "assets" {
		t.Fatalf("a publish with an asset pending: %+v", job)
	}
	err := f.rel.RunPublish(ctx, release.Job{OrganizationID: f.org, JobID: job.ID})
	pending, ok := errors.AsType[*release.AssetsPendingError](err)
	if !ok || pending.Pending != 1 || pending.RetryAfter <= 0 || pending.RetryAfter > 2*time.Second || pending.Error() == "" {
		t.Fatalf("waiting: %v", err)
	}
	// The asset job runs; the publish goes on, and the release made from
	// it recompiles to the same bundles.
	f.holdAssets = false
	f.run(t)
	if job, err = f.rel.GetPublishJob(ctx, f.viewer, job.ID); err != nil || job.State != release.StateSucceeded {
		t.Fatalf("after the asset job: %+v %v", job, err)
	}
	f.publish(t, f.loans, false)
	if _, diags, err := f.rel.CreateRelease(ctx, f.owner, f.app, f.envs["development"].ID, nil, ""); err != nil || len(diags) != 0 {
		t.Fatalf("the release: %v %v", diags, err)
	}
	// An asset that stays pending fails the publish after the wait.
	g := newFixture(t)
	g.holdAssets = true
	stuck := g.publish(t, "", false)
	if stuck.State != release.StateRunning {
		t.Fatalf("waiting: %+v", stuck)
	}
	g.now = g.now.Add(11 * time.Minute)
	g.run(t)
	if stuck, err = g.rel.GetPublishJob(ctx, g.viewer, stuck.ID); err != nil || stuck.State != release.StateFailed || !hasCode(stuck.Diagnostics, plxerr.AssetsNotReady) {
		t.Fatalf("after the wait: %+v %v", stuck, err)
	}
}

// Verifies: REL-002, REL-003, REL-004, REL-005, REL-006, REL-007, REL-080, REL-081, LIM-005.
// A release holds one version of the app and each plugin, checked as a
// set; it is promoted between environments by pointing channels at it,
// rolled back as a new sequence, described by a changelog, and purged
// from development after its retention unless it reached production.
func TestReleases(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	dev, staging, prod := f.envs["development"].ID, f.envs["staging"].ID, f.envs["production"].ID
	if _, diags, err := f.rel.CreateRelease(ctx, f.owner, f.app, dev, nil, ""); err != nil || !hasCode(diags, plxerr.PluginNotPublished) {
		t.Fatalf("a release with nothing published: %v %v", diags, err)
	}
	f.publish(t, "", false)
	f.publish(t, f.loans, false)
	first, diags, err := f.rel.CreateRelease(ctx, f.owner, f.app, dev, nil, "First")
	if err != nil || diags.HasErrors() || first.Sequence != 1 || len(first.VersionIDs) != 1 || first.AppBundleSHA256 == "" {
		t.Fatalf("CreateRelease: %+v %v %v", first, diags, err)
	}
	entries, notes, err := f.rel.Changelog(ctx, f.viewer, f.app, 1, 0)
	if err != nil || notes != "First" || len(entries) == 0 {
		t.Fatalf("the first changelog: %+v %v", entries, err)
	}
	for _, e := range entries {
		if e.Change != "added" {
			t.Errorf("first release entry %+v", e)
		}
	}
	// Change a page and a translation, publish the plugin and the app.
	if _, _, err := f.docs.AcquireLock(ctx, f.owner, f.app, f.loans, "edit", false); err != nil {
		t.Fatal(err)
	}
	page, err := f.docs.GetDocument(ctx, f.owner, f.app, f.loans, "plugins/loans/pages/result.page.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(page.Content, &doc); err != nil {
		t.Fatal(err)
	}
	doc["x-note"] = "changed"
	changed, _ := json.Marshal(doc)
	if _, err := f.docs.PutDocument(ctx, f.owner, f.app, f.loans, "edit", page.Path, changed, page.Revision); err != nil {
		t.Fatal(err)
	}
	f.publish(t, f.loans, false)
	second, _, err := f.rel.CreateRelease(ctx, f.owner, f.app, dev, map[string]int64{"loans": 2}, "Second")
	if err != nil || second.Sequence != 2 {
		t.Fatalf("the second release: %+v %v", second, err)
	}
	entries, _, err = f.rel.Changelog(ctx, f.viewer, f.app, 2, 0)
	if err != nil || len(entries) != 1 || entries[0].Change != "changed" || entries[0].Kind != "page" || entries[0].Name != "result" || entries[0].PluginKey != "loans" {
		t.Errorf("the second changelog: %+v %v", entries, err)
	}
	// Promotion copies nothing and points channels.
	if _, err := f.rel.PromoteRelease(ctx, f.owner, f.app, 2, staging, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.rel.PromoteRelease(ctx, f.owner, f.app, 2, prod, "production"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.rel.PromoteRelease(ctx, f.owner, f.app, 2, prod, "beta"); code(err) != plxerr.ResourceNotFound {
		t.Errorf("an unknown channel: %v", err)
	}
	if _, err := f.rel.PromoteRelease(ctx, f.viewer, f.app, 2, prod, ""); code(err) != plxerr.PermissionDenied {
		t.Errorf("a viewer promoted: %v", err)
	}
	channels, err := f.tenancy.ListChannels(ctx, f.owner, prod, tenancy.Page{Size: 10})
	if err != nil || len(channels) != 1 || channels[0].ReleaseSequence != 2 {
		t.Errorf("the production channel: %+v %v", channels, err)
	}
	// Rollback is a new sequence with the old content.
	back, err := f.rel.RollbackRelease(ctx, f.owner, f.app, prod, "", 1, "")
	if err != nil || back.Sequence != 3 || back.RollbackOf != 1 || back.AppBundleSHA256 != first.AppBundleSHA256 || back.VersionIDs[0] != first.VersionIDs[0] {
		t.Fatalf("rollback: %+v %v", back, err)
	}
	channels, _ = f.tenancy.ListChannels(ctx, f.owner, prod, tenancy.Page{Size: 10})
	if channels[0].ReleaseSequence != 3 {
		t.Errorf("the channel after rollback: %d", channels[0].ReleaseSequence)
	}
	got, versions, err := f.rel.GetRelease(ctx, f.viewer, f.app, 3)
	if err != nil || got.RollbackOf != 1 || len(versions) != 2 {
		t.Errorf("GetRelease: %+v %d %v", got, len(versions), err)
	}
	list, err := f.rel.ListReleases(ctx, f.viewer, f.app, "", 0, 10)
	if err != nil || len(list) != 3 || list[0].Sequence != 3 {
		t.Errorf("ListReleases: %+v %v", list, err)
	}
	inProd, err := f.rel.ListReleases(ctx, f.viewer, f.app, prod, 0, 10)
	if err != nil || len(inProd) != 1 {
		t.Errorf("releases created in production: %+v %v", inProd, err)
	}
	f.tenancy.RegisterUsage(f.rel.Usage)
	appUsage, err := f.tenancy.ListAppLimits(ctx, f.viewer, f.app)
	if err != nil || usageOf(appUsage, "app.plugins") != 1 || usageOf(appUsage, "release.appSize") == 0 {
		t.Errorf("app usage: %+v %v", appUsage, err)
	}
	pluginUsage, err := f.tenancy.ListPluginLimits(ctx, f.viewer, f.app, f.loans)
	if err != nil || usageOf(pluginUsage, "plugin.pages") != 2 || usageOf(pluginUsage, "bundle.pluginSize") == 0 {
		t.Errorf("plugin usage: %+v %v", pluginUsage, err)
	}
	c, err := f.rel.Compatibility(ctx, f.viewer, f.app, 3)
	if err != nil || c.MinRuntime == "" || c.IncompatibleDevices != 0 {
		t.Errorf("compatibility: %+v %v", c, err)
	}
	// A plugin version compiled against other app sources is refused.
	if err := f.db.InTx(ctx, storage.Tenant{OrganizationID: f.org}, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE plugin_versions SET bundle_sha256 = '\x00' WHERE plugin_key = 'loans' AND version = 1`)
		if err == nil && tag.RowsAffected() != 1 {
			err = errors.New("no version was changed")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, diags, err := f.rel.CreateRelease(ctx, f.owner, f.app, dev, map[string]int64{"loans": 1}, ""); err != nil || !hasCode(diags, plxerr.ReleaseInconsistent) {
		t.Errorf("an inconsistent set: %v %v", diags, err)
	}
	if _, _, err := f.rel.CreateRelease(ctx, f.owner, f.app, dev, map[string]int64{"nope": 1}, ""); code(err) != plxerr.ResourceNotFound {
		t.Errorf("an unknown plugin: %v", err)
	}
	// Retention: release 1, which no channel points at and never reached
	// production, goes; 2 reached production and 3 is the newest.
	f.now = f.now.AddDate(0, 0, 91)
	n, err := f.rel.PurgeReleases(ctx, f.org)
	if err != nil || n != 1 {
		t.Fatalf("purge: %d %v", n, err)
	}
	if _, _, err := f.rel.GetRelease(ctx, f.viewer, f.app, 1); code(err) != plxerr.ResourceNotFound {
		t.Errorf("release 1 survived: %v", err)
	}
	if _, _, err := f.rel.GetRelease(ctx, f.viewer, f.app, 2); err != nil {
		t.Errorf("the production release was purged: %v", err)
	}
}

// usageOf reads the measured usage of a limit.
func usageOf(us []tenancy.LimitUsage, key string) int64 {
	for _, u := range us {
		if string(u.Key) == key {
			return u.Value
		}
	}
	return -1
}
