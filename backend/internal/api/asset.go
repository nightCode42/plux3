// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"errors"
	"io"
	"time"

	"connectrpc.com/connect"

	"github.com/nightCode42/plux3/backend/internal/document"
	"github.com/nightCode42/plux3/backend/internal/pluxv1"
	"github.com/nightCode42/plux3/backend/internal/pluxv1/pluxv1connect"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
	"github.com/nightCode42/plux3/backend/internal/storage"
)

// assetURLTTL is how long an asset link handed to Studio stays valid.
const assetURLTTL = 15 * time.Minute

// Asset serves AssetService.
type Asset struct{ h *Handlers }

var _ pluxv1connect.AssetServiceHandler = Asset{}

// Asset returns the AssetService handler.
func (h *Handlers) Asset() Asset { return Asset{h: h} }

// UploadAsset receives a file in chunks; the first message names the
// app, the file and the editing session (SRV-060). The chunks together
// are bounded by the installation's asset.fileSize; the app's own limit
// is checked by the service.
func (s Asset) UploadAsset(ctx context.Context, stream *connect.ClientStream[pluxv1.UploadAssetRequest]) (*connect.Response[pluxv1.UploadAssetResponse], error) {
	p, err := s.h.principal(ctx, stream.RequestHeader(), "")
	if err != nil {
		return nil, err
	}
	var (
		appID, pluginID, name, session string
		data                           []byte
		first                          = true
	)
	max := s.h.Limits.Get(limits.AssetFileSize)
	for stream.Receive() {
		m := stream.Msg()
		if first {
			appID, pluginID, name, session, first = m.GetAppId(), m.GetPluginId(), m.GetName(), m.GetSession(), false
		}
		if int64(len(data))+int64(len(m.GetChunk())) > max {
			return nil, plxerr.New(plxerr.LimitExceeded, "the file is above asset.fileSize = %d bytes", max)
		}
		data = append(data, m.GetChunk()...)
	}
	if err := stream.Err(); err != nil && !errors.Is(err, io.EOF) {
		return nil, err //nolint:wrapcheck // the stream's own error
	}
	a, err := s.h.Documents.UploadAsset(ctx, p, appID, pluginID, session, name, data)
	if err != nil {
		return nil, err //nolint:wrapcheck // a domain error
	}
	return connect.NewResponse(&pluxv1.UploadAssetResponse{Asset: s.assetProto(ctx, a)}), nil
}

// GetAsset returns an asset with links to its file and variants.
func (s Asset) GetAsset(ctx context.Context, req *connect.Request[pluxv1.GetAssetRequest]) (*connect.Response[pluxv1.GetAssetResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.GetAssetResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		a, err := s.h.Documents.GetAsset(ctx, p, req.Msg.GetId())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.GetAssetResponse{Asset: s.assetProto(ctx, a)}, nil
	})
}

// ListAssets lists an app's assets in file order.
func (s Asset) ListAssets(ctx context.Context, req *connect.Request[pluxv1.ListAssetsRequest]) (*connect.Response[pluxv1.ListAssetsResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.ListAssetsResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		if req.Msg.GetPluginId() != "" {
			return nil, plxerr.New(plxerr.InvalidProjectLayout, "assets belong to the app, not to a plugin")
		}
		q := Query{OrderBy: "name", Filters: map[string]func(Item) string{
			"name":       func(m Item) string { return m.(*pluxv1.Asset).GetName() },
			"media_type": func(m Item) string { return m.(*pluxv1.Asset).GetMediaType() },
			"processing": func(m Item) string { return m.(*pluxv1.Asset).GetProcessing() },
		}}
		page := req.Msg.GetPage()
		clauses, err := q.Parse(page)
		if err != nil {
			return nil, err
		}
		after, size, err := s.h.Pages.Request(req.Spec().Procedure, p.OrganizationID, page)
		if err != nil {
			return nil, err
		}
		assets, err := s.h.Documents.ListAssets(ctx, p, req.Msg.GetAppId(), after.Key, size)
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		out := &pluxv1.ListAssetsResponse{Page: &pluxv1.PageResult{}}
		var last storage.Cursor
		for _, a := range assets {
			last = storage.Cursor{Key: a.File}
			if item := s.assetProto(ctx, a); q.Keep(clauses, item) {
				out.Assets = append(out.Assets, item)
			}
		}
		out.Page.NextPageToken = s.h.Pages.Next(req.Spec().Procedure, p.OrganizationID, page, len(assets), size, last)
		return out, Mask(page.GetReadMask(), out.Assets)
	})
}

// DeleteAsset removes an asset from the app's index.
func (s Asset) DeleteAsset(ctx context.Context, req *connect.Request[pluxv1.DeleteAssetRequest]) (*connect.Response[pluxv1.DeleteAssetResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.DeleteAssetResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		if err := s.h.Documents.DeleteAsset(ctx, p, req.Msg.GetId(), req.Msg.GetSession()); err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.DeleteAssetResponse{}, nil
	})
}

// artifact describes a stored object with a link to it; a link that
// cannot be made is left out rather than failing the call.
func (s Asset) artifact(ctx context.Context, sum string, size int64, mediaType string) *pluxv1.Artifact {
	out := &pluxv1.Artifact{Sha256: sum, Size: size, MediaType: mediaType}
	if url, err := s.h.Documents.AssetURL(ctx, sum, assetURLTTL); err == nil && url != "" {
		out.Urls = []string{url}
	}
	return out
}

func (s Asset) assetProto(ctx context.Context, a document.Asset) *pluxv1.Asset {
	out := &pluxv1.Asset{
		Id: a.ID, AppId: a.AppID, Name: a.File, MediaType: a.MediaType, Size: a.Size, Sha256: a.SHA256,
		Width: int32(a.Width), Height: int32(a.Height), Processing: a.Processing, //nolint:gosec // bounded by asset.imagePixels
		Diagnostics: diagnosticProtos(a.Diagnostics), UploadedBy: actorProto(a.UploadedBy), CreatedAt: ts(a.CreatedAt),
	}
	for _, v := range a.Variants {
		out.Variants = append(out.Variants, &pluxv1.AssetVariant{
			Kind: v.MediaType, Density: int32(v.Density), Width: int32(v.Width), Height: int32(v.Height), //nolint:gosec // small
			Artifact: s.artifact(ctx, v.SHA256, v.Size, v.MediaType),
		})
	}
	return out
}
