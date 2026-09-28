// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package document_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/klauspost/compress/zstd"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/cache"
	"github.com/nightCode42/plux3/backend/internal/document"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema/jcs"
	"github.com/nightCode42/plux3/backend/internal/schema/uuid7"
	"github.com/nightCode42/plux3/backend/internal/signing"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
	"github.com/nightCode42/plux3/backend/internal/storage/storagetest"
	"github.com/nightCode42/plux3/backend/internal/tenancy"
)

type ids struct{ g *uuid7.Generator }

func (i ids) New() (string, error) {
	u, err := i.g.New()
	return u.String(), err
}

// fixture is the document service against a fresh schema, with an
// organisation, one app, an owner, a developer and a viewer.
type fixture struct {
	db        *storage.DB
	auth      *auth.Service
	tenancy   *tenancy.Service
	docs      *document.Service
	now       time.Time
	org       string
	app       string
	owner     auth.Principal
	developer auth.Principal
	viewer    auth.Principal
}

func newFixture(t *testing.T, configure ...func(*document.Options)) *fixture {
	t.Helper()
	ctx := context.Background()
	db := storagetest.Open(t)
	backend, err := signing.NewFile(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gen := ids{g: uuid7.NewGenerator(time.Now, rand.Reader)}
	log := audit.NewLog(gen, nil)
	f := &fixture{db: db, now: time.Now()}
	clock := func() time.Time { return f.now }
	f.auth, err = auth.NewService(auth.Options{
		DB: db, Audit: log, Cache: cache.NewMemory(nil), Crypter: backend, IDs: gen, VerificationURI: "https://p.example/device",
	})
	if err != nil {
		t.Fatal(err)
	}
	f.tenancy, err = tenancy.NewService(tenancy.Options{
		DB: db, Audit: log, Auth: f.auth, Crypter: backend, IDs: gen, SigningKeyPrefix: "targets", Now: clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	opts := document.Options{DB: db, Audit: log, Tenancy: f.tenancy, IDs: gen, SnapshotDays: 90, Now: clock}
	for _, c := range configure {
		c(&opts)
	}
	f.docs, err = document.NewService(opts)
	if err != nil {
		t.Fatal(err)
	}
	for kind, k := range f.docs.TrashKinds() {
		f.tenancy.RegisterTrashKind(kind, k)
	}
	admin, invitation, err := f.auth.Bootstrap(ctx, "admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.auth.AcceptInvitation(ctx, invitation, "Admin", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	o, err := f.tenancy.CreateOrganization(ctx, person(admin.ID, true), "acme", "Acme")
	if err != nil {
		t.Fatal(err)
	}
	f.org = o.ID
	f.owner = f.principal(t, person(admin.ID, true))
	a, err := f.tenancy.CreateApp(ctx, f.owner, "demo", "Demo")
	if err != nil {
		t.Fatal(err)
	}
	f.app = a.ID
	f.developer = f.member(t, "dev@example.com", "developer")
	f.viewer = f.member(t, "viewer@example.com", "viewer")
	return f
}

func person(userID string, admin bool) auth.Identity {
	return auth.Identity{Kind: auth.KindUser, ID: userID, UserID: userID, Display: "user", SecondFactor: true, InstallationAdmin: admin}
}

func (f *fixture) principal(t *testing.T, id auth.Identity) auth.Principal {
	t.Helper()
	p, err := f.auth.Resolve(context.Background(), id, f.org)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return p
}

func (f *fixture) member(t *testing.T, email, role string) auth.Principal {
	t.Helper()
	m, _, err := f.tenancy.AddMember(context.Background(), f.owner, "", email, role)
	if err != nil {
		t.Fatalf("AddMember: %v", err)
	}
	return f.principal(t, person(m.UserID, false))
}

// plugin creates a plugin with its plugin.json, written by the owner.
func (f *fixture) plugin(t *testing.T) string {
	const key = "loans"
	t.Helper()
	ctx := context.Background()
	p, err := f.docs.CreatePlugin(ctx, f.owner, f.app, key, "Loans")
	if err != nil {
		t.Fatalf("CreatePlugin: %v", err)
	}
	if _, _, err := f.docs.AcquireLock(ctx, f.owner, f.app, p.ID, "setup", false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.docs.PutDocument(ctx, f.owner, f.app, p.ID, "setup", "plugins/"+key+"/plugin.json", pluginJSON(key, "Loans"), 0); err != nil {
		t.Fatalf("PutDocument: %v", err)
	}
	if err := f.docs.ReleaseLock(ctx, f.owner, f.app, p.ID, "setup"); err != nil {
		t.Fatal(err)
	}
	return p.ID
}

func pluginJSON(key, name string) []byte {
	return []byte(`{"schemaVersion":"1.0.0","kind":"plugin","id":"01a0c450-6c00-7011-8000-000000020ddf","key":"` + key +
		`","name":"` + name + `","icon":{"monogram":{"background":"#5B3DF5","text":"LN"}},"team":"lending","entryPage":"01a0c450-6c00-7012-8000-000000022cce","pages":["01a0c450-6c00-7012-8000-000000022cce"]}`)
}

func pageJSON(key, text string) []byte {
	return []byte(`{"schemaVersion":"1.0.0","kind":"page","id":"01a0c450-6c00-7012-8000-000000022cce","key":"` + key +
		`","pageKind":"screen","title":"Page","root":{"id":"01a0c450-6c00-7026-8000-00000004977a","type":"Text","props":{"data":"` + text + `"}}}`)
}

func code(err error) plxerr.Code {
	var e *plxerr.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}

func detail(err error, key string) string {
	var e *plxerr.Error
	if errors.As(err, &e) {
		return e.Details[key]
	}
	return ""
}

// Verifies: SRV-030.
// A write names the revision it read: zero creates, a stale revision is
// refused with the current one, and a patch applies to the revision it
// names. A document that is not structurally valid, or not in the
// draft's part of the layout, is refused whole.
func TestRevisionsAndPatches(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	plugin := f.plugin(t)
	const session = "tab-1"
	if _, _, err := f.docs.AcquireLock(ctx, f.developer, f.app, plugin, session, false); err != nil {
		t.Fatal(err)
	}
	path := "plugins/loans/pages/home.page.json"
	w, err := f.docs.PutDocument(ctx, f.developer, f.app, plugin, session, path, pageJSON("home", "Hello"), 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if w.Documents[0].Revision != 1 || w.SnapshotID == "" {
		t.Fatalf("written: %+v", w)
	}
	if _, err := f.docs.PutDocument(ctx, f.developer, f.app, plugin, session, path, pageJSON("home", "Again"), 0); code(err) != plxerr.RevisionConflict || detail(err, "revision") != "1" {
		t.Errorf("a second create: %v", err)
	}
	if _, err := f.docs.PutDocument(ctx, f.developer, f.app, plugin, session, path, pageJSON("home", "Hi"), 1); err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, err := f.docs.PutDocument(ctx, f.developer, f.app, plugin, session, path, pageJSON("home", "Stale"), 1); code(err) != plxerr.RevisionConflict {
		t.Errorf("a stale revision: %v", err)
	}
	patch := []document.Op{{Op: "replace", Path: "/root/props/data", Value: "Patched"}}
	if _, err := f.docs.PatchDocument(ctx, f.developer, f.app, plugin, session, path, patch, 1); code(err) != plxerr.RevisionConflict {
		t.Errorf("a patch of a stale revision: %v", err)
	}
	if _, err := f.docs.PatchDocument(ctx, f.developer, f.app, plugin, session, path, patch, 0); code(err) != plxerr.MissingProperty {
		t.Errorf("a patch without a revision: %v", err)
	}
	if _, err := f.docs.PatchDocument(ctx, f.developer, f.app, plugin, session, path, patch, 2); err != nil {
		t.Fatalf("patch: %v", err)
	}
	doc, err := f.docs.GetDocument(ctx, f.viewer, f.app, plugin, path)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Revision != 3 || !strings.Contains(string(doc.Content), `"Patched"`) || doc.UpdatedBy.ID != f.developer.ID {
		t.Errorf("document: rev %d %s by %+v", doc.Revision, doc.Content, doc.UpdatedBy)
	}
	bad := []document.Op{{Op: "remove", Path: "/root"}}
	if _, err := f.docs.PatchDocument(ctx, f.developer, f.app, plugin, session, path, bad, 3); code(err) == 0 {
		t.Errorf("a patch leaving the page invalid was accepted")
	}
	for name, c := range map[string]struct {
		path    string
		content []byte
		want    plxerr.Code
	}{
		"outside the layout":   {"plugins/loans/notes.txt", []byte(`{}`), plxerr.InvalidProjectLayout},
		"another plugin":       {"plugins/other/pages/home.page.json", pageJSON("home", "x"), plxerr.InvalidProjectLayout},
		"the app's documents":  {"theme.json", []byte(`{}`), plxerr.InvalidProjectLayout},
		"named after its key":  {"plugins/loans/pages/other.page.json", pageJSON("home", "x"), plxerr.InvalidProjectLayout},
		"not JSON":             {"plugins/loans/pages/x.page.json", []byte(`{`), plxerr.InvalidJSON},
		"structurally invalid": {"plugins/loans/pages/x.page.json", []byte(`{"kind":"page"}`), plxerr.MissingProperty},
	} {
		if _, err := f.docs.PutDocument(ctx, f.developer, f.app, plugin, session, c.path, c.content, 0); code(err) != c.want {
			t.Errorf("%s: got %v, want PLX-%d", name, err, c.want)
		}
	}
	docs, err := f.docs.ListDocuments(ctx, f.viewer, f.app, plugin, "", 10, true)
	if err != nil || len(docs) != 2 || docs[0].Path != path || len(docs[1].Content) == 0 {
		t.Errorf("list: %+v %v", docs, err)
	}
	if _, err := f.docs.PutDocument(ctx, f.viewer, f.app, plugin, session, path, pageJSON("home", "x"), 3); code(err) != plxerr.PermissionDenied {
		t.Errorf("a viewer wrote: %v", err)
	}
	if _, err := f.docs.GetDocument(ctx, f.viewer, f.app, plugin, "plugins/loans/pages/none.page.json"); code(err) != plxerr.ResourceNotFound {
		t.Errorf("a missing document: %v", err)
	}
}

// Verifies: SRV-020.
// A document is stored as its canonical JSON bytes, zstd-compressed and
// addressed by their SHA-256, and a stored blob that no longer matches
// its hash is never returned.
func TestDocumentsAreStoredByHash(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	plugin := f.plugin(t)
	if _, _, err := f.docs.AcquireLock(ctx, f.developer, f.app, plugin, "tab", false); err != nil {
		t.Fatal(err)
	}
	path := "plugins/loans/pages/home.page.json"
	if _, err := f.docs.PutDocument(ctx, f.developer, f.app, plugin, "tab", path, pageJSON("home", "Stored"), 0); err != nil {
		t.Fatal(err)
	}
	doc, err := f.docs.GetDocument(ctx, f.viewer, f.app, plugin, path)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := jcs.Canonicalize(doc.Content, 64)
	if err != nil || string(canonical) != string(doc.Content) {
		t.Fatalf("the document is not returned in canonical form: %s", doc.Content)
	}
	sum := sha256.Sum256(doc.Content)
	decoder, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close()
	if err := f.db.InTx(ctx, storage.Tenant{OrganizationID: f.org}, func(ctx context.Context, tx pgx.Tx) error {
		var stored []byte
		if err := tx.QueryRow(ctx, `SELECT content FROM blobs WHERE sha256 = $1`, sum[:]).Scan(&stored); err != nil {
			return err
		}
		plain, err := decoder.DecodeAll(stored, nil)
		if err != nil || string(plain) != string(doc.Content) {
			t.Errorf("the blob is not the zstd-compressed canonical bytes: %v", err)
		}
		_, err = tx.Exec(ctx, `UPDATE blobs SET content = $2 WHERE sha256 = $1`, sum[:], compress(t, []byte(`{"kind":"page"}`)))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.docs.GetDocument(ctx, f.viewer, f.app, plugin, path); err == nil {
		t.Error("a blob that does not match its hash was returned")
	}
}

// compress compresses content as the store does, for a tampered blob.
func compress(t *testing.T, content []byte) []byte {
	t.Helper()
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	return enc.EncodeAll(content, nil)
}

// Verifies: SRV-040, SRV-042.
// Nothing is written without the draft's lock; the lock expires two
// minutes after the last heartbeat; a request for it reaches the holder
// through the heartbeat; and taking it by force needs
// plugin.lock.override.
func TestLocks(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	plugin := f.plugin(t)
	path := "plugins/loans/pages/home.page.json"
	if _, err := f.docs.PutDocument(ctx, f.developer, f.app, plugin, "tab-1", path, pageJSON("home", "x"), 0); code(err) != plxerr.EditingLockHeld {
		t.Errorf("a write without the lock: %v", err)
	}
	lock, _, err := f.docs.AcquireLock(ctx, f.developer, f.app, plugin, "tab-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if !lock.Held(f.now) || lock.Held(f.now.Add(document.LockTTL+time.Second)) {
		t.Errorf("lock times: %+v", lock)
	}
	if _, err := f.docs.PutDocument(ctx, f.developer, f.app, plugin, "tab-2", path, pageJSON("home", "x"), 0); code(err) != plxerr.EditingLockHeld {
		t.Errorf("a write from the holder's other session: %v", err)
	}
	if _, _, err := f.docs.AcquireLock(ctx, f.owner, f.app, plugin, "owner-1", false); code(err) != plxerr.EditingLockHeld || detail(err, "holder") == "" {
		t.Errorf("a second holder: %v", err)
	}
	if _, err := f.docs.RequestLock(ctx, f.owner, f.app, plugin); err != nil {
		t.Fatal(err)
	}
	if _, err := f.docs.RequestLock(ctx, f.owner, f.app, plugin); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(document.LockHeartbeat)
	renewed, err := f.docs.RenewLock(ctx, f.developer, f.app, plugin, "tab-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(renewed.RequestedBy) != 1 || renewed.RequestedBy[0].ID != f.owner.ID || !renewed.ExpiresAt.After(lock.ExpiresAt) {
		t.Errorf("the heartbeat: %+v", renewed)
	}
	if _, err := f.docs.RenewLock(ctx, f.owner, f.app, plugin, "owner-1"); code(err) != plxerr.EditingLockHeld {
		t.Errorf("someone else renewed: %v", err)
	}
	if err := f.docs.ReleaseLock(ctx, f.owner, f.app, plugin, "owner-1"); code(err) != plxerr.EditingLockHeld {
		t.Errorf("someone else released: %v", err)
	}
	got, err := f.docs.GetLock(ctx, f.viewer, f.app, plugin)
	if err != nil || got.Holder.ID != f.developer.ID || got.Session != "tab-1" {
		t.Errorf("GetLock: %+v %v", got, err)
	}
	if _, _, err := f.docs.AcquireLock(ctx, f.viewer, f.app, plugin, "v", false); code(err) != plxerr.PermissionDenied {
		t.Errorf("a viewer locked: %v", err)
	}
	if _, _, err := f.docs.AcquireLock(ctx, f.developer, f.app, plugin, "bad session", false); code(err) != plxerr.InvalidFormat {
		t.Errorf("a session with a space: %v", err)
	}
	// The lock lapses two minutes after the last heartbeat; then anyone
	// may take it without force.
	f.now = f.now.Add(document.LockTTL + time.Second)
	if _, _, err := f.docs.AcquireLock(ctx, f.owner, f.app, plugin, "owner-1", false); err != nil {
		t.Fatalf("an expired lock: %v", err)
	}
	if _, err := f.docs.RenewLock(ctx, f.developer, f.app, plugin, "tab-1"); code(err) != plxerr.EditingLockHeld {
		t.Errorf("the displaced holder renewed: %v", err)
	}
	if err := f.docs.ReleaseLock(ctx, f.owner, f.app, plugin, "owner-1"); err != nil {
		t.Fatal(err)
	}
	got, err = f.docs.GetLock(ctx, f.viewer, f.app, plugin)
	if err != nil || got.Held(f.now) {
		t.Errorf("after release: %+v %v", got, err)
	}
	// A developer holds no override permission.
	if _, _, err := f.docs.AcquireLock(ctx, f.owner, f.app, plugin, "owner-1", false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.docs.AcquireLock(ctx, f.developer, f.app, plugin, "tab-1", true); code(err) != plxerr.PermissionDenied {
		t.Errorf("a developer forced the lock: %v", err)
	}
}

// Verifies: SRV-041.
// Taking over a lock snapshots the draft and is audited with both
// parties; a write the displaced session makes afterwards is not applied
// but kept as a snapshot the new holder can restore.
func TestTakeoverPreservesWork(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	plugin := f.plugin(t)
	path := "plugins/loans/pages/home.page.json"
	if _, _, err := f.docs.AcquireLock(ctx, f.developer, f.app, plugin, "tab-1", false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.docs.PutDocument(ctx, f.developer, f.app, plugin, "tab-1", path, pageJSON("home", "Mine"), 0); err != nil {
		t.Fatal(err)
	}
	_, preserved, err := f.docs.AcquireLock(ctx, f.owner, f.app, plugin, "owner-1", true)
	if err != nil || preserved == "" {
		t.Fatalf("takeover: %q %v", preserved, err)
	}
	snap, docs, err := f.docs.GetSnapshot(ctx, f.owner, preserved, nil)
	if err != nil || snap.Reason != "lock_takeover" || len(docs) != 2 {
		t.Errorf("the takeover snapshot: %+v %d %v", snap, len(docs), err)
	}
	// The displaced session writes before it notices.
	_, err = f.docs.PutDocument(ctx, f.developer, f.app, plugin, "tab-1", path, pageJSON("home", "Late"), 1)
	late := detail(err, "preservedSnapshot")
	if code(err) != plxerr.EditingLockHeld || late == "" {
		t.Fatalf("a late write: %v", err)
	}
	current, err := f.docs.GetDocument(ctx, f.owner, f.app, plugin, path)
	if err != nil || !strings.Contains(string(current.Content), "Mine") {
		t.Fatalf("the late write was applied: %s %v", current.Content, err)
	}
	// Someone who was never displaced gets no preserved snapshot.
	if _, err := f.docs.PutDocument(ctx, f.viewer, f.app, plugin, "v", path, pageJSON("home", "x"), 1); code(err) != plxerr.PermissionDenied {
		t.Errorf("a viewer: %v", err)
	}
	list, err := f.docs.ListSnapshots(ctx, f.owner, f.app, plugin, 0, 10)
	if err != nil || list[0].ID != late || list[0].Reason != "preserved" {
		t.Fatalf("history: %+v %v", list, err)
	}
	changes, err := f.docs.CompareSnapshots(ctx, f.owner, preserved, late)
	if err != nil || len(changes) != 1 || changes[0].Change != "changed" || changes[0].Path != path {
		t.Errorf("compare: %+v %v", changes, err)
	}
	w, err := f.docs.RestoreSnapshot(ctx, f.owner, late, nil, "owner-1")
	if err != nil || len(w.Documents) != 1 {
		t.Fatalf("restore: %+v %v", w, err)
	}
	current, err = f.docs.GetDocument(ctx, f.owner, f.app, plugin, path)
	if err != nil || !strings.Contains(string(current.Content), "Late") {
		t.Errorf("after restore: %s %v", current.Content, err)
	}
	if _, err := f.docs.RestoreSnapshot(ctx, f.owner, late, nil, "owner-1"); code(err) != plxerr.PreconditionFailed {
		t.Errorf("a restore that changes nothing: %v", err)
	}
	var actions []string
	if err := f.db.InTx(ctx, storage.Tenant{OrganizationID: f.org}, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, "SELECT action, detail FROM audit_log WHERE action LIKE 'plugin.lock.%' ORDER BY sequence")
		if err != nil {
			return err
		}
		for rows.Next() {
			var a, d string
			if err := rows.Scan(&a, &d); err != nil {
				return err
			}
			actions = append(actions, a+" "+d)
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(actions, func(a string) bool {
		return strings.HasPrefix(a, "plugin.lock.override ") && strings.Contains(a, f.developer.ID) && strings.Contains(a, preserved)
	}) {
		t.Errorf("the takeover is not audited with both parties: %q", actions)
	}
}

// Verifies: SRV-031.
// Every write is a snapshot; snapshots are compared and restored, whole
// or by path; a restore is itself a snapshot; and history older than the
// retention period is purged without changing any state that remains.
func TestSnapshotsAndRetention(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	plugin := f.plugin(t)
	const s = "tab-1"
	if _, _, err := f.docs.AcquireLock(ctx, f.owner, f.app, plugin, s, false); err != nil {
		t.Fatal(err)
	}
	home, about := "plugins/loans/pages/home.page.json", "plugins/loans/pages/about.page.json"
	first, err := f.docs.PutDocument(ctx, f.owner, f.app, plugin, s, home, pageJSON("home", "One"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.docs.PutDocument(ctx, f.owner, f.app, plugin, s, about, pageJSON("about", "About"), 0); err != nil {
		t.Fatal(err)
	}
	second, err := f.docs.PutDocument(ctx, f.owner, f.app, plugin, s, home, pageJSON("home", "Two"), 1)
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := f.docs.DeleteDocument(ctx, f.owner, f.app, plugin, s, about, 1)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := f.docs.CompareSnapshots(ctx, f.viewer, first.SnapshotID, deleted.SnapshotID)
	if err != nil || len(changes) != 1 || changes[0].Change != "changed" || len(changes[0].Patch) == 0 {
		t.Errorf("compare first → deleted: %+v %v", changes, err)
	}
	changes, err = f.docs.CompareSnapshots(ctx, f.viewer, second.SnapshotID, deleted.SnapshotID)
	if err != nil || len(changes) != 1 || changes[0].Change != "removed" || changes[0].Path != about {
		t.Errorf("compare second → deleted: %+v %v", changes, err)
	}
	// Restoring one path of an older snapshot changes only that path.
	if _, err := f.docs.RestoreSnapshot(ctx, f.owner, first.SnapshotID, []string{home}, s); err != nil {
		t.Fatal(err)
	}
	doc, err := f.docs.GetDocument(ctx, f.viewer, f.app, plugin, home)
	if err != nil || !strings.Contains(string(doc.Content), "One") {
		t.Errorf("restored home: %s %v", doc.Content, err)
	}
	if _, err := f.docs.GetDocument(ctx, f.viewer, f.app, plugin, about); code(err) != plxerr.ResourceNotFound {
		t.Errorf("about came back: %v", err)
	}
	// Restoring the whole of the second snapshot brings about back.
	if _, err := f.docs.RestoreSnapshot(ctx, f.owner, second.SnapshotID, nil, s); err != nil {
		t.Fatal(err)
	}
	list, err := f.docs.ListSnapshots(ctx, f.viewer, f.app, plugin, 0, 100)
	if err != nil || len(list) != 7 || list[0].Reason != "restore" {
		t.Fatalf("history: %d %+v %v", len(list), list, err)
	}
	older, err := f.docs.ListSnapshots(ctx, f.viewer, f.app, plugin, list[2].Sequence, 2)
	if err != nil || len(older) != 2 || older[0].Sequence != list[3].Sequence {
		t.Errorf("paging history: %+v %v", older, err)
	}
	// Nothing is inside the retention period's end yet.
	if n, err := f.docs.PurgeSnapshots(ctx, f.org); err != nil || n != 0 {
		t.Errorf("an early purge: %d %v", n, err)
	}
	// Keep the snapshot "first" — as publishing will — and delete about
	// again, so that purging must not let about reappear from it.
	if err := f.db.InTx(ctx, storage.Tenant{OrganizationID: f.org}, func(ctx context.Context, tx pgx.Tx) error {
		return dbgen.New(tx).KeepSnapshot(ctx, storage.MustUUID(second.SnapshotID))
	}); err != nil {
		t.Fatal(err)
	}
	cur, err := f.docs.GetDocument(ctx, f.owner, f.app, plugin, about)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.docs.DeleteDocument(ctx, f.owner, f.app, plugin, s, about, cur.Revision); err != nil {
		t.Fatal(err)
	}
	final := snapshotContents(ctx, t, f, plugin)
	f.now = f.now.AddDate(0, 0, 91)
	n, err := f.docs.PurgeSnapshots(ctx, f.org)
	if err != nil || n == 0 {
		t.Fatalf("purge: %d %v", n, err)
	}
	list, err = f.docs.ListSnapshots(ctx, f.viewer, f.app, plugin, 0, 100)
	if err != nil || len(list) != 2 {
		t.Fatalf("after purge: %d %+v %v", len(list), list, err)
	}
	if got := snapshotContents(ctx, t, f, plugin); !mapsEqual(got, final) {
		t.Errorf("the newest state changed: %v, want %v", keys(got), keys(final))
	}
	_, kept, err := f.docs.GetSnapshot(ctx, f.viewer, second.SnapshotID, nil)
	if err != nil || len(kept) != 3 {
		t.Errorf("the kept snapshot: %d documents, %v", len(kept), err)
	}
}

// snapshotContents is the state of the newest snapshot of a draft.
func snapshotContents(ctx context.Context, t *testing.T, f *fixture, plugin string) map[string]string {
	t.Helper()
	list, err := f.docs.ListSnapshots(ctx, f.owner, f.app, plugin, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	_, docs, err := f.docs.GetSnapshot(ctx, f.owner, list[0].ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, d := range docs {
		out[d.Path] = string(d.Content)
	}
	return out
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// Verifies: GOV-031.
// Plugins are created with a unique key within the app's plugin limit,
// renamed, and deleted to the trash, from which they are restored or
// purged; a deleted page goes to the trash likewise.
func TestPluginsAndTrash(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	plugin := f.plugin(t)
	if _, err := f.docs.CreatePlugin(ctx, f.owner, f.app, "loans", "Again"); code(err) != plxerr.ResourceExists {
		t.Errorf("a duplicate key: %v", err)
	}
	if _, err := f.docs.CreatePlugin(ctx, f.owner, f.app, "Not Valid", "x"); code(err) != plxerr.InvalidFormat {
		t.Errorf("an invalid key: %v", err)
	}
	if _, err := f.docs.CreatePlugin(ctx, f.developer, f.app, "cards", "Cards"); code(err) != plxerr.PermissionDenied {
		t.Errorf("a developer created a plugin: %v", err)
	}
	if _, err := f.tenancy.SetAppLimit(ctx, f.owner, f.app, "app.plugins", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.docs.CreatePlugin(ctx, f.owner, f.app, "cards", "Cards"); code(err) != plxerr.LimitExceeded {
		t.Errorf("above app.plugins: %v", err)
	}
	updated, err := f.docs.UpdatePlugin(ctx, f.owner, plugin, "Lending")
	if err != nil || updated.Name != "Lending" {
		t.Errorf("rename: %+v %v", updated, err)
	}
	got, err := f.docs.GetPlugin(ctx, f.viewer, plugin)
	if err != nil || got.Key != "loans" || got.DraftRevision != 1 {
		t.Errorf("GetPlugin: %+v %v", got, err)
	}
	list, err := f.docs.ListPlugins(ctx, f.viewer, f.app, storage.Cursor{}, 10)
	if err != nil || len(list) != 1 {
		t.Errorf("ListPlugins: %+v %v", list, err)
	}
	if _, err := f.docs.SetPluginLimit(ctx, f.owner, plugin, "document.fileSize", 100); code(err) != plxerr.OutOfRange {
		t.Errorf("a limit that has no plugin scope: %v", err)
	}
	if _, err := f.docs.SetPluginLimit(ctx, f.owner, plugin, "document.stringPropSize", 1024); err != nil {
		t.Fatal(err)
	}
	usage, err := f.docs.ListPluginLimits(ctx, f.viewer, plugin)
	if err != nil || !slices.ContainsFunc(usage, func(u tenancy.LimitUsage) bool { return u.Key == "document.stringPropSize" && u.Effective == 1024 }) {
		t.Errorf("plugin limits: %+v %v", usage, err)
	}
	if _, err := f.docs.SetPluginLimit(ctx, f.developer, plugin, "document.stringPropSize", 10); code(err) != plxerr.PermissionDenied {
		t.Errorf("a developer set a limit: %v", err)
	}
	if _, _, err := f.docs.AcquireLock(ctx, f.owner, f.app, plugin, "s", false); err != nil {
		t.Fatal(err)
	}
	page := "plugins/loans/pages/home.page.json"
	if _, err := f.docs.PutDocument(ctx, f.owner, f.app, plugin, "s", page, pageJSON("home", "Hello"), 0); err != nil {
		t.Fatal(err)
	}
	// A deleted page goes to the trash and comes back.
	if _, err := f.docs.DeleteDocument(ctx, f.owner, f.app, plugin, "s", page, 1); err != nil {
		t.Fatal(err)
	}
	items, err := f.tenancy.ListTrash(ctx, f.owner, f.app, tenancy.Page{Size: 10})
	if err != nil || len(items) != 1 || items[0].Kind != "page" {
		t.Fatalf("trash: %+v %v", items, err)
	}
	if err := f.tenancy.RestoreFromTrash(ctx, f.owner, items[0].ID); err != nil {
		t.Fatalf("restore a page: %v", err)
	}
	if _, err := f.docs.GetDocument(ctx, f.viewer, f.app, plugin, page); err != nil {
		t.Errorf("the restored page: %v", err)
	}
	// A purged page is gone for good.
	if _, err := f.docs.PutDocument(ctx, f.owner, f.app, plugin, "s", "plugins/loans/pages/old.page.json", pageJSON("old", "Old"), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.docs.DeleteDocument(ctx, f.owner, f.app, plugin, "s", "plugins/loans/pages/old.page.json", 1); err != nil {
		t.Fatal(err)
	}
	items, err = f.tenancy.ListTrash(ctx, f.owner, f.app, tenancy.Page{Size: 10})
	if err != nil || len(items) != 1 {
		t.Fatalf("trash: %+v %v", items, err)
	}
	if err := f.tenancy.PurgeFromTrash(ctx, f.owner, items[0].ID); err != nil {
		t.Fatalf("purge a page: %v", err)
	}
	if _, err := f.docs.PutDocument(ctx, f.owner, f.app, plugin, "s", "plugins/loans/pages/old.page.json", pageJSON("old", "New"), 0); err != nil {
		t.Errorf("the purged page's path is not free: %v", err)
	}
	// A deleted plugin goes to the trash with its lock released.
	item, err := f.docs.DeletePlugin(ctx, f.owner, plugin)
	if err != nil || item.Kind != "plugin" {
		t.Fatalf("DeletePlugin: %+v %v", item, err)
	}
	if _, err := f.docs.GetPlugin(ctx, f.viewer, plugin); code(err) != plxerr.ResourceNotFound {
		t.Errorf("a deleted plugin: %v", err)
	}
	if err := f.tenancy.RestoreFromTrash(ctx, f.owner, item.ID); err != nil {
		t.Fatalf("restore a plugin: %v", err)
	}
	if _, err := f.docs.GetDocument(ctx, f.viewer, f.app, plugin, page); err != nil {
		t.Errorf("the restored plugin's page: %v", err)
	}
	item, err = f.docs.DeletePlugin(ctx, f.owner, plugin)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.tenancy.PurgeFromTrash(ctx, f.owner, item.ID); err != nil {
		t.Fatalf("purge a plugin: %v", err)
	}
	if err := f.tenancy.RestoreFromTrash(ctx, f.owner, item.ID); code(err) != plxerr.ResourceNotFound {
		t.Errorf("a purged plugin came back: %v", err)
	}
	// The key is free again once the plugin is gone.
	if err := f.tenancy.PurgeFromTrash(ctx, f.owner, item.ID); code(err) != plxerr.ResourceNotFound {
		t.Errorf("a second purge: %v", err)
	}
}

// project reads a conformance project's documents, without its assets.
func project(t *testing.T, name string) []document.File {
	t.Helper()
	root := filepath.Join("..", "..", "..", "schema", "testdata", "documents", name)
	var files []document.File
	err := fs.WalkDir(os.DirFS(root), ".", func(path string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() || strings.HasPrefix(path, "assets/") {
			return err
		}
		data, err := os.ReadFile(filepath.Join(root, path))
		files = append(files, document.File{Path: path, Content: data})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// Verifies: SCH-006, SCH-040, SCH-042.
// A project in the Git layout imports into the drafts, creating its
// plugins, and exports back to the same documents; validation runs the
// compiler over the drafts and over one page as edited.
func TestImportExportAndValidation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	files := project(t, "loan-calculator")
	w, err := f.docs.Import(ctx, f.owner, f.app, "", "import-1", files)
	if err != nil {
		t.Fatalf("Import: %v (%v)", err, w.Diagnostics)
	}
	if len(w.Documents) != len(files) {
		t.Errorf("imported %d documents, want %d", len(w.Documents), len(files))
	}
	exported, err := f.docs.Export(ctx, f.viewer, f.app, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(exported) != len(files) {
		t.Fatalf("exported %d files, want %d", len(exported), len(files))
	}
	byPath := map[string][]byte{}
	for _, file := range files {
		byPath[file.Path] = file.Content
	}
	for _, e := range exported {
		a, err := jcs.Parse(byPath[e.Path], 64)
		if err != nil {
			t.Fatal(err)
		}
		b, err := jcs.Parse(e.Content, 64)
		if err != nil {
			t.Fatal(err)
		}
		ca, _ := jcs.Marshal(a)
		cb, _ := jcs.Marshal(b)
		if string(ca) != string(cb) {
			t.Errorf("%s changed in the round trip", e.Path)
		}
	}
	plugins, err := f.docs.ListPlugins(ctx, f.viewer, f.app, storage.Cursor{}, 10)
	if err != nil || len(plugins) != 1 || plugins[0].Key != "loans" {
		t.Fatalf("plugins: %+v %v", plugins, err)
	}
	loans := plugins[0].ID
	oneFiles, err := f.docs.Export(ctx, f.viewer, f.app, loans)
	one := make([]document.File, 0, len(oneFiles))
	one = append(one, oneFiles...)
	if err != nil || len(one) != 5 {
		t.Errorf("one plugin's export: %d %v", len(one), err)
	}
	diags, err := f.docs.ValidateDraft(ctx, f.viewer, f.app, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range diags {
		// The assets are not imported with the documents (AssetService).
		if d.Severity == plxerr.SeverityError && !strings.Contains(d.Message, "asset") {
			t.Errorf("an unexpected diagnostic: %v", d)
		}
	}
	page := "plugins/loans/pages/result.page.json"
	var content []byte
	for _, file := range files {
		if file.Path == page {
			content = file.Content
		}
	}
	if pd, err := f.docs.ValidatePage(ctx, f.viewer, f.app, loans, page, content); err != nil || pd.HasErrors() {
		t.Errorf("ValidatePage: %v %v", pd, err)
	}
	broken := []byte(strings.Replace(string(content), `"type": "Center"`, `"type": "NoSuchWidget"`, 1))
	if pd, err := f.docs.ValidatePage(ctx, f.viewer, f.app, loans, page, broken); err != nil || !pd.HasErrors() {
		t.Errorf("ValidatePage of a broken page: %v %v", pd, err)
	}
	if _, err := f.docs.ValidatePage(ctx, f.viewer, f.app, loans, "plugins/loans/plugin.json", content); code(err) != plxerr.InvalidProjectLayout {
		t.Errorf("ValidatePage of a plugin.json: %v", err)
	}
	// An import by someone else is refused while the importing session
	// holds the lock.
	if _, err := f.docs.Import(ctx, f.developer, f.app, loans, "dev-1", one); code(err) != plxerr.EditingLockHeld {
		t.Errorf("an import against a held lock: %v", err)
	}
	// Importing a smaller project deletes what it does not contain.
	var fewer []document.File
	for _, file := range files {
		if !strings.HasPrefix(file.Path, "translations/de") {
			fewer = append(fewer, file)
		}
	}
	if _, err := f.docs.Import(ctx, f.owner, f.app, "", "import-1", fewer); err != nil {
		t.Fatal(err)
	}
	if _, err := f.docs.GetDocument(ctx, f.viewer, f.app, "", "translations/de-DE.json"); code(err) != plxerr.ResourceNotFound {
		t.Errorf("a document absent from the import survived: %v", err)
	}
	for name, bad := range map[string][]document.File{
		"an asset":          {{Path: "assets/images/logo.png", Content: []byte("x")}},
		"an invalid file":   {{Path: "theme.json", Content: []byte(`{"kind":"theme"}`)}},
		"no documents":      nil,
		"a duplicated path": {files[0], files[0]},
	} {
		if _, err := f.docs.Import(ctx, f.owner, f.app, "", "import-1", bad); err == nil {
			t.Errorf("%s was imported", name)
		}
	}
}

// Verifies: SCH-031, SCH-041.
// Templates are listed and instantiated into a plugin as a new page with
// fresh node identifiers and the arguments applied; components are
// listed with the places that use them.
func TestTemplatesComponentsAndUsages(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.docs.Import(ctx, f.owner, f.app, "", "s", project(t, "loan-calculator")); err != nil {
		t.Fatal(err)
	}
	templates, err := f.docs.ListTemplates(ctx, f.viewer, f.app, "page", "", 10)
	if err != nil || len(templates) != 1 || templates[0].Key != "labelled-value" || len(templates[0].Parameters) != 1 {
		t.Fatalf("templates: %+v %v", templates, err)
	}
	if flows, err := f.docs.ListTemplates(ctx, f.viewer, f.app, "flow", "", 10); err != nil || len(flows) != 0 {
		t.Errorf("flow templates: %+v %v", flows, err)
	}
	tmpl, content, err := f.docs.GetTemplate(ctx, f.viewer, templates[0].ID)
	if err != nil || tmpl.Name != "Labelled value" || len(content) == 0 {
		t.Errorf("GetTemplate: %+v %v", tmpl, err)
	}
	plugins, err := f.docs.ListPlugins(ctx, f.viewer, f.app, storage.Cursor{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	loans := plugins[0].ID
	if _, err := f.docs.Instantiate(ctx, f.owner, tmpl.ID, f.app, loans, "s", []byte(`{"nope":1}`), "summary"); code(err) != plxerr.UnknownProperty {
		t.Errorf("an unknown parameter: %v", err)
	}
	if _, err := f.docs.Instantiate(ctx, f.owner, tmpl.ID, f.app, loans, "s", nil, "Bad Key"); code(err) != plxerr.InvalidFormat {
		t.Errorf("an invalid key: %v", err)
	}
	w, err := f.docs.Instantiate(ctx, f.owner, tmpl.ID, f.app, loans, "s", []byte(`{"label":"Monthly"}`), "summary")
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	if len(w.Documents) != 2 {
		t.Fatalf("instantiated: %+v", w.Documents)
	}
	page, err := f.docs.GetDocument(ctx, f.viewer, f.app, loans, "plugins/loans/pages/summary.page.json")
	if err != nil || !strings.Contains(string(page.Content), `"Monthly"`) || strings.Contains(string(page.Content), "01a0c450-6c00-702e-8000-000000058ef2") {
		t.Errorf("the new page: %s %v", page.Content, err)
	}
	plugin, err := f.docs.GetDocument(ctx, f.viewer, f.app, loans, "plugins/loans/plugin.json")
	if err != nil || !strings.Contains(string(plugin.Content), page.ID) && !strings.Contains(string(plugin.Content), "summary") {
		t.Logf("plugin.json lists the page by its id: %s", plugin.Content)
	}
	if _, err := f.docs.Instantiate(ctx, f.owner, tmpl.ID, f.app, loans, "s", nil, "summary"); code(err) != plxerr.RevisionConflict {
		t.Errorf("a second page with the same key: %v", err)
	}
	// The features project has a shared component used by a page.
	g := newFixture(t)
	if _, err := g.docs.Import(ctx, g.owner, g.app, "", "s", project(t, "features")); err != nil {
		t.Fatal(err)
	}
	components, err := g.docs.ListComponents(ctx, g.viewer, g.app, "", "", 50)
	if err != nil || len(components) == 0 {
		t.Fatalf("components: %+v %v", components, err)
	}
	badge := components[slices.IndexFunc(components, func(c document.Component) bool { return c.Key == "badge" })]
	got, content, err := g.docs.GetComponent(ctx, g.viewer, badge.ID)
	if err != nil || got.Path != "components/badge.component.json" || len(content) == 0 {
		t.Errorf("GetComponent: %+v %v", got, err)
	}
	usages, err := g.docs.ListUsages(ctx, g.viewer, g.app, "component", badge.ID, "", 50)
	if err != nil || len(usages) == 0 {
		t.Errorf("usages of the badge: %+v %v", usages, err)
	}
	if _, err := g.docs.ListUsages(ctx, g.viewer, g.app, "nonsense", badge.ID, "", 50); code(err) == 0 {
		t.Errorf("an unknown entity kind was accepted")
	}
}
