// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package document

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing/fstest"

	"github.com/jackc/pgx/v5"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/compiler"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema"
	"github.com/nightCode42/plux3/backend/internal/schema/jcs"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
)

// File is one file of the Git layout.
type File struct {
	Path    string
	Content []byte
}

// project reads every draft of an app into the Git layout the compiler
// reads (SCH-006): the app-level documents and those of every plugin
// that is not deleted.
func (s *Service) project(ctx context.Context, q *dbgen.Queries, app draft) (fstest.MapFS, error) {
	drafts, err := q.ListDrafts(ctx, app.row.AppID)
	if err != nil {
		return nil, failure(err, "draft")
	}
	fsys := fstest.MapFS{}
	for _, d := range drafts {
		docs, err := q.ListAllDocuments(ctx, d.ID)
		if err != nil {
			return nil, failure(err, "document")
		}
		for _, doc := range docs {
			content, err := s.blob(ctx, q, d.OrganizationID, doc.Sha256)
			if err != nil {
				return nil, err
			}
			fsys[doc.Path] = &fstest.MapFile{Data: content, Mode: 0o644}
		}
	}
	return fsys, nil
}

// compileOptions are the options validation compiles with: the app's
// limits, in development mode so nothing is withheld.
func (s *Service) compileOptions(lim limits.Set) compiler.Options {
	opts := compiler.DefaultOptions()
	opts.Limits = lim
	opts.Mode = compiler.Development
	opts.Version = s.o.CompilerVersion
	return opts
}

// ValidateDraft runs the compiler's checking stages over an app's
// drafts and returns every diagnostic, or, for one plugin, those of its
// files and of the app-level documents it depends on — never another
// plugin's (SCH-040). It writes nothing.
func (s *Service) ValidateDraft(ctx context.Context, p auth.Principal, appID, pluginID string) (plxerr.Diagnostics, error) {
	var out plxerr.Diagnostics
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		d, err := s.resolve(ctx, q, p, appID, pluginID)
		if err != nil {
			return err
		}
		if err := authorize(p, auth.PluginRead, appID); err != nil {
			return err
		}
		lim, err := s.limitsFor(ctx, tx, d)
		if err != nil {
			return err
		}
		fsys, err := s.project(ctx, q, d)
		if err != nil {
			return err
		}
		res := compiler.Compile(fsys, s.compileOptions(lim))
		for _, diag := range res.Diagnostics {
			if f := diag.File; strings.HasPrefix(f, d.prefix()) || !strings.HasPrefix(f, "plugins/") {
				out = append(out, diag)
			}
		}
		return nil
	})
	return out, err
}

// ValidatePage checks one page as the editor has it, against the rest of
// the draft, without writing it (SCH-042).
func (s *Service) ValidatePage(ctx context.Context, p auth.Principal, appID, pluginID, path string, content []byte) (plxerr.Diagnostics, error) {
	var out plxerr.Diagnostics
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		d, err := s.resolve(ctx, q, p, appID, pluginID)
		if err != nil {
			return err
		}
		if err := authorize(p, auth.PluginRead, appID); err != nil {
			return err
		}
		if place, ok := schema.PlaceOf(path); !ok || place.Kind != schema.KindPage || !strings.HasPrefix(path, d.prefix()) {
			return plxerr.New(plxerr.InvalidProjectLayout, "%q is not a page of this draft", path)
		}
		lim, err := s.limitsFor(ctx, tx, d)
		if err != nil {
			return err
		}
		fsys, err := s.project(ctx, q, d)
		if err != nil {
			return err
		}
		v, diags := compiler.NewValidator(fsys, s.compileOptions(lim))
		if v == nil {
			out = diags
			return nil
		}
		out = v.ValidatePage(path, content)
		return nil
	})
	return out, err
}

// Export returns a draft in the Git layout, pretty-printed in the
// canonical form meant for version control (SCH-006): the app-level
// documents and every plugin's for an empty plugin ID, or one plugin's.
func (s *Service) Export(ctx context.Context, p auth.Principal, appID, pluginID string) ([]File, error) {
	var out []File
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		d, err := s.resolve(ctx, q, p, appID, pluginID)
		if err != nil {
			return err
		}
		if err := authorize(p, auth.PluginRead, appID); err != nil {
			return err
		}
		fsys, err := s.project(ctx, q, d)
		if err != nil {
			return err
		}
		for _, path := range slices.Sorted(maps.Keys(fsys)) {
			if !strings.HasPrefix(path, d.prefix()) {
				continue
			}
			pretty, err := jcs.Format(fsys[path].Data, int(s.o.Limits.Get(limits.DocumentJSONDepth)))
			if err != nil {
				return fmt.Errorf("document: format %s: %w", path, err)
			}
			out = append(out, File{Path: path, Content: pretty})
		}
		return nil
	})
	return out, err
}

