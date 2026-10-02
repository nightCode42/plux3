// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"

	"connectrpc.com/connect"

	"github.com/nightCode42/plux3/backend/internal/pluxv1"
	"github.com/nightCode42/plux3/backend/internal/release"
	"github.com/nightCode42/plux3/backend/internal/storage"
)

// NativeCatalogue serves NativeCatalogueService (CLI-006, ADR-0041).
type NativeCatalogue struct{ h *Handlers }

// NativeCatalogue returns the NativeCatalogueService handler.
func (h *Handlers) NativeCatalogue() NativeCatalogue { return NativeCatalogue{h: h} }

// UploadNativeCatalogue stores a host build's catalogue.
func (s NativeCatalogue) UploadNativeCatalogue(ctx context.Context, req *connect.Request[pluxv1.UploadNativeCatalogueRequest]) (*connect.Response[pluxv1.UploadNativeCatalogueResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.UploadNativeCatalogueResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		b, created, err := s.h.Releases.UploadNativeCatalogue(ctx, p, req.Msg.GetAppId(), req.Msg.GetHostBuild(), req.Msg.GetCatalogue())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.UploadNativeCatalogueResponse{HostBuild: hostBuildProto(b), Created: created}, nil
	})
}

// GetNativeCatalogue returns a host build's catalogue.
func (s NativeCatalogue) GetNativeCatalogue(ctx context.Context, req *connect.Request[pluxv1.GetNativeCatalogueRequest]) (*connect.Response[pluxv1.GetNativeCatalogueResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.GetNativeCatalogueResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		b, data, err := s.h.Releases.GetNativeCatalogue(ctx, p, req.Msg.GetAppId(), req.Msg.GetHostBuild())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.GetNativeCatalogueResponse{HostBuild: hostBuildProto(b), Catalogue: data}, nil
	})
}

// ListHostBuilds lists an app's host builds, newest upload first.
func (s NativeCatalogue) ListHostBuilds(ctx context.Context, req *connect.Request[pluxv1.ListHostBuildsRequest]) (*connect.Response[pluxv1.ListHostBuildsResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.ListHostBuildsResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		page := req.Msg.GetPage()
		after, size, err := s.h.Pages.Request(req.Spec().Procedure, p.OrganizationID, page)
		if err != nil {
			return nil, err
		}
		builds, err := s.h.Releases.ListHostBuilds(ctx, p, req.Msg.GetAppId(), after, size)
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		out := &pluxv1.ListHostBuildsResponse{Page: &pluxv1.PageResult{}}
		var last storage.Cursor
		for _, b := range builds {
			last = storage.Cursor{Time: b.UploadedAt, Key: b.Build}
			out.HostBuilds = append(out.HostBuilds, hostBuildProto(b))
		}
		out.Page.NextPageToken = s.h.Pages.Next(req.Spec().Procedure, p.OrganizationID, page, len(builds), size, last)
		return out, nil
	})
}

func hostBuildProto(b release.HostBuild) *pluxv1.HostBuild {
	return &pluxv1.HostBuild{
		AppId: b.AppID, HostBuild: b.Build, Sha256: b.SHA256, UploadedBy: actorProto(b.UploadedBy),
		UploadedAt: ts(b.UploadedAt), Devices: b.Devices,
	}
}
