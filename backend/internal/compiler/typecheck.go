// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/pxl"
	"github.com/nightCode42/plux3/backend/internal/schema"
	"github.com/nightCode42/plux3/backend/internal/schema/registry"
)

// typecheck compiles every PXL expression against the environment of its
// use site (PXL-002, Appendix E.2): the scope roots of its page or
// component, `item` and `index` in item templates, `event` in handlers
// and `steps` in action graphs. It binds the type parameters of widgets
// from their expressions and records the state paths expressions read in
// the reference graph (CMP-023, SCH-041).
func typecheck(u *unit) {
	tc := &typer{u: u, opts: pxl.OptionsFrom(u.opts.Limits)}
	tc.appDecls()
	u.appScope = tc.baseScope(nil)
	tc.computed(nil, u.project.App.Doc.State, "app.json", u.appScope)
	tc.compileDataSources(nil, u.project.App.Doc.DataSources, "app.json", u.appScope, u.project.App.Doc.ID)
	tc.shellTabs()
	for _, c := range u.shared {
		if u.inFocus(c.file) {
			tc.component(c, u.appScope)
		}
	}
	for _, pl := range u.plugins {
		tc.plugin(pl)
	}
	u.finishGraph()
}

// shellTabs compiles the expressions of the tabs' labels and icons over
// the app scope (NAV-006); checkShellTabs checks their types.
func (t *typer) shellTabs() {
	app := t.u.project.App.Doc
	if app.Navigation == nil {
		return
	}
	for i, sh := range app.Navigation.Shells {
		for j, tab := range sh.Tabs {
			ptr := plxerr.Pointer("navigation", "shells", strconv.Itoa(i), "tabs", strconv.Itoa(j))
			t.compileAll(tab.Label, t.u.appScope, app.ID, "app.json", ptr+"/label")
			t.compileAll(tab.Icon, t.u.appScope, app.ID, "app.json", ptr+"/icon")
		}
	}
}

// plugin type-checks a plugin's components, pages, flows and page graphs.
func (t *typer) plugin(pl *plugin) {
	u := t.u
	base := t.baseScope(pl)
	pl.scope = base
	t.computed(pl, pl.doc.State, pl.file, base)
	t.compileDataSources(pl, pl.doc.DataSources, pl.file, base, pl.doc.ID)
	for _, c := range pl.components {
		if u.inFocus(c.file) {
			t.component(c, base)
		}
	}
	for _, pg := range pl.pages {
		if u.inFocus(pg.file) {
			t.page(pg, base)
		}
	}
	for _, g := range pl.graphs {
		if g.page == nil && u.inFocus(g.file) {
			t.flow(g, base)
		}
	}
	for _, g := range pl.graphs {
		if g.page != nil && u.graphInFocus(g) {
			t.graph(g, t.pageScope(g.page, base))
		}
	}
}

// typer holds the typecheck state.
type typer struct {
	u    *unit
	opts pxl.Options
	// The app-level roots, checked once.
	appState, flags, vars, user [][2]string
	appIDs                      map[string]string
	appSources                  []sourceField
}

// sourceField is a data source visible as data.<name>.
type sourceField struct {
	name, id, typ string
}

// userAuthenticated is the built-in member of the user root that says
// whether the host's auth delegate has a signed-in user (HST-010, NAV-009).
const userAuthenticated = "authenticated"

// appDecls checks the app-level declarations that become roots.
func (t *typer) appDecls() {
	app := t.u.project.App.Doc
	t.appState, t.appIDs = t.stateFields(nil, app.State, "app.json")
	for _, f := range app.Flags {
		t.flags = append(t.flags, [2]string{f.Name, string(f.Type)})
	}
	t.vars = t.fields(nil, app.Variables, "app.json", "variables")
	t.user = t.fields(nil, app.UserContext, "app.json", "userContext")
	for i, f := range app.UserContext {
		if f.Name == userAuthenticated {
			t.u.report(plxerr.DuplicateKey, "app.json", plxerr.Pointer("userContext", strconv.Itoa(i), "name"),
				"user.%s is built in: the host's auth delegate reports it", userAuthenticated)
		}
	}
	t.user = append(t.user, [2]string{userAuthenticated, "bool"})
	for i, ev := range app.HostEvents {
		for j, f := range ev.Fields {
			t.u.checkType(nil, f.Type, "app.json", plxerr.Pointer("hostEvents", strconv.Itoa(i), "fields", strconv.Itoa(j), "type"))
		}
	}
	t.appSources = t.sources(nil, app.DataSources, "app.json")
}

