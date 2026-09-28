// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package document

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema/jcs"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
)

// Change is how one document differs between two snapshots.
type Change struct {
	Path string
	// Change is "added", "removed" or "changed".
	Change string
	Patch  []Op
}

// entry is a document of a snapshot's state.
type entry struct {
	kind string
	sum  []byte
}

// snapshotDraft appends a snapshot that records the draft as it stands,
// without changing it: the state at a snapshot is every path's latest
// entry, so a snapshot with no entries of its own records exactly that.
// It is how a lock takeover preserves the displaced holder's work
// (SRV-041).
func (s *Service) snapshotDraft(ctx context.Context, q *dbgen.Queries, d draft, p auth.Principal, reason string) (string, error) {
	snap, err := s.newSnapshot(ctx, q, d, p, 0, reason, true)
	if err != nil {
		return "", err
	}
	return storage.ID(snap.ID), nil
}

// state returns the documents of a draft as they were at a snapshot.
func (*Service) state(ctx context.Context, q *dbgen.Queries, snap dbgen.Snapshot) (map[string]entry, error) {
	rows, err := q.SnapshotState(ctx, dbgen.SnapshotStateParams{DraftID: snap.DraftID, Sequence: snap.Sequence, SnapshotID: snap.ID})
	if err != nil {
		return nil, fmt.Errorf("document: read a snapshot's state: %w", err)
	}
	out := make(map[string]entry, len(rows))
	for _, r := range rows {
		if r.Sha256 != nil {
			out[r.Path] = entry{kind: r.Kind, sum: r.Sha256}
		}
	}
	return out, nil
}

// snapshotDraftOf reads a snapshot and its draft, and checks that the
// principal may read the app it belongs to.
func (s *Service) snapshotDraftOf(ctx context.Context, q *dbgen.Queries, p auth.Principal, snapshotID string) (dbgen.Snapshot, draft, error) {
	id, err := parseID(snapshotID, "snapshot")
	if err != nil {
		return dbgen.Snapshot{}, draft{}, err
	}
	snap, err := q.GetSnapshot(ctx, id)
	if err != nil {
		return dbgen.Snapshot{}, draft{}, failure(err, "snapshot")
	}
	row, err := q.GetDraftByID(ctx, snap.DraftID)
	if err != nil {
		return dbgen.Snapshot{}, draft{}, failure(err, "draft")
	}
	d, err := s.resolve(ctx, q, p, storage.ID(row.AppID), storage.ID(row.PluginID))
	if err != nil {
		return dbgen.Snapshot{}, draft{}, err
	}
	if err := authorize(p, auth.PluginRead, d.app); err != nil {
		return dbgen.Snapshot{}, draft{}, err
	}
	return snap, d, nil
}

// ListSnapshots lists a draft's history, newest first; before is the
// sequence to continue before, zero for the newest.
func (s *Service) ListSnapshots(ctx context.Context, p auth.Principal, appID, pluginID string, before int64, size int32) ([]Snapshot, error) {
	var out []Snapshot
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		d, err := s.resolve(ctx, q, p, appID, pluginID)
		if err != nil {
			return err
		}
		if err := authorize(p, auth.PluginRead, appID); err != nil {
			return err
		}
		if before <= 0 {
			before = d.row.Snapshots + 1
		}
		rows, err := q.ListSnapshots(ctx, dbgen.ListSnapshotsParams{DraftID: d.row.ID, BeforeSequence: before, PageSize: size})
		if err != nil {
			return failure(err, "snapshot")
		}
		for _, row := range rows {
			snap, err := s.snapshotOf(ctx, q, row, d)
			if err != nil {
				return err
			}
			out = append(out, snap)
		}
		return nil
	})
	return out, err
}

