// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"crypto/sha256"
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema"
	"github.com/nightCode42/plux3/backend/internal/schema/registry"
	"github.com/nightCode42/plux3/backend/internal/schema/uuid7"
)

// resolve builds the index of the project: every entity by ID, route
// names, node trees, graphs and components; it reports duplicate IDs,
// keys and route names and unresolved references (SCH-002, SCH-025,
// SCH-040), and derives the reference graph (SCH-041).
func resolve(u *unit) {
	p := u.project
	u.natives = indexNatives(u, p)
	u.indexShared(p)
	for i := range p.Plugins {
		if pl := u.indexPlugin(&p.Plugins[i]); pl != nil {
			u.plugins = append(u.plugins, pl)
		}
	}
	slices.SortFunc(u.plugins, func(a, b *plugin) int { return strings.Compare(a.key, b.key) })
	u.types = newUniverse(u)
	for _, c := range u.shared {
		u.buildComponent(c)
	}
	for _, pl := range u.plugins {
		u.resolvePlugin(pl)
	}
	u.resolveApp()
	u.finishGraph()
}

// claimID records an entity ID and reports a second use (PLX-1011).
func (u *unit) claimID(id, file, ptr string) {
	if id == "" {
		return
	}
	loc := plxerr.Location{File: file, Path: ptr}
	if first, seen := u.ids[id]; seen {
		u.duplicate(plxerr.DuplicateID, loc, first, "identifier %s is already used", id)
		return
	}
	u.ids[id] = loc
}

// duplicate reports a second declaration at loc, relating it to the first.
// ValidatePage returns only the edited page's diagnostics, so when the
// first declaration is there and the second elsewhere, it is reported at
// the first.
func (u *unit) duplicate(code plxerr.Code, loc, first plxerr.Location, format string, args ...any) {
	if u.focus != "" && first.File == u.focus && loc.File != u.focus {
		loc, first = first, loc
	}
	u.diags = append(u.diags, plxerr.NewDiagnostic(code, loc, format, args...).WithRelated(first))
}

// keys reports duplicate keys or names within one parent (PLX-1101).
type keys struct {
	u    *unit
	what string
	seen map[string]plxerr.Location
}

func (u *unit) newKeys(what string) *keys {
	return &keys{u: u, what: what, seen: map[string]plxerr.Location{}}
}

func (k *keys) claim(key, file, ptr string) {
	loc := plxerr.Location{File: file, Path: ptr}
	if first, seen := k.seen[key]; seen {
		k.u.duplicate(plxerr.DuplicateKey, loc, first, "%s %q is declared twice", k.what, key)
		return
	}
	k.seen[key] = loc
}

// uuidBytes returns the 16 bytes of a UUID; the structural stage has
// checked the format, so a malformed one yields zero bytes.
func uuidBytes(s string) [16]byte {
	id, err := uuid7.Parse(s)
	if err != nil {
		return [16]byte{}
	}
	return id
}

// derivedID returns a stable UUID (version 8) for an entity without one of
// its own, such as an inline graph, from the parts that identify it.
func derivedID(parts ...string) [16]byte {
	sum := sha256.Sum256([]byte("plux.derived\x00" + strings.Join(parts, "\x00")))
	var id [16]byte
	copy(id[:], sum[:16])
	id[6] = id[6]&0x0f | 0x80
	id[8] = id[8]&0x3f | 0x80
	return id
}

// indexNatives indexes the native catalogue (SCH-032).
func indexNatives(u *unit, p *schema.Project) natives {
	n := natives{routes: map[string]*schema.NativeRoute{}, actions: map[string]*schema.NativeAction{}, slots: map[string]*schema.NativeSlot{}}
	if p.NativeCatalogue == nil || p.NativeCatalogue.Doc == nil {
		return n
	}
	doc, file := p.NativeCatalogue.Doc, p.NativeCatalogue.Source.File
	u.claimID(doc.ID, file, "/id")
	actions := u.newKeys("native action")
	for i := range doc.Routes {
		n.routes[doc.Routes[i].Name] = &doc.Routes[i]
	}
	for i := range doc.Actions {
		name, ptr := doc.Actions[i].Name, plxerr.Pointer("actions", strconv.Itoa(i), "name")
		actions.claim(name, file, ptr)
		// A step of a built-in action's name runs the built-in, whatever
		// the host registers under it.
		if _, builtIn := registry.LookupAction(name); builtIn {
			u.report(plxerr.CustomActionNamedLikeBuiltIn, file, ptr, "the custom action %q has the name of a built-in action, which a step of that name runs instead", name)
		}
		n.actions[name] = &doc.Actions[i]
	}
	slots := u.newKeys("native slot")
	for i := range doc.Slots {
		slots.claim(doc.Slots[i].Type, file, plxerr.Pointer("slots", strconv.Itoa(i), "type"))
		n.slots[doc.Slots[i].Type] = &doc.Slots[i]
	}
	return n
}

