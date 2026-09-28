// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package document

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema"
	"github.com/nightCode42/plux3/backend/internal/schema/jcs"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
)

// Snapshot reasons.
const (
	reasonWrite     = "write"
	reasonDelete    = "delete"
	reasonRestore   = "restore"
	reasonImport    = "import"
	reasonTakeover  = "lock_takeover"
	reasonPreserved = "preserved"
	// ReasonPublish marks the snapshot a version was published from.
	ReasonPublish = "publish"
)

// anyRevision marks a change that is not conditional on a revision, such
// as a restore or an import, which replace whatever is there.
const anyRevision = -1

// change is one document a write sets or deletes.
type change struct {
	path       string
	kind       string
	content    []byte
	entityID   string
	entityKey  string
	ifRevision int64
	delete     bool
}

// limitsFor returns the limits in force for a draft (LIM-002).
func (s *Service) limitsFor(ctx context.Context, tx pgx.Tx, d draft) (limits.Set, error) {
	set, err := s.o.Tenancy.Effective(ctx, tx, storage.ID(d.row.OrganizationID), d.app, d.pluginID())
	if err != nil {
		return limits.Set{}, fmt.Errorf("document: %w", err)
	}
	return set, nil
}

// prepare checks one document for a draft: its path in the Git layout,
// its size, and its structure; and canonicalises it (SCH-003, SCH-005,
// SCH-006). A document with a structural error is refused whole, so a
// write never leaves a draft broken.
func (s *Service) prepare(d draft, lim limits.Set, path string, data []byte, ifRevision int64) (change, plxerr.Diagnostics, error) {
	place, ok := schema.PlaceOf(path)
	if !ok {
		return change{}, nil, plxerr.New(plxerr.InvalidProjectLayout, "%q is not a document path of the project layout", path)
	}
	if !strings.HasPrefix(path, d.prefix()) || (d.plugin == nil) != (place.Plugin == "") {
		return change{}, nil, plxerr.New(plxerr.InvalidProjectLayout, "%q does not belong to this draft", path)
	}
	if max := lim.Get(limits.DocumentFileSize); int64(len(data)) > max {
		return change{}, nil, plxerr.New(plxerr.LimitExceeded, "%s is %d bytes, above the limit document.fileSize = %d", path, len(data), max)
	}
	loader := schema.NewLoader(s.validator(), schema.DefaultMigrator(), lim)
	src, diags := loader.ParseDocument(path, data, place.Kind)
	if diags.HasErrors() || src == nil {
		return change{}, diags, refusal(diags)
	}
	c := change{path: path, kind: string(place.Kind), content: src.Canonical, ifRevision: ifRevision}
	c.entityID, _ = src.Tree["id"].(string)
	c.entityKey, _ = src.Tree["key"].(string)
	name := c.entityKey
	if place.Kind == schema.KindTranslations {
		name, _ = src.Tree["locale"].(string)
	}
	if place.Key != "" && name != place.Key {
		return change{}, diags, plxerr.New(plxerr.InvalidProjectLayout, "%s must be named after its key %q", path, name)
	}
	if place.Kind == schema.KindPlugin && c.entityKey != d.plugin.Key {
		return change{}, diags, plxerr.New(plxerr.InvalidProjectLayout, "plugin.json has key %q, but this is plugin %q", c.entityKey, d.plugin.Key)
	}
	return c, diags, nil
}

// refusal turns the first error of a document's diagnostics into the
// error a write is refused with.
func refusal(diags plxerr.Diagnostics) error {
	for _, d := range diags {
		if d.Severity == plxerr.SeverityError {
			e := &plxerr.Error{Code: d.Code, Message: d.Message}
			return e.WithDetail("file", d.File).WithDetail("path", d.Path)
		}
	}
	return plxerr.New(plxerr.InvalidStructure, "the document could not be read")
}

// recorded is one path a snapshot records: its content hash, or none
// for a deletion.
type recorded struct {
	path, kind string
	sum        []byte
}

