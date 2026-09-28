// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"errors"
	"io"

	"connectrpc.com/connect"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/document"
	"github.com/nightCode42/plux3/backend/internal/pluxv1"
	"github.com/nightCode42/plux3/backend/internal/pluxv1/pluxv1connect"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema/jcs"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
	"github.com/nightCode42/plux3/backend/internal/storage"
)

// Document serves DocumentService.
type Document struct{ h *Handlers }

var _ pluxv1connect.DocumentServiceHandler = Document{}

// Document returns the DocumentService handler.
func (h *Handlers) Document() Document { return Document{h: h} }

// pathQuery is the query of a list ordered by path and filterable by it.
func pathQuery(path func(Item) string) Query {
	return Query{OrderBy: "path", Filters: map[string]func(Item) string{"path": path}}
}

// GetDocument reads one document of a draft.
func (s Document) GetDocument(ctx context.Context, req *connect.Request[pluxv1.GetDocumentRequest]) (*connect.Response[pluxv1.GetDocumentResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.GetDocumentResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		d, err := s.h.Documents.GetDocument(ctx, p, req.Msg.GetAppId(), req.Msg.GetPluginId(), req.Msg.GetPath())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.GetDocumentResponse{Document: documentProto(d)}, nil
	})
}

// ListDocuments lists a draft's documents in path order.
func (s Document) ListDocuments(ctx context.Context, req *connect.Request[pluxv1.ListDocumentsRequest]) (*connect.Response[pluxv1.ListDocumentsResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.ListDocumentsResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		q := pathQuery(func(m Item) string { return m.(*pluxv1.Document).GetPath() })
		page := req.Msg.GetPage()
		clauses, err := q.Parse(page)
		if err != nil {
			return nil, err
		}
		after, size, err := s.h.Pages.Request(req.Spec().Procedure, p.OrganizationID, page)
		if err != nil {
			return nil, err
		}
		docs, err := s.h.Documents.ListDocuments(ctx, p, req.Msg.GetAppId(), req.Msg.GetPluginId(), after.Key, size, req.Msg.GetContentIncluded())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		out := &pluxv1.ListDocumentsResponse{Page: &pluxv1.PageResult{}}
		var last storage.Cursor
		for _, d := range docs {
			last = storage.Cursor{Key: d.Path}
			if item := documentProto(d); q.Keep(clauses, item) {
				out.Documents = append(out.Documents, item)
			}
		}
		out.Page.NextPageToken = s.h.Pages.Next(req.Spec().Procedure, p.OrganizationID, page, len(docs), size, last)
		return out, Mask(page.GetReadMask(), out.Documents)
	})
}

// PutDocument writes a document whole (SRV-030).
func (s Document) PutDocument(ctx context.Context, req *connect.Request[pluxv1.PutDocumentRequest]) (*connect.Response[pluxv1.PutDocumentResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.PutDocumentResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		m := req.Msg
		w, err := s.h.Documents.PutDocument(ctx, p, m.GetAppId(), m.GetPluginId(), m.GetSession(), m.GetPath(), m.GetContent(), m.GetIfRevision())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.PutDocumentResponse{Document: firstDocument(w), SnapshotId: w.SnapshotID, Diagnostics: diagnosticProtos(w.Diagnostics)}, nil
	})
}

// PatchDocument applies a JSON Patch to a document (SRV-030).
func (s Document) PatchDocument(ctx context.Context, req *connect.Request[pluxv1.PatchDocumentRequest]) (*connect.Response[pluxv1.PatchDocumentResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.PatchDocumentResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		m := req.Msg
		ops, err := patchOps(m.GetPatch(), int(s.h.Limits.Get(limits.DocumentJSONDepth)))
		if err != nil {
			return nil, err
		}
		w, err := s.h.Documents.PatchDocument(ctx, p, m.GetAppId(), m.GetPluginId(), m.GetSession(), m.GetPath(), ops, m.GetIfRevision())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.PatchDocumentResponse{Document: firstDocument(w), SnapshotId: w.SnapshotID, Diagnostics: diagnosticProtos(w.Diagnostics)}, nil
	})
}

