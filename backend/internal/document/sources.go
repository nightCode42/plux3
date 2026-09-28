// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package document

import (
	"context"
	"errors"
	"fmt"
	"testing/fstest"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
)

// This file serves the publish pipeline (SRV-050): it freezes a draft at
// a revision as a kept snapshot, and reads snapshots back as the Git
// layout the compiler reads. A frozen snapshot is kept, whatever its
// age, for as long as a version was compiled from it (SRV-031).

// Frozen is a draft frozen at a revision.
type Frozen struct {
	DraftID    string
	SnapshotID string
	Revision   int64
	// PluginID and PluginKey name the plugin; both are empty for the
	// app-level draft.
	PluginID  string
	PluginKey string
}

// FreezeDraft marks the snapshot of a draft at a revision kept and
// returns it; revision zero is the current one. It runs in the caller's
// transaction, which is bound to the draft's organisation.
func (*Service) FreezeDraft(ctx context.Context, tx pgx.Tx, appID, pluginID string, revision int64) (Frozen, error) {
	q := dbgen.New(tx)
	app, err := parseID(appID, "app")
	if err != nil {
		return Frozen{}, err
	}
	var plugin pgtype.UUID
	out := Frozen{}
	if pluginID != "" {
		if plugin, err = parseID(pluginID, "plugin"); err != nil {
			return Frozen{}, err
		}
		row, err := q.GetPlugin(ctx, plugin)
		if err != nil {
			return Frozen{}, failure(err, "plugin")
		}
		if row.AppID != app {
			return Frozen{}, plxerr.New(plxerr.ResourceNotFound, "no such plugin in this app")
		}
		out.PluginID, out.PluginKey = pluginID, row.Key
	}
	d, err := q.GetDraft(ctx, dbgen.GetDraftParams{AppID: app, PluginID: plugin})
	if errors.Is(err, pgx.ErrNoRows) {
		return Frozen{}, plxerr.New(plxerr.PreconditionFailed, "the draft is empty; there is nothing to publish")
	}
	if err != nil {
		return Frozen{}, failure(err, "draft")
	}
	if revision == 0 {
		revision = d.Revision
	}
	if revision == 0 {
		return Frozen{}, plxerr.New(plxerr.PreconditionFailed, "the draft is empty; there is nothing to publish")
	}
	if revision > d.Revision {
		return Frozen{}, plxerr.New(plxerr.OutOfRange, "the draft is at revision %d; %d does not exist yet", d.Revision, revision)
	}
	snap, err := q.SnapshotAtRevision(ctx, dbgen.SnapshotAtRevisionParams{DraftID: d.ID, Revision: revision})
	if errors.Is(err, pgx.ErrNoRows) {
		return Frozen{}, plxerr.New(plxerr.ResourceNotFound, "no snapshot records revision %d; its history may have been purged", revision)
	}
	if err != nil {
		return Frozen{}, failure(err, "snapshot")
	}
	if err := q.KeepSnapshot(ctx, snap.ID); err != nil {
		return Frozen{}, fmt.Errorf("document: keep the snapshot: %w", err)
	}
	out.DraftID, out.SnapshotID, out.Revision = storage.ID(d.ID), storage.ID(snap.ID), revision
	return out, nil
}

// SourceFiles reads snapshots — each one draft's state — as one file set
// in the Git layout, with the app's asset files as they are now.
func (s *Service) SourceFiles(ctx context.Context, tx pgx.Tx, appID string, snapshotIDs []string) (fstest.MapFS, error) {
	q := dbgen.New(tx)
	fsys := fstest.MapFS{}
	for _, id := range snapshotIDs {
		sid, err := parseID(id, "snapshot")
		if err != nil {
			return nil, err
		}
		snap, err := q.GetSnapshot(ctx, sid)
		if err != nil {
			return nil, failure(err, "snapshot")
		}
		st, err := s.state(ctx, q, snap)
		if err != nil {
			return nil, err
		}
		for path, e := range st {
			content, err := s.blob(ctx, q, snap.OrganizationID, e.sum)
			if err != nil {
				return nil, err
			}
			fsys[path] = &fstest.MapFile{Data: content, Mode: 0o644}
		}
	}
	if s.o.Objects == nil {
		return fsys, nil
	}
	app, err := parseID(appID, "app")
	if err != nil {
		return nil, err
	}
	assets, err := q.ListAllAssets(ctx, app)
	if err != nil {
		return nil, failure(err, "asset")
	}
	for _, a := range assets {
		data, _, err := s.o.Objects.Get(ctx, objectKey(a.Sha256))
		if err != nil {
			return nil, fmt.Errorf("document: read asset %s: %w", a.File, err)
		}
		fsys["assets/"+a.File] = &fstest.MapFile{Data: data, Mode: 0o644}
	}
	return fsys, nil
}

// SourceHashes returns the SHA-256 of every document of snapshots, by
// path, without reading their content: what a changelog compares.
func (s *Service) SourceHashes(ctx context.Context, tx pgx.Tx, snapshotIDs []string) (map[string]string, error) {
	q := dbgen.New(tx)
	out := map[string]string{}
	for _, id := range snapshotIDs {
		sid, err := parseID(id, "snapshot")
		if err != nil {
			return nil, err
		}
		snap, err := q.GetSnapshot(ctx, sid)
		if err != nil {
			return nil, failure(err, "snapshot")
		}
		st, err := s.state(ctx, q, snap)
		if err != nil {
			return nil, err
		}
		for path, e := range st {
			out[path] = hexHash(e.sum)
		}
	}
	return out, nil
}