// indexShared indexes the app-level documents.
func (u *unit) indexShared(p *schema.Project) {
	app := p.App.Doc
	u.claimID(app.ID, "app.json", "/id")
	envs := u.newKeys("environment")
	for i, e := range app.Environments {
		envs.claim(e.Key, "app.json", plxerr.Pointer("environments", strconv.Itoa(i), "key"))
	}
	if p.Theme.Doc != nil {
		u.claimID(p.Theme.Doc.ID, p.Theme.Source.File, "/id")
		if p.Theme.Doc.ID != app.Theme {
			u.report(plxerr.UnresolvedReference, "app.json", "/theme", "the theme is %s, not %s", p.Theme.Doc.ID, app.Theme)
		}
		u.indexTokens(p.Theme.Doc.Tokens, "", "")
	}
	if nc := p.NativeCatalogue; app.NativeCatalogue != "" && (nc == nil || nc.Doc == nil || nc.Doc.ID != app.NativeCatalogue) {
		u.report(plxerr.UnresolvedReference, "app.json", "/nativeCatalogue", "no native catalogue with ID %s", app.NativeCatalogue)
	}
	u.indexTranslations(p)
	u.indexAssets(p)
	for _, t := range p.Templates {
		if t.Doc != nil {
			u.claimID(t.Doc.ID, t.Source.File, "/id")
		}
	}
	names := u.newKeys("component")
	for _, c := range p.Components {
		if c.Doc == nil {
			continue
		}
		names.claim(c.Doc.Key, c.Source.File, "/key")
		comp := &component{doc: c.Doc, file: c.Source.File}
		u.addComponent(comp)
		u.shared = append(u.shared, comp)
	}
	u.indexAppDecls(app)
}

// indexTranslations indexes the translation keys.
func (u *unit) indexTranslations(p *schema.Project) {
	if tk := p.TranslationKeys; tk != nil && tk.Doc != nil {
		u.claimID(tk.Doc.ID, tk.Source.File, "/id")
		names := u.newKeys("translation key")
		for i := range tk.Doc.Keys {
			k := &tk.Doc.Keys[i]
			ptr := plxerr.Pointer("keys", strconv.Itoa(i))
			u.claimID(k.ID, tk.Source.File, ptr+"/id")
			names.claim(k.Key, tk.Source.File, ptr+"/key")
			u.tkeys[k.ID] = k
		}
	}
	for _, t := range p.Translations {
		if t.Doc != nil {
			u.claimID(t.Doc.ID, t.Source.File, "/id")
		}
	}
}

// indexAssets indexes the asset index.
func (u *unit) indexAssets(p *schema.Project) {
	a := p.Assets
	if a == nil || a.Doc == nil {
		return
	}
	u.claimID(a.Doc.ID, a.Source.File, "/id")
	names := u.newKeys("asset")
	for i := range a.Doc.Assets {
		e := &a.Doc.Assets[i]
		ptr := plxerr.Pointer("assets", strconv.Itoa(i))
		u.claimID(e.ID, a.Source.File, ptr+"/id")
		names.claim(e.Key, a.Source.File, ptr+"/key")
		u.assetIDs[e.ID] = e
	}
}