// DeleteDocument deletes a document; a page goes to the trash.
func (s Document) DeleteDocument(ctx context.Context, req *connect.Request[pluxv1.DeleteDocumentRequest]) (*connect.Response[pluxv1.DeleteDocumentResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.DeleteDocumentResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		m := req.Msg
		w, err := s.h.Documents.DeleteDocument(ctx, p, m.GetAppId(), m.GetPluginId(), m.GetSession(), m.GetPath(), m.GetIfRevision())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.DeleteDocumentResponse{SnapshotId: w.SnapshotID}, nil
	})
}

// ValidateDraft compiles a draft and returns its diagnostics (SCH-040).
func (s Document) ValidateDraft(ctx context.Context, req *connect.Request[pluxv1.ValidateDraftRequest]) (*connect.Response[pluxv1.ValidateDraftResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.ValidateDraftResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		diags, err := s.h.Documents.ValidateDraft(ctx, p, req.Msg.GetAppId(), req.Msg.GetPluginId())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.ValidateDraftResponse{Diagnostics: diagnosticProtos(diags)}, nil
	})
}

// ValidatePage checks one page as edited, without writing it (SCH-042).
func (s Document) ValidatePage(ctx context.Context, req *connect.Request[pluxv1.ValidatePageRequest]) (*connect.Response[pluxv1.ValidatePageResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.ValidatePageResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		m := req.Msg
		diags, err := s.h.Documents.ValidatePage(ctx, p, m.GetAppId(), m.GetPluginId(), m.GetPath(), m.GetContent())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.ValidatePageResponse{Diagnostics: diagnosticProtos(diags)}, nil
	})
}

// ListSnapshots lists a draft's history, newest first (SRV-031).
func (s Document) ListSnapshots(ctx context.Context, req *connect.Request[pluxv1.ListSnapshotsRequest]) (*connect.Response[pluxv1.ListSnapshotsResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.ListSnapshotsResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		page := req.Msg.GetPage()
		q := Query{OrderBy: "sequence desc", Filters: map[string]func(Item) string{
			"reason": func(m Item) string { return m.(*pluxv1.Snapshot).GetReason() },
		}}
		clauses, err := q.Parse(page)
		if err != nil {
			return nil, err
		}
		after, size, err := s.h.Pages.Request(req.Spec().Procedure, p.OrganizationID, page)
		if err != nil {
			return nil, err
		}
		snaps, err := s.h.Documents.ListSnapshots(ctx, p, req.Msg.GetAppId(), req.Msg.GetPluginId(), after.Sequence, size)
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		out := &pluxv1.ListSnapshotsResponse{Page: &pluxv1.PageResult{}}
		var last storage.Cursor
		for _, sn := range snaps {
			last = storage.Cursor{Sequence: sn.Sequence, ID: sn.ID}
			if item := snapshotProto(sn); q.Keep(clauses, item) {
				out.Snapshots = append(out.Snapshots, item)
			}
		}
		out.Page.NextPageToken = s.h.Pages.Next(req.Spec().Procedure, p.OrganizationID, page, len(snaps), size, last)
		return out, Mask(page.GetReadMask(), out.Snapshots)
	})
}

// GetSnapshot returns a snapshot and the documents of its state.
func (s Document) GetSnapshot(ctx context.Context, req *connect.Request[pluxv1.GetSnapshotRequest]) (*connect.Response[pluxv1.GetSnapshotResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.GetSnapshotResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		sn, docs, err := s.h.Documents.GetSnapshot(ctx, p, req.Msg.GetId(), req.Msg.GetPaths())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		out := &pluxv1.GetSnapshotResponse{Snapshot: snapshotProto(sn)}
		for _, d := range docs {
			out.Documents = append(out.Documents, documentProto(d))
		}
		return out, nil
	})
}

// CompareSnapshots returns how two snapshots differ, per document.
func (s Document) CompareSnapshots(ctx context.Context, req *connect.Request[pluxv1.CompareSnapshotsRequest]) (*connect.Response[pluxv1.CompareSnapshotsResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.CompareSnapshotsResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		changes, err := s.h.Documents.CompareSnapshots(ctx, p, req.Msg.GetFromId(), req.Msg.GetToId())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		out := &pluxv1.CompareSnapshotsResponse{}
		for _, c := range changes {
			patch, err := patchProtos(c.Patch)
			if err != nil {
				return nil, err
			}
			out.Changes = append(out.Changes, &pluxv1.DocumentChange{Path: c.Path, Change: c.Change, Patch: patch})
		}
		return out, nil
	})
}