// apply writes changes to a draft as one snapshot, in the caller's
// transaction, which holds the draft row locked.
func (s *Service) apply(ctx context.Context, q *dbgen.Queries, tx pgx.Tx, d draft, p auth.Principal, changes []change, reason string) (Written, error) {
	out := Written{}
	entries := make([]recorded, 0, len(changes))
	for _, c := range changes {
		existing, err := q.GetDocumentForUpdate(ctx, dbgen.GetDocumentForUpdateParams{DraftID: d.row.ID, Path: c.path})
		found := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return Written{}, fmt.Errorf("document: read %s: %w", c.path, err)
		}
		if err := checkRevision(c, existing, found); err != nil {
			return Written{}, err
		}
		var e recorded
		if c.delete {
			e, err = s.applyDelete(ctx, q, tx, d, p, c, existing, found)
		} else {
			var doc Document
			doc, e, err = s.applyPut(ctx, q, tx, d, p, c, existing, found)
			out.Documents = append(out.Documents, doc)
		}
		if err != nil {
			return Written{}, err
		}
		entries = append(entries, e)
	}
	snap, err := s.newSnapshot(ctx, q, d, p, int64(len(changes)), reason, true)
	if err != nil {
		return Written{}, err
	}
	for _, e := range entries {
		if err := q.AddSnapshotDocument(ctx, dbgen.AddSnapshotDocumentParams{
			SnapshotID: snap.ID, OrganizationID: d.row.OrganizationID, Path: e.path, Kind: e.kind, Sha256: e.sum,
		}); err != nil {
			return Written{}, fmt.Errorf("document: record the snapshot: %w", err)
		}
	}
	out.SnapshotID, out.Revision = storage.ID(snap.ID), snap.Revision
	return out, nil
}

// applyDelete deletes one document and audits it.
func (s *Service) applyDelete(ctx context.Context, q *dbgen.Queries, tx pgx.Tx, d draft, p auth.Principal,
	c change, existing dbgen.Document, found bool,
) (recorded, error) {
	if !found {
		return recorded{}, plxerr.New(plxerr.ResourceNotFound, "no document %s", c.path)
	}
	if err := s.remove(ctx, q, tx, d, p, existing); err != nil {
		return recorded{}, err
	}
	if err := s.record(ctx, tx, p, audit.DocumentDeleted, "document", c.path, hexHash(existing.Sha256), ""); err != nil {
		return recorded{}, err
	}
	return recorded{path: c.path, kind: existing.Kind}, nil
}

// applyPut writes one document and audits it with the hashes before and
// after (SEC-140).
func (s *Service) applyPut(ctx context.Context, q *dbgen.Queries, tx pgx.Tx, d draft, p auth.Principal,
	c change, existing dbgen.Document, found bool,
) (Document, recorded, error) {
	sum, err := s.putBlob(ctx, q, d.row.OrganizationID, c.content)
	if err != nil {
		return Document{}, recorded{}, err
	}
	var entityID pgtype.UUID
	if id, err := storage.UUID(c.entityID); err == nil {
		entityID = id
	}
	var (
		row    dbgen.Document
		before string
	)
	if found {
		before = hexHash(existing.Sha256)
		row, err = q.UpdateDocument(ctx, dbgen.UpdateDocumentParams{
			ID: existing.ID, Kind: c.kind, EntityID: entityID, EntityKey: c.entityKey, Sha256: sum,
			UpdatedByKind: p.Kind, UpdatedByID: p.ID, UpdatedBy: p.Display,
		})
	} else {
		id, idErr := s.newID()
		if idErr != nil {
			return Document{}, recorded{}, idErr
		}
		row, err = q.InsertDocument(ctx, dbgen.InsertDocumentParams{
			ID: storage.MustUUID(id), OrganizationID: d.row.OrganizationID, DraftID: d.row.ID, Path: c.path, Kind: c.kind,
			EntityID: entityID, EntityKey: c.entityKey, Sha256: sum,
			UpdatedByKind: p.Kind, UpdatedByID: p.ID, UpdatedBy: p.Display,
		})
	}
	if err != nil {
		return Document{}, recorded{}, failure(err, "document")
	}
	if err := s.record(ctx, tx, p, audit.DocumentWritten, "document", c.path, before, hexHash(sum)); err != nil {
		return Document{}, recorded{}, err
	}
	doc := documentOf(row, d)
	doc.Content = c.content
	return doc, recorded{path: c.path, kind: c.kind, sum: sum}, nil
}

