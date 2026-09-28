// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package document

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
	"github.com/nightCode42/plux3/backend/internal/tenancy"
)

// Plugin is a plugin of an app, with its draft's revision.
type Plugin struct {
	ID            string
	AppID         string
	Key           string
	Name          string
	LatestVersion int64
	DraftRevision int64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// CreatePlugin adds a plugin to an app, with an empty draft whose
// documents live under plugins/<key>/ (SCH-006). The app's plugin count
// is bounded by app.plugins (LIM-001).
func (s *Service) CreatePlugin(ctx context.Context, p auth.Principal, appID, key, name string) (Plugin, error) {
	if err := authorize(p, auth.AppManage, appID); err != nil {
		return Plugin{}, err
	}
	if !schema.ValidKey(key) {
		return Plugin{}, plxerr.New(plxerr.InvalidFormat, "a plugin key is a lower-kebab slug of at most 64 characters, starting with a letter")
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 200 {
		return Plugin{}, plxerr.New(plxerr.InvalidFormat, "a plugin name is 1 to 200 characters")
	}
	id, err := s.newID()
	if err != nil {
		return Plugin{}, err
	}
	var out Plugin
	err = s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		d, err := s.resolve(ctx, q, p, appID, "")
		if err != nil {
			return err
		}
		lim, err := s.limitsFor(ctx, tx, d)
		if err != nil {
			return err
		}
		n, err := q.CountPlugins(ctx, d.row.AppID)
		if err != nil {
			return failure(err, "plugin")
		}
		if max := lim.Get(limits.AppPlugins); n >= max {
			return plxerr.New(plxerr.LimitExceeded, "the app has %d plugins, the limit app.plugins", max)
		}
		row, err := q.CreatePlugin(ctx, dbgen.CreatePluginParams{
			ID: storage.MustUUID(id), OrganizationID: d.row.OrganizationID, AppID: d.row.AppID, Key: key, Name: name,
		})
		if err != nil {
			return failure(err, "plugin")
		}
		pd, err := s.resolve(ctx, q, p, appID, id)
		if err != nil {
			return err
		}
		out = pluginOf(row, pd.row)
		return s.record(ctx, tx, p, audit.PluginCreated, "plugin", id, "", "")
	})
	return out, err
}

// plugin reads a plugin with its draft after checking that the
// principal may read its app.
func (s *Service) plugin(ctx context.Context, q *dbgen.Queries, p auth.Principal, pluginID string) (draft, error) {
	id, err := parseID(pluginID, "plugin")
	if err != nil {
		return draft{}, err
	}
	row, err := q.GetPlugin(ctx, id)
	if err != nil {
		return draft{}, failure(err, "plugin")
	}
	return s.resolve(ctx, q, p, storage.ID(row.AppID), pluginID)
}

// GetPlugin returns a plugin.
func (s *Service) GetPlugin(ctx context.Context, p auth.Principal, pluginID string) (Plugin, error) {
	var out Plugin
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		d, err := s.plugin(ctx, dbgen.New(tx), p, pluginID)
		if err != nil {
			return err
		}
		if err := authorize(p, auth.PluginRead, d.app); err != nil {
			return err
		}
		out = pluginOf(*d.plugin, d.row)
		return nil
	})
	return out, err
}

// ListPlugins lists an app's plugins in key order.
func (s *Service) ListPlugins(ctx context.Context, p auth.Principal, appID string, after storage.Cursor, size int32) ([]Plugin, error) {
	var out []Plugin
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		d, err := s.resolve(ctx, q, p, appID, "")
		if err != nil {
			return err
		}
		if err := authorize(p, auth.PluginRead, appID); err != nil {
			return err
		}
		rows, err := q.ListPlugins(ctx, dbgen.ListPluginsParams{AppID: d.row.AppID, AfterKey: after.Key, AfterID: after.AfterID(), PageSize: size})
		if err != nil {
			return failure(err, "plugin")
		}
		for _, row := range rows {
			dr, err := q.GetDraft(ctx, dbgen.GetDraftParams{AppID: row.AppID, PluginID: row.ID})
			if err != nil {
				return failure(err, "draft")
			}
			out = append(out, pluginOf(row, dr))
		}
		return nil
	})
	return out, err
}

// UpdatePlugin renames a plugin.
func (s *Service) UpdatePlugin(ctx context.Context, p auth.Principal, pluginID, name string) (Plugin, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 200 {
		return Plugin{}, plxerr.New(plxerr.InvalidFormat, "a plugin name is 1 to 200 characters")
	}
	var out Plugin
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		d, err := s.plugin(ctx, q, p, pluginID)
		if err != nil {
			return err
		}
		if err := authorize(p, auth.AppManage, d.app); err != nil {
			return err
		}
		row, err := q.UpdatePlugin(ctx, dbgen.UpdatePluginParams{ID: d.plugin.ID, Name: name})
		if err != nil {
			return failure(err, "plugin")
		}
		out = pluginOf(row, d.row)
		return s.record(ctx, tx, p, audit.PluginUpdated, "plugin", pluginID, "", "")
	})
	return out, err
}

// DeletePlugin moves a plugin, with its draft and history, to the trash
// for the retention period (GOV-031); its lock is released.
func (s *Service) DeletePlugin(ctx context.Context, p auth.Principal, pluginID string) (tenancy.TrashItem, error) {
	var out tenancy.TrashItem
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		d, err := s.plugin(ctx, q, p, pluginID)
		if err != nil {
			return err
		}
		if err := authorize(p, auth.AppManage, d.app); err != nil {
			return err
		}
		if _, err := q.SoftDeletePlugin(ctx, d.plugin.ID); err != nil {
			return failure(err, "plugin")
		}
		if _, err := q.DeleteLock(ctx, d.row.ID); err != nil {
			return fmt.Errorf("document: release the lock: %w", err)
		}
		if out, err = s.o.Tenancy.Trash(ctx, tx, p, "plugin", pluginID, d.app, d.plugin.Name); err != nil {
			return fmt.Errorf("document: %w", err)
		}
		return s.record(ctx, tx, p, audit.PluginDeleted, "plugin", pluginID, "", "")
	})
	return out, err
}

