// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"time"

	"connectrpc.com/connect"

	"github.com/nightCode42/plux3/backend/internal/pluxv1"
	"github.com/nightCode42/plux3/backend/internal/pluxv1/pluxv1connect"
	"github.com/nightCode42/plux3/backend/internal/release"
	"github.com/nightCode42/plux3/backend/internal/storage"
)

// watchInterval is how often WatchPublish looks at a running job.
const watchInterval = 250 * time.Millisecond

// Publish serves PublishService.
type Publish struct{ h *Handlers }

var _ pluxv1connect.PublishServiceHandler = Publish{}

// Publish returns the PublishService handler.
func (h *Handlers) Publish() Publish { return Publish{h: h} }

// Publish starts a publish job (SRV-050); the idempotency key at the
// edge makes a repeat return the same job (SRV-005).
func (s Publish) Publish(ctx context.Context, req *connect.Request[pluxv1.PublishRequest]) (*connect.Response[pluxv1.PublishResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.PublishResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		m := req.Msg
		j, err := s.h.Releases.Publish(ctx, p, release.PublishRequest{
			AppID: m.GetAppId(), PluginID: m.GetPluginId(), EnvironmentID: m.GetEnvironmentId(), Revision: m.GetRevision(),
			Label: m.GetLabel(), Notes: m.GetNotes(), AcknowledgeWarnings: m.GetAcknowledgeWarnings(),
		})
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.PublishResponse{Job: jobProto(j)}, nil
	})
}

// WatchPublish streams a job's progress until it finishes: a message
// whenever its stage, percentage, state or diagnostics change.
func (s Publish) WatchPublish(ctx context.Context, req *connect.Request[pluxv1.WatchPublishRequest], stream *connect.ServerStream[pluxv1.WatchPublishResponse]) error {
	p, err := s.h.principal(ctx, req.Header(), "")
	if err != nil {
		return err
	}
	var last release.PublishJob
	sent := false
	for {
		j, err := s.h.Releases.GetPublishJob(ctx, p, req.Msg.GetJobId())
		if err != nil {
			return err //nolint:wrapcheck // a domain error
		}
		if !sent || j.State != last.State || j.Stage != last.Stage || j.Percent != last.Percent || len(j.Diagnostics) != len(last.Diagnostics) {
			fresh := j.Diagnostics
			if sent && len(last.Diagnostics) <= len(fresh) {
				fresh = fresh[len(last.Diagnostics):]
			}
			if err := stream.Send(&pluxv1.WatchPublishResponse{Job: jobProto(j), NewDiagnostics: diagnosticProtos(fresh)}); err != nil {
				return err //nolint:wrapcheck // the stream's own error
			}
			last, sent = j, true
		}
		if j.Done() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err() //nolint:wrapcheck // the caller went away
		case <-time.After(watchInterval):
		}
	}
}

// GetPublishJob returns a job.
func (s Publish) GetPublishJob(ctx context.Context, req *connect.Request[pluxv1.GetPublishJobRequest]) (*connect.Response[pluxv1.GetPublishJobResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.GetPublishJobResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		j, err := s.h.Releases.GetPublishJob(ctx, p, req.Msg.GetJobId())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.GetPublishJobResponse{Job: jobProto(j)}, nil
	})
}

// ListPublishJobs lists an app's jobs, newest first.
func (s Publish) ListPublishJobs(ctx context.Context, req *connect.Request[pluxv1.ListPublishJobsRequest]) (*connect.Response[pluxv1.ListPublishJobsResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.ListPublishJobsResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		q := Query{OrderBy: "created_at desc", Filters: map[string]func(Item) string{
			"state": func(m Item) string { return m.(*pluxv1.PublishJob).GetState() },
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
		jobs, err := s.h.Releases.ListPublishJobs(ctx, p, req.Msg.GetAppId(), req.Msg.GetPluginId(), after, size)
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		out := &pluxv1.ListPublishJobsResponse{Page: &pluxv1.PageResult{}}
		var last storage.Cursor
		for _, j := range jobs {
			last = storage.Cursor{Time: j.CreatedAt, ID: j.ID}
			if item := jobProto(j); q.Keep(clauses, item) {
				out.Jobs = append(out.Jobs, item)
			}
		}
		out.Page.NextPageToken = s.h.Pages.Next(req.Spec().Procedure, p.OrganizationID, page, len(jobs), size, last)
		return out, Mask(page.GetReadMask(), out.Jobs)
	})
}

// CancelPublishJob cancels a job that has not recorded its version.
func (s Publish) CancelPublishJob(ctx context.Context, req *connect.Request[pluxv1.CancelPublishJobRequest]) (*connect.Response[pluxv1.CancelPublishJobResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.CancelPublishJobResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		j, err := s.h.Releases.CancelPublishJob(ctx, p, req.Msg.GetJobId())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.CancelPublishJobResponse{Job: jobProto(j)}, nil
	})
}