// RestoreSnapshot writes a snapshot's documents back to its draft.
func (s Document) RestoreSnapshot(ctx context.Context, req *connect.Request[pluxv1.RestoreSnapshotRequest]) (*connect.Response[pluxv1.RestoreSnapshotResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.RestoreSnapshotResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		w, err := s.h.Documents.RestoreSnapshot(ctx, p, req.Msg.GetId(), req.Msg.GetPaths(), req.Msg.GetSession())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.RestoreSnapshotResponse{SnapshotId: w.SnapshotID, Diagnostics: diagnosticProtos(w.Diagnostics)}, nil
	})
}

// ExportDraft streams a draft in the Git layout, one file a message.
func (s Document) ExportDraft(ctx context.Context, req *connect.Request[pluxv1.ExportDraftRequest], stream *connect.ServerStream[pluxv1.ExportDraftResponse]) error {
	p, err := s.h.principal(ctx, req.Header(), "")
	if err != nil {
		return err
	}
	files, err := s.h.Documents.Export(ctx, p, req.Msg.GetAppId(), req.Msg.GetPluginId())
	if err != nil {
		return err //nolint:wrapcheck // a domain error
	}
	for _, f := range files {
		if err := stream.Send(&pluxv1.ExportDraftResponse{Path: f.Path, Content: f.Content}); err != nil {
			return err //nolint:wrapcheck // the stream's own error
		}
	}
	return nil
}

// ImportDraft reads a draft in the Git layout, one file a message; the
// first message names the app, plugin and session. The files together
// are bounded by release.appSize, the most a whole app may be.
func (s Document) ImportDraft(ctx context.Context, stream *connect.ClientStream[pluxv1.ImportDraftRequest]) (*connect.Response[pluxv1.ImportDraftResponse], error) {
	p, err := s.h.principal(ctx, stream.RequestHeader(), "")
	if err != nil {
		return nil, err
	}
	var (
		appID, pluginID, session string
		files                    []document.File
		total                    int64
		first                    = true
	)
	max := s.h.Limits.Get(limits.ReleaseAppSize)
	for stream.Receive() {
		m := stream.Msg()
		if first {
			appID, pluginID, session, first = m.GetAppId(), m.GetPluginId(), m.GetSession(), false
		}
		if m.GetPath() == "" {
			continue
		}
		total += int64(len(m.GetContent()))
		if total > max {
			return nil, plxerr.New(plxerr.LimitExceeded, "the import is above the limit release.appSize = %d bytes", max)
		}
		files = append(files, document.File{Path: m.GetPath(), Content: m.GetContent()})
	}
	if err := stream.Err(); err != nil && !errors.Is(err, io.EOF) {
		return nil, err //nolint:wrapcheck // the stream's own error
	}
	w, err := s.h.Documents.Import(ctx, p, appID, pluginID, session, files)
	if err != nil {
		return nil, err //nolint:wrapcheck // a domain error
	}
	return connect.NewResponse(&pluxv1.ImportDraftResponse{
		Revision: w.Revision, SnapshotId: w.SnapshotID, Diagnostics: diagnosticProtos(w.Diagnostics),
	}), nil
}

// Plugin serves PluginService.
type Plugin struct{ h *Handlers }

var _ pluxv1connect.PluginServiceHandler = Plugin{}

// Plugin returns the PluginService handler.
func (h *Handlers) Plugin() Plugin { return Plugin{h: h} }

// CreatePlugin adds a plugin to an app.
func (s Plugin) CreatePlugin(ctx context.Context, req *connect.Request[pluxv1.CreatePluginRequest]) (*connect.Response[pluxv1.CreatePluginResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.CreatePluginResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		pl, err := s.h.Documents.CreatePlugin(ctx, p, req.Msg.GetAppId(), req.Msg.GetKey(), req.Msg.GetName())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.CreatePluginResponse{Plugin: pluginProto(pl)}, nil
	})
}

// GetPlugin returns a plugin.
func (s Plugin) GetPlugin(ctx context.Context, req *connect.Request[pluxv1.GetPluginRequest]) (*connect.Response[pluxv1.GetPluginResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.GetPluginResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		pl, err := s.h.Documents.GetPlugin(ctx, p, req.Msg.GetId())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.GetPluginResponse{Plugin: pluginProto(pl)}, nil
	})
}

