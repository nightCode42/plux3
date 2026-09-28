// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package release

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/nightCode42/plux3/backend/internal/schema/limits"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
)

// Usage measures what P2 can measure against the limits (LIM-005): for
// an app, its plugins and the size of its newest release; for a plugin,
// its pages and the size of its newest bundle. It is registered with
// tenancy, whose limit listings report it.
func (*Service) Usage(ctx context.Context, tx pgx.Tx, appID, pluginID string) (map[limits.Key]int64, error) {
	q := dbgen.New(tx)
	app, err := parseID(appID, "app")
	if err != nil {
		return nil, err
	}
	out := map[limits.Key]int64{}
	if pluginID == "" {
		if out[limits.AppPlugins], err = q.CountPlugins(ctx, app); err != nil {
			return nil, failure(err, "plugin")
		}
		if out[limits.ReleaseAppSize], err = q.LatestReleaseSize(ctx, app); err != nil {
			return nil, failure(err, "release")
		}
		return out, nil
	}
	plugin, err := parseID(pluginID, "plugin")
	if err != nil {
		return nil, err
	}
	if out[limits.PluginPages], err = q.CountPages(ctx, dbgen.CountPagesParams{AppID: app, PluginID: plugin}); err != nil {
		return nil, failure(err, "page")
	}
	v, err := q.LatestVersion(ctx, dbgen.LatestVersionParams{AppID: app, PluginID: plugin})
	switch {
	case err == nil:
		out[limits.BundlePluginSize] = v.BundleSize
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, failure(err, "version")
	}
	return out, nil
}
