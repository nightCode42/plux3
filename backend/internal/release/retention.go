// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package release

import (
	"context"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
)

// Compatibility is who can use a release (REL-080).
type Compatibility struct {
	MinRuntime       string
	RequiredFeatures []string
	// IncompatibleDevices is how many registered devices cannot use it.
	IncompatibleDevices int64
	// FallbackSequence is the newest earlier release that asks less of a
	// device — a lower minimum runtime, or fewer features — which the
	// manifest gives devices that cannot use this one; zero when none.
	FallbackSequence int64
}

// Compatibility computes which runtimes can use a release and how many
// devices cannot (REL-080).
func (s *Service) Compatibility(ctx context.Context, p auth.Principal, appID string, sequence int64) (Compatibility, error) {
	if err := authorize(p, auth.PluginRead, appID); err != nil {
		return Compatibility{}, err
	}
	var out Compatibility
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		row, _, err := s.releaseRow(ctx, q, appID, sequence)
		if err != nil {
			return err
		}
		out = Compatibility{MinRuntime: row.MinRuntime, RequiredFeatures: row.RequiredFeatures}
		all, err := q.ListAllReleases(ctx, row.AppID)
		if err != nil {
			return failure(err, "release")
		}
		for _, r := range all {
			if r.Sequence < row.Sequence && asksLess(r, row) {
				out.FallbackSequence = r.Sequence
			}
		}
		if s.o.Devices != nil {
			n, err := s.o.Devices.Incompatible(ctx, tx, appID, row.MinRuntime, row.RequiredFeatures)
			if err != nil {
				return fmt.Errorf("release: %w", err)
			}
			out.IncompatibleDevices = n
		}
		return nil
	})
	return out, err
}

// asksLess reports whether release a asks strictly less of a device than
// b: a lower minimum runtime, or the same runtime with a strict subset of
// the features.
func asksLess(a, b dbgen.Release) bool {
	if semverLess(a.MinRuntime, b.MinRuntime) {
		return true
	}
	if a.MinRuntime != b.MinRuntime || len(a.RequiredFeatures) >= len(b.RequiredFeatures) {
		return false
	}
	for _, f := range a.RequiredFeatures {
		if !slices.Contains(b.RequiredFeatures, f) {
			return false
		}
	}
	return true
}

// PurgeReleases deletes an organisation's development releases past
// their retention and the versions no remaining release holds (REL-007):
// a release ever promoted to a production environment, current on any
// channel, or an app's newest, stays; so does each plugin's newest
// version. The snapshots kept for deleted versions return to ordinary
// history retention. It returns how many releases it deleted.
func (s *Service) PurgeReleases(ctx context.Context, organizationID string) (int, error) {
	system := auth.System(organizationID)
	before := storage.Timestamp(s.now().AddDate(0, 0, -s.o.DevelopmentDays))
	n := 0
	err := s.inOrg(ctx, system, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		rows, err := q.ListPurgeableReleases(ctx, dbgen.ListPurgeableReleasesParams{Before: before, PageSize: 1000})
		if err != nil {
			return failure(err, "release")
		}
		for _, r := range rows {
			if err := q.DeleteRelease(ctx, r.ID); err != nil {
				return failure(err, "release")
			}
			n++
		}
		if _, err := q.DeleteUnreferencedVersions(ctx, before); err != nil {
			return failure(err, "version")
		}
		if _, err := q.ReleaseUnusedSnapshots(ctx, storage.MustUUID(organizationID)); err != nil {
			return failure(err, "snapshot")
		}
		if n == 0 {
			return nil
		}
		return s.record(ctx, tx, system, audit.Entry{Action: audit.ReleasesPurged, TargetKind: "organization", TargetID: organizationID, Detail: fmt.Sprintf("%d releases", n)})
	})
	return n, err
}