// ListPlugins lists an app's plugins in key order.
func (s Plugin) ListPlugins(ctx context.Context, req *connect.Request[pluxv1.ListPluginsRequest]) (*connect.Response[pluxv1.ListPluginsResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.ListPluginsResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		q := keyQuery(func(m Item) string { return m.(*pluxv1.Plugin).GetKey() })
		page := req.Msg.GetPage()
		clauses, err := q.Parse(page)
		if err != nil {
			return nil, err
		}
		after, size, err := s.h.Pages.Request(req.Spec().Procedure, p.OrganizationID, page)
		if err != nil {
			return nil, err
		}
		plugins, err := s.h.Documents.ListPlugins(ctx, p, req.Msg.GetAppId(), after, size)
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		out := &pluxv1.ListPluginsResponse{Page: &pluxv1.PageResult{}}
		var last storage.Cursor
		for _, pl := range plugins {
			last = storage.Cursor{Key: pl.Key, ID: pl.ID}
			if item := pluginProto(pl); q.Keep(clauses, item) {
				out.Plugins = append(out.Plugins, item)
			}
		}
		out.Page.NextPageToken = s.h.Pages.Next(req.Spec().Procedure, p.OrganizationID, page, len(plugins), size, last)
		return out, Mask(page.GetReadMask(), out.Plugins)
	})
}

// UpdatePlugin renames a plugin.
func (s Plugin) UpdatePlugin(ctx context.Context, req *connect.Request[pluxv1.UpdatePluginRequest]) (*connect.Response[pluxv1.UpdatePluginResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.UpdatePluginResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		pl, err := s.h.Documents.UpdatePlugin(ctx, p, req.Msg.GetId(), req.Msg.GetName())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.UpdatePluginResponse{Plugin: pluginProto(pl)}, nil
	})
}

// DeletePlugin moves a plugin to the trash (GOV-031).
func (s Plugin) DeletePlugin(ctx context.Context, req *connect.Request[pluxv1.DeletePluginRequest]) (*connect.Response[pluxv1.DeletePluginResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.DeletePluginResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		item, err := s.h.Documents.DeletePlugin(ctx, p, req.Msg.GetId())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.DeletePluginResponse{Trash: trashProto(item)}, nil
	})
}

// AcquireLock takes a draft's editing lock (SRV-040, SRV-041).
func (s Plugin) AcquireLock(ctx context.Context, req *connect.Request[pluxv1.AcquireLockRequest]) (*connect.Response[pluxv1.AcquireLockResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.AcquireLockResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		m := req.Msg
		l, preserved, err := s.h.Documents.AcquireLock(ctx, p, m.GetAppId(), m.GetPluginId(), m.GetSession(), m.GetForce())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.AcquireLockResponse{Lock: lockProto(l), PreservedSnapshotId: preserved}, nil
	})
}

// RenewLock is the holder's heartbeat.
func (s Plugin) RenewLock(ctx context.Context, req *connect.Request[pluxv1.RenewLockRequest]) (*connect.Response[pluxv1.RenewLockResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.RenewLockResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		l, err := s.h.Documents.RenewLock(ctx, p, req.Msg.GetAppId(), req.Msg.GetPluginId(), req.Msg.GetSession())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.RenewLockResponse{Lock: lockProto(l)}, nil
	})
}

// ReleaseLock gives a lock up.
func (s Plugin) ReleaseLock(ctx context.Context, req *connect.Request[pluxv1.ReleaseLockRequest]) (*connect.Response[pluxv1.ReleaseLockResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.ReleaseLockResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		if err := s.h.Documents.ReleaseLock(ctx, p, req.Msg.GetAppId(), req.Msg.GetPluginId(), req.Msg.GetSession()); err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.ReleaseLockResponse{}, nil
	})
}

// RequestLock asks the holder for the lock; the holder learns of it
// through its heartbeat (SRV-041).
func (s Plugin) RequestLock(ctx context.Context, req *connect.Request[pluxv1.RequestLockRequest]) (*connect.Response[pluxv1.RequestLockResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.RequestLockResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		l, err := s.h.Documents.RequestLock(ctx, p, req.Msg.GetAppId(), req.Msg.GetPluginId())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.RequestLockResponse{Lock: lockProto(l)}, nil
	})
}

// GetLock returns who holds a draft's lock.
func (s Plugin) GetLock(ctx context.Context, req *connect.Request[pluxv1.GetLockRequest]) (*connect.Response[pluxv1.GetLockResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.GetLockResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		l, err := s.h.Documents.GetLock(ctx, p, req.Msg.GetAppId(), req.Msg.GetPluginId())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.GetLockResponse{Lock: lockProto(l)}, nil
	})
}

