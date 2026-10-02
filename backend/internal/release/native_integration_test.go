// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package release_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/release"
	"github.com/nightCode42/plux3/backend/internal/storage"
)

// routingCatalogue is the routing project's native catalogue: the route
// profile, the slot Counter and the custom action scan.
func routingCatalogue(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "schema", "testdata", "documents", "routing", "native-catalogue.json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// without returns the catalogue with one entry list emptied.
func without(t *testing.T, catalogue []byte, list string) []byte {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(catalogue, &doc); err != nil {
		t.Fatal(err)
	}
	doc[list] = []any{}
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestNativeCatalogues stores host builds' catalogues and checks that a
// catalogue never changes once stored and that only publishers upload.
// Verifies: CLI-006, SCH-032.
func TestNativeCatalogues(t *testing.T) {
	t.Parallel()
	f := newFixtureOf(t, "routing")
	ctx := context.Background()
	catalogue := routingCatalogue(t)
	b, created, err := f.rel.UploadNativeCatalogue(ctx, f.owner, f.app, "1.0.0+1", catalogue)
	if err != nil || !created || b.Build != "1.0.0+1" || len(b.SHA256) != 64 || b.UploadedBy.Display != "Admin" {
		t.Fatalf("the first upload: %+v %v %v", b, created, err)
	}
	// Reformatted, the same catalogue is the same content.
	var tree any
	_ = json.Unmarshal(catalogue, &tree)
	spaced, _ := json.MarshalIndent(tree, "", "    ")
	again, created, err := f.rel.UploadNativeCatalogue(ctx, f.owner, f.app, "1.0.0+1", spaced)
	if err != nil || created || again.SHA256 != b.SHA256 {
		t.Errorf("the same catalogue again: %+v %v %v", again, created, err)
	}
	if _, _, err := f.rel.UploadNativeCatalogue(ctx, f.owner, f.app, "1.0.0+1", without(t, catalogue, "slots")); code(err) != plxerr.ResourceExists {
		t.Errorf("another catalogue for the build: %v", err)
	}
	if _, _, err := f.rel.UploadNativeCatalogue(ctx, f.viewer, f.app, "1.0.0+9", catalogue); code(err) != plxerr.PermissionDenied {
		t.Errorf("a viewer uploaded: %v", err)
	}
	for build, data := range map[string][]byte{
		"1.0.0+3":                             []byte(`{"routes": 3}`),
		"":                                    catalogue,
		"has space":                           catalogue,
		string(bytes.Repeat([]byte("x"), 65)): catalogue,
	} {
		if _, _, err := f.rel.UploadNativeCatalogue(ctx, f.owner, f.app, build, data); code(err) != plxerr.InvalidFormat {
			t.Errorf("build %q: %v", build, err)
		}
	}
	got, data, err := f.rel.GetNativeCatalogue(ctx, f.viewer, f.app, "1.0.0+1")
	if err != nil || got.SHA256 != b.SHA256 || !json.Valid(data) || bytes.Contains(data, []byte("\n")) {
		t.Errorf("GetNativeCatalogue: %+v %s %v", got, data, err)
	}
	if _, _, err := f.rel.GetNativeCatalogue(ctx, f.viewer, f.app, "2.0.0+1"); code(err) != plxerr.ResourceNotFound {
		t.Errorf("an unknown build: %v", err)
	}
	f.now = f.now.Add(1e9)
	if _, _, err := f.rel.UploadNativeCatalogue(ctx, f.owner, f.app, "1.0.0+2", without(t, catalogue, "slots")); err != nil {
		t.Fatal(err)
	}
	builds, err := f.rel.ListHostBuilds(ctx, f.viewer, f.app, storage.Cursor{}, 10)
	if err != nil || len(builds) != 2 || builds[0].Build != "1.0.0+2" || builds[1].Build != "1.0.0+1" {
		t.Errorf("ListHostBuilds: %+v %v", builds, err)
	}
	page, err := f.rel.ListHostBuilds(ctx, f.viewer, f.app, storage.Cursor{Time: builds[0].UploadedAt, Key: builds[0].Build}, 10)
	if err != nil || len(page) != 1 || page[0].Build != "1.0.0+1" {
		t.Errorf("the next page: %+v %v", page, err)
	}
}

// TestHostBuildCompatibility publishes the routing project against two
// host builds, one lacking the slot Counter: publishing the plugin that
// uses it warns at the use's JSON path, the release reports the build as
// incompatible, and the build's devices get a manifest of the newest
// release they can run.
// Verifies: WGT-032, REL-080.
func TestHostBuildCompatibility(t *testing.T) {
	t.Parallel()
	f := newFixtureOf(t, "routing")
	ctx := context.Background()
	catalogue := routingCatalogue(t)
	if _, _, err := f.rel.UploadNativeCatalogue(ctx, f.owner, f.app, "1.0.0+1", catalogue); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.rel.UploadNativeCatalogue(ctx, f.owner, f.app, "1.0.0+2", without(t, catalogue, "slots")); err != nil {
		t.Fatal(err)
	}
	// Release 1: the slots page shows text instead of the slot.
	if _, _, err := f.docs.AcquireLock(ctx, f.owner, f.app, f.loans, "edit", false); err != nil {
		t.Fatal(err)
	}
	const path = "plugins/nav/pages/slots.page.json"
	page, err := f.docs.GetDocument(ctx, f.owner, f.app, f.loans, path)
	if err != nil {
		t.Fatal(err)
	}
	slotted := page.Content
	var doc map[string]any
	if err := json.Unmarshal(slotted, &doc); err != nil {
		t.Fatal(err)
	}
	body := doc["root"].(map[string]any)["slots"].(map[string]any)["body"].(map[string]any)
	body["slots"] = map[string]any{"child": map[string]any{
		"id": "01f0c450-6c00-7000-8000-000000000245", "type": "Text", "props": map[string]any{"data": "Taps"},
	}}
	plain, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.docs.PutDocument(ctx, f.owner, f.app, f.loans, "edit", path, plain, page.Revision); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", f.loans} {
		if j := f.publish(t, id, false); j.State != release.StateSucceeded {
			t.Fatalf("publish %q without the slot: %+v", id, j)
		}
	}
	prod := f.envs["production"].ID
	first, diags, err := f.rel.CreateRelease(ctx, f.owner, f.app, f.envs["development"].ID, nil, "")
	if err != nil || diags.HasErrors() {
		t.Fatalf("the first release: %v %v", diags, err)
	}
	if _, err := f.rel.PromoteRelease(ctx, f.owner, f.app, first.Sequence, prod, ""); err != nil {
		t.Fatal(err)
	}
	// Release 2 uses the slot again: build 1.0.0+2 cannot run it.
	edited, err := f.docs.GetDocument(ctx, f.owner, f.app, f.loans, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.docs.PutDocument(ctx, f.owner, f.app, f.loans, "edit", path, slotted, edited.Revision); err != nil {
		t.Fatal(err)
	}
	job := f.publish(t, f.loans, false)
	if job.State != release.StateFailed || !hasCode(job.Diagnostics, plxerr.WarningsNotAcknowledged) {
		t.Fatalf("an unacknowledged publish: %+v", job)
	}
	i := slices.IndexFunc(job.Diagnostics, func(d plxerr.Diagnostic) bool { return d.Code == plxerr.HostBuildIncompatible })
	if i < 0 || job.Diagnostics[i].File != path || job.Diagnostics[i].Path != "/root/slots/body/slots/child/type" ||
		job.Diagnostics[i].Message != `host build 1.0.0+2 registers no native slot "Counter"` {
		t.Fatalf("the host build warning: %+v", job.Diagnostics)
	}
	if j := f.publish(t, f.loans, true); j.State != release.StateSucceeded {
		t.Fatalf("an acknowledged publish: %+v", j)
	}
	second, diags, err := f.rel.CreateRelease(ctx, f.owner, f.app, f.envs["development"].ID, map[string]int64{"nav": 2}, "")
	if err != nil || diags.HasErrors() {
		t.Fatalf("the second release: %v %v", diags, err)
	}
	if _, err := f.rel.PromoteRelease(ctx, f.owner, f.app, second.Sequence, prod, ""); err != nil {
		t.Fatal(err)
	}
	c, err := f.rel.Compatibility(ctx, f.viewer, f.app, second.Sequence)
	want := []release.IncompatibleBuild{{Build: "1.0.0+2", FallbackSequence: first.Sequence, Missing: []string{"slot Counter"}}}
	if err != nil || len(c.IncompatibleBuilds) != 1 || c.IncompatibleBuilds[0].Build != want[0].Build ||
		c.IncompatibleBuilds[0].FallbackSequence != want[0].FallbackSequence || !slices.Equal(c.IncompatibleBuilds[0].Missing, want[0].Missing) {
		t.Fatalf("the second release's compatibility: %+v %v", c, err)
	}
	if c, err := f.rel.Compatibility(ctx, f.viewer, f.app, first.Sequence); err != nil || len(c.IncompatibleBuilds) != 0 {
		t.Errorf("the first release's compatibility: %+v %v", c, err)
	}
	// The worker signs the channel's manifest and the build's.
	f.run(t)
	manifest := func(build string) int64 {
		t.Helper()
		m, err := f.rel.GetManifest(ctx, release.ManifestRequest{OrganizationID: f.org, AppID: f.app, EnvironmentID: prod, HostBuild: build})
		if err != nil {
			t.Fatalf("the manifest of build %q: %v", build, err)
		}
		return m.Document.ReleaseSequence
	}
	for build, want := range map[string]int64{"": second.Sequence, "1.0.0+1": second.Sequence, "1.0.0+2": first.Sequence, "7.0.0+1": second.Sequence} {
		if got := manifest(build); got != want {
			t.Errorf("build %q receives release %d, want %d", build, got, want)
		}
	}
	// A build that lacks the route no release can run: it keeps the
	// channel's manifest. Uploading it signs the channels again.
	f.now = f.now.Add(2e9)
	if _, _, err := f.rel.UploadNativeCatalogue(ctx, f.owner, f.app, "1.0.0+3", without(t, catalogue, "routes")); err != nil {
		t.Fatal(err)
	}
	f.run(t)
	if got := manifest("1.0.0+3"); got != second.Sequence {
		t.Errorf("a build no release fits receives release %d", got)
	}
	if got := manifest("1.0.0+2"); got != first.Sequence {
		t.Errorf("after signing again, build 1.0.0+2 receives release %d", got)
	}
	// Once the channel's release fits the build again, the build's old
	// manifest is no longer served.
	if _, err := f.rel.PromoteRelease(ctx, f.owner, f.app, first.Sequence, prod, ""); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(2e9)
	f.run(t)
	if got := manifest("1.0.0+2"); got != first.Sequence {
		t.Errorf("after promoting release 1 again: %d", got)
	}
	if got := manifest("1.0.0+1"); got != first.Sequence {
		t.Errorf("the channel's own manifest after promoting release 1 again: %d", got)
	}
	etag := func(build string) string {
		t.Helper()
		m, err := f.rel.GetManifest(ctx, release.ManifestRequest{OrganizationID: f.org, AppID: f.app, EnvironmentID: prod, HostBuild: build})
		if err != nil {
			t.Fatal(err)
		}
		return m.ETag
	}
	if etag("1.0.0+2") != etag("") {
		t.Error("build 1.0.0+2 still receives its own, older manifest")
	}
}