// indexAppDecls claims the IDs and names the app declares.
func (u *unit) indexAppDecls(app *schema.AppDocument) {
	state := u.newKeys("state entry")
	for i, s := range app.State {
		ptr := plxerr.Pointer("state", strconv.Itoa(i))
		u.claimID(s.ID, "app.json", ptr+"/id")
		state.claim(s.Name, "app.json", ptr+"/name")
	}
	sources := u.newKeys("data source")
	for i, s := range app.DataSources {
		ptr := plxerr.Pointer("dataSources", strconv.Itoa(i))
		u.claimID(s.ID, "app.json", ptr+"/id")
		sources.claim(s.Name, "app.json", ptr+"/name")
	}
	collections := u.newKeys("collection")
	for i, c := range app.Collections {
		ptr := plxerr.Pointer("collections", strconv.Itoa(i))
		u.claimID(c.ID, "app.json", ptr+"/id")
		collections.claim(c.Key, "app.json", ptr+"/key")
	}
	flags := u.newKeys("flag")
	for i, f := range app.Flags {
		flags.claim(f.Name, "app.json", plxerr.Pointer("flags", strconv.Itoa(i), "name"))
	}
	vars := u.newKeys("variable")
	for i, v := range app.Variables {
		vars.claim(v.Name, "app.json", plxerr.Pointer("variables", strconv.Itoa(i), "name"))
	}
}

// addComponent indexes a component by ID.
func (u *unit) addComponent(c *component) {
	u.claimID(c.doc.ID, c.file, "/id")
	u.components[c.doc.ID] = c
}

// indexTokens flattens a W3C design-token tree; $type is inherited from
// groups (THM-001).
func (u *unit) indexTokens(raw json.RawMessage, path, inherited string) {
	var group map[string]json.RawMessage
	if json.Unmarshal(raw, &group) != nil {
		return
	}
	typ := inherited
	if t, ok := group["$type"]; ok {
		_ = json.Unmarshal(t, &typ)
	}
	if v, ok := group["$value"]; ok {
		tok := &token{path: path, typ: typ, light: v}
		var ext map[string]json.RawMessage
		if json.Unmarshal(group["$extensions"], &ext) == nil {
			tok.dark = ext["dev.plux.dark"]
		}
		u.tokens[path] = tok
		return
	}
	for _, name := range sortedKeys(group) {
		if strings.HasPrefix(name, "$") {
			continue
		}
		child := name
		if path != "" {
			child = path + "." + name
		}
		u.indexTokens(group[name], child, typ)
	}
}

// indexPlugin indexes a plugin directory.
func (u *unit) indexPlugin(lp *schema.Plugin) *plugin {
	if lp.Doc == nil {
		return nil
	}
	pl := &plugin{doc: lp.Doc, file: lp.Source.File, key: lp.Doc.Key, functions: map[string]bool{}}
	u.claimID(pl.doc.ID, pl.file, "/id")
	byID := map[string]*page{}
	pageKeys := u.newKeys("page")
	for _, lpg := range lp.Pages {
		if lpg.Doc == nil {
			continue
		}
		pg := &page{doc: lpg.Doc, file: lpg.Source.File, plugin: pl, route: lpg.Doc.Route}
		if pg.route == "" {
			pg.route = pg.doc.Key
		}
		pageKeys.claim(pg.doc.Key, pg.file, "/key")
		u.claimID(pg.doc.ID, pg.file, "/id")
		u.pages[pg.doc.ID] = pg
		byID[pg.doc.ID] = pg
	}
	for i, id := range pl.doc.Pages {
		if pg, ok := byID[id]; ok {
			pl.pages = append(pl.pages, pg)
			delete(byID, id)
			continue
		}
		u.report(plxerr.UnresolvedReference, pl.file, plxerr.Pointer("pages", strconv.Itoa(i)), "plugin %q has no page %s", pl.key, id)
	}
	for _, pg := range sortedPages(byID) {
		u.report(plxerr.InvalidProjectLayout, pg.file, "/id", "page %s is not listed in %s", pg.doc.ID, pl.file)
		pl.pages = append(pl.pages, pg)
	}
	compKeys := u.newKeys("component")
	for _, c := range lp.Components {
		if c.Doc == nil {
			continue
		}
		compKeys.claim(c.Doc.Key, c.Source.File, "/key")
		comp := &component{doc: c.Doc, file: c.Source.File, plugin: pl}
		u.addComponent(comp)
		pl.components = append(pl.components, comp)
	}
	graphKeys := u.newKeys("action graph")
	for _, g := range lp.Graphs {
		if g.Doc == nil {
			continue
		}
		graphKeys.claim(g.Doc.Key, g.Source.File, "/key")
		gr := &graph{id: uuidBytes(g.Doc.ID), key: g.Doc.Key, file: g.Source.File, doc: g.Doc, steps: g.Doc.Steps, plugin: pl, output: g.Doc.Output}
		u.claimID(g.Doc.ID, gr.file, "/id")
		u.graphs[g.Doc.ID] = gr
		pl.graphs = append(pl.graphs, gr)
	}
	u.indexPluginDecls(pl)
	return pl
}

