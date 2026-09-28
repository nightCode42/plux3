// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"context"
	"crypto/ed25519"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/nightCode42/plux3/backend/internal/api"
	"github.com/nightCode42/plux3/backend/internal/pluxv1"
	"github.com/nightCode42/plux3/backend/internal/pluxv1/pluxv1connect"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

func code(err error) plxerr.Code {
	c, _ := plxerr.CodeOf(err)
	return c
}

// releasedApp signs an administrator in, imports the loan calculator,
// publishes it and promotes release 1 to production.
func (w *world) releasedApp(t *testing.T) (caller, string, map[string]string) {
	t.Helper()
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
	must(w.identity.ConfirmFactor(ctx, req(admin, &pluxv1.ConfirmFactorRequest{FactorId: enrolled.GetFactor().GetId(), Code: totpNow(t, enrolled.GetSecret())})))(t)
	admin.org = must(w.org.CreateOrganization(ctx, req(admin, &pluxv1.CreateOrganizationRequest{Key: "acme", Name: "Acme"})))(t).GetOrganization().GetId()
	app := must(w.app.CreateApp(ctx, req(admin, &pluxv1.CreateAppRequest{Key: "demo", Name: "Demo"})))(t).GetApp().GetId()
	envs := map[string]string{}
	for _, e := range must(w.app.ListEnvironments(ctx, req(admin, &pluxv1.ListEnvironmentsRequest{AppId: app})))(t).GetEnvironments() {
		envs[e.GetKey()] = e.GetId()
	}
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
	must(w.publish.Publish(ctx, req(admin, &pluxv1.PublishRequest{AppId: app, EnvironmentId: envs["development"]})))(t)
	must(w.publish.Publish(ctx, req(admin, &pluxv1.PublishRequest{AppId: app, PluginId: loans, EnvironmentId: envs["development"]})))(t)
	w.runPublishes(t)
	must(w.release.CreateRelease(ctx, req(admin, &pluxv1.CreateReleaseRequest{AppId: app, EnvironmentId: envs["development"]})))(t)
	must(w.release.PromoteRelease(ctx, req(admin, &pluxv1.PromoteReleaseRequest{AppId: app, Sequence: 1, EnvironmentId: envs["production"]})))(t)
	w.runPublishes(t)
	return admin, app, envs
}