// GetSnapshot returns a snapshot and the documents of the draft as they
// were at it: all of them, or the paths named.
func (s *Service) GetSnapshot(ctx context.Context, p auth.Principal, snapshotID string, paths []string) (Snapshot, []Document, error) {
	var (
		out  Snapshot
		docs []Document
	)
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		snap, d, err := s.snapshotDraftOf(ctx, q, p, snapshotID)
		if err != nil {
			return err
		}
		if out, err = s.snapshotOf(ctx, q, snap, d); err != nil {
			return err
		}
		st, err := s.state(ctx, q, snap)
		if err != nil {
			return err
		}
		want := paths
		if len(want) == 0 {
			want = slices.Sorted(maps.Keys(st))
		}
		for _, path := range want {
			e, ok := st[path]
			if !ok {
				continue
			}
			content, err := s.blob(ctx, q, d.row.OrganizationID, e.sum)
			if err != nil {
				return err
			}
			docs = append(docs, Document{
				AppID: d.app, PluginID: d.pluginID(), Path: path, Kind: e.kind, Content: content,
				SHA256: hexHash(e.sum), Revision: snap.Revision, UpdatedBy: out.Actor, UpdatedAt: out.CreatedAt,
			})
		}
		return nil
	})
	return out, docs, err
}

// CompareSnapshots returns, for every document that differs between two
// snapshots of one draft, the patch that turns the first into the second
// (SRV-031).
func (s *Service) CompareSnapshots(ctx context.Context, p auth.Principal, fromID, toID string) ([]Change, error) {
	var out []Change
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		from, d, err := s.snapshotDraftOf(ctx, q, p, fromID)
		if err != nil {
			return err
		}
		to, _, err := s.snapshotDraftOf(ctx, q, p, toID)
		if err != nil {
			return err
		}
		if from.DraftID != to.DraftID {
			return plxerr.New(plxerr.InvalidStructure, "the snapshots belong to different drafts")
		}
		a, err := s.state(ctx, q, from)
		if err != nil {
			return err
		}
		b, err := s.state(ctx, q, to)
		if err != nil {
			return err
		}
		out, err = s.compareStates(ctx, q, d, a, b)
		return err
	})
	return out, err
}

// compareStates lists how every path differs between two states.
func (s *Service) compareStates(ctx context.Context, q *dbgen.Queries, d draft, a, b map[string]entry) ([]Change, error) {
	paths := slices.Sorted(maps.Keys(a))
	for path := range b {
		if _, ok := a[path]; !ok {
			paths = append(paths, path)
		}
	}
	slices.Sort(paths)
	var out []Change
	for _, path := range paths {
		c, err := s.compare(ctx, q, d, path, a, b)
		if err != nil {
			return nil, err
		}
		if c.Change != "" {
			out = append(out, c)
		}
	}
	return out, nil
}

// compare works out how one path differs between two states.
func (s *Service) compare(ctx context.Context, q *dbgen.Queries, d draft, path string, a, b map[string]entry) (Change, error) {
	ea, inA := a[path]
	eb, inB := b[path]
	if inA && inB && equalBytes(ea.sum, eb.sum) {
		return Change{}, nil
	}
	read := func(e entry, present bool) (any, error) {
		if !present {
			return map[string]any{}, nil
		}
		content, err := s.blob(ctx, q, d.row.OrganizationID, e.sum)
		if err != nil {
			return nil, err
		}
		v, err := jcs.Parse(content, int(s.o.Limits.Get(limits.DocumentJSONDepth)))
		if err != nil {
			return nil, fmt.Errorf("document: read %s: %w", path, err)
		}
		return v, nil
	}
	va, err := read(ea, inA)
	if err != nil {
		return Change{}, err
	}
	vb, err := read(eb, inB)
	if err != nil {
		return Change{}, err
	}
	c := Change{Path: path, Change: "changed", Patch: Diff(va, vb)}
	switch {
	case !inA:
		c.Change = "added"
	case !inB:
		c.Change, c.Patch = "removed", nil
	}
	return c, nil
}