// ListPluginLimits lists a plugin's limits (LIM-002).
func (s Plugin) ListPluginLimits(ctx context.Context, req *connect.Request[pluxv1.ListPluginLimitsRequest]) (*connect.Response[pluxv1.ListPluginLimitsResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.ListPluginLimitsResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		us, err := s.h.Documents.ListPluginLimits(ctx, p, req.Msg.GetPluginId())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.ListPluginLimitsResponse{Limits: limitProtos(us)}, nil
	})
}

// SetPluginLimit tightens a limit for a plugin (LIM-002).
func (s Plugin) SetPluginLimit(ctx context.Context, req *connect.Request[pluxv1.SetPluginLimitRequest]) (*connect.Response[pluxv1.SetPluginLimitResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.SetPluginLimitResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		u, err := s.h.Documents.SetPluginLimit(ctx, p, req.Msg.GetPluginId(), req.Msg.GetKey(), req.Msg.GetValue())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.SetPluginLimitResponse{Limit: limitProto(u)}, nil
	})
}

// Component serves ComponentService.
type Component struct{ h *Handlers }

var _ pluxv1connect.ComponentServiceHandler = Component{}

// Component returns the ComponentService handler.
func (h *Handlers) Component() Component { return Component{h: h} }

// ListComponents lists an app's components, or one plugin's.
func (s Component) ListComponents(ctx context.Context, req *connect.Request[pluxv1.ListComponentsRequest]) (*connect.Response[pluxv1.ListComponentsResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.ListComponentsResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		q := pathQuery(func(m Item) string { return m.(*pluxv1.Component).GetPath() })
		q.Filters["key"] = func(m Item) string { return m.(*pluxv1.Component).GetKey() }
		page := req.Msg.GetPage()
		clauses, err := q.Parse(page)
		if err != nil {
			return nil, err
		}
		after, size, err := s.h.Pages.Request(req.Spec().Procedure, p.OrganizationID, page)
		if err != nil {
			return nil, err
		}
		cs, err := s.h.Documents.ListComponents(ctx, p, req.Msg.GetAppId(), req.Msg.GetPluginId(), after.Key, size)
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		out := &pluxv1.ListComponentsResponse{Page: &pluxv1.PageResult{}}
		var last storage.Cursor
		for _, c := range cs {
			last = storage.Cursor{Key: c.Path}
			if item := componentProto(c); q.Keep(clauses, item) {
				out.Components = append(out.Components, item)
			}
		}
		out.Page.NextPageToken = s.h.Pages.Next(req.Spec().Procedure, p.OrganizationID, page, len(cs), size, last)
		return out, Mask(page.GetReadMask(), out.Components)
	})
}

// GetComponent returns a component with its document.
func (s Component) GetComponent(ctx context.Context, req *connect.Request[pluxv1.GetComponentRequest]) (*connect.Response[pluxv1.GetComponentResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.GetComponentResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		c, content, err := s.h.Documents.GetComponent(ctx, p, req.Msg.GetId())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.GetComponentResponse{Component: componentProto(c), Content: content}, nil
	})
}

