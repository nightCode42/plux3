// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package release

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/compiler"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
)

// Version is an immutable published bundle (REL-001): a plugin's, or the
// app's own when PluginID is empty.
type Version struct {
	ID               string
	AppID            string
	PluginID         string
	PluginKey        string
	Version          int64
	Label            string
	Notes            string
	BundleSHA256     string
	BundleSize       int64
	RequiredFeatures []string
	MinRuntime       string
	Signature        []byte
	KeyID            string
	Algorithm        string
	SourceSnapshotID string
	PublishedBy      audit.Actor
	CreatedAt        time.Time
}

// Release is an immutable set of one version per active plugin and one
// app bundle (REL-002).
type Release struct {
	ID               string
	AppID            string
	EnvironmentID    string
	Sequence         int64
	AppVersionID     string
	VersionIDs       []string
	AppBundleSHA256  string
	AppBundleSize    int64
	RollbackOf       int64
	Notes            string
	MinRuntime       string
	RequiredFeatures []string
	Size             int64
	CreatedBy        audit.Actor
	CreatedAt        time.Time
}

// CreateRelease creates an app release in an environment from a version
// of every active plugin and of the app bundle (REL-002). A plugin not
// named keeps the version of the environment's last release, or takes
// its newest; the key "" names the app bundle's version. The whole set
// is compiled again from the versions' sources: an unresolved reference,
// or a version whose bundle those sources would not reproduce, stops the
// release, reported as diagnostics (REL-003).
func (s *Service) CreateRelease(ctx context.Context, p auth.Principal, appID, envID string, chosen map[string]int64, notes string) (Release, plxerr.Diagnostics, error) {
	if err := authorize(p, auth.ReleasePublish, appID); err != nil {
		return Release{}, nil, err
	}
	var (
		out   Release
		diags plxerr.Diagnostics
	)
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		app, env, err := s.environment(ctx, q, appID, envID)
		if err != nil {
			return err
		}
		if _, err := q.LockApp(ctx, app); err != nil {
			return failure(err, "app")
		}
		versions, missing, err := s.chooseVersions(ctx, q, app, env, chosen)
		if err != nil || len(missing) > 0 {
			diags = missing
			return err
		}
		if diags, err = s.validateSet(ctx, tx, p, appID, versions); err != nil || diags.HasErrors() {
			return err
		}
		out, err = s.insertRelease(ctx, tx, p, env, versions, 0, notes)
		return err
	})
	return out, diags, err
}

// chooseVersions picks the version of the app bundle and of every active
// plugin; plugins with none are reported.
func (s *Service) chooseVersions(ctx context.Context, q *dbgen.Queries, app pgtype.UUID, env dbgen.Environment, chosen map[string]int64) ([]dbgen.PluginVersion, plxerr.Diagnostics, error) {
	base := map[string]int64{}
	if last, err := q.LatestReleaseInEnvironment(ctx, dbgen.LatestReleaseInEnvironmentParams{AppID: app, EnvironmentID: env.ID}); err == nil {
		vs, err := q.ListReleaseVersions(ctx, last.ID)
		if err != nil {
			return nil, nil, failure(err, "release")
		}
		for _, v := range vs {
			base[v.PluginKey] = v.Version
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, failure(err, "release")
	}
	plugins, err := q.ListAllPlugins(ctx, app)
	if err != nil {
		return nil, nil, failure(err, "plugin")
	}
	known := map[string]pgtype.UUID{"": {}}
	for _, pl := range plugins {
		known[pl.Key] = pl.ID
	}
	for key := range chosen {
		if _, ok := known[key]; !ok {
			return nil, nil, plxerr.New(plxerr.ResourceNotFound, "the app has no plugin %q", key)
		}
	}
	var (
		out     []dbgen.PluginVersion
		missing plxerr.Diagnostics
	)
	for _, key := range slices.Sorted(maps.Keys(known)) {
		v, err := s.versionFor(ctx, q, app, known[key], key, chosen, base)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			what := "plugin " + key
			if key == "" {
				what = "the app bundle"
			}
			missing = append(missing, plxerr.NewDiagnostic(plxerr.PluginNotPublished, plxerr.Location{}, "%s has no published version", what))
		case err != nil:
			return nil, nil, failure(err, "version")
		default:
			out = append(out, v)
		}
	}
	return out, missing, nil
}

// versionFor returns the version a release takes for one key: the one
// asked for, else the environment's last release's, else the newest.
func (*Service) versionFor(ctx context.Context, q *dbgen.Queries, app, plugin pgtype.UUID, key string, chosen, base map[string]int64) (dbgen.PluginVersion, error) {
	if n, ok := chosen[key]; ok {
		return q.GetVersion(ctx, dbgen.GetVersionParams{AppID: app, PluginID: plugin, Version: n}) //nolint:wrapcheck // examined by the caller
	}
	if n, ok := base[key]; ok {
		return q.GetVersion(ctx, dbgen.GetVersionParams{AppID: app, PluginID: plugin, Version: n}) //nolint:wrapcheck // examined by the caller
	}
	return q.LatestVersion(ctx, dbgen.LatestVersionParams{AppID: app, PluginID: plugin}) //nolint:wrapcheck // examined by the caller
}