// stateFields checks state entries and returns their root fields and IDs.
func (t *typer) stateFields(pl *plugin, entries []schema.StateEntry, file string) ([][2]string, map[string]string) {
	var out [][2]string
	ids := map[string]string{}
	for i, s := range entries {
		ptr := plxerr.Pointer("state", strconv.Itoa(i), "type")
		if te := t.u.checkType(pl, s.Type, file, ptr); te != nil {
			out = append(out, [2]string{s.Name, te.String()})
			ids[s.Name] = s.ID
		}
	}
	return out, ids
}

// fields checks declared fields or parameters.
func (t *typer) fields(pl *plugin, fields []schema.Field, file, list string) [][2]string {
	var out [][2]string
	for i, f := range fields {
		if te := t.u.checkType(pl, f.Type, file, plxerr.Pointer(list, strconv.Itoa(i), "type")); te != nil {
			out = append(out, [2]string{f.Name, te.String()})
		}
	}
	return out
}

// params checks page parameters or graph inputs.
func (t *typer) params(pl *plugin, params []schema.Param, file, list string) [][2]string {
	var out [][2]string
	for i, p := range params {
		if te := t.u.checkType(pl, p.Type, file, plxerr.Pointer(list, strconv.Itoa(i), "type")); te != nil {
			out = append(out, [2]string{p.Name, te.String()})
		}
	}
	return out
}

// sources checks data sources.
func (t *typer) sources(pl *plugin, sources []schema.DataSource, file string) []sourceField {
	var out []sourceField
	for i, s := range sources {
		if te := t.u.checkType(pl, s.Type, file, plxerr.Pointer("dataSources", strconv.Itoa(i), "type")); te != nil {
			out = append(out, sourceField{name: s.Name, id: s.ID, typ: te.String()})
		}
	}
	return out
}

// dataTypes builds the data root: data.<name> is {value, loading, error,
// hasMore, status}. hasMore says a paginated source has another page
// (DAT-011); status is the ListStatus a list bound to the source shows
// (WGT-012).
func dataTypes(sources []sourceField) map[string]pxl.TypeSpec {
	types := map[string]pxl.TypeSpec{}
	var fields [][2]string
	for _, s := range sources {
		name := "PluxData" + upperFirst(s.name)
		value, _ := parseTypeExpr(s.typ)
		value.nullable = true
		types[name] = objectType([][2]string{{"value", value.String()}, {"loading", "bool"}, {"error", "PluxActionError?"}, {"hasMore", "bool"}, {"status", "ListStatus"}})
		fields = append(fields, [2]string{s.name, name})
	}
	types["PluxData"] = objectType(fields)
	return types
}

// baseScope is the scope of a plugin (nil: a shared component): the app
// and plugin state, flags, variables, user context, device and data.
func (t *typer) baseScope(pl *plugin) *scope {
	s := &scope{plugin: pl, roots: map[string]string{
		"app": "PluxAppState", "flags": "PluxFlags", "env": "PluxEnv", "user": "PluxUser", "device": "PluxDevice", "data": "PluxData",
	}, synth: map[string]pxl.TypeSpec{
		"PluxAppState": objectType(t.appState), "PluxFlags": objectType(t.flags), "PluxEnv": objectType(t.vars), "PluxUser": objectType(t.user),
	}, ids: map[string]map[string]string{"app": t.appIDs, "data": {}}}
	sources := t.appSources
	if pl != nil {
		fields, ids := t.stateFields(pl, pl.doc.State, pl.file)
		s.roots["plugin"] = "PluxPluginState"
		s.synth["PluxPluginState"] = objectType(fields)
		s.ids["plugin"] = ids
		sources = append(append([]sourceField(nil), sources...), t.sources(pl, pl.doc.DataSources, pl.file)...)
	}
	for k, v := range dataTypes(sources) {
		s.synth[k] = v
	}
	for _, src := range sources {
		s.ids["data"][src.name] = src.id
	}
	s.sources = sources
	return s
}

