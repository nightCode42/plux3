// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package release

import (
	"context"
	"errors"
	"maps"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
)

// ChangelogEntry is one change between two releases (REL-081).
type ChangelogEntry struct {
	// Kind is "page", "action" or "translation"; data sources and
	// functions join with their phases.
	Kind string
	// Change is "added", "removed" or "changed".
	Change    string
	PluginKey string
	Name      string
}

// changelogKinds are the document kinds a changelog reports.
var changelogKinds = map[schema.DocumentKind]string{
	schema.KindPage:         "page",
	schema.KindActionGraph:  "action",
	schema.KindTranslations: "translation",
}

// Changelog compares a release with an earlier one — the one before it
// unless from is given — from their sources, and returns the entries
// with the release's notes (REL-081).
func (s *Service) Changelog(ctx context.Context, p auth.Principal, appID string, sequence, from int64) ([]ChangelogEntry, string, error) {
	if err := authorize(p, auth.PluginRead, appID); err != nil {
		return nil, "", err
	}
	var (
		out   []ChangelogEntry
		notes string
	)
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		row, vs, err := s.releaseRow(ctx, q, appID, sequence)
		if err != nil {
			return err
		}
		notes = row.Notes
		after, err := s.o.Documents.SourceHashes(ctx, tx, sourcesOf(vs))
		if err != nil {
			return err //nolint:wrapcheck // a domain error
		}
		before := map[string]string{}
		if from == 0 {
			from = sequence - 1
		}
		if from > 0 {
			_, prev, err := s.releaseRow(ctx, q, appID, from)
			if err == nil {
				if before, err = s.o.Documents.SourceHashes(ctx, tx, sourcesOf(prev)); err != nil {
					return err //nolint:wrapcheck // a domain error
				}
			} else if !isNotFound(err) || from != sequence-1 {
				return err //nolint:wrapcheck // a domain error
			}
		}
		out = diffSources(before, after)
		return nil
	})
	return out, notes, err
}

// sourcesOf lists the source snapshots of versions.
func sourcesOf(vs []dbgen.PluginVersion) []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, storage.ID(v.SourceSnapshotID))
	}
	return out
}

// diffSources classifies how the documents a changelog reports differ.
func diffSources(before, after map[string]string) []ChangelogEntry {
	paths := map[string]bool{}
	for p := range before {
		paths[p] = true
	}
	for p := range after {
		paths[p] = true
	}
	var out []ChangelogEntry
	for _, path := range slices.Sorted(maps.Keys(paths)) {
		place, ok := schema.PlaceOf(path)
		kind := changelogKinds[place.Kind]
		if !ok || kind == "" {
			continue
		}
		a, inBefore := before[path]
		b, inAfter := after[path]
		e := ChangelogEntry{Kind: kind, PluginKey: place.Plugin, Name: place.Key}
		switch {
		case !inBefore:
			e.Change = "added"
		case !inAfter:
			e.Change = "removed"
		case a != b:
			e.Change = "changed"
		default:
			continue
		}
		out = append(out, e)
	}
	return out
}

// isNotFound reports a missing row.
func isNotFound(err error) bool {
	code, ok := plxerr.CodeOf(err)
	return errors.Is(err, pgx.ErrNoRows) || ok && code == plxerr.ResourceNotFound
}