// validateSet compiles a set of versions from their sources and checks
// that every bundle it produces is the one each version stored.
func (s *Service) validateSet(ctx context.Context, tx pgx.Tx, p auth.Principal, appID string, versions []dbgen.PluginVersion) (plxerr.Diagnostics, error) {
	sources := make([]string, 0, len(versions))
	for _, v := range versions {
		sources = append(sources, storage.ID(v.SourceSnapshotID))
	}
	fsys, err := s.o.Documents.SourceFiles(ctx, tx, appID, sources)
	if err != nil {
		return nil, err //nolint:wrapcheck // a domain error
	}
	opts, err := s.compileOptions(ctx, tx, p.OrganizationID, appID, "")
	if err != nil {
		return nil, err
	}
	res := compiler.Compile(fsys, opts)
	diags := res.Diagnostics
	if diags.HasErrors() {
		return diags, nil
	}
	produced := map[string]*compiler.Bundle{"": res.App}
	for _, b := range res.Plugins {
		produced[b.Key] = b
	}
	for _, v := range versions {
		b := produced[v.PluginKey]
		if b == nil || !bytes.Equal(b.Hash[:], v.BundleSha256) {
			diags = append(diags, plxerr.NewDiagnostic(plxerr.ReleaseInconsistent, plxerr.Location{},
				"%s version %d was compiled against other sources than this release's; publish it again", nameOf(v), v.Version))
		}
	}
	return diags, nil
}

// nameOf names a version's bundle in messages.
func nameOf(v dbgen.PluginVersion) string {
	if !v.PluginID.Valid {
		return "the app bundle"
	}
	return "plugin " + v.PluginKey
}

// insertRelease records a release with the next sequence of the app.
func (s *Service) insertRelease(ctx context.Context, tx pgx.Tx, p auth.Principal, env dbgen.Environment, versions []dbgen.PluginVersion, rollbackOf int64, notes string) (Release, error) {
	q := dbgen.New(tx)
	seq, err := q.NextReleaseSequence(ctx, env.AppID)
	if err != nil {
		return Release{}, failure(err, "release")
	}
	id, err := s.newID()
	if err != nil {
		return Release{}, err
	}
	var (
		appVersion pgtype.UUID
		size       int64
		minRuntime string
		features   = map[string]bool{}
	)
	for _, v := range versions {
		if !v.PluginID.Valid {
			appVersion = v.ID
		}
		size += v.BundleSize
		if semverLess(minRuntime, v.MinRuntime) {
			minRuntime = v.MinRuntime
		}
		for _, f := range v.RequiredFeatures {
			features[f] = true
		}
	}
	row, err := q.InsertRelease(ctx, dbgen.InsertReleaseParams{
		ID: storage.MustUUID(id), OrganizationID: env.OrganizationID, AppID: env.AppID, EnvironmentID: env.ID,
		Sequence: seq, AppVersionID: appVersion, RollbackOf: rollbackOf, Notes: notes,
		MinRuntime: minRuntime, RequiredFeatures: nonNilStrings(slices.Sorted(maps.Keys(features))), Size: size,
		CreatedByKind: p.Kind, CreatedByID: p.ID, CreatedBy: p.Display,
	})
	if err != nil {
		return Release{}, failure(err, "release")
	}
	for _, v := range versions {
		if err := q.InsertReleaseVersion(ctx, dbgen.InsertReleaseVersionParams{ReleaseID: row.ID, OrganizationID: env.OrganizationID, PluginVersionID: v.ID}); err != nil {
			return Release{}, failure(err, "release")
		}
	}
	action := audit.ReleaseCreated
	if rollbackOf > 0 {
		action = audit.ReleaseRolledBack
	}
	if err := s.record(ctx, tx, p, audit.Entry{Action: action, TargetKind: "release", TargetID: id, Detail: "sequence " + strconv.FormatInt(seq, 10)}); err != nil {
		return Release{}, err
	}
	return releaseOf(row, versions), nil
}

// GetRelease returns a release with its versions.
func (s *Service) GetRelease(ctx context.Context, p auth.Principal, appID string, sequence int64) (Release, []Version, error) {
	if err := authorize(p, auth.PluginRead, appID); err != nil {
		return Release{}, nil, err
	}
	var (
		out      Release
		versions []Version
	)
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		row, vs, err := s.releaseRow(ctx, q, appID, sequence)
		if err != nil {
			return err
		}
		out = releaseOf(row, vs)
		for _, v := range vs {
			versions = append(versions, versionOf(v))
		}
		return nil
	})
	return out, versions, err
}