// pageScope adds the page's state, parameters and data sources.
func (t *typer) pageScope(pg *page, base *scope) *scope {
	if pg.scope != nil {
		return pg.scope
	}
	fields, ids := t.stateFields(pg.plugin, pg.doc.State, pg.file)
	s := base.with("page", "PluxPageState").with("params", "PluxPageParams").withTypes(map[string]pxl.TypeSpec{
		"PluxPageState":  objectType(fields),
		"PluxPageParams": objectType(t.params(pg.plugin, pg.doc.Params, pg.file, "params")),
	})
	s.ids = cloneIDs(base.ids)
	s.ids["page"] = ids
	sources := append(append([]sourceField(nil), base.sources...), t.sources(pg.plugin, pg.doc.DataSources, pg.file)...)
	s = s.withTypes(dataTypes(sources))
	s.sources = sources
	for _, src := range sources {
		s.ids["data"][src.name] = src.id
	}
	pg.scope = s
	return s
}

func cloneIDs(m map[string]map[string]string) map[string]map[string]string {
	out := map[string]map[string]string{}
	for k, v := range m {
		c := map[string]string{}
		for a, b := range v {
			c[a] = b
		}
		out[k] = c
	}
	return out
}

// computed compiles the computed state entries of a scope.
func (t *typer) computed(pl *plugin, entries []schema.StateEntry, file string, s *scope) {
	from := t.u.project.App.Doc.ID
	if pl != nil {
		from = pl.doc.ID
	}
	for i, st := range entries {
		if st.Computed != nil {
			t.compile(st.Computed.Expr, s, from, file, plxerr.Pointer("state", strconv.Itoa(i), "computed"))
		}
	}
}

// page type-checks a page.
func (t *typer) page(pg *page, base *scope) {
	s := t.pageScope(pg, base)
	from := pg.doc.ID
	t.computed(pg.plugin, pg.doc.State, pg.file, s)
	t.compileDataSources(pg.plugin, pg.doc.DataSources, pg.file, s, from)
	t.compileAll(pg.doc.Title, s, from, pg.file, "/title")
	t.node(pg.root, s)
	for _, name := range sortedKeys(pg.graphs) {
		t.useGraph(pg.graphs[name], "", pg.file, plxerr.Pointer("lifecycle", name))
	}
	for _, g := range pg.plugin.inline {
		if g.page == pg {
			t.graph(g, s)
		}
	}
}

// component type-checks a component definition.
func (t *typer) component(c *component, base *scope) {
	stateFields, ids := t.stateFields(c.plugin, c.doc.State, c.file)
	var props [][2]string
	for i, p := range c.doc.Props {
		if te := t.u.checkType(c.plugin, p.Type, c.file, plxerr.Pointer("props", strconv.Itoa(i), "type")); te != nil {
			props = append(props, [2]string{p.Name, te.String()})
		}
	}
	s := base.with("component", "PluxComponentState").with("props", "PluxComponentProps").withTypes(map[string]pxl.TypeSpec{
		"PluxComponentState": objectType(stateFields), "PluxComponentProps": objectType(props),
	})
	s.ids = cloneIDs(base.ids)
	s.ids["component"] = ids
	s.sources = base.sources
	t.computed(c.plugin, c.doc.State, c.file, s)
	t.node(c.root, s)
	graphs := t.u.appGraphs
	if c.plugin != nil {
		graphs = c.plugin.inline
	}
	for _, g := range graphs {
		if g.page == nil && g.file == c.file {
			t.graph(g, s)
		}
	}
}

// node type-checks a node and its subtree in scope s.
func (t *typer) node(n *node, s *scope) {
	n.scope = s
	file := n.owner.file()
	t.nodeExprs(n, s)
	t.bindTypeArgs(n)
	for _, name := range sortedKeys(n.graphs) {
		t.useGraph(n.graphs[name], t.payload(n, name), file, n.ptr+plxerr.Pointer("events", name))
	}
	for _, c := range n.children {
		t.node(c, s)
	}
	for _, sf := range n.slots {
		inner := t.slotScope(n, sf, s)
		for _, c := range sf.nodes {
			t.node(c, inner)
		}
	}
}

