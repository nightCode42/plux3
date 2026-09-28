// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/pluxv1"
)

// runPublishes runs the publish jobs the API enqueued, as the worker
// would.
func (w *world) runPublishes(t *testing.T) {
	t.Helper()
	w.queue.mu.Lock()
	jobs := w.queue.jobs
	w.queue.jobs = nil
	w.queue.mu.Unlock()
	for _, j := range jobs {
		if err := w.releases.RunPublish(context.Background(), j); err != nil {
			t.Fatalf("RunPublish: %v", err)
		}
	}
}

// Verifies: SRV-050, SRV-051, SRV-052, REL-001, REL-002, REL-004, REL-006, REL-080, REL-081.
// Publishing and releasing over HTTP: a publish job streamed to its end,
// a release created, promoted and rolled back, with its changelog and
// compatibility.
func TestPublishAndReleaseEndToEnd(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ctx := context.Background()
	_, invitation, err := w.auth.Bootstrap(ctx, "admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	must(w.identity.AcceptInvitation(ctx, connect.NewRequest(&pluxv1.AcceptInvitationRequest{
		Invitation: invitation, DisplayName: "Admin", Password: "correct horse battery",
	})))(t)
	admin := w.signIn(t, "admin@example.com")
	enrolled := must(w.identity.EnrollTotp(ctx, req(admin, &pluxv1.EnrollTotpRequest{Label: "phone"})))(t)
	code := totpNow(t, enrolled.GetSecret())
	must(w.identity.ConfirmFactor(ctx, req(admin, &pluxv1.ConfirmFactorRequest{FactorId: enrolled.GetFactor().GetId(), Code: code})))(t)
	admin.org = must(w.org.CreateOrganization(ctx, req(admin, &pluxv1.CreateOrganizationRequest{Key: "acme", Name: "Acme"})))(t).GetOrganization().GetId()
	app := must(w.app.CreateApp(ctx, req(admin, &pluxv1.CreateAppRequest{Key: "demo", Name: "Demo"})))(t).GetApp().GetId()
	envs := map[string]string{}
	for _, e := range must(w.app.ListEnvironments(ctx, req(admin, &pluxv1.ListEnvironmentsRequest{AppId: app})))(t).GetEnvironments() {
		envs[e.GetKey()] = e.GetId()
	}

	// Import the conformance project over the import stream.
	stream := w.document.ImportDraft(ctx)
	stream.RequestHeader().Set("Cookie", admin.cookie)
	stream.RequestHeader().Set("X-CSRF-Token", admin.csrf)
	stream.RequestHeader().Set("X-Plux-Organization", admin.org)
	root := filepath.Join("..", "..", "..", "schema", "testdata", "documents", "loan-calculator")
	first := true
	if err := fs.WalkDir(os.DirFS(root), ".", func(path string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			return err
		}
		m := &pluxv1.ImportDraftRequest{Path: path, Content: data}
		if first {
			m.AppId, m.Session, first = app, "s", false
		}
		return stream.Send(m)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.CloseAndReceive(); err != nil {
		t.Fatalf("ImportDraft: %v", err)
	}
	loans := must(w.plugin.ListPlugins(ctx, req(admin, &pluxv1.ListPluginsRequest{AppId: app})))(t).GetPlugins()[0].GetId()

	// Publish the app bundle and the plugin; watch one to its end.
	pub := must(w.publish.Publish(ctx, req(admin, &pluxv1.PublishRequest{AppId: app, EnvironmentId: envs["development"]})))(t).GetJob()
	if pub.GetState() != "queued" {
		t.Errorf("Publish = %+v", pub)
	}
	w.runPublishes(t)
	watch, err := w.publish.WatchPublish(ctx, req(admin, &pluxv1.WatchPublishRequest{JobId: pub.GetId()}))
	if err != nil {
		t.Fatal(err)
	}
	var final *pluxv1.PublishJob
	for watch.Receive() {
		final = watch.Msg().GetJob()
	}
	if err := watch.Err(); err != nil || final.GetState() != "succeeded" || final.GetVersion() != 1 {
		t.Fatalf("WatchPublish ended with %+v, %v", final, err)
	}
	must(w.publish.Publish(ctx, req(admin, &pluxv1.PublishRequest{AppId: app, PluginId: loans, EnvironmentId: envs["development"], Label: "1.0.0"})))(t)
	w.runPublishes(t)
	jobs := must(w.publish.ListPublishJobs(ctx, req(admin, &pluxv1.ListPublishJobsRequest{AppId: app})))(t)
	if len(jobs.GetJobs()) != 2 {
		t.Errorf("ListPublishJobs = %+v", jobs)
	}
	got := must(w.publish.GetPublishJob(ctx, req(admin, &pluxv1.GetPublishJobRequest{JobId: pub.GetId()})))(t)
	if got.GetJob().GetPercent() != 100 {
		t.Errorf("GetPublishJob = %+v", got)
	}
	queued := must(w.publish.Publish(ctx, req(admin, &pluxv1.PublishRequest{AppId: app, EnvironmentId: envs["development"]})))(t).GetJob()
	if c := must(w.publish.CancelPublishJob(ctx, req(admin, &pluxv1.CancelPublishJobRequest{JobId: queued.GetId()})))(t); c.GetJob().GetState() != "cancelled" {
		t.Errorf("CancelPublishJob = %+v", c)
	}
	w.runPublishes(t)
	version := must(w.release.GetPluginVersion(ctx, req(admin, &pluxv1.GetPluginVersionRequest{PluginId: loans, Version: 1})))(t).GetVersion()
	if version.GetLabel() != "1.0.0" || len(version.GetSignature()) != 64 {
		t.Errorf("GetPluginVersion = %+v", version)
	}
	if vs := must(w.release.ListPluginVersions(ctx, req(admin, &pluxv1.ListPluginVersionsRequest{PluginId: loans})))(t); len(vs.GetVersions()) != 1 {
		t.Errorf("ListPluginVersions = %+v", vs)
	}

	// Release, promote, roll back.
	created := must(w.release.CreateRelease(ctx, req(admin, &pluxv1.CreateReleaseRequest{AppId: app, EnvironmentId: envs["development"], Notes: "First"})))(t)
	if created.GetRelease().GetSequence() != 1 || len(created.GetRelease().GetPluginVersionIds()) != 1 {
		t.Fatalf("CreateRelease = %+v", created)
	}
	must(w.release.CreateRelease(ctx, req(admin, &pluxv1.CreateReleaseRequest{AppId: app, EnvironmentId: envs["development"], Notes: "Second"})))(t)
	must(w.release.PromoteRelease(ctx, req(admin, &pluxv1.PromoteReleaseRequest{AppId: app, Sequence: 2, EnvironmentId: envs["production"]})))(t)
	back := must(w.release.RollbackRelease(ctx, req(admin, &pluxv1.RollbackReleaseRequest{AppId: app, EnvironmentId: envs["production"], ToSequence: 1})))(t)
	if back.GetRelease().GetSequence() != 3 || back.GetRelease().GetRollbackOf() != 1 {
		t.Errorf("RollbackRelease = %+v", back)
	}
	full := must(w.release.GetRelease(ctx, req(admin, &pluxv1.GetReleaseRequest{AppId: app, Sequence: 3})))(t)
	if len(full.GetVersions()) != 2 {
		t.Errorf("GetRelease = %+v", full)
	}
	if rs := must(w.release.ListReleases(ctx, req(admin, &pluxv1.ListReleasesRequest{AppId: app})))(t); len(rs.GetReleases()) != 3 {
		t.Errorf("ListReleases = %+v", rs)
	}
	log := must(w.release.GetChangelog(ctx, req(admin, &pluxv1.GetChangelogRequest{AppId: app, Sequence: 1})))(t)
	if log.GetNotes() != "First" || len(log.GetEntries()) == 0 || !strings.Contains(log.String(), "calculator") {
		t.Errorf("GetChangelog = %+v", log)
	}
	compat := must(w.release.GetCompatibility(ctx, req(admin, &pluxv1.GetCompatibilityRequest{AppId: app, Sequence: 3})))(t)
	if compat.GetCompatibility().GetMinRuntime() == "" {
		t.Errorf("GetCompatibility = %+v", compat)
	}
	if _, err := w.release.GetRelease(ctx, req(admin, &pluxv1.GetReleaseRequest{AppId: app, Sequence: 99})); codeOf(err) != connect.CodeNotFound {
		t.Errorf("a missing release: %v", err)
	}
}

// totpNow is the current code of a TOTP secret.
func totpNow(t *testing.T, secret string) string {
	t.Helper()
	c, err := auth.TOTPCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return c
}