// Release serves ReleaseService.
type Release struct{ h *Handlers }

var _ pluxv1connect.ReleaseServiceHandler = Release{}

// Release returns the ReleaseService handler.
func (h *Handlers) Release() Release { return Release{h: h} }

// GetPluginVersion returns one version of a plugin.
func (s Release) GetPluginVersion(ctx context.Context, req *connect.Request[pluxv1.GetPluginVersionRequest]) (*connect.Response[pluxv1.GetPluginVersionResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.GetPluginVersionResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		v, err := s.h.Releases.GetVersion(ctx, p, req.Msg.GetPluginId(), req.Msg.GetVersion())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.GetPluginVersionResponse{Version: versionProto(v)}, nil
	})
}

// ListPluginVersions lists a plugin's versions, newest first.
func (s Release) ListPluginVersions(ctx context.Context, req *connect.Request[pluxv1.ListPluginVersionsRequest]) (*connect.Response[pluxv1.ListPluginVersionsResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.ListPluginVersionsResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		q := Query{OrderBy: "version desc", Filters: map[string]func(Item) string{
			"label": func(m Item) string { return m.(*pluxv1.PluginVersion).GetLabel() },
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
		vs, err := s.h.Releases.ListVersions(ctx, p, req.Msg.GetPluginId(), after.Sequence, size)
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		out := &pluxv1.ListPluginVersionsResponse{Page: &pluxv1.PageResult{}}
		var last storage.Cursor
		for _, v := range vs {
			last = storage.Cursor{Sequence: v.Version}
			if item := versionProto(v); q.Keep(clauses, item) {
				out.Versions = append(out.Versions, item)
			}
		}
		out.Page.NextPageToken = s.h.Pages.Next(req.Spec().Procedure, p.OrganizationID, page, len(vs), size, last)
		return out, Mask(page.GetReadMask(), out.Versions)
	})
}

// CreateRelease creates an app release, or returns why it cannot.
func (s Release) CreateRelease(ctx context.Context, req *connect.Request[pluxv1.CreateReleaseRequest]) (*connect.Response[pluxv1.CreateReleaseResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.CreateReleaseResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		m := req.Msg
		r, diags, err := s.h.Releases.CreateRelease(ctx, p, m.GetAppId(), m.GetEnvironmentId(), m.GetPluginVersions(), m.GetNotes())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		out := &pluxv1.CreateReleaseResponse{Diagnostics: diagnosticProtos(diags)}
		if r.ID != "" {
			out.Release = releaseProto(r)
		}
		return out, nil
	})
}

// GetRelease returns a release with its versions.
func (s Release) GetRelease(ctx context.Context, req *connect.Request[pluxv1.GetReleaseRequest]) (*connect.Response[pluxv1.GetReleaseResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.GetReleaseResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		r, vs, err := s.h.Releases.GetRelease(ctx, p, req.Msg.GetAppId(), req.Msg.GetSequence())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		out := &pluxv1.GetReleaseResponse{Release: releaseProto(r)}
		for _, v := range vs {
			out.Versions = append(out.Versions, versionProto(v))
		}
		return out, nil
	})
}

// ListReleases lists an app's releases, newest first.
func (s Release) ListReleases(ctx context.Context, req *connect.Request[pluxv1.ListReleasesRequest]) (*connect.Response[pluxv1.ListReleasesResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.ListReleasesResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		q := Query{OrderBy: "sequence desc", Filters: map[string]func(Item) string{
			"notes": func(m Item) string { return m.(*pluxv1.Release).GetNotes() },
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
		rs, err := s.h.Releases.ListReleases(ctx, p, req.Msg.GetAppId(), req.Msg.GetEnvironmentId(), after.Sequence, size)
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		out := &pluxv1.ListReleasesResponse{Page: &pluxv1.PageResult{}}
		var last storage.Cursor
		for _, r := range rs {
			last = storage.Cursor{Sequence: r.Sequence}
			if item := releaseProto(r); q.Keep(clauses, item) {
				out.Releases = append(out.Releases, item)
			}
		}
		out.Page.NextPageToken = s.h.Pages.Next(req.Spec().Procedure, p.OrganizationID, page, len(rs), size, last)
		return out, Mask(page.GetReadMask(), out.Releases)
	})
}

// PromoteRelease points a channel at a release (REL-004).
func (s Release) PromoteRelease(ctx context.Context, req *connect.Request[pluxv1.PromoteReleaseRequest]) (*connect.Response[pluxv1.PromoteReleaseResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.PromoteReleaseResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		m := req.Msg
		r, err := s.h.Releases.PromoteRelease(ctx, p, m.GetAppId(), m.GetSequence(), m.GetEnvironmentId(), m.GetChannelKey())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.PromoteReleaseResponse{Release: releaseProto(r)}, nil
	})
}