// slotScope is the scope of a slot's nodes: item templates add `item`
// and `index`.
func (t *typer) slotScope(n *node, sf *slotFill, s *scope) *scope {
	if n.widget == nil {
		return s
	}
	if n.widget.Type == "If" {
		return s.narrow(t.ifGuards(n, sf.name))
	}
	slot, ok := n.widget.Slot(sf.name)
	if !ok || !slot.Template {
		return s
	}
	inner := s.with("index", "int")
	if item, ok := n.typeArgs["T"]; ok {
		return inner.with("item", item.String())
	}
	t.u.report(plxerr.PropTypeMismatch, n.owner.file(), n.ptr, "the item type of %s cannot be inferred; bind its items to a typed expression", n.widget.Type)
	return inner
}

// nodeExprs compiles every expression of a node.
func (t *typer) nodeExprs(n *node, s *scope) {
	file, from := n.owner.file(), ownerID(n.owner)
	doc := n.doc
	for _, name := range sortedKeys(doc.Props) {
		t.compileAll(doc.Props[name], s, from, file, n.ptr+plxerr.Pointer("props", name))
	}
	t.compileAll(doc.Visible, s, from, file, n.ptr+"/visible")
	if sem := doc.Semantics; sem != nil {
		t.compileAll(sem.Label, s, from, file, n.ptr+"/semantics/label")
		t.compileAll(sem.Hint, s, from, file, n.ptr+"/semantics/hint")
		t.compileAll(sem.Value, s, from, file, n.ptr+"/semantics/value")
	}
	if r := doc.Responsive; r != nil {
		for _, name := range sortedKeys(r.Medium) {
			t.compileAll(r.Medium[name], s, from, file, n.ptr+plxerr.Pointer("responsive", "medium", name))
		}
		for _, name := range sortedKeys(r.Expanded) {
			t.compileAll(r.Expanded[name], s, from, file, n.ptr+plxerr.Pointer("responsive", "expanded", name))
		}
	}
}

// payload returns the type expression of an event's payload, "" for none.
func (t *typer) payload(n *node, event string) string {
	switch {
	case n.widget != nil:
		if e, ok := n.widget.Event(event); ok {
			return e.Payload
		}
	case n.target != nil:
		for _, e := range n.target.doc.Events {
			if e.Name == event {
				return e.Payload
			}
		}
	default:
		if ns, ok := t.u.natives.slots[n.doc.Type]; ok {
			for _, e := range ns.Events {
				if e.Name == event {
					return e.Payload
				}
			}
		}
	}
	return ""
}

// useGraph records that a handler with the given payload runs g; every
// handler of a graph must agree on the payload, which types `event`.
func (t *typer) useGraph(g *graph, payload, file, ptr string) {
	if !g.eventSet {
		g.eventSet, g.eventType = true, payload
		return
	}
	if g.eventType != payload {
		t.u.report(plxerr.InvalidActionGraph, file, ptr, "action graph %q already handles events with payload %q, not %q", g.key, g.eventType, payload)
	}
}

// bindTypeArgs binds a widget's type parameters from the types of the
// expressions or literals of the props that mention them.
func (t *typer) bindTypeArgs(n *node) {
	if n.widget == nil || len(n.widget.TypeParameters) == 0 {
		return
	}
	params := map[string]bool{}
	for _, p := range n.widget.TypeParameters {
		params[p] = true
	}
	bind := map[string]*texpr{}
	for _, p := range n.widget.Props {
		raw, set := n.doc.Props[p.Name]
		pattern, err := parseTypeExpr(p.Type)
		if !set || err != nil || !mentions(pattern, params) {
			continue
		}
		actual := t.literalType(raw, n.owner.file(), n.ptr+plxerr.Pointer("props", p.Name))
		if actual == nil {
			continue
		}
		if !pattern.match(actual, params, bind) {
			t.u.report(plxerr.PropTypeMismatch, n.owner.file(), n.ptr+plxerr.Pointer("props", p.Name), "prop %q of %s must be %s, not %s", p.Name, n.widget.Type, p.Type, actual)
		}
	}
	n.typeArgs = map[string]*pxl.Type{}
	for _, name := range sortedKeys(bind) {
		env, err := t.u.env(n.scope)
		if err != nil {
			t.u.internalError("%v", err)
			return
		}
		if typ, err := env.ParseType(bind[name].String()); err == nil {
			n.typeArgs[name] = typ
		}
	}
}