// sortedPages returns pages by file.
func sortedPages(m map[string]*page) []*page {
	out := make([]*page, 0, len(m))
	for _, p := range m {
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b *page) int { return strings.Compare(a.file, b.file) })
	return out
}

// indexPluginDecls claims the IDs and names a plugin declares.
func (u *unit) indexPluginDecls(pl *plugin) {
	state := u.newKeys("state entry")
	for i, s := range pl.doc.State {
		ptr := plxerr.Pointer("state", strconv.Itoa(i))
		u.claimID(s.ID, pl.file, ptr+"/id")
		state.claim(s.Name, pl.file, ptr+"/name")
	}
	sources := u.newKeys("data source")
	for i, s := range u.project.App.Doc.DataSources {
		sources.seen[s.Name] = plxerr.Location{File: "app.json", Path: plxerr.Pointer("dataSources", strconv.Itoa(i), "name")}
	}
	for i, s := range pl.doc.DataSources {
		ptr := plxerr.Pointer("dataSources", strconv.Itoa(i))
		u.claimID(s.ID, pl.file, ptr+"/id")
		sources.claim(s.Name, pl.file, ptr+"/name")
	}
	collections := u.newKeys("collection")
	for i, c := range pl.doc.Collections {
		ptr := plxerr.Pointer("collections", strconv.Itoa(i))
		u.claimID(c.ID, pl.file, ptr+"/id")
		collections.claim(c.Key, pl.file, ptr+"/key")
	}
	if caps := pl.doc.Capabilities; caps != nil {
		for i, f := range caps.Functions {
			u.claimID(f.ID, pl.file, plxerr.Pointer("capabilities", "functions", strconv.Itoa(i), "id"))
			pl.functions[f.Function] = true
			if f.Alias != "" {
				pl.functions[f.Alias] = true
			}
		}
	}
}

// resolvePlugin resolves the references of a plugin and builds its trees.
func (u *unit) resolvePlugin(pl *plugin) {
	doc := pl.doc
	u.pageRef(pl, doc.EntryPage, pl.file, "/entryPage")
	if doc.FallbackPage != "" {
		u.pageRef(pl, doc.FallbackPage, pl.file, "/fallbackPage")
	}
	u.assetRefIcon(doc.Icon.Asset, pl.doc.ID, pl.file, "/icon/$asset")
	for _, c := range pl.components {
		u.buildComponent(c)
	}
	for _, pg := range pl.pages {
		u.buildPage(pg)
	}
	for _, g := range pl.graphs {
		if g.doc.Page != "" {
			pg, ok := u.pages[g.doc.Page]
			if !ok || pg.plugin != pl {
				u.report(plxerr.UnresolvedReference, g.file, "/page", "the plugin has no page %s", g.doc.Page)
				continue
			}
			g.page = pg
		}
	}
	for _, g := range pl.graphs {
		u.resolveGraphRefs(g)
	}
	pl.triggers = u.resolveTriggers(doc.Triggers, pl, nil, doc.ID, pl.file)
}

// pageRef checks that id is a page of pl.
func (u *unit) pageRef(pl *plugin, id, file, ptr string) {
	if pg, ok := u.pages[id]; !ok || pg.plugin != pl {
		u.report(plxerr.UnresolvedReference, file, ptr, "plugin %q has no page %s", pl.key, id)
	}
}

// assetRefIcon checks an icon's asset reference.
func (u *unit) assetRefIcon(id, from, file, ptr string) {
	if id == "" {
		return
	}
	if _, ok := u.assetIDs[id]; !ok {
		u.report(plxerr.UnresolvedReference, file, ptr, "no asset %s in assets/index.json", id)
		return
	}
	u.graph.add(Edge{From: from, Kind: EdgeUsesAsset, To: id, File: file, Path: ptr})
}