// RestoreSnapshot writes a snapshot's documents forward as a new
// snapshot (SRV-031): all of the draft, or the paths named. History is
// never rewritten; a restore is itself restorable.
func (s *Service) RestoreSnapshot(ctx context.Context, p auth.Principal, snapshotID string, paths []string, session string) (Written, error) {
	var appID, pluginID string
	if err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		_, d, err := s.snapshotDraftOf(ctx, dbgen.New(tx), p, snapshotID)
		appID, pluginID = d.app, d.pluginID()
		return err
	}); err != nil {
		return Written{}, err
	}
	out, err := s.write(ctx, p, appID, pluginID, session, reasonRestore,
		func(ctx context.Context, q *dbgen.Queries, d draft, lim limits.Set) ([]change, plxerr.Diagnostics, error) {
			snap, err := q.GetSnapshot(ctx, storage.MustUUID(snapshotID))
			if err != nil {
				return nil, nil, failure(err, "snapshot")
			}
			st, err := s.state(ctx, q, snap)
			if err != nil {
				return nil, nil, err
			}
			current, err := q.ListAllDocuments(ctx, d.row.ID)
			if err != nil {
				return nil, nil, failure(err, "document")
			}
			return s.restoreChanges(ctx, q, d, lim, st, current, paths)
		})
	if err != nil {
		return out, err
	}
	return out, s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		return s.record(ctx, tx, p, audit.SnapshotRestored, "snapshot", snapshotID, "", "")
	})
}

// restoreChanges lists what a restore writes: every document of the old
// state that differs now, and the deletion of every current document the
// old state did not have.
func (s *Service) restoreChanges(ctx context.Context, q *dbgen.Queries, d draft, lim limits.Set,
	st map[string]entry, current []dbgen.Document, paths []string,
) ([]change, plxerr.Diagnostics, error) {
	now := map[string]dbgen.Document{}
	for _, doc := range current {
		now[doc.Path] = doc
	}
	want := paths
	if len(want) == 0 {
		want = slices.Sorted(maps.Keys(st))
		for path := range now {
			if _, ok := st[path]; !ok {
				want = append(want, path)
			}
		}
		slices.Sort(want)
	}
	var (
		changes []change
		diags   plxerr.Diagnostics
	)
	for _, path := range want {
		e, inOld := st[path]
		doc, inNow := now[path]
		switch {
		case inOld && inNow && equalBytes(e.sum, doc.Sha256):
		case inOld:
			content, err := s.blob(ctx, q, d.row.OrganizationID, e.sum)
			if err != nil {
				return nil, nil, err
			}
			// The content is checked again: the schema may have moved on
			// since, and a migration brings it forward (SCH-043).
			c, dg, err := s.prepare(d, lim, path, content, anyRevision)
			if err != nil {
				return nil, dg, err
			}
			changes, diags = append(changes, c), append(diags, dg...)
		case inNow:
			changes = append(changes, change{path: path, delete: true, ifRevision: anyRevision})
		}
	}
	if len(changes) == 0 {
		return nil, nil, plxerr.New(plxerr.PreconditionFailed, "the draft already matches the snapshot")
	}
	return changes, diags, nil
}

// snapshotOf converts a stored snapshot, with the paths it changed.
func (*Service) snapshotOf(ctx context.Context, q *dbgen.Queries, row dbgen.Snapshot, d draft) (Snapshot, error) {
	paths, err := q.ListSnapshotPaths(ctx, row.ID)
	if err != nil {
		return Snapshot{}, fmt.Errorf("document: read a snapshot's paths: %w", err)
	}
	return Snapshot{
		ID: storage.ID(row.ID), AppID: d.app, PluginID: d.pluginID(), Sequence: row.Sequence,
		Revision: row.Revision, Reason: row.Reason, Paths: paths, Kept: row.Keep,
		Actor:     audit.Actor{Kind: row.ActorKind, ID: row.ActorID, Display: row.ActorDisplay},
		CreatedAt: storage.Time(row.CreatedAt),
	}, nil
}

// materialise makes a snapshot self-contained: every path of its state
// that it does not record itself is copied in as a carried entry, so the
// snapshots before it can be purged without losing what it records. A
// deletion is carried too, or a purge would let a path deleted in purged
// history reappear from an older kept snapshot.
func (*Service) materialise(ctx context.Context, q *dbgen.Queries, snap dbgen.Snapshot) error {
	rows, err := q.SnapshotState(ctx, dbgen.SnapshotStateParams{DraftID: snap.DraftID, Sequence: snap.Sequence, SnapshotID: snap.ID})
	if err != nil {
		return fmt.Errorf("document: read a snapshot's state: %w", err)
	}
	for _, r := range rows {
		if err := q.AddSnapshotDocument(ctx, dbgen.AddSnapshotDocumentParams{
			SnapshotID: snap.ID, OrganizationID: snap.OrganizationID, Path: r.Path, Kind: r.Kind, Sha256: r.Sha256, Carried: true,
		}); err != nil {
			return fmt.Errorf("document: materialise a snapshot: %w", err)
		}
	}
	return nil
}