// checkRevision enforces optimistic concurrency (SRV-030): zero creates,
// and a positive revision must be the stored one.
func checkRevision(c change, existing dbgen.Document, found bool) error {
	switch {
	case c.ifRevision == anyRevision:
		return nil
	case c.ifRevision == 0 && found:
		return withDetail(plxerr.New(plxerr.RevisionConflict, "%s already exists at revision %d; read it and write with its revision", c.path, existing.Revision),
			"revision", fmt.Sprint(existing.Revision))
	case c.ifRevision > 0 && !found:
		return plxerr.New(plxerr.RevisionConflict, "%s does not exist", c.path)
	case c.ifRevision > 0 && existing.Revision != c.ifRevision:
		return withDetail(plxerr.New(plxerr.RevisionConflict, "%s is at revision %d, not %d; read it again", c.path, existing.Revision, c.ifRevision),
			"revision", fmt.Sprint(existing.Revision))
	case c.ifRevision < 0:
		return plxerr.New(plxerr.OutOfRange, "if_revision must not be negative")
	}
	return nil
}

// remove deletes a document. A page goes to the trash for the retention
// period (GOV-031); anything else is removed, its content kept by the
// snapshots that recorded it.
func (s *Service) remove(ctx context.Context, q *dbgen.Queries, tx pgx.Tx, d draft, p auth.Principal, row dbgen.Document) error {
	if row.Kind != string(schema.KindPage) {
		if _, err := q.HardDeleteDocument(ctx, row.ID); err != nil {
			return fmt.Errorf("document: delete %s: %w", row.Path, err)
		}
		return nil
	}
	if _, err := q.SoftDeleteDocument(ctx, row.ID); err != nil {
		return fmt.Errorf("document: delete %s: %w", row.Path, err)
	}
	if _, err := s.o.Tenancy.Trash(ctx, tx, p, "page", storage.ID(row.ID), d.app, row.Path); err != nil {
		return fmt.Errorf("document: %w", err)
	}
	return nil
}

// newSnapshot advances the draft and appends a snapshot row. An applied
// snapshot advances the revision by the number of documents it wrote; a
// preserved one does not change the draft at all.
func (s *Service) newSnapshot(ctx context.Context, q *dbgen.Queries, d draft, p auth.Principal, revisions int64, reason string, applied bool) (dbgen.Snapshot, error) {
	if !applied {
		revisions = 0
	}
	advanced, err := q.AdvanceDraft(ctx, dbgen.AdvanceDraftParams{ID: d.row.ID, Revisions: revisions})
	if err != nil {
		return dbgen.Snapshot{}, fmt.Errorf("document: advance the draft: %w", err)
	}
	id, err := s.newID()
	if err != nil {
		return dbgen.Snapshot{}, err
	}
	snap, err := q.CreateSnapshot(ctx, dbgen.CreateSnapshotParams{
		ID: storage.MustUUID(id), OrganizationID: d.row.OrganizationID, DraftID: d.row.ID,
		Sequence: advanced.Snapshots, Revision: advanced.Revision, Reason: reason, Applied: applied,
		ActorKind: p.Kind, ActorID: p.ID, ActorDisplay: p.Display,
	})
	if err != nil {
		return dbgen.Snapshot{}, fmt.Errorf("document: create a snapshot: %w", err)
	}
	return snap, nil
}

