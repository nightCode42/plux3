// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"connectrpc.com/connect"

	"github.com/nightCode42/plux3/backend/internal/pluxv1"
)

const pluginDoc = `{"schemaVersion":"1.0.0","kind":"plugin","id":"01a0c450-6c00-7011-8000-000000020ddf","key":"loans",` +
	`"name":"Loans","icon":{"monogram":{"background":"#5B3DF5","text":"LN"}},"team":"lending",` +
	`"entryPage":"01a0c450-6c00-7012-8000-000000022cce","pages":["01a0c450-6c00-7012-8000-000000022cce"]}`

const pageDoc = `{"schemaVersion":"1.0.0","kind":"page","id":"01a0c450-6c00-7012-8000-000000022cce","key":"home",` +
	`"pageKind":"screen","title":"Home","root":{"id":"01a0c450-6c00-7026-8000-00000004977a","type":"Text","props":{"data":"Hello"}}}`

const templateDoc = `{"schemaVersion":"1.0.0","kind":"template","id":"01a0c450-6c00-702d-8000-000000057003","key":"greeting",` +
	`"name":"Greeting","category":"display","visibility":"organization","parameters":[{"name":"text","type":"string","path":"/props/data","default":"Hi"}],` +
	`"root":{"id":"01a0c450-6c00-702e-8000-000000058ef2","type":"Text","props":{"data":"Hi"}}}`