// Verifies: GOV-010, SRV-065, REL-030, REL-031, REL-032, REL-080.
// A device registers, exchanges its credential for a token, syncs
// against the signed manifest, gets "not modified" when nothing changed,
// reports what it runs and sends telemetry; a device token opens nothing
// else, and people see the device and its events.
func TestDevicesEndToEnd(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ctx := context.Background()
	admin, app, envs := w.releasedApp(t)

	if _, err := w.device.RegisterDevice(ctx, connect.NewRequest(&pluxv1.RegisterDeviceRequest{AppId: app, Environment: "nope", Platform: "ios"})); codeOf(err) != connect.CodeNotFound {
		t.Errorf("an unknown environment: %v", err)
	}
	if _, err := w.device.RegisterDevice(ctx, connect.NewRequest(&pluxv1.RegisterDeviceRequest{AppId: app, Environment: "production", Platform: "toaster"})); codeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("an unknown platform: %v", err)
	}
	reg := must(w.device.RegisterDevice(ctx, connect.NewRequest(&pluxv1.RegisterDeviceRequest{
		AppId: app, Environment: "production", Platform: "android", OsVersion: "15", RuntimeVersion: "0.0.9", HostBuild: "42",
	})))(t)
	if reg.GetDeviceSecret() == "" || reg.GetDevice().GetEnvironmentId() != envs["production"] {
		t.Fatalf("RegisterDevice = %+v", reg)
	}
	wrong := &pluxv1.IssueDeviceTokenRequest{DeviceId: reg.GetDevice().GetId(), DeviceSecret: "plux_dsec_wrong"} //nolint:gosec // G101: a wrong credential on purpose.
	if _, err := w.token.IssueDeviceToken(ctx, connect.NewRequest(wrong)); codeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("a wrong secret: %v", err)
	}
	tok := must(w.token.IssueDeviceToken(ctx, connect.NewRequest(&pluxv1.IssueDeviceTokenRequest{DeviceId: reg.GetDevice().GetId(), DeviceSecret: reg.GetDeviceSecret()})))(t)
	if time.Until(tok.GetExpiresAt().AsTime()) > 16*time.Minute {
		t.Errorf("the token lives too long: %v", tok.GetExpiresAt())
	}
	dev := caller{bearer: tok.GetAccessToken()}

	// A device token opens device procedures only, and device procedures
	// need one.
	if _, err := w.app.ListApps(ctx, req(dev, &pluxv1.ListAppsRequest{})); codeOf(err) != connect.CodePermissionDenied {
		t.Errorf("a device listed apps: %v", err)
	}
	if _, err := w.manifest.GetManifest(ctx, req(admin, &pluxv1.GetManifestRequest{})); codeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("a person read a device manifest: %v", err)
	}

	// Each device has its own allowance (SRV-065).
	counts := map[string]int64{}
	limiter := api.RateLimiter{Window: time.Minute, Count: func(_ context.Context, key string, _ time.Duration) (int64, error) {
		counts[key]++
		return counts[key], nil
	}}
	people := func(context.Context, api.Call, func(context.Context) error) error {
		return errors.New("not a device call")
	}
	around := api.DeviceAuthentication(w.devices, limiter, 1, people)
	call := api.Call{Procedure: pluxv1connect.ManifestServiceGetManifestProcedure, Header: http.Header{"Authorization": {"Bearer " + tok.GetAccessToken()}}, ResponseHeader: http.Header{}}
	var seen string
	if err := around(ctx, call, func(ctx context.Context) error {
		d, _ := api.DeviceFrom(ctx)
		seen = d.DeviceID
		return nil
	}); err != nil || seen != reg.GetDevice().GetId() {
		t.Errorf("the first call: %q %v", seen, err)
	}
	if err := around(ctx, call, func(context.Context) error { return nil }); code(err) != plxerr.RateLimited || call.ResponseHeader.Get("Retry-After") == "" {
		t.Errorf("the second call in a minute: %v", err)
	}

	// Sync: nothing installed, so every bundle is downloaded whole.
	res, err := w.manifest.GetManifest(ctx, req(dev, &pluxv1.GetManifestRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	m := res.Msg.GetManifest()
	if m.GetReleaseSequence() != 1 || len(m.GetPlugins()) != 1 || m.GetAppBundle().GetSync().GetAction() != "full" ||
		res.Header().Get("ETag") != res.Msg.GetEtag() || len(m.GetSignatures()) != 1 {
		t.Fatalf("GetManifest = %+v", res.Msg)
	}
	keys := must(w.manifest.GetRootKeys(ctx, req(dev, &pluxv1.GetRootKeysRequest{})))(t).GetKeys()
	if len(keys) != 1 || !ed25519.Verify(keys[0].GetPublicKey(), m.GetSigned(), m.GetSignatures()[0].GetSignature()) {
		t.Fatal("the manifest does not verify with the root key")
	}
	if byPerson := must(w.manifest.GetRootKeys(ctx, req(admin, &pluxv1.GetRootKeysRequest{AppId: app, Environment: "production"})))(t); len(byPerson.GetKeys()) != 1 {
		t.Errorf("GetRootKeys for a person = %+v", byPerson)
	}

	// Installed everything: the manifest is unchanged, and a repeat with
	// the ETag is a small "not modified" (REL-031).
	installed := []*pluxv1.InstalledBundle{{Key: "", Sha256: m.GetAppBundle().GetSha256()}}
	for _, p := range m.GetPlugins() {
		installed = append(installed, &pluxv1.InstalledBundle{Key: p.GetKey(), Sha256: p.GetBundle().GetSha256()})
	}
	synced := must(w.manifest.GetManifest(ctx, req(dev, &pluxv1.GetManifestRequest{InstalledSequence: 1, Installed: installed})))(t)
	if synced.GetManifest().GetAppBundle().GetSync().GetAction() != "keep" {
		t.Errorf("an installed bundle: %+v", synced.GetManifest().GetAppBundle().GetSync())
	}
	nm := must(w.manifest.GetManifest(ctx, req(dev, &pluxv1.GetManifestRequest{InstalledSequence: 1, Installed: installed, IfNoneMatch: synced.GetEtag()})))(t)
	if !nm.GetNotModified() || nm.GetManifest() != nil || proto.Size(nm) > 1024 {
		t.Errorf("not modified = %+v (%d B)", nm, proto.Size(nm))
	}
	must(w.device.ReportInstalled(ctx, req(dev, &pluxv1.ReportInstalledRequest{ReleaseSequence: 1})))(t)
	if _, err := w.device.ReportInstalled(ctx, req(dev, &pluxv1.ReportInstalledRequest{DeviceId: envs["production"], ReleaseSequence: 1})); codeOf(err) != connect.CodePermissionDenied {
		t.Errorf("a report for another device: %v", err)
	}

	// Telemetry: catalogued events are stored; a sensitive field is not.
	now := timestamppb.Now()
	ingest := must(w.events.IngestEvents(ctx, req(dev, &pluxv1.IngestEventsRequest{Events: []*pluxv1.Event{
		{Name: "sync_result", Time: now, ReleaseSequence: 1, Fields: []byte(`{"durationMs": 120, "outcome": "ok"}`)},
		{Name: "custom", Time: now, Fields: []byte(`{"cardNumber": "4111"}`)},
		{Name: "made_up", Time: now},
	}})))(t)
	if ingest.GetAccepted() != 1 || ingest.GetRejected() != 2 || len(ingest.GetDiagnostics()) != 2 {
		t.Errorf("IngestEvents = %+v", ingest)
	}
	listed := must(w.events.ListEvents(ctx, req(admin, &pluxv1.ListEventsRequest{AppId: app, Environment: "production"})))(t)
	if len(listed.GetEvents()) != 1 || listed.GetEvents()[0].GetDeviceId() != reg.GetDevice().GetId() {
		t.Errorf("ListEvents = %+v", listed)
	}

	// People see the device; the compatibility counts it (REL-080).
	got := must(w.device.GetDevice(ctx, req(admin, &pluxv1.GetDeviceRequest{Id: reg.GetDevice().GetId()})))(t).GetDevice()
	if got.GetInstalledSequence() != 1 || got.GetPlatform() != "android" {
		t.Errorf("GetDevice = %+v", got)
	}
	if list := must(w.device.ListDevices(ctx, req(admin, &pluxv1.ListDevicesRequest{AppId: app})))(t); len(list.GetDevices()) != 1 {
		t.Errorf("ListDevices = %+v", list)
	}
	compat := must(w.release.GetCompatibility(ctx, req(admin, &pluxv1.GetCompatibilityRequest{AppId: app, Sequence: 1})))(t).GetCompatibility()
	if compat.GetIncompatibleDevices() != 1 {
		t.Errorf("a 0.0.9 runtime against %q: %+v", compat.GetMinRuntime(), compat)
	}

	// Switches reach the device with the next manifest.
	must(w.control.SetControl(ctx, req(admin, &pluxv1.SetControlRequest{AppId: app, EnvironmentId: envs["production"], KillSwitchPlugins: []string{"loans"}})))(t)
	w.runPublishes(t)
	killed := must(w.manifest.GetManifest(ctx, req(dev, &pluxv1.GetManifestRequest{InstalledSequence: 1, Installed: installed, IfNoneMatch: synced.GetEtag()})))(t)
	if killed.GetNotModified() || len(killed.GetManifest().GetControl().GetKillSwitchPlugins()) != 1 {
		t.Errorf("the kill switch did not arrive: %+v", killed)
	}
	if c := must(w.control.GetControl(ctx, req(admin, &pluxv1.GetControlRequest{AppId: app, EnvironmentId: envs["production"]})))(t); c.GetControl().GetUpdatedBy().GetDisplay() != "Admin" {
		t.Errorf("GetControl = %+v", c)
	}
}