// write is the path every edit of a draft takes: resolve the draft,
// authorise, lock its row, check the editing lock, prepare the changes
// and apply them. A write from a session whose lock was taken over is
// not applied but preserved as a snapshot of its own (SRV-041).
func (s *Service) write(ctx context.Context, p auth.Principal, appID, pluginID, session, reason string,
	build func(context.Context, *dbgen.Queries, draft, limits.Set) ([]change, plxerr.Diagnostics, error),
) (Written, error) {
	if err := checkSession(session); err != nil {
		return Written{}, err
	}
	var (
		out       Written
		lockError error
	)
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		d, err := s.resolve(ctx, q, p, appID, pluginID)
		if err != nil {
			return err
		}
		if err := authorize(p, d.editPermission(), appID); err != nil {
			return err
		}
		if _, err := q.GetDraftForUpdate(ctx, d.row.ID); err != nil {
			return failure(err, "draft")
		}
		lim, err := s.limitsFor(ctx, tx, d)
		if err != nil {
			return err
		}
		changes, diags, err := build(ctx, q, d, lim)
		if err != nil || len(changes) == 0 {
			return err
		}
		if lockErr := s.holds(ctx, q, d, p, session); lockErr != nil {
			displaced, err := s.displaced(ctx, q, d, p, session)
			if err != nil || !displaced {
				return lockErr
			}
			out, err = s.preserve(ctx, q, d, p, changes)
			lockError = lockErr
			return err
		}
		out, err = s.apply(ctx, q, tx, d, p, changes, reason)
		out.Diagnostics = diags
		return err
	})
	if err != nil {
		return Written{}, err
	}
	if lockError != nil {
		return out, withDetail(lockError, "preservedSnapshot", out.SnapshotID)
	}
	return out, nil
}

// displaced reports whether a session is the one a takeover displaced.
func (*Service) displaced(ctx context.Context, q *dbgen.Queries, d draft, p auth.Principal, session string) (bool, error) {
	row, err := q.GetLock(ctx, d.row.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("document: read the lock: %w", err)
	}
	return row.PreviousSession == session && row.PreviousHolder == p.ID, nil
}

// preserve keeps the work of a displaced holder as a snapshot that is
// not applied to the draft (SRV-041): it can be listed, compared and
// restored by whoever holds the lock now.
func (s *Service) preserve(ctx context.Context, q *dbgen.Queries, d draft, p auth.Principal, changes []change) (Written, error) {
	snap, err := s.newSnapshot(ctx, q, d, p, 0, reasonPreserved, false)
	if err != nil {
		return Written{}, err
	}
	for _, c := range changes {
		var sum []byte
		if !c.delete {
			if sum, err = s.putBlob(ctx, q, d.row.OrganizationID, c.content); err != nil {
				return Written{}, err
			}
		}
		if err := q.AddSnapshotDocument(ctx, dbgen.AddSnapshotDocumentParams{
			SnapshotID: snap.ID, OrganizationID: d.row.OrganizationID, Path: c.path, Kind: c.kind, Sha256: sum,
		}); err != nil {
			return Written{}, fmt.Errorf("document: record the snapshot: %w", err)
		}
	}
	return Written{SnapshotID: storage.ID(snap.ID), Revision: snap.Revision}, nil
}

// PutDocument writes a document whole (SRV-030).
func (s *Service) PutDocument(ctx context.Context, p auth.Principal, appID, pluginID, session, path string, content []byte, ifRevision int64) (Written, error) {
	return s.write(ctx, p, appID, pluginID, session, reasonWrite,
		func(_ context.Context, _ *dbgen.Queries, d draft, lim limits.Set) ([]change, plxerr.Diagnostics, error) {
			c, diags, err := s.prepare(d, lim, path, content, ifRevision)
			return []change{c}, diags, err
		})
}