// mentions reports whether a pattern uses a type parameter.
func mentions(t *texpr, params map[string]bool) bool {
	found := false
	t.names(func(n string) { found = found || params[n] })
	return found
}

// literalType is the type of a prop value: the compiled expression's
// type, or the element type of a list of primitive literals.
func (t *typer) literalType(raw json.RawMessage, file, ptr string) *texpr {
	if e, ok := t.u.exprs[file+"#"+ptr]; ok {
		if e.typ == nil {
			return nil
		}
		te, _ := parseTypeExpr(e.typ.String())
		return te
	}
	var items []any
	if json.Unmarshal(raw, &items) != nil || len(items) == 0 {
		return nil
	}
	elem := ""
	for _, it := range items {
		k := primitiveOf(it)
		if k == "" || elem != "" && k != elem {
			return nil
		}
		elem = k
	}
	return &texpr{name: "list", elem: &texpr{name: elem}}
}

// primitiveOf names the type of a JSON scalar literal.
func primitiveOf(v any) string {
	switch x := v.(type) {
	case string:
		return "string"
	case bool:
		return "bool"
	case float64:
		if x == float64(int64(x)) {
			return "int"
		}
		return "double"
	}
	return ""
}

// flow type-checks a plugin flow: its inputs are the `params` root.
func (t *typer) flow(g *graph, base *scope) {
	s := base.with("params", "PluxFlowParams").withTypes(map[string]pxl.TypeSpec{
		"PluxFlowParams": objectType(t.params(g.plugin, g.doc.Inputs, g.file, "inputs")),
	})
	if g.doc.Output != "" {
		// A flow may return a registry value type, such as a guard's
		// GuardResult (NAV-009).
		t.u.checkTypeIn(t.u.types, g.plugin, t.u.types.base, g.doc.Output, g.file, "/output")
	}
	t.graph(g, s)
}

// graph type-checks the inputs of every step. `event` is the payload of
// the handlers that run the graph; `steps.<id>` gives the output and error
// of each step. A payload may be a registry value type such as a
// RangeSlider's RangeValues: widget payloads come from the registry, and
// a component's declared payload was checked where it was declared.
func (t *typer) graph(g *graph, s *scope) {
	if g.eventType != "" {
		known := maps.Clone(t.u.types.base)
		maps.Copy(known, s.synth)
		if te := t.u.checkTypeIn(t.u.types, g.plugin, known, g.eventType, g.file, g.ptr); te != nil {
			s = s.with("event", te.String())
		}
	}
	steps := map[string]pxl.TypeSpec{}
	var fields [][2]string
	for _, st := range g.steps {
		name := "PluxStep" + upperFirst(st.ID)
		out := [][2]string{{"error", "PluxActionError?"}}
		if o := t.stepOutput(g, st); o != "" {
			out = append(out, [2]string{"output", o})
		}
		steps[name] = objectType(out)
		fields = append(fields, [2]string{st.ID, name})
	}
	steps["PluxSteps"] = objectType(fields)
	s = s.with("steps", "PluxSteps").withTypes(steps)
	from := ownerOrGraphID(g)
	entries := stepEntries(g)
	compileStep := func(i int, s *scope) {
		s = s.narrow(entries[i])
		for _, name := range sortedKeys(g.steps[i].Input) {
			t.compileAll(g.steps[i].Input[name], s, from, g.file, g.ptr+plxerr.Pointer("steps", strconv.Itoa(i), "input", name))
		}
	}
	t.loops(g, s, compileStep)
	g.scope = s
}

// loops compiles the inputs of every step of a graph in its scope: steps
// in the body branch of a forEach see its `item` and `index` (Appendix
// E.2), those of the innermost loop when loops nest. A loop's items are
// compiled before its body, whose item type they give.
func (t *typer) loops(g *graph, s *scope, compileStep func(int, *scope)) {
	in, loops := loopBodies(g)
	innermost := func(i int) int {
		best := -1
		for _, f := range in[i] {
			if best < 0 || len(in[f]) > len(in[best]) {
				best = f
			}
		}
		return best
	}
	// Outer loops first: a loop's scope is the body scope of the loop
	// around it.
	slices.SortStableFunc(loops, func(a, b int) int { return len(in[a]) - len(in[b]) })
	body := map[int]*scope{}
	scopeOf := func(i int) *scope {
		if f := innermost(i); f >= 0 && body[f] != nil {
			return body[f]
		}
		return s
	}
	for _, f := range loops {
		fs := scopeOf(f)
		compileStep(f, fs)
		b := fs.with("index", "int")
		ptr := g.ptr + plxerr.Pointer("steps", strconv.Itoa(f), "input", "items")
		if te := t.literalType(g.steps[f].Input["items"], g.file, ptr); te != nil && te.name == "list" {
			b = b.with("item", te.elem.String())
		}
		body[f] = b
	}
	for i, st := range g.steps {
		if st.Action != "forEach" {
			compileStep(i, scopeOf(i))
		}
	}
}