// Verifies: SRV-030, SRV-031, SRV-040, SRV-041, SRV-042, GOV-031, SCH-006, SCH-031.
// The draft services driven over HTTP: plugins, locks, documents with
// revisions and patches, history, export and import as streams,
// templates, and the error each refusal maps to.
func TestDocumentServicesEndToEnd(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ctx := context.Background()
	_, invitation, err := w.auth.Bootstrap(ctx, "admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.identity.AcceptInvitation(ctx, connect.NewRequest(&pluxv1.AcceptInvitationRequest{
		Invitation: invitation, DisplayName: "Admin", Password: "correct horse battery",
	})); err != nil {
		t.Fatal(err)
	}
	admin := w.signIn(t, "admin@example.com")
	admin.org = must(w.org.CreateOrganization(ctx, req(admin, &pluxv1.CreateOrganizationRequest{Key: "acme", Name: "Acme"})))(t).GetOrganization().GetId()
	app := must(w.app.CreateApp(ctx, req(admin, &pluxv1.CreateAppRequest{Key: "demo", Name: "Demo"})))(t).GetApp().GetId()

	// A plugin, its lock and its documents.
	plugin := must(w.plugin.CreatePlugin(ctx, req(admin, &pluxv1.CreatePluginRequest{AppId: app, Key: "loans", Name: "Loans"})))(t).GetPlugin()
	if _, err := w.plugin.CreatePlugin(ctx, req(admin, &pluxv1.CreatePluginRequest{AppId: app, Key: "loans", Name: "Again"})); codeOf(err) != connect.CodeAlreadyExists {
		t.Errorf("a duplicate plugin: %v", err)
	}
	pid := plugin.GetId()
	put := &pluxv1.PutDocumentRequest{AppId: app, PluginId: pid, Path: "plugins/loans/plugin.json", Content: []byte(pluginDoc), Session: "tab-1"}
	if _, err := w.document.PutDocument(ctx, req(admin, put)); codeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("a write without the lock: %v", err)
	}
	lock := must(w.plugin.AcquireLock(ctx, req(admin, &pluxv1.AcquireLockRequest{AppId: app, PluginId: pid, Session: "tab-1"})))(t).GetLock()
	if lock.GetSession() != "tab-1" || lock.GetHolder().GetId() == "" {
		t.Errorf("AcquireLock = %+v", lock)
	}
	must(w.plugin.RenewLock(ctx, req(admin, &pluxv1.RenewLockRequest{AppId: app, PluginId: pid, Session: "tab-1"})))(t)
	if got := must(w.plugin.GetLock(ctx, req(admin, &pluxv1.GetLockRequest{AppId: app, PluginId: pid})))(t); got.GetLock().GetSession() != "tab-1" {
		t.Errorf("GetLock = %+v", got)
	}
	must(w.plugin.RequestLock(ctx, req(admin, &pluxv1.RequestLockRequest{AppId: app, PluginId: pid})))(t)
	must(w.document.PutDocument(ctx, req(admin, put)))(t)
	page := must(w.document.PutDocument(ctx, req(admin, &pluxv1.PutDocumentRequest{
		AppId: app, PluginId: pid, Path: "plugins/loans/pages/home.page.json", Content: []byte(pageDoc), Session: "tab-1",
	})))(t)
	if page.GetDocument().GetRevision() != 1 || page.GetSnapshotId() == "" {
		t.Errorf("PutDocument = %+v", page)
	}
	stale := &pluxv1.PutDocumentRequest{AppId: app, PluginId: pid, Path: "plugins/loans/pages/home.page.json", Content: []byte(pageDoc), Session: "tab-1"}
	if _, err := w.document.PutDocument(ctx, req(admin, stale)); codeOf(err) != connect.CodeAborted {
		t.Errorf("a stale write: %v", err)
	}
	patched := must(w.document.PatchDocument(ctx, req(admin, &pluxv1.PatchDocumentRequest{
		AppId: app, PluginId: pid, Path: "plugins/loans/pages/home.page.json", IfRevision: 1, Session: "tab-1",
		Patch: []*pluxv1.PatchOp{{Op: "replace", Path: "/root/props/data", Value: []byte(`"Patched"`)}},
	})))(t)
	if patched.GetDocument().GetRevision() != 2 {
		t.Errorf("PatchDocument = %+v", patched)
	}
	if _, err := w.document.PatchDocument(ctx, req(admin, &pluxv1.PatchDocumentRequest{
		AppId: app, PluginId: pid, Path: "plugins/loans/pages/home.page.json", IfRevision: 2, Session: "tab-1",
		Patch: []*pluxv1.PatchOp{{Op: "replace", Path: "/root/props/data", Value: []byte(`{`)}},
	})); codeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a patch value that is not JSON: %v", err)
	}
	got := must(w.document.GetDocument(ctx, req(admin, &pluxv1.GetDocumentRequest{AppId: app, PluginId: pid, Path: "plugins/loans/pages/home.page.json"})))(t)
	if !strings.Contains(string(got.GetDocument().GetContent()), "Patched") {
		t.Errorf("GetDocument = %s", got.GetDocument().GetContent())
	}
	list := must(w.document.ListDocuments(ctx, req(admin, &pluxv1.ListDocumentsRequest{AppId: app, PluginId: pid, Page: &pluxv1.Page{PageSize: 1}})))(t)
	if len(list.GetDocuments()) != 1 || list.GetPage().GetNextPageToken() == "" {
		t.Fatalf("ListDocuments = %+v", list)
	}
	next := must(w.document.ListDocuments(ctx, req(admin, &pluxv1.ListDocumentsRequest{AppId: app, PluginId: pid, Page: &pluxv1.Page{PageSize: 1, PageToken: list.GetPage().GetNextPageToken()}})))(t)
	if len(next.GetDocuments()) != 1 || next.GetDocuments()[0].GetPath() == list.GetDocuments()[0].GetPath() {
		t.Errorf("the second page = %+v", next)
	}
	diags := must(w.document.ValidateDraft(ctx, req(admin, &pluxv1.ValidateDraftRequest{AppId: app, PluginId: pid})))(t)
	if len(diags.GetDiagnostics()) == 0 {
		t.Error("a draft without app.json validated clean")
	}
	must(w.document.ValidatePage(ctx, req(admin, &pluxv1.ValidatePageRequest{AppId: app, PluginId: pid, Path: "plugins/loans/pages/home.page.json", Content: []byte(pageDoc)})))(t)

	// History.
	snaps := must(w.document.ListSnapshots(ctx, req(admin, &pluxv1.ListSnapshotsRequest{AppId: app, PluginId: pid})))(t).GetSnapshots()
	if len(snaps) != 3 {
		t.Fatalf("ListSnapshots = %+v", snaps)
	}
	snap := must(w.document.GetSnapshot(ctx, req(admin, &pluxv1.GetSnapshotRequest{Id: snaps[1].GetId()})))(t)
	if len(snap.GetDocuments()) != 2 {
		t.Errorf("GetSnapshot = %+v", snap)
	}
	cmp := must(w.document.CompareSnapshots(ctx, req(admin, &pluxv1.CompareSnapshotsRequest{FromId: snaps[1].GetId(), ToId: snaps[0].GetId()})))(t)
	if len(cmp.GetChanges()) != 1 || len(cmp.GetChanges()[0].GetPatch()) == 0 {
		t.Errorf("CompareSnapshots = %+v", cmp)
	}
	must(w.document.RestoreSnapshot(ctx, req(admin, &pluxv1.RestoreSnapshotRequest{Id: snaps[1].GetId(), Session: "tab-1"})))(t)

	// Export, then import it back as a stream.
	export, err := w.document.ExportDraft(ctx, req(admin, &pluxv1.ExportDraftRequest{AppId: app, PluginId: pid}))
	if err != nil {
		t.Fatal(err)
	}
	var files []*pluxv1.ExportDraftResponse
	for export.Receive() {
		files = append(files, export.Msg())
	}
	if err := export.Err(); err != nil || len(files) != 2 {
		t.Fatalf("ExportDraft = %d files, %v", len(files), err)
	}
	stream := w.document.ImportDraft(ctx)
	stream.RequestHeader().Set("Cookie", admin.cookie)
	stream.RequestHeader().Set("X-CSRF-Token", admin.csrf)
	stream.RequestHeader().Set("X-Plux-Organization", admin.org)
	for i, f := range files {
		m := &pluxv1.ImportDraftRequest{Path: f.GetPath(), Content: f.GetContent()}
		if i == 0 {
			m.AppId, m.PluginId, m.Session = app, pid, "tab-1"
		}
		if err := stream.Send(m); err != nil && !errors.Is(err, io.EOF) {
			t.Fatal(err)
		}
	}
	imported, err := stream.CloseAndReceive()
	if err != nil || imported.Msg.GetSnapshotId() == "" {
		t.Fatalf("ImportDraft = %+v, %v", imported, err)
	}

	// Templates live in the app-level draft.
	must(w.plugin.AcquireLock(ctx, req(admin, &pluxv1.AcquireLockRequest{AppId: app, Session: "tab-1"})))(t)
	must(w.document.PutDocument(ctx, req(admin, &pluxv1.PutDocumentRequest{
		AppId: app, Path: "templates/greeting.template.json", Content: []byte(templateDoc), Session: "tab-1",
	})))(t)
	templates := must(w.template.ListTemplates(ctx, req(admin, &pluxv1.ListTemplatesRequest{AppId: app, Kind: "page"})))(t).GetTemplates()
	if len(templates) != 1 || len(templates[0].GetParameters()) != 1 || templates[0].GetParameters()[0].GetRequired() {
		t.Fatalf("ListTemplates = %+v", templates)
	}
	must(w.template.GetTemplate(ctx, req(admin, &pluxv1.GetTemplateRequest{Id: templates[0].GetId()})))(t)
	inst := must(w.template.Instantiate(ctx, req(admin, &pluxv1.InstantiateRequest{
		TemplateId: templates[0].GetId(), AppId: app, PluginId: pid, Session: "tab-1", Key: "welcome", Arguments: []byte(`{"text":"Welcome"}`),
	})))(t)
	if len(inst.GetCreated()) != 2 {
		t.Errorf("Instantiate = %+v", inst)
	}
	comps := must(w.comp.ListComponents(ctx, req(admin, &pluxv1.ListComponentsRequest{AppId: app})))(t)
	if len(comps.GetComponents()) != 0 {
		t.Errorf("ListComponents = %+v", comps)
	}
	if _, err := w.comp.GetComponent(ctx, req(admin, &pluxv1.GetComponentRequest{Id: pid})); codeOf(err) != connect.CodeNotFound {
		t.Errorf("GetComponent of a plugin: %v", err)
	}
	if _, err := w.comp.ListUsages(ctx, req(admin, &pluxv1.ListUsagesRequest{AppId: app, EntityKind: "page", EntityId: "01a0c450-6c00-7012-8000-000000022cce"})); err != nil {
		t.Errorf("ListUsages: %v", err)
	}

	// Limits, rename, release and delete.
	must(w.plugin.SetPluginLimit(ctx, req(admin, &pluxv1.SetPluginLimitRequest{PluginId: pid, Key: "plugin.pages", Value: 10})))(t)
	limitsRes := must(w.plugin.ListPluginLimits(ctx, req(admin, &pluxv1.ListPluginLimitsRequest{PluginId: pid})))(t)
	if len(limitsRes.GetLimits()) == 0 {
		t.Error("ListPluginLimits is empty")
	}
	must(w.plugin.UpdatePlugin(ctx, req(admin, &pluxv1.UpdatePluginRequest{Id: pid, Name: "Lending"})))(t)
	if got := must(w.plugin.GetPlugin(ctx, req(admin, &pluxv1.GetPluginRequest{Id: pid})))(t); got.GetPlugin().GetName() != "Lending" {
		t.Errorf("GetPlugin = %+v", got)
	}
	if ps := must(w.plugin.ListPlugins(ctx, req(admin, &pluxv1.ListPluginsRequest{AppId: app})))(t); len(ps.GetPlugins()) != 1 {
		t.Errorf("ListPlugins = %+v", ps)
	}
	must(w.document.DeleteDocument(ctx, req(admin, &pluxv1.DeleteDocumentRequest{
		AppId: app, PluginId: pid, Path: "plugins/loans/pages/welcome.page.json", IfRevision: 1, Session: "tab-1",
	})))(t)
	must(w.plugin.ReleaseLock(ctx, req(admin, &pluxv1.ReleaseLockRequest{AppId: app, PluginId: pid, Session: "tab-1"})))(t)
	deleted := must(w.plugin.DeletePlugin(ctx, req(admin, &pluxv1.DeletePluginRequest{Id: pid})))(t)
	if deleted.GetTrash().GetKind() != "plugin" {
		t.Errorf("DeletePlugin = %+v", deleted)
	}
}