// Import replaces a draft with files in the Git layout and records it as
// one snapshot per draft. For an empty plugin ID it replaces the
// app-level draft and the draft of every plugin directory in the files,
// creating plugins that do not exist yet. It takes each draft's lock for
// the importing session, and is refused where someone else holds one.
// Documents the files do not contain are deleted, so the draft matches
// what was imported. Structural errors refuse the whole
// import; the diagnostics of the project are returned with it.
func (s *Service) Import(ctx context.Context, p auth.Principal, appID, pluginID, session string, files []File) (Written, error) {
	if len(files) == 0 {
		return Written{}, plxerr.New(plxerr.MissingProperty, "the import holds no documents")
	}
	// Every file is checked before anything is written, so a structural
	// error refuses the whole import rather than part of it.
	byDir := map[string][]File{}
	checker := schema.NewLoader(s.schemaValidator, schema.DefaultMigrator(), s.o.Limits)
	for _, f := range files {
		place, ok := schema.PlaceOf(f.Path)
		if !ok {
			return Written{}, plxerr.New(plxerr.InvalidProjectLayout, "%q is not a document of the project layout; assets are uploaded with AssetService", f.Path)
		}
		if _, diags := checker.ParseDocument(f.Path, f.Content, place.Kind); diags.HasErrors() {
			return Written{Diagnostics: diags}, refusal(diags)
		}
		byDir[place.Plugin] = append(byDir[place.Plugin], f)
	}
	var out Written
	if pluginID != "" {
		return s.importDraft(ctx, p, appID, pluginID, session, files)
	}
	for _, key := range slices.Sorted(maps.Keys(byDir)) {
		if key == "" {
			continue
		}
		id, err := s.pluginForImport(ctx, p, appID, key)
		if err != nil {
			return Written{}, err
		}
		w, err := s.importDraft(ctx, p, appID, id, session, byDir[key])
		if err != nil {
			return Written{}, err
		}
		out.Documents = append(out.Documents, w.Documents...)
		out.Diagnostics = append(out.Diagnostics, w.Diagnostics...)
	}
	w, err := s.importDraft(ctx, p, appID, "", session, byDir[""])
	if err != nil {
		return Written{}, err
	}
	out.Documents = append(out.Documents, w.Documents...)
	out.SnapshotID, out.Revision = w.SnapshotID, w.Revision
	diags, err := s.ValidateDraft(ctx, p, appID, "")
	if err != nil {
		return Written{}, err
	}
	out.Diagnostics = diags
	return out, nil
}

// pluginForImport returns the plugin with a key, creating it and taking
// its lock for the importing session when it does not exist.
func (s *Service) pluginForImport(ctx context.Context, p auth.Principal, appID, key string) (string, error) {
	var id string
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		app, err := parseID(appID, "app")
		if err != nil {
			return err
		}
		row, err := q.GetPluginByKey(ctx, dbgen.GetPluginByKeyParams{AppID: app, Key: key})
		switch {
		case err == nil:
			id = storage.ID(row.ID)
		case !errors.Is(err, pgx.ErrNoRows):
			return failure(err, "plugin")
		}
		return nil
	})
	if err != nil || id != "" {
		return id, err
	}
	created, err := s.CreatePlugin(ctx, p, appID, key, key)
	if err != nil {
		return "", err
	}
	return created.ID, nil
}

// importDraft replaces one draft with files, taking its lock for the
// importing session when nobody else holds it.
func (s *Service) importDraft(ctx context.Context, p auth.Principal, appID, pluginID, session string, files []File) (Written, error) {
	if _, _, err := s.AcquireLock(ctx, p, appID, pluginID, session, false); err != nil {
		return Written{}, err
	}
	out, err := s.write(ctx, p, appID, pluginID, session, reasonImport,
		func(ctx context.Context, q *dbgen.Queries, d draft, lim limits.Set) ([]change, plxerr.Diagnostics, error) {
			var (
				changes []change
				diags   plxerr.Diagnostics
				seen    = map[string]bool{}
			)
			for _, f := range files {
				if seen[f.Path] {
					return nil, nil, plxerr.New(plxerr.InvalidProjectLayout, "%s appears twice", f.Path)
				}
				seen[f.Path] = true
				c, dg, err := s.prepare(d, lim, f.Path, f.Content, anyRevision)
				diags = append(diags, dg...)
				if err != nil {
					return nil, diags, err
				}
				changes = append(changes, c)
			}
			current, err := q.ListAllDocuments(ctx, d.row.ID)
			if err != nil {
				return nil, nil, failure(err, "document")
			}
			for _, doc := range current {
				if !seen[doc.Path] {
					changes = append(changes, change{path: doc.Path, delete: true, ifRevision: anyRevision})
				}
			}
			if len(changes) == 0 {
				return nil, nil, plxerr.New(plxerr.MissingProperty, "the import holds no documents")
			}
			return changes, diags, nil
		})
	if err != nil {
		return out, err
	}
	return out, s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		return s.record(ctx, tx, p, audit.DraftImported, "draft", appID+"/"+pluginID, "", "")
	})
}
