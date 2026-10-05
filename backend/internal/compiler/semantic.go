// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"slices"
	"strconv"
	"strings"

	"github.com/nightCode42/plux3/backend/internal/bundle/fbs"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema"
)

// semantic checks the project against the descriptors and its own
// declarations (SCH-040): props, events, children and slots of every node;
// defaults, mocks and computed state; action graphs and their inputs;
// route parameters at every navigation; redirect loops; and the runtime
// version each member needs (WGT-004).
func semantic(u *unit) {
	u.checkDecls()
	for _, c := range u.shared {
		if u.inFocus(c.file) {
			u.checkComponent(c)
		}
	}
	for _, g := range u.appGraphs {
		if u.graphInFocus(g) {
			u.checkGraph(g)
		}
		g.navs = u.navigations(g)
	}
	if u.inFocus("app.json") {
		u.checkTriggers(u.appTriggers, nil)
	}
	for _, pl := range u.plugins {
		u.checkPlugin(pl)
	}
	u.checkFlowCycles()
	u.checkRedirects()
	u.checkGuards()
	u.finishGraph()
}

// checkPlugin checks a plugin's components, pages and graphs.
func (u *unit) checkPlugin(pl *plugin) {
	if u.inFocus(pl.file) {
		u.checkTriggers(pl.triggers, pl)
	}
	for _, c := range pl.components {
		if u.inFocus(c.file) {
			u.checkComponent(c)
		}
	}
	for _, pg := range pl.pages {
		if u.inFocus(pg.file) {
			u.checkPage(pg)
		}
	}
	// Every graph's navigations are needed for redirect loops, which can
	// pass through pages other than the one ValidatePage checks.
	for _, g := range slices.Concat(pl.graphs, pl.inline) {
		if u.graphInFocus(g) {
			u.checkGraph(g)
		}
		g.navs = u.navigations(g)
	}
}

// literalCtx is the context of a default or mock: literals only.
func literalCtx(pl *plugin, file, ptr string) vctx {
	return vctx{file: file, ptr: ptr, pl: pl, code: plxerr.ValueTypeMismatch}
}

// checkPage checks a page's declarations, nodes and handlers.
func (u *unit) checkPage(pg *page) {
	doc, pl, file := pg.doc, pg.plugin, pg.file
	if pg.scope == nil {
		return
	}
	for i, p := range doc.Params {
		ptr := plxerr.Pointer("params", strconv.Itoa(i))
		pg.params = append(pg.params, u.checkParam(pl, p, file, ptr, true))
	}
	pg.state = u.checkState(pl, doc.State, file, pg.scope)
	pg.sources = u.checkSources(pl, doc.DataSources, file, pg.scope)
	pg.forms = u.checkForms(pl, doc.Forms, doc.State, file)
	if len(doc.Title) > 0 {
		pg.title = u.checkRaw(vctx{file: file, ptr: "/title", scope: pg.scope, pl: pl, code: plxerr.PropTypeMismatch, from: doc.ID}, doc.Title, &texpr{name: "string"})
	}
	for _, n := range pg.nodes {
		u.checkNode(n)
	}
	u.checkAnimations(pg)
	for i, name := range []string{"onInit", "onEnter", "onResume", "onLeave", "onDispose"} {
		if g := pg.graphs[name]; g != nil {
			h := &handler{event: uint32(i), graph: g} //nolint:gosec // G115: five events.
			if eh := lifecycleHandler(doc.Lifecycle, name); eh != nil {
				c := vctx{file: file, ptr: plxerr.Pointer("lifecycle", name), pl: pl}
				u.policy(h, eh, fbs.ConcurrencyQueue, c)
				u.requireFeature(triggersFeature, c, false)
			}
			pg.lifecycle = append(pg.lifecycle, h)
		}
	}
	u.checkTriggers(pg.triggers, pl)
}

// lifecycleHandler returns a lifecycle event's handler.
func lifecycleHandler(lc *schema.Lifecycle, name string) *schema.EventHandler {
	if lc == nil {
		return nil
	}
	return map[string]*schema.EventHandler{"onInit": lc.OnInit, "onEnter": lc.OnEnter, "onResume": lc.OnResume, "onLeave": lc.OnLeave, "onDispose": lc.OnDispose}[name]
}

