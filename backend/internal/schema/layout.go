// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package schema

import (
	"errors"
	"io/fs"
	"path"
	"regexp"
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

// Place is where a path sits in the Git layout (SCH-006).
type Place struct {
	// Kind is the document kind the path holds.
	Kind DocumentKind
	// Plugin is the plugin key for a path under plugins/<key>/, and ""
	// for an app-level document.
	Plugin string
	// Key is the key the file name carries, "" for fixed names such as
	// app.json.
	Key string
}

// PlaceOf reports what a path of the Git layout holds. It accepts only
// the document paths Load reads; asset files and anything else are not
// documents.
func PlaceOf(p string) (Place, bool) {
	if p != path.Clean(p) || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") {
		return Place{}, false
	}
	switch p {
	case "app.json":
		return Place{Kind: KindApp}, true
	case "theme.json":
		return Place{Kind: KindTheme}, true
	case "native-catalogue.json":
		return Place{Kind: KindNativeCatalogue}, true
	case "translations/keys.json":
		return Place{Kind: KindTranslationKeys}, true
	case "assets/index.json":
		return Place{Kind: KindAssetIndex}, true
	}
	parts := strings.Split(p, "/")
	switch {
	case len(parts) == 2 && parts[0] == "translations":
		return keyed(parts[1], ".json", Place{Kind: KindTranslations})
	case len(parts) == 2 && parts[0] == "components":
		return keyed(parts[1], ".component.json", Place{Kind: KindComponent})
	case len(parts) == 2 && parts[0] == "templates":
		return keyed(parts[1], ".template.json", Place{Kind: KindTemplate})
	case len(parts) == 3 && parts[0] == "plugins" && parts[2] == "plugin.json":
		return Place{Kind: KindPlugin, Plugin: parts[1]}, validKey(parts[1])
	case len(parts) == 4 && parts[0] == "plugins" && validKey(parts[1]):
		in := Place{Plugin: parts[1]}
		switch parts[2] {
		case "pages":
			in.Kind = KindPage
			return keyed(parts[3], ".page.json", in)
		case "components":
			in.Kind = KindComponent
			return keyed(parts[3], ".component.json", in)
		case "actions":
			in.Kind = KindActionGraph
			return keyed(parts[3], ".graph.json", in)
		}
	}
	return Place{}, false
}

// keyed reads the key from a file name with a suffix.
func keyed(name, suffix string, in Place) (Place, bool) {
	key, ok := strings.CutSuffix(name, suffix)
	if !ok || strings.Contains(key, ".") || key == "" {
		return Place{}, false
	}
	in.Key = key
	return in, in.Kind == KindTranslations || validKey(key)
}

// keyPattern is the form of a key (SCH-002), as common.schema.json
// defines it.
var keyPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

// ValidKey reports whether a string is a document key (SCH-002).
func ValidKey(s string) bool { return len(s) <= 64 && keyPattern.MatchString(s) }

// validKey is ValidKey.
func validKey(s string) bool { return ValidKey(s) }