// ListUsages lists where an entity is used (SCH-041).
func (s Component) ListUsages(ctx context.Context, req *connect.Request[pluxv1.ListUsagesRequest]) (*connect.Response[pluxv1.ListUsagesResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.ListUsagesResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		q := Query{OrderBy: "location", Filters: map[string]func(Item) string{
			"plugin_key": func(m Item) string { return m.(*pluxv1.Usage).GetPluginKey() },
			"kind":       func(m Item) string { return m.(*pluxv1.Usage).GetKind() },
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
		m := req.Msg
		us, err := s.h.Documents.ListUsages(ctx, p, m.GetAppId(), m.GetEntityKind(), m.GetEntityId(), after.Key, size)
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		out := &pluxv1.ListUsagesResponse{Page: &pluxv1.PageResult{}}
		var last storage.Cursor
		for _, u := range us {
			last = storage.Cursor{Key: u.File + "#" + u.Path}
			item := &pluxv1.Usage{PluginKey: u.PluginKey, Kind: u.Kind, Location: &pluxv1.Location{File: u.File, Path: u.Path}}
			if q.Keep(clauses, item) {
				out.Usages = append(out.Usages, item)
			}
		}
		out.Page.NextPageToken = s.h.Pages.Next(req.Spec().Procedure, p.OrganizationID, page, len(us), size, last)
		return out, Mask(page.GetReadMask(), out.Usages)
	})
}

// Template serves TemplateService.
type Template struct{ h *Handlers }

var _ pluxv1connect.TemplateServiceHandler = Template{}

// Template returns the TemplateService handler.
func (h *Handlers) Template() Template { return Template{h: h} }

// ListTemplates lists an app's templates of a kind.
func (s Template) ListTemplates(ctx context.Context, req *connect.Request[pluxv1.ListTemplatesRequest]) (*connect.Response[pluxv1.ListTemplatesResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.ListTemplatesResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		q := keyQuery(func(m Item) string { return m.(*pluxv1.Template).GetKey() })
		q.OrderBy = "path"
		page := req.Msg.GetPage()
		clauses, err := q.Parse(page)
		if err != nil {
			return nil, err
		}
		after, size, err := s.h.Pages.Request(req.Spec().Procedure, p.OrganizationID, page)
		if err != nil {
			return nil, err
		}
		ts, err := s.h.Documents.ListTemplates(ctx, p, req.Msg.GetAppId(), req.Msg.GetKind(), after.Key, size)
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		out := &pluxv1.ListTemplatesResponse{Page: &pluxv1.PageResult{}}
		var last storage.Cursor
		for _, t := range ts {
			last = storage.Cursor{Key: "templates/" + t.Key + ".template.json"}
			if item := templateProto(t); q.Keep(clauses, item) {
				out.Templates = append(out.Templates, item)
			}
		}
		out.Page.NextPageToken = s.h.Pages.Next(req.Spec().Procedure, p.OrganizationID, page, len(ts), size, last)
		return out, Mask(page.GetReadMask(), out.Templates)
	})
}

// GetTemplate returns a template with its document.
func (s Template) GetTemplate(ctx context.Context, req *connect.Request[pluxv1.GetTemplateRequest]) (*connect.Response[pluxv1.GetTemplateResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.GetTemplateResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		t, content, err := s.h.Documents.GetTemplate(ctx, p, req.Msg.GetId())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.GetTemplateResponse{Template: templateProto(t), Content: content}, nil
	})
}

// Instantiate inserts a template into a plugin as a new page (SCH-031).
func (s Template) Instantiate(ctx context.Context, req *connect.Request[pluxv1.InstantiateRequest]) (*connect.Response[pluxv1.InstantiateResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.InstantiateResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		m := req.Msg
		w, err := s.h.Documents.Instantiate(ctx, p, m.GetTemplateId(), m.GetAppId(), m.GetPluginId(), m.GetSession(), m.GetArguments(), m.GetKey())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		out := &pluxv1.InstantiateResponse{SnapshotId: w.SnapshotID, Diagnostics: diagnosticProtos(w.Diagnostics)}
		for _, d := range w.Documents {
			out.Created = append(out.Created, d.Path)
		}
		return out, nil
	})
}

// actorProto converts an actor for the wire.
func actorProto(a audit.Actor) *pluxv1.Actor {
	if a.ID == "" {
		return nil
	}
	return &pluxv1.Actor{Kind: a.Kind, Id: a.ID, Display: a.Display}
}

func documentProto(d document.Document) *pluxv1.Document {
	return &pluxv1.Document{
		AppId: d.AppID, PluginId: d.PluginID, Path: d.Path, Kind: d.Kind, Content: d.Content,
		Sha256: d.SHA256, Revision: d.Revision, UpdatedBy: actorProto(d.UpdatedBy), UpdatedAt: ts(d.UpdatedAt),
	}
}

// firstDocument is the document a single-document write wrote.
func firstDocument(w document.Written) *pluxv1.Document {
	if len(w.Documents) == 0 {
		return nil
	}
	return documentProto(w.Documents[0])
}

func snapshotProto(s document.Snapshot) *pluxv1.Snapshot {
	return &pluxv1.Snapshot{
		Id: s.ID, AppId: s.AppID, PluginId: s.PluginID, Revision: s.Revision, Reason: s.Reason,
		Paths: s.Paths, Actor: actorProto(s.Actor), CreatedAt: ts(s.CreatedAt),
	}
}

