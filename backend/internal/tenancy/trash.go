// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package tenancy

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
)

// TrashItem is a deleted app, plugin or page awaiting restore or purge
// (GOV-031).
type TrashItem struct {
	ID         string
	Kind       string
	TargetID   string
	AppID      string
	Name       string
	DeletedBy  string
	DeletedAt  time.Time
	PurgeAfter time.Time
}

// TrashKind is what the trash does to restore or purge one kind of
// item. Apps are handled here; the document service registers plugins
// and pages, so the trash never needs to know how they are stored.
type TrashKind struct {
	// Restore undeletes the item in the caller's transaction, for the
	// principal who asked.
	Restore func(ctx context.Context, tx pgx.Tx, p auth.Principal, item TrashItem) error
	// Purge deletes the item for good in the caller's transaction.
	Purge func(ctx context.Context, tx pgx.Tx, p auth.Principal, item TrashItem) error
}

// trashKinds returns the kinds the trash handles, with apps built in.
func (s *Service) trashKinds() map[string]TrashKind {
	kinds := map[string]TrashKind{
		"app": {
			Restore: func(ctx context.Context, tx pgx.Tx, _ auth.Principal, item TrashItem) error {
				if _, err := dbgen.New(tx).RestoreApp(ctx, storage.MustUUID(item.TargetID)); err != nil {
					return failure(err, "app")
				}
				return nil
			},
			Purge: func(ctx context.Context, tx pgx.Tx, _ auth.Principal, item TrashItem) error {
				if _, err := dbgen.New(tx).PurgeApp(ctx, storage.MustUUID(item.TargetID)); err != nil {
					return failure(err, "app")
				}
				return nil
			},
		},
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.kinds {
		kinds[k] = v
	}
	return kinds
}

// RegisterTrashKind tells the trash how to restore and purge one more
// kind of item. The document service registers plugins and pages when it
// is built, so the trash never needs to know how they are stored.
func (s *Service) RegisterTrashKind(kind string, k TrashKind) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kinds[kind] = k
}

// Trash puts an item in the trash in the caller's transaction, to be
// purged after the retention period. The caller has already marked the
// item deleted.
func (s *Service) Trash(ctx context.Context, tx pgx.Tx, p auth.Principal, kind, targetID, appID, name string) (TrashItem, error) {
	id, err := s.newID()
	if err != nil {
		return TrashItem{}, err
	}
	var by pgtype.UUID
	if p.UserID != "" {
		by = storage.MustUUID(p.UserID)
	}
	row, err := dbgen.New(tx).CreateTrashItem(ctx, dbgen.CreateTrashItemParams{
		ID: storage.MustUUID(id), OrganizationID: storage.MustUUID(p.OrganizationID),
		Kind: kind, TargetID: storage.MustUUID(targetID), AppID: storage.MustUUID(appID), Name: name,
		DeletedBy: by, PurgeAfter: storage.Timestamp(s.now().AddDate(0, 0, s.o.TrashDays)),
	})
	if err != nil {
		return TrashItem{}, failure(err, "trash item")
	}
	return trashOf(row), nil
}

// DeleteApp moves an app to the trash, from which it can be restored
// for the retention period (GOV-031).
func (s *Service) DeleteApp(ctx context.Context, p auth.Principal, appID string) (TrashItem, error) {
	var out TrashItem
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		app, err := s.app(ctx, q, p, auth.AppManage, appID)
		if err != nil {
			return err
		}
		if _, err := q.SoftDeleteApp(ctx, app.ID); err != nil {
			return failure(err, "app")
		}
		out, err = s.Trash(ctx, tx, p, "app", appID, appID, app.Name)
		if err != nil {
			return err
		}
		return s.record(ctx, tx, p, audit.AppDeleted, "app", appID)
	})
	return out, err
}

// ListTrash lists what waits in the trash, oldest first, for the whole
// organisation or one app.
func (s *Service) ListTrash(ctx context.Context, p auth.Principal, appID string, page Page) ([]TrashItem, error) {
	var app pgtype.UUID
	if appID != "" {
		var err error
		if app, err = parseID(appID, "app"); err != nil {
			return nil, err
		}
		if err := authorizeApp(p, auth.TrashManage, appID); err != nil {
			return nil, err
		}
	} else if err := authorize(p, auth.TrashManage); err != nil {
		return nil, err
	}
	var out []TrashItem
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := dbgen.New(tx).ListTrash(ctx, dbgen.ListTrashParams{
			OrganizationID: storage.MustUUID(p.OrganizationID), AppID: app,
			AfterTime: page.After.AfterTime(), AfterID: page.After.AfterID(), PageSize: page.Size,
		})
		if err != nil {
			return failure(err, "trash item")
		}
		out = make([]TrashItem, len(rows))
		for i, row := range rows {
			out[i] = trashOf(row)
		}
		return nil
	})
	return out, err
}