// TrashKinds are how the trash restores and purges plugins and pages;
// the server registers them with the tenancy service.
func (s *Service) TrashKinds() map[string]tenancy.TrashKind {
	return map[string]tenancy.TrashKind{
		"plugin": {Restore: s.restorePlugin, Purge: s.purgePlugin},
		"page":   {Restore: s.restorePage, Purge: s.purgePage},
	}
}

// restorePlugin undeletes a plugin, unless its key was reused meanwhile.
func (s *Service) restorePlugin(ctx context.Context, tx pgx.Tx, p auth.Principal, item tenancy.TrashItem) error {
	if _, err := dbgen.New(tx).RestorePlugin(ctx, storage.MustUUID(item.TargetID)); err != nil {
		return failure(err, "plugin")
	}
	return s.record(ctx, tx, p, audit.PluginRestored, "plugin", item.TargetID, "", "")
}

// purgePlugin deletes a plugin with its draft and history.
func (s *Service) purgePlugin(ctx context.Context, tx pgx.Tx, p auth.Principal, item tenancy.TrashItem) error {
	if _, err := dbgen.New(tx).PurgePlugin(ctx, storage.MustUUID(item.TargetID)); err != nil {
		return failure(err, "plugin")
	}
	return s.record(ctx, tx, p, audit.PluginPurged, "plugin", item.TargetID, "", "")
}

// restorePage undeletes a page, unless its path was reused meanwhile,
// and records the restore as a snapshot of its draft.
func (s *Service) restorePage(ctx context.Context, tx pgx.Tx, p auth.Principal, item tenancy.TrashItem) error {
	q := dbgen.New(tx)
	row, err := q.RestoreDocument(ctx, storage.MustUUID(item.TargetID))
	if err != nil {
		return failure(err, "page")
	}
	dr, err := q.GetDraftForUpdate(ctx, row.DraftID)
	if err != nil {
		return failure(err, "draft")
	}
	snap, err := s.newSnapshot(ctx, q, draft{row: dr}, p, 1, reasonRestore, true)
	if err != nil {
		return err
	}
	if err := q.AddSnapshotDocument(ctx, dbgen.AddSnapshotDocumentParams{
		SnapshotID: snap.ID, OrganizationID: row.OrganizationID, Path: row.Path, Kind: row.Kind, Sha256: row.Sha256,
	}); err != nil {
		return fmt.Errorf("document: record the snapshot: %w", err)
	}
	return s.record(ctx, tx, p, audit.DocumentRestored, "document", row.Path, "", hexHash(row.Sha256))
}

// purgePage deletes a trashed page for good; its content stays in the
// snapshots that recorded it until they expire.
func (s *Service) purgePage(ctx context.Context, tx pgx.Tx, p auth.Principal, item tenancy.TrashItem) error {
	q := dbgen.New(tx)
	row, err := q.GetDocumentByID(ctx, storage.MustUUID(item.TargetID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // purged with its plugin already
	}
	if err != nil {
		return failure(err, "page")
	}
	if _, err := q.HardDeleteDocument(ctx, row.ID); err != nil {
		return fmt.Errorf("document: purge %s: %w", row.Path, err)
	}
	return s.record(ctx, tx, p, audit.DocumentPurged, "document", row.Path, hexHash(row.Sha256), "")
}

// ListPluginLimits reports every limit a plugin can set (LIM-005).
func (s *Service) ListPluginLimits(ctx context.Context, p auth.Principal, pluginID string) ([]tenancy.LimitUsage, error) {
	appID, err := s.appOf(ctx, p, pluginID)
	if err != nil {
		return nil, err
	}
	out, err := s.o.Tenancy.ListPluginLimits(ctx, p, appID, pluginID)
	if err != nil {
		return nil, fmt.Errorf("document: %w", err)
	}
	return out, nil
}

// SetPluginLimit tightens a limit for one plugin (LIM-002).
func (s *Service) SetPluginLimit(ctx context.Context, p auth.Principal, pluginID, key string, value int64) (tenancy.LimitUsage, error) {
	appID, err := s.appOf(ctx, p, pluginID)
	if err != nil {
		return tenancy.LimitUsage{}, err
	}
	out, err := s.o.Tenancy.SetPluginLimit(ctx, p, appID, pluginID, key, value)
	if err != nil {
		return tenancy.LimitUsage{}, fmt.Errorf("document: %w", err)
	}
	return out, nil
}

// appOf returns the app a plugin belongs to.
func (s *Service) appOf(ctx context.Context, p auth.Principal, pluginID string) (string, error) {
	var appID string
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		d, err := s.plugin(ctx, dbgen.New(tx), p, pluginID)
		appID = d.app
		return err
	})
	return appID, err
}

// pluginOf converts a stored plugin.
func pluginOf(row dbgen.Plugin, d dbgen.Draft) Plugin {
	return Plugin{
		ID: storage.ID(row.ID), AppID: storage.ID(row.AppID), Key: row.Key, Name: row.Name,
		LatestVersion: row.LatestVersion, DraftRevision: d.Revision,
		CreatedAt: storage.Time(row.CreatedAt), UpdatedAt: storage.Time(row.UpdatedAt),
	}
}