// releaseRow reads a release and its versions.
func (*Service) releaseRow(ctx context.Context, q *dbgen.Queries, appID string, sequence int64) (dbgen.Release, []dbgen.PluginVersion, error) {
	app, err := parseID(appID, "app")
	if err != nil {
		return dbgen.Release{}, nil, err
	}
	row, err := q.GetRelease(ctx, dbgen.GetReleaseParams{AppID: app, Sequence: sequence})
	if err != nil {
		return dbgen.Release{}, nil, failure(err, "release")
	}
	vs, err := q.ListReleaseVersions(ctx, row.ID)
	if err != nil {
		return dbgen.Release{}, nil, failure(err, "release")
	}
	return row, vs, nil
}

// ListReleases lists an app's releases newest first, or those created
// in one environment; before is the sequence to continue before.
func (s *Service) ListReleases(ctx context.Context, p auth.Principal, appID, envID string, before int64, size int32) ([]Release, error) {
	if err := authorize(p, auth.PluginRead, appID); err != nil {
		return nil, err
	}
	app, err := parseID(appID, "app")
	if err != nil {
		return nil, err
	}
	var env pgtype.UUID
	if envID != "" {
		if env, err = parseID(envID, "environment"); err != nil {
			return nil, err
		}
	}
	if before <= 0 {
		before = 1 << 62
	}
	var out []Release
	err = s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		rows, err := q.ListReleases(ctx, dbgen.ListReleasesParams{AppID: app, EnvironmentID: env, BeforeSequence: before, PageSize: size})
		if err != nil {
			return failure(err, "release")
		}
		for _, r := range rows {
			vs, err := q.ListReleaseVersions(ctx, r.ID)
			if err != nil {
				return failure(err, "release")
			}
			out = append(out, releaseOf(r, vs))
		}
		return nil
	})
	return out, err
}

// GetVersion returns one version of a plugin.
func (s *Service) GetVersion(ctx context.Context, p auth.Principal, pluginID string, version int64) (Version, error) {
	var out Version
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		plugin, err := s.pluginRow(ctx, q, p, pluginID)
		if err != nil {
			return err
		}
		v, err := q.GetVersion(ctx, dbgen.GetVersionParams{AppID: plugin.AppID, PluginID: plugin.ID, Version: version})
		if err != nil {
			return failure(err, "version")
		}
		out = versionOf(v)
		return nil
	})
	return out, err
}

// ListVersions lists a plugin's versions, newest first; before is the
// version to continue before.
func (s *Service) ListVersions(ctx context.Context, p auth.Principal, pluginID string, before int64, size int32) ([]Version, error) {
	if before <= 0 {
		before = 1 << 62
	}
	var out []Version
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		plugin, err := s.pluginRow(ctx, q, p, pluginID)
		if err != nil {
			return err
		}
		rows, err := q.ListVersions(ctx, dbgen.ListVersionsParams{AppID: plugin.AppID, PluginID: plugin.ID, BeforeVersion: before, PageSize: size})
		if err != nil {
			return failure(err, "version")
		}
		for _, r := range rows {
			out = append(out, versionOf(r))
		}
		return nil
	})
	return out, err
}

// pluginRow reads a plugin the principal may read.
func (*Service) pluginRow(ctx context.Context, q *dbgen.Queries, p auth.Principal, pluginID string) (dbgen.Plugin, error) {
	id, err := parseID(pluginID, "plugin")
	if err != nil {
		return dbgen.Plugin{}, err
	}
	row, err := q.GetPluginIncludingDeleted(ctx, id)
	if err != nil {
		return dbgen.Plugin{}, failure(err, "plugin")
	}
	if err := authorize(p, auth.PluginRead, storage.ID(row.AppID)); err != nil {
		return dbgen.Plugin{}, plxerr.New(plxerr.ResourceNotFound, "no such plugin")
	}
	return row, nil
}

// PromoteRelease points a channel of an environment at a release. It
// copies no bytes and recompiles nothing (REL-004, REL-005); a release
// promoted to a production environment is kept for ever (REL-007).
func (s *Service) PromoteRelease(ctx context.Context, p auth.Principal, appID string, sequence int64, envID, channelKey string) (Release, error) {
	if err := authorize(p, auth.ReleasePromote, appID); err != nil {
		return Release{}, err
	}
	var out Release
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		_, env, err := s.environment(ctx, q, appID, envID)
		if err != nil {
			return err
		}
		row, vs, err := s.releaseRow(ctx, q, appID, sequence)
		if err != nil {
			return err
		}
		if err := s.point(ctx, tx, p, env, channelKey, row); err != nil {
			return err
		}
		out = releaseOf(row, vs)
		return nil
	})
	return out, err
}