// checkComponent checks a component definition.
func (u *unit) checkComponent(c *component) {
	if c.root == nil || c.root.scope == nil {
		return
	}
	props := slices.Clone(c.doc.Props)
	slices.SortFunc(props, func(a, b schema.ComponentProp) int { return strings.Compare(a.Name, b.Name) })
	for _, p := range props {
		i := slices.IndexFunc(c.doc.Props, func(q schema.ComponentProp) bool { return q.Name == p.Name })
		ptr := plxerr.Pointer("props", strconv.Itoa(i))
		out := &param{name: p.Name, typ: p.Type, required: p.Required != nil && *p.Required}
		if te := u.checkType(c.plugin, p.Type, c.file, ptr+"/type"); te != nil && len(p.Default) > 0 {
			out.def = u.checkRaw(literalCtx(c.plugin, c.file, ptr+"/default"), p.Default, te)
		}
		c.props = append(c.props, out)
	}
	c.state = u.checkState(c.plugin, c.doc.State, c.file, c.root.scope)
	c.forms = u.checkForms(c.plugin, c.doc.Forms, c.doc.State, c.file)
	for _, n := range c.nodes {
		u.checkNode(n)
	}
}

// checkParam checks a parameter's default and mock; pages need a mock for
// each parameter (SCH-024).
func (u *unit) checkParam(pl *plugin, p schema.Param, file, ptr string, needMock bool) *param {
	out := &param{id: uuidBytes(p.ID), name: p.Name, typ: p.Type, required: p.Required != nil && *p.Required, sensitive: p.Sensitive != nil && *p.Sensitive}
	te := u.checkType(pl, p.Type, file, ptr+"/type")
	if te == nil {
		return out
	}
	if len(p.Default) > 0 {
		out.def = u.checkRaw(literalCtx(pl, file, ptr+"/default"), p.Default, te)
	}
	switch {
	case len(p.Mock) > 0:
		u.checkRaw(literalCtx(pl, file, ptr+"/mock"), p.Mock, te)
	case needMock:
		u.report(plxerr.MissingMock, file, ptr, "parameter %q has no design-time mock", p.Name)
	}
	return out
}

// persistence maps a document persistence to the bundle's.
var persistence = map[schema.Persistence]fbs.Persistence{
	"": fbs.PersistenceMemory, schema.PersistenceMemory: fbs.PersistenceMemory, schema.PersistenceSession: fbs.PersistenceSession,
	schema.PersistencePersisted: fbs.PersistencePersisted, schema.PersistenceSecure: fbs.PersistenceSecure,
}

// checkState checks state entries: defaults and computed expressions.
func (u *unit) checkState(pl *plugin, entries []schema.StateEntry, file string, s *scope) []*stateEntry {
	return u.checkEntries(pl, entries, file, s, false)
}

// checkEntries checks state entries; run is set for a graph's run
// variables (STA-001).
func (u *unit) checkEntries(pl *plugin, entries []schema.StateEntry, file string, s *scope, run bool) []*stateEntry {
	var out []*stateEntry
	for i, st := range entries {
		ptr := plxerr.Pointer("state", strconv.Itoa(i))
		e := &stateEntry{
			id: uuidBytes(st.ID), name: st.Name, typ: st.Type, persistence: persistence[st.Persistence],
			sensitive: st.Sensitive != nil && *st.Sensitive, exposed: st.Exposed != nil && *st.Exposed,
		}
		te, err := parseTypeExpr(st.Type)
		if err != nil {
			continue // reported by typecheck
		}
		switch {
		case st.Computed != nil:
			if len(st.Default) > 0 {
				u.report(plxerr.InvalidStructure, file, ptr+"/default", "a computed state entry has no default")
			}
			c := vctx{file: file, ptr: ptr + "/computed", scope: s, pl: pl, code: plxerr.ValueTypeMismatch}
			if v := u.exprValue(c, te); v != nil {
				e.computed = &expr{prog: v.prog}
			}
		case len(st.Default) > 0:
			e.def = u.checkRaw(literalCtx(pl, file, ptr+"/default"), st.Default, te)
		}
		if e.exposed && file != "app.json" {
			// Hosts address exposed entries by name, never by plugin
			// (ADR-0023).
			u.report(plxerr.InvalidStructure, file, ptr+"/exposed", "only app state entries can be exposed; %q belongs to a plugin, page or component", st.Name)
		}
		if e.sensitive && e.persistence == fbs.PersistencePersisted {
			u.report(plxerr.SensitiveValueExposed, file, ptr+"/persistence", "sensitive state %q must use secure persistence, not persisted", st.Name)
		}
		u.checkEntryStorage(pl, &entries[i], e, te, file, ptr, run)
		out = append(out, e)
	}
	return out
}

