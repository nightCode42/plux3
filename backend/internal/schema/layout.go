// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package schema

import (
	"errors"
	"io/fs"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
)

// loadPlugins loads every plugin directory and checks it against the app's
// plugin list and the plugin's page list.
func (r *run) loadPlugins(p *Project) {
	entries, err := fs.ReadDir(r.fsys, "plugins")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		r.report(plxerr.FileSystemError, "plugins", "", "read directory: %v", err)
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			dirs = append(dirs, e.Name())
		}
	}
	if max := r.l.limits.Get(limits.AppPlugins); int64(len(dirs)) > max {
		r.report(plxerr.LimitExceeded, "plugins", "", "the app has %d plugins, above the limit app.plugins = %d", len(dirs), max)
	}
	for _, dir := range dirs {
		p.Plugins = append(p.Plugins, r.loadPlugin(path.Join("plugins", dir)))
	}
	if p.App.Doc == nil {
		return
	}
	for i, key := range p.App.Doc.Plugins {
		if !slices.Contains(dirs, key) {
			r.report(plxerr.InvalidProjectLayout, "app.json", plxerr.Pointer("plugins", strconv.Itoa(i)), "plugin %q has no directory plugins/%s/", key, key)
		}
	}
	for _, dir := range dirs {
		if !slices.Contains(p.App.Doc.Plugins, dir) {
			r.report(plxerr.InvalidProjectLayout, path.Join("plugins", dir, "plugin.json"), "", "plugin directory %q is not listed in app.json plugins", dir)
		}
	}
}

// loadPlugin loads one plugin directory.
func (r *run) loadPlugin(dir string) Plugin {
	pl := Plugin{Dir: dir, Loaded: load[PluginDocument](r, path.Join(dir, "plugin.json"), KindPlugin)}
	pl.Pages = loadAll[PageDocument](r, path.Join(dir, "pages"), ".page.json", KindPage)
	pl.Components = loadAll[ComponentDocument](r, path.Join(dir, "components"), ".component.json", KindComponent)
	pl.Graphs = loadAll[ActionGraphDocument](r, path.Join(dir, "actions"), ".graph.json", KindActionGraph)
	if max := r.l.limits.Get(limits.PluginPages); int64(len(pl.Pages)) > max {
		r.report(plxerr.LimitExceeded, path.Join(dir, "pages"), "", "the plugin has %d pages, above the limit plugin.pages = %d", len(pl.Pages), max)
	}
	if pl.Doc == nil {
		return pl
	}
	file := path.Join(dir, "plugin.json")
	if pl.Doc.Key != path.Base(dir) {
		r.report(plxerr.InvalidProjectLayout, file, "/key", "plugin key %q must match its directory %q", pl.Doc.Key, path.Base(dir))
	}
	found := map[string]bool{}
	for _, pg := range pl.Pages {
		if pg.Doc == nil {
			continue
		}
		found[pg.Doc.ID] = true
		if !slices.Contains(pl.Doc.Pages, pg.Doc.ID) {
			r.report(plxerr.InvalidProjectLayout, pg.Source.File, "/id", "page %q is not listed in %s pages", pg.Doc.Key, file)
		}
	}
	for i, id := range pl.Doc.Pages {
		if !found[id] && !hasInvalidPage(pl) {
			r.report(plxerr.UnresolvedReference, file, plxerr.Pointer("pages", strconv.Itoa(i)), "no page file has id %s", id)
		}
	}
	return pl
}

// hasInvalidPage reports whether a page file failed to decode, in which case
// unresolved page IDs are consequences, not separate problems.
func hasInvalidPage(pl Plugin) bool {
	return slices.ContainsFunc(pl.Pages, func(p Loaded[PageDocument]) bool { return p.Doc == nil })
}

// loadAssets reads every file the asset index lists.
func (r *run) loadAssets(p *Project) {
	if p.Assets == nil || p.Assets.Doc == nil {
		return
	}
	for i, a := range p.Assets.Doc.Assets {
		data, err := fs.ReadFile(r.fsys, path.Join("assets", a.File))
		if err != nil {
			r.report(plxerr.InvalidProjectLayout, "assets/index.json", plxerr.Pointer("assets", strconv.Itoa(i), "file"), "asset file assets/%s is missing", a.File)
			continue
		}
		p.AssetFiles[a.File] = data
	}
}

// checkTranslations verifies that each translation file is named after its
// locale.
func (r *run) checkTranslations(p *Project) {
	for _, t := range p.Translations {
		if t.Doc == nil {
			continue
		}
		if want := t.Doc.Locale + ".json"; path.Base(t.Source.File) != want {
			r.report(plxerr.InvalidProjectLayout, t.Source.File, "/locale", "translations for %q must be in translations/%s", t.Doc.Locale, want)
		}
	}
}