// resolveApp checks the app-level references and route names.
func (u *unit) resolveApp() {
	app := u.project.App.Doc
	u.assetRefIcon(app.Icon.Asset, app.ID, "app.json", "/icon/$asset")
	names := map[string]*route{}
	for _, name := range sortedKeys(u.natives.routes) {
		names[name] = &route{name: name, native: u.natives.routes[name], file: u.project.NativeCatalogue.Source.File}
	}
	for _, pl := range u.plugins {
		for _, pg := range pl.pages {
			r := &route{name: pg.route, page: pg, file: pg.file, ptr: "/route"}
			if first, dup := names[pg.route]; dup {
				u.duplicate(plxerr.DuplicateRouteName, plxerr.Location{File: pg.file, Path: "/route"}, plxerr.Location{File: first.file, Path: first.ptr},
					"route name %q is already used", pg.route)
				continue
			}
			names[pg.route] = r
		}
	}
	u.routes = names
	u.checkExportedNames(names)
	if _, ok := names[app.EntryRoute]; !ok {
		u.report(plxerr.UnknownRoute, "app.json", "/entryRoute", "no page or native route is named %q", app.EntryRoute)
	}
	u.resolveNavigation()
	u.resolveHostEvents()
	u.appTriggers = u.resolveTriggers(app.Triggers, nil, nil, app.ID, "app.json")
}

// checkExportedNames checks that each exported component's key names it
// alone, since PluxView shows a route or an exported component by name
// (NAV-004, ADR-0023): unique across the app's exported components and
// different from every route name.
func (u *unit) checkExportedNames(routes map[string]*route) {
	exported := map[string]*component{}
	for _, pl := range u.plugins {
		for _, c := range pl.components {
			if c.doc.Exported == nil || !*c.doc.Exported {
				continue
			}
			loc := plxerr.Location{File: c.file, Path: "/key"}
			if r, clash := routes[c.doc.Key]; clash {
				u.duplicate(plxerr.DuplicateRouteName, loc, plxerr.Location{File: r.file, Path: r.ptr},
					"exported component %q has the name of a route; PluxView shows either by name", c.doc.Key)
				continue
			}
			if first, dup := exported[c.doc.Key]; dup {
				u.duplicate(plxerr.DuplicateKey, loc, plxerr.Location{File: first.file, Path: "/key"},
					"exported component key %q is already used by another plugin", c.doc.Key)
				continue
			}
			exported[c.doc.Key] = c
		}
	}
}

// buildPage builds a page's node tree and indexes its declarations.
func (u *unit) buildPage(pg *page) {
	doc := pg.doc
	params := u.newKeys("parameter")
	for i, p := range doc.Params {
		ptr := plxerr.Pointer("params", strconv.Itoa(i))
		u.claimID(p.ID, pg.file, ptr+"/id")
		params.claim(p.Name, pg.file, ptr+"/name")
	}
	state := u.newKeys("state entry")
	for i, s := range doc.State {
		ptr := plxerr.Pointer("state", strconv.Itoa(i))
		u.claimID(s.ID, pg.file, ptr+"/id")
		state.claim(s.Name, pg.file, ptr+"/name")
	}
	sources := u.newKeys("data source")
	for i, s := range u.project.App.Doc.DataSources {
		sources.seen[s.Name] = plxerr.Location{File: "app.json", Path: plxerr.Pointer("dataSources", strconv.Itoa(i), "name")}
	}
	for i, s := range pg.plugin.doc.DataSources {
		sources.seen[s.Name] = plxerr.Location{File: pg.plugin.file, Path: plxerr.Pointer("dataSources", strconv.Itoa(i), "name")}
	}
	for i, s := range doc.DataSources {
		ptr := plxerr.Pointer("dataSources", strconv.Itoa(i))
		u.claimID(s.ID, pg.file, ptr+"/id")
		sources.claim(s.Name, pg.file, ptr+"/name")
	}
	o := owner{page: pg}
	if u.inFocus(pg.file) {
		pg.root = u.buildNode(o, &doc.Root, nil, "", "/root", &pg.nodes)
	} else {
		// ValidatePage checks another page: this one's node IDs are
		// claimed, so duplicates are found, but its tree is not built.
		u.claimNodeIDs(&doc.Root, pg.file, "/root")
	}
	u.lifecycleGraphs(pg, o)
	pg.triggers = u.resolveTriggers(doc.Triggers, pg.plugin, pg, doc.ID, pg.file)
	u.scanRefs(doc.ID, pg.file, "/title", doc.Title)
	if doc.RouteOptions != nil {
		for i, g := range doc.RouteOptions.Guards {
			ptr := plxerr.Pointer("routeOptions", "guards", strconv.Itoa(i), "$graph")
			if gr := u.graphRef(pg.plugin, pg, g.Graph, pg.doc.ID, pg.file, ptr); gr != nil {
				pg.guards = append(pg.guards, gr)
			}
		}
	}
}