// PurgeSnapshots deletes history older than the retention period in one
// organisation (SRV-031). The oldest snapshot that remains, and every
// kept one, is made self-contained first, so every remaining state can
// still be rebuilt; the newest snapshot of a draft always remains; and a
// published version's snapshot is never deleted.
func (s *Service) PurgeSnapshots(ctx context.Context, organizationID string) (int, error) {
	system := auth.System(organizationID)
	before := s.now().AddDate(0, 0, -s.o.SnapshotDays)
	purged := 0
	err := s.inOrg(ctx, system, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		drafts, err := q.ListDraftsWithExpiredSnapshots(ctx, dbgen.ListDraftsWithExpiredSnapshotsParams{
			Before: storage.Timestamp(before), PageSize: 1000,
		})
		if err != nil {
			return fmt.Errorf("document: find expired history: %w", err)
		}
		for _, draftID := range drafts {
			n, err := s.purgeDraft(ctx, q, draftID, before)
			if err != nil {
				return err
			}
			purged += n
		}
		if _, err := q.DeleteUnreferencedBlobs(ctx, storage.MustUUID(organizationID)); err != nil {
			return fmt.Errorf("document: delete unreferenced content: %w", err)
		}
		return nil
	})
	return purged, err
}

// purgeDraft purges one draft's expired history. The newest applied
// snapshot is never deleted, and every snapshot that remains but had
// deleted history before it is materialised first: the kept ones among
// the old, and the recent ones up to the first applied one.
func (s *Service) purgeDraft(ctx context.Context, q *dbgen.Queries, draftID pgtype.UUID, before time.Time) (int, error) {
	if _, err := q.GetDraftForUpdate(ctx, draftID); err != nil {
		return 0, failure(err, "draft")
	}
	expired, err := q.ListExpiredSnapshots(ctx, dbgen.ListExpiredSnapshotsParams{DraftID: draftID, Before: storage.Timestamp(before)})
	if err != nil {
		return 0, fmt.Errorf("document: read expired history: %w", err)
	}
	newest, err := q.NewestAppliedSnapshot(ctx, draftID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("document: read the newest snapshot: %w", err)
	}
	var doomed []dbgen.Snapshot
	for _, snap := range expired {
		if snap.ID != newest.ID {
			doomed = append(doomed, snap)
		}
	}
	if len(doomed) == 0 {
		return 0, nil
	}
	if err := s.materialiseSurvivors(ctx, q, draftID, newest, doomed[len(doomed)-1].Sequence); err != nil {
		return 0, err
	}
	for i, snap := range doomed {
		if err := q.DeleteSnapshot(ctx, snap.ID); err != nil {
			return i, fmt.Errorf("document: delete a snapshot: %w", err)
		}
	}
	return len(doomed), nil
}

// materialiseSurvivors makes self-contained every snapshot whose state
// depends on history up to sequence last, which is about to be deleted:
// the kept ones before it, the newest applied one, and the recent ones up
// to and including the first applied one after it.
func (s *Service) materialiseSurvivors(ctx context.Context, q *dbgen.Queries, draftID pgtype.UUID, newest dbgen.Snapshot, last int64) error {
	kept, err := q.ListKeptSnapshotsBefore(ctx, dbgen.ListKeptSnapshotsBeforeParams{DraftID: draftID, Sequence: last})
	if err != nil {
		return fmt.Errorf("document: read kept history: %w", err)
	}
	after, err := q.ListSnapshotsFrom(ctx, dbgen.ListSnapshotsFromParams{DraftID: draftID, Sequence: last, PageSize: 1000})
	if err != nil {
		return fmt.Errorf("document: read recent history: %w", err)
	}
	if newest.ID.Valid && newest.Sequence < last {
		kept = append(kept, newest)
	}
	for _, snap := range kept {
		if err := s.materialise(ctx, q, snap); err != nil {
			return err
		}
	}
	for _, snap := range after {
		if err := s.materialise(ctx, q, snap); err != nil {
			return err
		}
		if snap.Applied {
			break
		}
	}
	return nil
}