// loopBodies returns the forEach steps of a graph and, for each step, the
// loops whose body branch reaches it.
func loopBodies(g *graph) (in [][]int, loops []int) {
	index := map[string]int{}
	for i, st := range g.steps {
		index[st.ID] = i
	}
	in = make([][]int, len(g.steps))
	for f, st := range g.steps {
		if st.Action != "forEach" {
			continue
		}
		loops = append(loops, f)
		seen := map[int]bool{f: true}
		queue := []string{st.Branches["body"]}
		for len(queue) > 0 {
			i, ok := index[queue[0]]
			queue = queue[1:]
			if !ok || seen[i] {
				continue
			}
			seen[i] = true
			in[i] = append(in[i], f)
			next := g.steps[i]
			queue = append(append(queue, next.Next, next.OnSuccess, next.OnError), branchTargets(next)...)
		}
	}
	return in, loops
}

// branchTargets returns the step IDs of a step's named branches, sorted.
func branchTargets(st schema.Step) []string {
	out := make([]string, 0, len(st.Branches))
	for _, name := range sortedKeys(st.Branches) {
		out = append(out, st.Branches[name])
	}
	return out
}

// stepOutput is the output type of a step when it is known without
// inference: a concrete action output, made nullable because the step
// may not have run.
func (t *typer) stepOutput(g *graph, st schema.Step) string {
	if st.Action == "callFlow" {
		f := flowByKey(g.plugin, literalString(st.Input["flow"]))
		if f == nil || f.doc.Output == "" {
			return ""
		}
		te, err := parseTypeExpr(f.doc.Output)
		if err != nil {
			return ""
		}
		te.nullable = true
		return te.String()
	}
	if out, ok := t.customOutput(st); ok {
		return out
	}
	if st.Action == "apiCall" {
		return t.apiCallOutput(g, st)
	}
	a, ok := registry.LookupAction(st.Action)
	if !ok || a.Output == "" {
		return ""
	}
	output := a.Output
	if st.Action == "openDialog" || st.Action == "openBottomSheet" {
		output = t.presentedResult(st)
	}
	te, err := parseTypeExpr(output)
	if err != nil || len(a.TypeParameters) > 0 && mentions(te, setOf(a.TypeParameters)) {
		return ""
	}
	if !t.u.types.known(g.plugin, te.name) && !hasKey(t.u.types.base, te.name) && te.elem == nil {
		return ""
	}
	te.nullable = true
	return te.String()
}

// customOutput is the output type of a custom action step, as the native
// catalogue declares it (ACT-060): a step named after the action, or a
// callNative step naming it literally. ok is false for any other step.
func (t *typer) customOutput(st schema.Step) (string, bool) {
	name := st.Action
	if st.Action == "callNative" {
		name = literalString(st.Input["action"])
	} else if _, builtin := registry.LookupAction(st.Action); builtin {
		return "", false
	}
	native, ok := t.u.natives.actions[name]
	if !ok {
		return "", false
	}
	if native.Output == "" {
		return "", true
	}
	te, err := parseTypeExpr(native.Output)
	if err != nil {
		return "", true
	}
	te.nullable = true
	return te.String(), true
}

// presentedResult is the result type of the route a presenting step
// opens, as its page or native route declares it (NAV-003), or "".
func (t *typer) presentedResult(st schema.Step) string {
	r := t.u.routes[literalString(st.Input["route"])]
	switch {
	case r == nil:
		return ""
	case r.page != nil:
		return r.page.doc.Result
	case r.native != nil:
		return r.native.Result
	}
	return ""
}