// lifecycleGraphs resolves the handlers of a page's lifecycle events.
func (u *unit) lifecycleGraphs(pg *page, o owner) {
	pg.graphs = map[string]*graph{}
	lc := pg.doc.Lifecycle
	if lc == nil {
		return
	}
	for _, h := range []struct {
		name string
		eh   *schema.EventHandler
	}{{"onInit", lc.OnInit}, {"onEnter", lc.OnEnter}, {"onResume", lc.OnResume}, {"onLeave", lc.OnLeave}, {"onDispose", lc.OnDispose}} {
		if h.eh == nil {
			continue
		}
		if g := u.handlerGraph(o, h.eh, pg.doc.ID, h.name, pg.file, plxerr.Pointer("lifecycle", h.name)); g != nil {
			pg.graphs[h.name] = g
		}
	}
}

// claimNodeIDs claims the IDs of a node tree without building it.
func (u *unit) claimNodeIDs(doc *schema.Node, file, ptr string) {
	u.claimID(doc.ID, file, ptr+"/id")
	for i := range doc.Children {
		u.claimNodeIDs(&doc.Children[i], file, ptr+"/children/"+strconv.Itoa(i))
	}
	for _, name := range sortedKeys(doc.Slots) {
		fill, sp := doc.Slots[name], ptr+plxerr.Pointer("slots", name)
		if fill.One != nil {
			u.claimNodeIDs(fill.One, file, sp)
		}
		for i := range fill.Many {
			u.claimNodeIDs(&fill.Many[i], file, sp+"/"+strconv.Itoa(i))
		}
	}
}

// buildComponent builds a component's node tree.
func (u *unit) buildComponent(c *component) {
	state := u.newKeys("state entry")
	for i, s := range c.doc.State {
		ptr := plxerr.Pointer("state", strconv.Itoa(i))
		u.claimID(s.ID, c.file, ptr+"/id")
		state.claim(s.Name, c.file, ptr+"/name")
	}
	props := u.newKeys("prop")
	for i, p := range c.doc.Props {
		props.claim(p.Name, c.file, plxerr.Pointer("props", strconv.Itoa(i), "name"))
	}
	slots := u.newKeys("slot")
	for i, s := range c.doc.Slots {
		slots.claim(s.Name, c.file, plxerr.Pointer("slots", strconv.Itoa(i), "name"))
	}
	c.root = u.buildNode(owner{component: c}, &c.doc.Root, nil, "", "/root", &c.nodes)
}