func pluginProto(p document.Plugin) *pluxv1.Plugin {
	return &pluxv1.Plugin{
		Id: p.ID, AppId: p.AppID, Key: p.Key, Name: p.Name, LatestVersion: p.LatestVersion,
		DraftRevision: p.DraftRevision, CreatedAt: ts(p.CreatedAt), UpdatedAt: ts(p.UpdatedAt),
	}
}

func lockProto(l document.Lock) *pluxv1.Lock {
	out := &pluxv1.Lock{
		AppId: l.AppID, PluginId: l.PluginID, Holder: actorProto(l.Holder), Session: l.Session,
		AcquiredAt: ts(l.AcquiredAt), ExpiresAt: ts(l.ExpiresAt),
	}
	for _, a := range l.RequestedBy {
		out.RequestedBy = append(out.RequestedBy, actorProto(a))
	}
	return out
}

func componentProto(c document.Component) *pluxv1.Component {
	return &pluxv1.Component{
		Id: c.ID, AppId: c.AppID, PluginId: c.PluginID, Key: c.Key, Name: c.Name, Path: c.Path,
		Revision: c.Revision, UpdatedAt: ts(c.UpdatedAt),
	}
}

func templateProto(t document.Template) *pluxv1.Template {
	out := &pluxv1.Template{
		Id: t.ID, AppId: t.AppID, Key: t.Key, Name: t.Name, Description: t.Description, Kind: t.Kind,
		UpdatedAt: ts(t.UpdatedAt),
	}
	for _, p := range t.Parameters {
		out.Parameters = append(out.Parameters, &pluxv1.TemplateParameter{
			Name: p.Name, Type: p.Type, Required: p.Default == nil, Description: p.Description,
		})
	}
	return out
}

// diagnosticProtos converts diagnostics for the wire.
func diagnosticProtos(ds plxerr.Diagnostics) []*pluxv1.Diagnostic {
	out := make([]*pluxv1.Diagnostic, 0, len(ds))
	for _, d := range ds {
		pd := &pluxv1.Diagnostic{
			Code: d.Code.String(), Reason: string(d.Reason), Severity: pluxv1.Severity(d.Severity), //nolint:gosec // 1 to 3
			Location: locationProto(d.Location), Message: d.Message, Cause: d.Cause, Fix: d.Fix, DocUrl: d.DocURL,
		}
		for _, op := range d.Patch {
			pd.Patch = append(pd.Patch, &pluxv1.PatchOp{Op: op.Op, Path: op.Path, From: op.From, Value: op.Value})
		}
		for _, r := range d.Related {
			pd.Related = append(pd.Related, locationProto(r))
		}
		out = append(out, pd)
	}
	return out
}

func locationProto(l plxerr.Location) *pluxv1.Location {
	out := &pluxv1.Location{File: l.File, Path: l.Path}
	if l.Range != nil {
		out.Range = &pluxv1.Range{Start: int32(l.Range.Start), End: int32(l.Range.End)} //nolint:gosec // bounded by the document size
	}
	return out
}

// patchOps reads a JSON Patch from the wire; each value is JSON.
func patchOps(ops []*pluxv1.PatchOp, depth int) ([]document.Op, error) {
	out := make([]document.Op, 0, len(ops))
	for i, op := range ops {
		o := document.Op{Op: op.GetOp(), Path: op.GetPath(), From: op.GetFrom()}
		if len(op.GetValue()) > 0 {
			v, err := jcs.Parse(op.GetValue(), depth)
			if err != nil {
				return nil, plxerr.Wrap(plxerr.InvalidJSON, err, "patch operation %d has a value that is not JSON", i)
			}
			o.Value = v
		}
		out = append(out, o)
	}
	return out, nil
}

// patchProtos writes a JSON Patch for the wire.
func patchProtos(ops []document.Op) ([]*pluxv1.PatchOp, error) {
	out := make([]*pluxv1.PatchOp, 0, len(ops))
	for _, op := range ops {
		po := &pluxv1.PatchOp{Op: op.Op, Path: op.Path, From: op.From}
		if op.Op == "add" || op.Op == "replace" || op.Op == "test" {
			v, err := jcs.Marshal(op.Value)
			if err != nil {
				return nil, plxerr.Wrap(plxerr.InvalidStructure, err, "a patch value cannot be encoded")
			}
			po.Value = v
		}
		out = append(out, po)
	}
	return out, nil
}