// PatchDocument applies an RFC 6902 patch to a document, which is what
// autosave sends (SRV-030). The patch applies to the revision it names;
// the result is validated like a whole document.
func (s *Service) PatchDocument(ctx context.Context, p auth.Principal, appID, pluginID, session, path string, patch []Op, ifRevision int64) (Written, error) {
	if ifRevision <= 0 {
		return Written{}, plxerr.New(plxerr.MissingProperty, "a patch names the revision it applies to")
	}
	return s.write(ctx, p, appID, pluginID, session, reasonWrite,
		func(ctx context.Context, q *dbgen.Queries, d draft, lim limits.Set) ([]change, plxerr.Diagnostics, error) {
			row, err := q.GetDocument(ctx, dbgen.GetDocumentParams{DraftID: d.row.ID, Path: path})
			if err != nil {
				return nil, nil, failure(err, "document")
			}
			current, err := s.blob(ctx, q, d.row.OrganizationID, row.Sha256)
			if err != nil {
				return nil, nil, err
			}
			tree, err := jcs.Parse(current, int(lim.Get(limits.DocumentJSONDepth)))
			if err != nil {
				return nil, nil, fmt.Errorf("document: read %s: %w", path, err)
			}
			patched, err := Apply(tree, patch)
			if err != nil {
				return nil, nil, err
			}
			data, err := jcs.Marshal(patched)
			if err != nil {
				return nil, nil, plxerr.Wrap(plxerr.InvalidStructure, err, "the patched document is not JSON")
			}
			c, diags, err := s.prepare(d, lim, path, data, ifRevision)
			return []change{c}, diags, err
		})
}

// DeleteDocument deletes a document; a page goes to the trash (GOV-031).
func (s *Service) DeleteDocument(ctx context.Context, p auth.Principal, appID, pluginID, session, path string, ifRevision int64) (Written, error) {
	if ifRevision <= 0 {
		return Written{}, plxerr.New(plxerr.MissingProperty, "a delete names the revision it read")
	}
	return s.write(ctx, p, appID, pluginID, session, reasonDelete,
		func(_ context.Context, _ *dbgen.Queries, d draft, _ limits.Set) ([]change, plxerr.Diagnostics, error) {
			if !strings.HasPrefix(path, d.prefix()) {
				return nil, nil, plxerr.New(plxerr.InvalidProjectLayout, "%q does not belong to this draft", path)
			}
			return []change{{path: path, delete: true, ifRevision: ifRevision}}, nil, nil
		})
}

// GetDocument reads one document with its content.
func (s *Service) GetDocument(ctx context.Context, p auth.Principal, appID, pluginID, path string) (Document, error) {
	var out Document
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		d, err := s.resolve(ctx, q, p, appID, pluginID)
		if err != nil {
			return err
		}
		if err := authorize(p, auth.PluginRead, appID); err != nil {
			return err
		}
		row, err := q.GetDocument(ctx, dbgen.GetDocumentParams{DraftID: d.row.ID, Path: path})
		if err != nil {
			return failure(err, "document")
		}
		out = documentOf(row, d)
		out.Content, err = s.blob(ctx, q, d.row.OrganizationID, row.Sha256)
		return err
	})
	return out, err
}

// ListDocuments lists a draft's documents in path order, with their
// content when asked.
func (s *Service) ListDocuments(ctx context.Context, p auth.Principal, appID, pluginID, afterPath string, size int32, content bool) ([]Document, error) {
	var out []Document
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		d, err := s.resolve(ctx, q, p, appID, pluginID)
		if err != nil {
			return err
		}
		if err := authorize(p, auth.PluginRead, appID); err != nil {
			return err
		}
		rows, err := q.ListDocuments(ctx, dbgen.ListDocumentsParams{DraftID: d.row.ID, AfterPath: afterPath, PageSize: size})
		if err != nil {
			return failure(err, "document")
		}
		for _, row := range rows {
			doc := documentOf(row, d)
			if content {
				if doc.Content, err = s.blob(ctx, q, d.row.OrganizationID, row.Sha256); err != nil {
					return err
				}
			}
			out = append(out, doc)
		}
		return nil
	})
	return out, err
}

// validator returns the structural validator, built once.
func (s *Service) validator() *schema.Validator { return s.schemaValidator }