// dataSourceKinds maps a document data-source kind to the bundle's.
var dataSourceKinds = map[schema.DataSourceKind]fbs.DataSourceKind{
	schema.DataSourceKindRest: fbs.DataSourceKindRest, schema.DataSourceKindGraphql: fbs.DataSourceKindGraphql,
	schema.DataSourceKindWebsocket: fbs.DataSourceKindWebsocket, schema.DataSourceKindSSE: fbs.DataSourceKindSse,
	schema.DataSourceKindFunction: fbs.DataSourceKindFunction, schema.DataSourceKindDatabase: fbs.DataSourceKindDatabase,
	schema.DataSourceKindStatic: fbs.DataSourceKindStatic,
}

// checkSources checks data sources: the mock against the type, and the
// configuration as a value.
func (u *unit) checkSources(pl *plugin, sources []schema.DataSource, file string, sc *scope) []*dataSource {
	var out []*dataSource
	for i := range sources {
		s := &sources[i]
		ptr := plxerr.Pointer("dataSources", strconv.Itoa(i))
		d := &dataSource{id: uuidBytes(s.ID), name: s.Name, kind: dataSourceKinds[s.Kind], typ: s.Type}
		if te, err := parseTypeExpr(s.Type); err == nil {
			if len(s.Mock) == 0 {
				u.report(plxerr.MissingMock, file, ptr, "data source %q has no design-time mock", s.Name)
			} else {
				u.checkRaw(literalCtx(pl, file, ptr+"/mock"), s.Mock, te)
			}
		}
		switch {
		case runsData(s.Kind):
			d.config = u.checkDataSource(pl, s, file, ptr, sc)
		case len(s.Config) > 0:
			d.config = u.inferred(literalCtx(pl, file, ptr+"/config"), s.Config)
		}
		out = append(out, d)
	}
	return out
}

// checkDecls checks the app's and each plugin's declarations that the
// schemas section carries: state, data sources, collections, flags and
// environment values.
func (u *unit) checkDecls() {
	app := u.project.App.Doc
	if u.appScope != nil {
		u.appState = u.checkState(nil, app.State, "app.json", u.appScope)
	}
	u.appSources = u.checkSources(nil, app.DataSources, "app.json", u.appScope)
	for i, f := range app.Flags {
		ptr := plxerr.Pointer("flags", strconv.Itoa(i))
		u.checkRaw(literalCtx(nil, "app.json", ptr+"/default"), f.Default, &texpr{name: string(f.Type)})
	}
	vars := map[string]*texpr{}
	for _, v := range app.Variables {
		if te, err := parseTypeExpr(v.Type); err == nil {
			vars[v.Name] = te
		}
	}
	for i, env := range app.Environments {
		for _, name := range sortedKeys(env.Values) {
			ptr := plxerr.Pointer("environments", strconv.Itoa(i), "values", name)
			te, ok := vars[name]
			if !ok {
				u.report(plxerr.UnresolvedReference, "app.json", ptr, "no variable %q is declared", name)
				continue
			}
			u.checkRaw(literalCtx(nil, "app.json", ptr), env.Values[name], te)
		}
	}
	u.checkCollections(nil, app.Collections, "app.json")
	u.checkShellTabs()
	for _, pl := range u.plugins {
		if pl.scope != nil {
			pl.state = u.checkState(pl, pl.doc.State, pl.file, pl.scope)
		}
		pl.sources = u.checkSources(pl, pl.doc.DataSources, pl.file, pl.scope)
		u.checkSourceCount(pl)
		u.checkCollections(pl, pl.doc.Collections, pl.file)
	}
}

// checkCollections checks the fields, primary keys and indexes of local
// collections.
func (u *unit) checkCollections(pl *plugin, cols []schema.Collection, file string) {
	for i, c := range cols {
		ptr := plxerr.Pointer("collections", strconv.Itoa(i))
		fields := map[string]bool{}
		names := u.newKeys("field")
		for j, f := range c.Fields {
			fptr := ptr + plxerr.Pointer("fields", strconv.Itoa(j))
			names.claim(f.Name, file, fptr+"/name")
			u.checkType(pl, f.Type, file, fptr+"/type")
			fields[f.Name] = true
		}
		for j, k := range c.PrimaryKey {
			if !fields[k] {
				u.report(plxerr.UnresolvedReference, file, ptr+plxerr.Pointer("primaryKey", strconv.Itoa(j)), "collection %q has no field %q", c.Key, k)
			}
		}
		for j, idx := range c.Indexes {
			for k, f := range idx {
				if !fields[f] {
					u.report(plxerr.UnresolvedReference, file, ptr+plxerr.Pointer("indexes", strconv.Itoa(j), strconv.Itoa(k)), "collection %q has no field %q", c.Key, f)
				}
			}
		}
	}
}
