// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
	"testing"

	"connectrpc.com/connect"

	"github.com/nightCode42/plux3/backend/internal/pluxv1"
)

// Verifies: SRV-060.
// An asset is uploaded in chunks over a client stream, listed, read with
// links, and deleted; the stream is authenticated like any call.
func TestAssetServiceEndToEnd(t *testing.T) {
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
	admin.org = must(w.org.CreateOrganization(ctx, req(admin, &pluxv1.CreateOrganizationRequest{Key: "acme", Name: "Acme"})))(t).GetOrganization().GetId()
	app := must(w.app.CreateApp(ctx, req(admin, &pluxv1.CreateAppRequest{Key: "demo", Name: "Demo"})))(t).GetApp().GetId()
	must(w.plugin.AcquireLock(ctx, req(admin, &pluxv1.AcquireLockRequest{AppId: app, Session: "s"})))(t)

	var img bytes.Buffer
	if err := png.Encode(&img, image.NewNRGBA(image.Rect(0, 0, 16, 16))); err != nil {
		t.Fatal(err)
	}
	upload := func(c caller, name string, data []byte) (*connect.Response[pluxv1.UploadAssetResponse], error) {
		stream := w.asset.UploadAsset(ctx)
		stream.RequestHeader().Set("Cookie", c.cookie)
		stream.RequestHeader().Set("X-CSRF-Token", c.csrf)
		stream.RequestHeader().Set("X-Plux-Organization", c.org)
		for i := 0; i < len(data); i += 64 {
			m := &pluxv1.UploadAssetRequest{Chunk: data[i:min(i+64, len(data))]}
			if i == 0 {
				m.AppId, m.Name, m.Session = app, name, "s"
			}
			if err := stream.Send(m); err != nil && !errors.Is(err, io.EOF) {
				t.Fatal(err)
			}
		}
		return stream.CloseAndReceive()
	}
	res, err := upload(admin, "images/logo.png", img.Bytes())
	if err != nil {
		t.Fatalf("UploadAsset: %v", err)
	}
	a := res.Msg.GetAsset()
	if a.GetMediaType() != "image/png" || a.GetProcessing() != "pending" || a.GetWidth() != 16 {
		t.Errorf("uploaded %+v", a)
	}
	if _, err := upload(caller{}, "images/x.png", img.Bytes()); codeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("an anonymous upload: %v", err)
	}
	if _, err := upload(admin, "images/x.png", []byte("not an image")); codeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("an unknown type: %v", err)
	}
	got := must(w.asset.GetAsset(ctx, req(admin, &pluxv1.GetAssetRequest{Id: a.GetId()})))(t).GetAsset()
	if got.GetSha256() != a.GetSha256() || got.GetName() != "images/logo.png" {
		t.Errorf("GetAsset = %+v", got)
	}
	list := must(w.asset.ListAssets(ctx, req(admin, &pluxv1.ListAssetsRequest{AppId: app})))(t)
	if len(list.GetAssets()) != 1 {
		t.Errorf("ListAssets = %+v", list)
	}
	if _, err := w.asset.ListAssets(ctx, req(admin, &pluxv1.ListAssetsRequest{AppId: app, PluginId: app})); codeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a plugin's assets: %v", err)
	}
	must(w.asset.DeleteAsset(ctx, req(admin, &pluxv1.DeleteAssetRequest{Id: a.GetId(), Session: "s"})))(t)
	if _, err := w.asset.GetAsset(ctx, req(admin, &pluxv1.GetAssetRequest{Id: a.GetId()})); codeOf(err) != connect.CodeNotFound {
		t.Errorf("a deleted asset: %v", err)
	}
}
