// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"encoding/json"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema"
)

// Validator validates edited pages of one project as the user types
// (SCH-042, NFR-033): the project is loaded once, and each edit re-parses
// only the edited page and runs the checking stages.
type Validator struct {
	loader  *schema.Loader
	project *schema.Project
	opts    Options
}

// NewValidator loads the project in fsys and returns a validator with the
// diagnostics of the loaded project. It never panics (CMP-052).
func NewValidator(fsys fs.FS, opts Options) (v *Validator, diags plxerr.Diagnostics) {
	defer func() {
		if r := recover(); r != nil {
			v = nil
			diags = plxerr.Diagnostics{plxerr.NewDiagnostic(plxerr.InternalCompilerError, plxerr.Location{}, "the validator failed unexpectedly: %v", r)}
		}
	}()
	sv, err := sharedValidator()
	if err != nil {
		return nil, plxerr.Diagnostics{plxerr.NewDiagnostic(plxerr.InternalCompilerError, plxerr.Location{}, "the embedded JSON Schemas do not compile: %v", err)}
	}
	loader := schema.NewLoader(sv, schema.DefaultMigrator(), opts.Limits)
	project, diags := loader.Load(fsys)
	return &Validator{loader: loader, project: project, opts: opts}, diags
}

// ValidatePage validates the content of a page file — edited, or new in
// an existing plugin directory — against the rest of the project and
// returns the diagnostics located in that file and in the action graphs
// the page owns, which an edit of its state or parameters can break. It
// checks only that page and its graphs; redirect loops are followed
// through every page.
func (v *Validator) ValidatePage(file string, data []byte) (diags plxerr.Diagnostics) {
	defer func() {
		if r := recover(); r != nil {
			diags = append(diags, plxerr.NewDiagnostic(plxerr.InternalCompilerError, plxerr.Location{File: file}, "the validator failed unexpectedly: %v", r))
		}
	}()
	src, diags := v.loader.ParseDocument(file, data, schema.KindPage)
	if src == nil || diags.HasErrors() {
		return diags
	}
	var doc schema.PageDocument
	if err := json.Unmarshal(src.Canonical, &doc); err != nil {
		return append(diags, plxerr.NewDiagnostic(plxerr.InternalCompilerError, plxerr.Location{File: file}, "decode validated page: %v", err))
	}
	project, ok := v.withPage(file, schema.Loaded[schema.PageDocument]{Doc: &doc, Source: src})
	if !ok {
		return append(diags, plxerr.NewDiagnostic(plxerr.InvalidProjectLayout, plxerr.Location{File: file}, "no plugin directory holds %s", file))
	}
	u := newUnit(v.opts)
	u.project = project
	u.focus = file
	checks := slices.DeleteFunc(slices.Clone(pipeline), func(s stage) bool { return !s.checking })
	run(u, checks)
	files := map[string]bool{file: true}
	for _, pl := range u.plugins {
		for _, g := range pl.graphs {
			if g.page != nil && g.page.file == file {
				files[g.file] = true
			}
		}
	}
	for _, d := range u.diags {
		if files[d.File] {
			diags = append(diags, d)
		}
	}
	diags.Sort()
	return diags
}

// withPage returns a copy of the project in which file holds page; the
// loaded project itself is never modified.
func (v *Validator) withPage(file string, page schema.Loaded[schema.PageDocument]) (*schema.Project, bool) {
	p := *v.project
	p.Plugins = slices.Clone(v.project.Plugins)
	for i := range p.Plugins {
		pl := &p.Plugins[i]
		if path.Dir(path.Dir(file)) != pl.Dir || !strings.HasSuffix(path.Dir(file), "/pages") {
			continue
		}
		pl.Pages = slices.Clone(pl.Pages)
		for j := range pl.Pages {
			if pl.Pages[j].Source != nil && pl.Pages[j].Source.File == file {
				pl.Pages[j] = page
				return &p, true
			}
		}
		pl.Pages = append(pl.Pages, page)
		return &p, true
	}
	return nil, false
}