// buildNode builds a node and its subtree, appending it to all in
// pre-order.
func (u *unit) buildNode(o owner, doc *schema.Node, parent *node, slot, ptr string, all *[]*node) *node {
	n := &node{doc: doc, owner: o, ptr: ptr, parent: parent, slot: slot}
	*all = append(*all, n)
	file := o.file()
	u.claimID(doc.ID, file, ptr+"/id")
	from := ownerID(o)
	switch {
	case doc.Type != "":
		if w, ok := registry.LookupWidget(doc.Type); ok {
			n.widget = &w
		} else if _, native := u.natives.slots[doc.Type]; native {
			u.graph.add(Edge{From: from, Kind: EdgeUsesSlot, To: doc.Type, File: file, Path: ptr + "/type"})
		} else {
			u.report(plxerr.UnknownWidgetType, file, ptr+"/type", "no widget or native slot is named %q", doc.Type)
		}
	case doc.Component != nil:
		n.target = u.componentRef(o, doc.Component, file, ptr+"/component")
		if n.target != nil {
			u.graph.add(Edge{From: from, Kind: EdgeUsesComponent, To: n.target.doc.ID, File: file, Path: ptr + "/component"})
		}
	}
	for i := range doc.Children {
		n.children = append(n.children, u.buildNode(o, &doc.Children[i], n, "", ptr+"/children/"+strconv.Itoa(i), all))
	}
	for _, name := range sortedKeys(doc.Slots) {
		fill := doc.Slots[name]
		sf := &slotFill{name: name, ptr: ptr + plxerr.Pointer("slots", name)}
		if fill.One != nil {
			sf.nodes = append(sf.nodes, u.buildNode(o, fill.One, n, name, sf.ptr, all))
		}
		for i := range fill.Many {
			sf.nodes = append(sf.nodes, u.buildNode(o, &fill.Many[i], n, name, sf.ptr+"/"+strconv.Itoa(i), all))
		}
		n.slots = append(n.slots, sf)
	}
	u.scanNodeRefs(doc, from, file, ptr)
	n.graphs = map[string]*graph{}
	for _, name := range sortedKeys(doc.Events) {
		eh := doc.Events[name]
		if g := u.handlerGraph(o, &eh, doc.ID, name, file, ptr+plxerr.Pointer("events", name)); g != nil {
			n.graphs[name] = g
		}
	}
	return n
}

// scanNodeRefs resolves the references in a node's values.
func (u *unit) scanNodeRefs(doc *schema.Node, from, file, ptr string) {
	for _, name := range sortedKeys(doc.Props) {
		u.scanRefs(from, file, ptr+plxerr.Pointer("props", name), doc.Props[name])
	}
	if sem := doc.Semantics; sem != nil {
		u.scanRefs(from, file, ptr+"/semantics/label", sem.Label)
		u.scanRefs(from, file, ptr+"/semantics/hint", sem.Hint)
		u.scanRefs(from, file, ptr+"/semantics/value", sem.Value)
	}
	if r := doc.Responsive; r != nil {
		for _, name := range sortedKeys(r.Medium) {
			u.scanRefs(from, file, ptr+plxerr.Pointer("responsive", "medium", name), r.Medium[name])
		}
		for _, name := range sortedKeys(r.Expanded) {
			u.scanRefs(from, file, ptr+plxerr.Pointer("responsive", "expanded", name), r.Expanded[name])
		}
	}
	u.scanRefs(from, file, ptr+"/visible", doc.Visible)
}

// handlerGraph resolves an event or lifecycle handler to its graph: a
// referenced graph, or an inline one, which gets an ID derived from its
// anchor (a node or page) and event (BND-014).
func (u *unit) handlerGraph(o owner, eh *schema.EventHandler, anchor, event, file, ptr string) *graph {
	pl := o.plugin()
	if eh.Graph != "" {
		if pl == nil {
			u.report(plxerr.UnresolvedReference, file, ptr+"/$graph", "a shared component can only use inline action graphs")
			return nil
		}
		return u.graphRef(pl, o.page, eh.Graph, ownerID(o), file, ptr+"/$graph")
	}
	g := &graph{id: derivedID("graph", anchor, event), key: event, file: file, ptr: ptr, steps: eh.Steps, plugin: pl, page: o.page, component: o.component, inline: true, used: true}
	for i := range g.steps {
		for _, name := range sortedKeys(g.steps[i].Input) {
			u.scanRefs(ownerID(o), file, ptr+plxerr.Pointer("steps", strconv.Itoa(i), "input", name), g.steps[i].Input[name])
		}
	}
	if pl != nil {
		pl.inline = append(pl.inline, g)
	} else {
		u.appGraphs = append(u.appGraphs, g)
	}
	return g
}

// ownerID returns the ID of a node owner.
func ownerID(o owner) string {
	if o.page != nil {
		return o.page.doc.ID
	}
	return o.component.doc.ID
}