// RestoreFromTrash restores an item. An app whose key was reused while
// it was in the trash cannot come back under that key and is refused
// as already existing.
func (s *Service) RestoreFromTrash(ctx context.Context, p auth.Principal, itemID string) error {
	return s.withTrashItem(ctx, p, itemID, func(ctx context.Context, tx pgx.Tx, item TrashItem, kind TrashKind) error {
		if err := kind.Restore(ctx, tx, p, item); err != nil {
			return err
		}
		if _, err := dbgen.New(tx).MarkTrashRestored(ctx, storage.MustUUID(item.ID)); err != nil {
			return failure(err, "trash item")
		}
		if item.Kind == "app" {
			if err := s.record(ctx, tx, p, audit.AppRestored, "app", item.TargetID); err != nil {
				return err
			}
		}
		return s.record(ctx, tx, p, audit.TrashRestored, item.Kind, item.TargetID)
	})
}

// PurgeFromTrash deletes an item for good, before its retention ends.
func (s *Service) PurgeFromTrash(ctx context.Context, p auth.Principal, itemID string) error {
	return s.withTrashItem(ctx, p, itemID, func(ctx context.Context, tx pgx.Tx, item TrashItem, kind TrashKind) error {
		return s.purge(ctx, tx, p, item, kind)
	})
}

// purge deletes one item and records it.
func (s *Service) purge(ctx context.Context, tx pgx.Tx, p auth.Principal, item TrashItem, kind TrashKind) error {
	if err := kind.Purge(ctx, tx, p, item); err != nil {
		return err
	}
	if _, err := dbgen.New(tx).DeleteTrashItem(ctx, storage.MustUUID(item.ID)); err != nil {
		return failure(err, "trash item")
	}
	if item.Kind == "app" {
		if err := s.record(ctx, tx, p, audit.AppPurged, "app", item.TargetID); err != nil {
			return err
		}
	}
	return s.record(ctx, tx, p, audit.TrashPurged, item.Kind, item.TargetID)
}

// withTrashItem locks an item, authorises the principal on it and runs
// f on it.
func (s *Service) withTrashItem(ctx context.Context, p auth.Principal, itemID string,
	f func(context.Context, pgx.Tx, TrashItem, TrashKind) error,
) error {
	id, err := parseID(itemID, "trash item")
	if err != nil {
		return err
	}
	return s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		row, err := dbgen.New(tx).GetTrashItemForUpdate(ctx, id)
		if err != nil {
			return failure(err, "trash item")
		}
		item := trashOf(row)
		if err := authorizeApp(p, auth.TrashManage, item.AppID); err != nil {
			return err
		}
		kind, ok := s.trashKinds()[item.Kind]
		if !ok {
			return fmt.Errorf("tenancy: no handler for trash of kind %q", item.Kind)
		}
		return f(ctx, tx, item, kind)
	})
}

// PurgeExpired deletes everything whose retention has ended, visiting
// each organisation in its own transactions (GOV-031). It is the
// maintenance job's work and returns how many items it purged.
func (s *Service) PurgeExpired(ctx context.Context, batch int32) (int, error) {
	purged := 0
	kinds := s.trashKinds()
	err := s.ForEachOrganization(ctx, func(org string) error {
		n, err := s.purgeOrganization(ctx, org, batch, kinds)
		purged += n
		return err
	})
	return purged, err
}

// ForEachOrganization calls f with every organisation's identifier, in
// pages, stopping at the first error. It is how maintenance jobs visit
// each tenant in transactions of its own.
func (s *Service) ForEachOrganization(ctx context.Context, f func(org string) error) error {
	after := storage.Cursor{}
	for {
		var orgs []pgtype.UUID
		if err := s.o.DB.InTx(ctx, storage.Tenant{}, func(ctx context.Context, tx pgx.Tx) error {
			var err error
			orgs, err = dbgen.New(tx).ListOrganizationIDs(ctx, dbgen.ListOrganizationIDsParams{AfterID: after.AfterID(), PageSize: 100})
			return err //nolint:wrapcheck // wrapped below
		}); err != nil {
			return fmt.Errorf("tenancy: list organisations: %w", err)
		}
		if len(orgs) == 0 {
			return nil
		}
		for _, org := range orgs {
			if err := f(storage.ID(org)); err != nil {
				return err
			}
		}
		after.ID = storage.ID(orgs[len(orgs)-1])
	}
}

// purgeOrganization purges one organisation's expired items.
func (s *Service) purgeOrganization(ctx context.Context, org string, batch int32, kinds map[string]TrashKind) (int, error) {
	system := auth.System(org)
	purged := 0
	err := s.inOrg(ctx, system, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := dbgen.New(tx).ListExpiredTrash(ctx, batch)
		if err != nil {
			return failure(err, "trash item")
		}
		for _, row := range rows {
			item := trashOf(row)
			kind, ok := kinds[item.Kind]
			if !ok {
				return fmt.Errorf("tenancy: no handler for trash of kind %q", item.Kind)
			}
			if err := s.purge(ctx, tx, system, item, kind); err != nil {
				return err
			}
			purged++
		}
		return nil
	})
	return purged, err
}

// trashOf converts a stored trash item.
func trashOf(row dbgen.Trash) TrashItem {
	return TrashItem{
		ID: storage.ID(row.ID), Kind: row.Kind, TargetID: storage.ID(row.TargetID), AppID: storage.ID(row.AppID),
		Name: row.Name, DeletedBy: storage.ID(row.DeletedBy),
		DeletedAt: storage.Time(row.DeletedAt), PurgeAfter: storage.Time(row.PurgeAfter),
	}
}