// RollbackRelease creates a release reproducing an earlier one (REL-006).
func (s Release) RollbackRelease(ctx context.Context, req *connect.Request[pluxv1.RollbackReleaseRequest]) (*connect.Response[pluxv1.RollbackReleaseResponse], error) {
	return mutate(ctx, s.h, req, func(ctx context.Context) (*pluxv1.RollbackReleaseResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		m := req.Msg
		r, err := s.h.Releases.RollbackRelease(ctx, p, m.GetAppId(), m.GetEnvironmentId(), m.GetChannelKey(), m.GetToSequence(), m.GetNotes())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		return &pluxv1.RollbackReleaseResponse{Release: releaseProto(r)}, nil
	})
}

// GetChangelog returns a release's generated changelog (REL-081).
func (s Release) GetChangelog(ctx context.Context, req *connect.Request[pluxv1.GetChangelogRequest]) (*connect.Response[pluxv1.GetChangelogResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.GetChangelogResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		m := req.Msg
		entries, notes, err := s.h.Releases.Changelog(ctx, p, m.GetAppId(), m.GetSequence(), m.GetFromSequence())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		out := &pluxv1.GetChangelogResponse{Notes: notes}
		for _, e := range entries {
			out.Entries = append(out.Entries, &pluxv1.ChangelogEntry{Kind: e.Kind, Change: e.Change, PluginKey: e.PluginKey, Name: e.Name})
		}
		return out, nil
	})
}

// GetCompatibility reports who can use a release (REL-080).
func (s Release) GetCompatibility(ctx context.Context, req *connect.Request[pluxv1.GetCompatibilityRequest]) (*connect.Response[pluxv1.GetCompatibilityResponse], error) {
	return read(ctx, func(ctx context.Context) (*pluxv1.GetCompatibilityResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		c, err := s.h.Releases.Compatibility(ctx, p, req.Msg.GetAppId(), req.Msg.GetSequence())
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		out := &pluxv1.Compatibility{
			MinRuntime: c.MinRuntime, RequiredFeatures: c.RequiredFeatures,
			IncompatibleDevices: c.IncompatibleDevices, FallbackSequence: c.FallbackSequence,
		}
		for _, b := range c.IncompatibleBuilds {
			out.IncompatibleHostBuilds = append(out.IncompatibleHostBuilds, &pluxv1.IncompatibleHostBuild{
				HostBuild: b.Build, Devices: b.Devices, FallbackSequence: b.FallbackSequence, Missing: b.Missing,
			})
		}
		return &pluxv1.GetCompatibilityResponse{Compatibility: out}, nil
	})
}

func jobProto(j release.PublishJob) *pluxv1.PublishJob {
	return &pluxv1.PublishJob{
		Id: j.ID, AppId: j.AppID, PluginId: j.PluginID, EnvironmentId: j.EnvironmentID, Revision: j.Revision,
		State: j.State, Stage: j.Stage, Percent: j.Percent, Version: j.Version,
		Diagnostics: diagnosticProtos(j.Diagnostics), Actor: actorProto(j.Actor),
		CreatedAt: ts(j.CreatedAt), FinishedAt: ts(j.FinishedAt),
	}
}

func versionProto(v release.Version) *pluxv1.PluginVersion {
	return &pluxv1.PluginVersion{
		Id: v.ID, PluginId: v.PluginID, PluginKey: v.PluginKey, Version: v.Version, Label: v.Label, Notes: v.Notes,
		BundleSha256: v.BundleSHA256, BundleSize: v.BundleSize, RequiredFeatures: v.RequiredFeatures,
		MinRuntime: v.MinRuntime, Signature: v.Signature, KeyId: v.KeyID, Algorithm: v.Algorithm,
		SourceSnapshotId: v.SourceSnapshotID, PublishedBy: actorProto(v.PublishedBy), CreatedAt: ts(v.CreatedAt),
	}
}

func releaseProto(r release.Release) *pluxv1.Release {
	return &pluxv1.Release{
		Id: r.ID, AppId: r.AppID, Sequence: r.Sequence, PluginVersionIds: r.VersionIDs,
		AppBundleSha256: r.AppBundleSHA256, AppBundleSize: r.AppBundleSize, RollbackOf: r.RollbackOf,
		Notes: r.Notes, CreatedBy: actorProto(r.CreatedBy), CreatedAt: ts(r.CreatedAt),
	}
}