// componentRef resolves an instance's component: a shared component, one
// of the owner's plugin, or an exported one of another plugin (SCH-030).
func (u *unit) componentRef(o owner, ref *schema.ComponentRef, file, ptr string) *component {
	c, ok := u.components[ref.ID]
	if !ok {
		u.report(plxerr.UnresolvedReference, file, ptr+"/id", "no component %s", ref.ID)
		return nil
	}
	if c.plugin != nil && c.plugin != o.plugin() && (c.doc.Exported == nil || !*c.doc.Exported) {
		u.report(plxerr.UnresolvedReference, file, ptr+"/id", "component %q is private to plugin %q", c.doc.Key, c.plugin.key)
		return nil
	}
	if c.plugin != nil && o.plugin() == nil {
		u.report(plxerr.UnresolvedReference, file, ptr+"/id", "a shared component cannot use component %q of plugin %q", c.doc.Key, c.plugin.key)
		return nil
	}
	if c.doc.Version != ref.Version {
		u.report(plxerr.ComponentVersionNotFound, file, ptr+"/version", "component %q is at version %d, not %d", c.doc.Key, c.doc.Version, ref.Version)
		return nil
	}
	return c
}

// graphRef resolves a handler's graph: a flow of the plugin, or a graph
// scoped to the page.
func (u *unit) graphRef(pl *plugin, pg *page, id, from, file, ptr string) *graph {
	g, ok := u.graphs[id]
	switch {
	case !ok || pl == nil || g.plugin != pl:
		u.report(plxerr.UnresolvedReference, file, ptr, "the plugin has no action graph %s", id)
		return nil
	case g.doc.Page != "" && (pg == nil || g.doc.Page != pg.doc.ID):
		u.report(plxerr.UnresolvedReference, file, ptr, "action graph %q belongs to another page", g.key)
		return nil
	}
	g.used = true
	u.graph.add(Edge{From: from, Kind: EdgeUsesGraph, To: id, File: file, Path: ptr})
	return g
}

// resolveGraphRefs scans the step inputs of a document graph.
func (u *unit) resolveGraphRefs(g *graph) {
	for i := range g.steps {
		for _, name := range sortedKeys(g.steps[i].Input) {
			u.scanRefs(g.doc.ID, g.file, plxerr.Pointer("steps", strconv.Itoa(i), "input", name), g.steps[i].Input[name])
		}
	}
}

// scanRefs resolves the $t, $token and $asset references anywhere in a
// raw value and records them in the reference graph.
func (u *unit) scanRefs(from, file, ptr string, raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return
	}
	u.scanValue(from, file, ptr, v)
}

func (u *unit) scanValue(from, file, ptr string, v any) {
	switch x := v.(type) {
	case []any:
		for i, item := range x {
			u.scanValue(from, file, ptr+"/"+strconv.Itoa(i), item)
		}
	case map[string]any:
		if id, ok := x["$t"].(string); ok {
			u.translationRef(from, file, ptr+"/$t", id)
			if args, ok := x["args"].(map[string]any); ok {
				u.scanValue(from, file, ptr+"/args", args)
			}
			return
		}
		if path, ok := x["$token"].(string); ok {
			if _, found := u.tokens[path]; !found {
				u.report(plxerr.UnresolvedReference, file, ptr+"/$token", "the theme has no token %q", path)
				return
			}
			u.graph.add(Edge{From: from, Kind: EdgeUsesToken, To: path, File: file, Path: ptr + "/$token"})
			return
		}
		if id, ok := x["$asset"].(string); ok {
			u.assetRefIcon(id, from, file, ptr+"/$asset")
			return
		}
		if _, ok := x["$expr"]; ok {
			return
		}
		for _, k := range sortedKeys(x) {
			u.scanValue(from, file, ptr+plxerr.Pointer(k), x[k])
		}
	}
}

// translationRef resolves a translation key.
func (u *unit) translationRef(from, file, ptr, id string) {
	if _, ok := u.tkeys[id]; !ok {
		u.report(plxerr.UnresolvedReference, file, ptr, "no translation key %s in translations/keys.json", id)
		return
	}
	u.graph.add(Edge{From: from, Kind: EdgeUsesTranslation, To: id, File: file, Path: ptr})
}

// sortedKeys returns the keys of m in order, so iteration is
// deterministic (CMP-002).
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// uuidString formats 16 bytes as a UUID.
func uuidString(id [16]byte) string { return uuid7.UUID(id).String() }