func setOf(xs []string) map[string]bool {
	m := map[string]bool{}
	for _, x := range xs {
		m[x] = true
	}
	return m
}

// ownerOrGraphID is the entity a graph's references come from.
func ownerOrGraphID(g *graph) string {
	if g.doc != nil {
		return g.doc.ID
	}
	return uuidString(g.id)
}

// compileAll compiles every $expr inside a raw value.
func (t *typer) compileAll(raw json.RawMessage, s *scope, from, file, ptr string) {
	if len(raw) == 0 {
		return
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return
	}
	t.walk(v, s, from, file, ptr)
}

func (t *typer) walk(v any, s *scope, from, file, ptr string) {
	switch x := v.(type) {
	case []any:
		for i, item := range x {
			t.walk(item, s, from, file, ptr+"/"+strconv.Itoa(i))
		}
	case map[string]any:
		if src, ok := x["$expr"].(string); ok {
			t.compile(src, s, from, file, ptr)
			return
		}
		for _, k := range sortedKeys(x) {
			t.walk(x[k], s, from, file, ptr+plxerr.Pointer(k))
		}
	}
}

// compile compiles one expression and records it by its binding's
// pointer; diagnostics point at the source text (CMP-004).
func (t *typer) compile(src string, s *scope, from, file, ptr string) {
	e := &expr{src: src, file: file, ptr: ptr, scope: s}
	t.u.exprs[file+"#"+ptr] = e
	env, err := t.u.env(s)
	if err != nil {
		t.u.internalError("%v", err)
		return
	}
	prog, typ, diags := pxl.Compile(src, env, s.options(t.opts), plxerr.Location{File: file, Path: ptr + "/$expr"})
	t.u.diags = append(t.u.diags, diags...)
	e.prog, e.typ = prog, typ
	if prog != nil {
		t.recordReads(prog.Reads, s, from, file, ptr)
	}
}

// recordReads adds the state and data sources an expression reads to the
// reference graph.
func (t *typer) recordReads(reads []string, s *scope, from, file, ptr string) {
	for _, r := range reads {
		parts := strings.SplitN(r, ".", 3)
		if len(parts) < 2 {
			continue
		}
		id, ok := s.ids[parts[0]][parts[1]]
		if !ok {
			continue
		}
		kind := EdgeUsesState
		if parts[0] == "data" {
			kind = EdgeUsesDataSource
		}
		t.u.graph.add(Edge{From: from, Kind: kind, To: id, File: file, Path: ptr})
	}
}

// ifGuards returns the paths an If widget's condition proves not null in
// one of its slots: in then when it is true, in else when it is false.
func (t *typer) ifGuards(n *node, slot string) []string {
	var cond struct {
		Expr string `json:"$expr"`
	}
	if json.Unmarshal(n.doc.Props["condition"], &cond) != nil || cond.Expr == "" {
		return nil
	}
	whenTrue, whenFalse := pxl.Guards(cond.Expr, t.opts)
	switch slot {
	case "then":
		return whenTrue
	case "else":
		return whenFalse
	default:
		return nil
	}
}

// stepEntries returns, per step, the step results it can read as not
// null: a step entered only from X's onError sees steps.X.error, one
// entered only from X's onSuccess sees steps.X.output (ADR-0009).
func stepEntries(g *graph) [][]string {
	type edge struct {
		from int
		kind string
	}
	index := map[string]int{}
	for i, st := range g.steps {
		index[st.ID] = i
	}
	in := make([][]edge, len(g.steps))
	for i, st := range g.steps {
		add := func(id, kind string) {
			if j, ok := index[id]; ok {
				in[j] = append(in[j], edge{i, kind})
			}
		}
		add(st.Next, "")
		add(st.OnSuccess, "output")
		add(st.OnError, "error")
		for _, id := range branchTargets(st) {
			add(id, "")
		}
	}
	out := make([][]string, len(g.steps))
	for i, edges := range in {
		if i == 0 || len(edges) == 0 {
			continue // the entry step can also be entered by the handler
		}
		first := edges[0]
		same := first.kind != ""
		for _, e := range edges[1:] {
			same = same && e == first
		}
		if same {
			out[i] = []string{"steps." + g.steps[first.from].ID + "." + first.kind}
		}
	}
	return out
}