// point sets a channel to a release.
func (s *Service) point(ctx context.Context, tx pgx.Tx, p auth.Principal, env dbgen.Environment, channelKey string, row dbgen.Release) error {
	q := dbgen.New(tx)
	if channelKey == "" {
		channelKey = "production"
	}
	ch, err := q.GetChannelForUpdate(ctx, dbgen.GetChannelForUpdateParams{EnvironmentID: env.ID, Key: channelKey})
	if err != nil {
		return failure(err, "channel")
	}
	if _, err := q.PointChannel(ctx, dbgen.PointChannelParams{ID: ch.ID, ReleaseSequence: row.Sequence}); err != nil {
		return failure(err, "channel")
	}
	if err := s.enqueueManifest(ctx, tx, ch); err != nil {
		return err
	}
	if env.Production {
		if err := q.MarkReleaseProduction(ctx, row.ID); err != nil {
			return failure(err, "release")
		}
	}
	return s.record(ctx, tx, p, audit.Entry{
		Action: audit.ReleasePromoted, TargetKind: "release", TargetID: storage.ID(row.ID),
		Detail: fmt.Sprintf("sequence %d to %s/%s", row.Sequence, env.Key, channelKey),
	})
}

// RollbackRelease creates a release whose content equals an earlier one,
// with a higher sequence, and points the channel at it, so a device's
// anti-rollback rule never has to be relaxed (REL-006).
func (s *Service) RollbackRelease(ctx context.Context, p auth.Principal, appID, envID, channelKey string, toSequence int64, notes string) (Release, error) {
	if err := authorize(p, auth.ReleasePromote, appID); err != nil {
		return Release{}, err
	}
	var out Release
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		app, env, err := s.environment(ctx, q, appID, envID)
		if err != nil {
			return err
		}
		if _, err := q.LockApp(ctx, app); err != nil {
			return failure(err, "app")
		}
		_, vs, err := s.releaseRow(ctx, q, appID, toSequence)
		if err != nil {
			return err
		}
		if notes == "" {
			notes = "Rollback to release " + strconv.FormatInt(toSequence, 10)
		}
		out, err = s.insertRelease(ctx, tx, p, env, vs, toSequence, notes)
		if err != nil {
			return err
		}
		row, err := q.GetRelease(ctx, dbgen.GetReleaseParams{AppID: app, Sequence: out.Sequence})
		if err != nil {
			return failure(err, "release")
		}
		return s.point(ctx, tx, p, env, channelKey, row)
	})
	return out, err
}

// semverLess compares MAJOR.MINOR.PATCH versions; "" is lowest.
func semverLess(a, b string) bool {
	if a == b {
		return false
	}
	if a == "" {
		return true
	}
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := range min(len(pa), len(pb)) {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			return x < y
		}
	}
	return len(pa) < len(pb)
}

// versionOf converts a stored version.
func versionOf(v dbgen.PluginVersion) Version {
	return Version{
		ID: storage.ID(v.ID), AppID: storage.ID(v.AppID), PluginID: storage.ID(v.PluginID), PluginKey: v.PluginKey,
		Version: v.Version, Label: v.Label, Notes: v.Notes, BundleSHA256: hex.EncodeToString(v.BundleSha256),
		BundleSize: v.BundleSize, RequiredFeatures: v.RequiredFeatures, MinRuntime: v.MinRuntime,
		Signature: v.Signature, KeyID: v.KeyID, Algorithm: v.Algorithm, SourceSnapshotID: storage.ID(v.SourceSnapshotID),
		PublishedBy: audit.Actor{Kind: v.PublishedByKind, ID: v.PublishedByID, Display: v.PublishedBy},
		CreatedAt:   storage.Time(v.CreatedAt),
	}
}

// releaseOf converts a stored release with its versions.
func releaseOf(r dbgen.Release, vs []dbgen.PluginVersion) Release {
	out := Release{
		ID: storage.ID(r.ID), AppID: storage.ID(r.AppID), EnvironmentID: storage.ID(r.EnvironmentID),
		Sequence: r.Sequence, AppVersionID: storage.ID(r.AppVersionID), RollbackOf: r.RollbackOf, Notes: r.Notes,
		MinRuntime: r.MinRuntime, RequiredFeatures: r.RequiredFeatures, Size: r.Size,
		CreatedBy: audit.Actor{Kind: r.CreatedByKind, ID: r.CreatedByID, Display: r.CreatedBy},
		CreatedAt: storage.Time(r.CreatedAt),
	}
	for _, v := range vs {
		if v.ID == r.AppVersionID {
			out.AppBundleSHA256, out.AppBundleSize = hex.EncodeToString(v.BundleSha256), v.BundleSize
			continue
		}
		out.VersionIDs = append(out.VersionIDs, storage.ID(v.ID))
	}
	return out
}
