// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/nightCode42/plux3/backend/internal/bundle/fbs"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema"
	"github.com/nightCode42/plux3/backend/internal/schema/registry"
)

// checkGraph checks an action graph (ACT-001, SCH-040) and lowers its
// steps: actions by permanent ID, inputs checked against their types,
// successors as step indices (BND-017).
func (u *unit) checkGraph(g *graph) {
	if g.scope == nil {
		return // not type-checked: its page or plugin failed earlier
	}
	if g.doc != nil && len(g.doc.State) > 0 {
		g.state = u.checkEntries(g.plugin, g.doc.State, g.file, g.scope, true)
	}
	index := map[string]int32{}
	ids := u.newKeys("step")
	for i, st := range g.steps {
		ids.claim(st.ID, g.file, g.ptr+plxerr.Pointer("steps", strconv.Itoa(i), "id"))
		if _, dup := index[st.ID]; !dup {
			index[st.ID] = int32(i) //nolint:gosec // G115: bounded by the document size.
		}
	}
	for i := range g.steps {
		if s := u.checkStep(g, i, index); s != nil {
			g.lowered = append(g.lowered, s)
		}
	}
	if len(g.lowered) == len(g.steps) {
		u.checkFlow(g)
	}
}

// graphFrom is the entity a graph's references come from: its page, or
// the graph itself.
func graphFrom(g *graph) string {
	if g.page != nil {
		return g.page.doc.ID
	}
	return ownerOrGraphID(g)
}

// checkStep checks one step and returns it lowered.
func (u *unit) checkStep(g *graph, i int, index map[string]int32) *step {
	st := g.steps[i]
	ptr := g.ptr + plxerr.Pointer("steps", strconv.Itoa(i))
	a, ok := registry.LookupAction(st.Action)
	input := st.Input
	if !ok {
		native, isNative := u.natives.actions[st.Action]
		if !isNative {
			u.report(plxerr.UnknownAction, g.file, ptr+"/action", "no built-in or custom action is named %q", st.Action)
			return nil
		}
		// A custom action is called through callNative (ACT-060).
		a, _ = registry.LookupAction("callNative")
		input = nativeInput(native, st.Input)
		u.graph.add(Edge{From: graphFrom(g), Kind: EdgeUsesAction, To: st.Action, File: g.file, Path: ptr + "/action"})
	} else if name := literalString(st.Input["action"]); st.Action == "callNative" && u.natives.actions[name] != nil {
		u.graph.add(Edge{From: graphFrom(g), Kind: EdgeUsesAction, To: name, File: g.file, Path: ptr + "/input/action"})
	}
	out := &step{ptr: ptr, id: st.ID, action: a.ID, next: -1, onSuccess: -1, onError: -1, retry: st.Retry}
	if st.TimeoutMs != nil {
		out.timeoutMs = uint32(max(0, min(*st.TimeoutMs, 1<<32-1))) //nolint:gosec // G115: clamped.
	}
	out.inputs = u.checkInputs(g, &a, input, ptr)
	u.stepFeatures(g, &a, st, ptr)
	u.checkDeviceStep(g, a.Name, input, ptr)
	if stateWrites[a.Name] {
		u.checkStateWrite(g, a.Name, input, ptr)
	}
	for _, e := range []struct {
		name, target string
		dst          *int32
	}{{"next", st.Next, &out.next}, {"onSuccess", st.OnSuccess, &out.onSuccess}, {"onError", st.OnError, &out.onError}} {
		if e.target == "" {
			continue
		}
		j, found := index[e.target]
		if !found {
			u.report(plxerr.InvalidActionGraph, g.file, ptr+"/"+e.name, "no step %q", e.target)
			continue
		}
		*e.dst = j
	}
	allowed := branchNames(&a, input)
	for _, name := range sortedKeys(st.Branches) {
		bptr := ptr + plxerr.Pointer("branches", name)
		if !slices.Contains(allowed, name) {
			u.report(plxerr.InvalidActionGraph, g.file, bptr, "action %s has no branch %q", a.Name, name)
			continue
		}
		j, found := index[st.Branches[name]]
		if !found {
			u.report(plxerr.InvalidActionGraph, g.file, bptr, "no step %q", st.Branches[name])
			continue
		}
		out.branches = append(out.branches, branch{name: name, step: j})
	}
	return out
}

// branchNames lists an action's branches: declared ones and the values of
// its branchesFrom input.
func branchNames(a *registry.Action, input map[string]json.RawMessage) []string {
	names := slices.Clone(a.Branches)
	if a.BranchesFrom != "" {
		var extra []string
		if json.Unmarshal(input[a.BranchesFrom], &extra) == nil {
			names = append(names, extra...)
		}
	}
	return names
}

// nativeInput rewrites the inputs of a custom action as the inputs of
// callNative: the action's name and an object of its inputs, which
// specialInput checks the inputs whose type an action does not declare
// alone: route parameters, patches, event payloads, results, and a custom
// action's or flow's inputs. handled is false for any other input.
func (u *unit) specialInput(g *graph, action, input string, raw json.RawMessage, c vctx, bind map[string]*texpr) (v *value, handled bool) {
	switch [2]string{action, input} {
	case [2]string{"navigate", "params"}, [2]string{"openDialog", "params"}, [2]string{"openBottomSheet", "params"}:
		return u.routeParams(g, raw, bind["P"], c), true
	case [2]string{"patchState", "patch"}:
		return u.patchValue(g, raw, c), true
	case [2]string{"emitHostEvent", "payload"}:
		return u.eventPayload(g, raw, c), true
	case [2]string{"pop", "result"}:
		if g.page != nil && g.page.doc.Result == "" {
			u.report(plxerr.PropTypeMismatch, c.file, c.ptr, "page %q declares no result type to return", g.page.doc.Key)
			return nil, true
		}
	case [2]string{"stop", "result"}:
		if g.output == "" {
			u.report(plxerr.PropTypeMismatch, c.file, c.ptr, "the graph declares no output to return")
			return nil, true
		}
	case [2]string{"callNative", "input"}:
		return u.nativeActionInput(g, raw, c), true
	case [2]string{"emitEvent", "payload"}:
		return u.componentEventPayload(g, raw, c), true
	case [2]string{"callFlow", "input"}:
		return u.flowInput(g, raw, c), true
	}
	return nil, false
}

// nativeActionInput checks against the action's declaration.
func nativeInput(na *schema.NativeAction, input map[string]json.RawMessage) map[string]json.RawMessage {
	name, _ := json.Marshal(na.Name)
	obj, _ := json.Marshal(input)
	if input == nil {
		obj = []byte("{}")
	}
	return map[string]json.RawMessage{"action": name, "input": obj}
}

// checkInputs checks a step's inputs and binds the action's type
// parameters from the step's context and values.
func (u *unit) checkInputs(g *graph, a *registry.Action, input map[string]json.RawMessage, ptr string) []*prop {
	c := vctx{file: g.file, scope: g.scope, pl: g.plugin, code: plxerr.PropTypeMismatch, from: graphFrom(g)}
	for _, name := range sortedKeys(input) {
		if _, ok := a.Input(name); !ok {
			u.report(plxerr.UnknownProp, g.file, ptr+plxerr.Pointer("input", name), "action %s has no input %q", a.Name, name)
		}
	}
	for _, in := range a.Inputs {
		if _, set := input[in.Name]; in.Required && !set {
			u.report(plxerr.MissingRequiredProp, g.file, ptr, "action %s needs input %q", a.Name, in.Name)
		}
	}
	bind := u.bindAction(g, a, input, ptr)
	var out []*prop
	for _, in := range a.Inputs {
		raw, set := input[in.Name]
		if !set {
			continue
		}
		ic := c
		ic.ptr = ptr + plxerr.Pointer("input", in.Name)
		if a.Name == "apiCall" && in.Name == "input" && !u.apiCallInput(g, ic) {
			continue
		}
		v := u.checkInput(g, a, in, raw, ic, bind)
		if v != nil {
			out = append(out, &prop{name: in.Name, id: in.ID, ptr: ic.ptr, value: v})
		}
	}
	return out
}

// checkInput checks one input.
func (u *unit) checkInput(g *graph, a *registry.Action, in registry.Input, raw json.RawMessage, c vctx, bind map[string]*texpr) *value {
	if in.Ref != "" {
		return u.checkRef(g, in, raw, c)
	}
	if v, handled := u.specialInput(g, a.Name, in.Name, raw, c, bind); handled {
		return v
	}
	te, err := parseTypeExpr(in.Type)
	if err != nil {
		u.internalError("action input %s.%s: %v", a.Name, in.Name, err)
		return nil
	}
	te = te.subst(bind)
	if unbound(te, a.TypeParameters) {
		return u.inferred(c, raw)
	}
	return u.checkRaw(c, raw, te)
}

// nativeActionInput checks a custom action's inputs against the native
// catalogue's declaration (ACT-060). A step named after the action has
// its inputs moved under callNative's input (nativeInput), so their
// expressions were type-checked at the step's own input pointers.
func (u *unit) nativeActionInput(g *graph, raw json.RawMessage, c vctx) *value {
	st := g.steps[stepIndex(c.ptr)]
	name, src := literalString(st.Input["action"]), c
	if st.Action != "callNative" {
		name, src.ptr = st.Action, strings.TrimSuffix(c.ptr, plxerr.Pointer("input"))
	}
	na := u.natives.actions[name]
	if na == nil {
		return u.inferred(c, raw) // an unknown action is reported by its reference
	}
	return u.fieldValues(raw, na.Inputs, fmt.Sprintf("custom action %q", name), src)
}

// unbound reports whether a type still names a type parameter.
func unbound(t *texpr, params []string) bool {
	found := false
	t.names(func(n string) { found = found || slices.Contains(params, n) })
	return found
}

// bindAction binds an action's type parameters: from the state entry a
// path names (setState), the route's parameters (navigate), the flow's
// declaration (callFlow), and the types of bound inputs.
func (u *unit) bindAction(g *graph, a *registry.Action, input map[string]json.RawMessage, ptr string) map[string]*texpr {
	bind := map[string]*texpr{}
	params := setOf(a.TypeParameters)
	for _, in := range a.Inputs {
		_, set := input[in.Name]
		pattern, err := parseTypeExpr(in.Type)
		if !set || err != nil || !mentions(pattern, params) || in.Ref != "" {
			continue
		}
		if e, ok := u.exprs[g.file+"#"+ptr+plxerr.Pointer("input", in.Name)]; ok && e.typ != nil {
			if actual, err := parseTypeExpr(e.typ.String()); err == nil {
				pattern.match(actual, params, bind)
			}
		}
	}
	if name, t := declaredBinding(g, a, input); t != nil {
		bind[name] = t
	}
	if a.Name == "apiCall" {
		// The operation declares its input type (DAT-001).
		if _, op := u.operation(g, literalString(input["operation"])); op != nil && op.Input != "" {
			if t, err := parseTypeExpr(op.Input); err == nil {
				bind["I"] = t
			}
		}
	}
	return bind
}

// declaredBinding binds the type parameter a declaration fixes: pop's
// result to the page's declared result (NAV-003), stop's to the graph's
// declared output (a guard's GuardResult, NAV-009), setState's value to
// the state entry its path names.
func declaredBinding(g *graph, a *registry.Action, input map[string]json.RawMessage) (string, *texpr) {
	switch a.Name {
	case "pop":
		if g.page == nil || g.page.doc.Result == "" {
			return "", nil
		}
		if t, err := parseTypeExpr(g.page.doc.Result); err == nil {
			return "R", t
		}
	case "stop":
		if g.output == "" {
			return "", nil
		}
		if t, err := parseTypeExpr(g.output); err == nil {
			return "T", t
		}
	case "setState", "patchState":
		if path := literalString(input["path"]); path != "" {
			return "T", statePathType(g, path)
		}
	}
	return "", nil
}

// literalString decodes a raw JSON string, or "".
func literalString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}

// statePathType resolves "<scope>.<name>" to the state entry's type.
func statePathType(g *graph, path string) *texpr {
	root, name, ok := strings.Cut(path, ".")
	if !ok {
		return nil
	}
	typ, isRoot := g.scope.roots[root]
	if !isRoot {
		return nil
	}
	// A form's state is nested: <form>.values.<field> (STA-020).
	for _, part := range strings.Split(name, ".") {
		spec, ok := g.scope.synth[typ]
		if !ok {
			return nil
		}
		f, ok := spec.Fields[part]
		if !ok {
			return nil
		}
		typ = f
	}
	te, _ := parseTypeExpr(typ)
	return te
}

// refRoots are the roots whose entries a state path may name.
var refRoots = map[string]bool{"app": true, "plugin": true, "page": true, "component": true, "run": true}

// checkRef resolves a string input that names an entity (ADR-0010).
func (u *unit) checkRef(g *graph, in registry.Input, raw json.RawMessage, c vctx) *value {
	name := literalString(raw)
	if name == "" {
		u.report(c.code, c.file, c.ptr, "input %q names a %s and is written as a literal string", in.Name, in.Ref)
		return nil
	}
	resolved, kind, to := true, EdgeKind(""), ""
	switch in.Ref {
	case "state":
		if v, isForm := u.formStatePath(g, name, c); isForm {
			return v
		}
		root, entry, _ := strings.Cut(name, ".")
		id, ok := g.scope.ids[root][entry]
		resolved, kind, to = ok && refRoots[root], EdgeUsesState, id
	case "flow":
		f, private := u.flowRef(g.plugin, name)
		if private {
			u.report(plxerr.FlowNotExported, c.file, c.ptr, "flow %q is not exported by its plugin", name)
			return nil
		}
		resolved = f != nil
		if f != nil {
			f.used = true
			kind, to = EdgeUsesGraph, f.doc.ID
		}
	case "dataSource":
		id, ok := g.scope.ids["data"][name]
		resolved, kind, to = ok, EdgeUsesDataSource, id
	case "operation":
		src, _ := u.operation(g, name)
		resolved = src != nil
		if src != nil {
			kind, to = EdgeUsesDataSource, src.ID
		}
	case "collection":
		id := u.collectionID(g.plugin, name)
		resolved, kind, to = id != "", EdgeUsesCollection, id
	case "function":
		resolved, kind, to = g.plugin != nil && g.plugin.functions[name], EdgeUsesFunction, name
	case "nativeAction":
		_, resolved = u.natives.actions[name]
	case "hostEvent":
		_, resolved = u.hostEvents[name]
	case "permission":
		resolved = slices.Contains(permissionAPIs, name)
	case "componentEvent":
		return u.componentEventRef(g, name, c)
	case "tab":
		resolved = u.hasTab(name)
	case "form":
		resolved = u.formRef(g, name)
	}
	if !resolved {
		u.report(plxerr.UnresolvedReference, c.file, c.ptr, "no %s is named %q", in.Ref, name)
		return nil
	}
	if kind != "" {
		u.graph.add(Edge{From: c.from, Kind: kind, To: to, File: c.file, Path: c.ptr})
	}
	return &value{kind: fbs.ValueKindString, s: name}
}

// flowByKey finds a plugin flow by key.
func flowByKey(pl *plugin, key string) *graph {
	if pl == nil {
		return nil
	}
	for _, g := range pl.graphs {
		if g.page == nil && g.key == key {
			return g
		}
	}
	return nil
}

// collectionID finds a collection of the plugin or the app by key.
func (u *unit) collectionID(pl *plugin, key string) string {
	if pl != nil {
		for _, c := range pl.doc.Collections {
			if c.Key == key {
				return c.ID
			}
		}
	}
	for _, c := range u.project.App.Doc.Collections {
		if c.Key == key {
			return c.ID
		}
	}
	return ""
}

// inferred checks an input whose type is a parameter no context binds,
// such as the props of trackEvent: literals keep their JSON types and
// bindings their own types.
func (u *unit) inferred(c vctx, raw json.RawMessage) *value {
	v, ok := decodeJSON(raw)
	if !ok {
		return nil
	}
	return u.infer(c, v)
}

func (u *unit) infer(c vctx, v any) *value {
	switch x := v.(type) {
	case map[string]any:
		return u.inferObject(c, x)
	case []any:
		out := &value{kind: fbs.ValueKindList}
		for i, it := range x {
			item := u.infer(c.at(strconv.Itoa(i)), it)
			if item == nil {
				return nil
			}
			out.items = append(out.items, item)
		}
		return out
	case json.Number:
		if i, err := x.Int64(); err == nil && i <= maxJSONInt && i >= -maxJSONInt {
			return &value{kind: fbs.ValueKindInt, i: i}
		}
		f, _ := x.Float64()
		return &value{kind: fbs.ValueKindDouble, d: f}
	case string:
		return &value{kind: fbs.ValueKindString, s: x}
	case bool:
		return primitiveValue(x)
	}
	return &value{kind: fbs.ValueKindNull}
}

// inferObject infers a map from an object, or an expression's value.
func (u *unit) inferObject(c vctx, x map[string]any) *value {
	if isBinding(x) {
		if _, isExpr := x["$expr"]; isExpr {
			e, ok := u.exprs[c.file+"#"+c.ptr]
			if !ok || e.prog == nil {
				return nil
			}
			return &value{kind: fbs.ValueKindExpr, prog: e.prog}
		}
		u.report(c.code, c.file, c.ptr, "only literals and expressions are allowed here")
		return nil
	}
	out := &value{kind: fbs.ValueKindMap}
	for _, k := range sortedKeys(x) {
		item := u.infer(c.at(k), x[k])
		if item == nil {
			return nil
		}
		out.entries = append(out.entries, entry{key: k, value: item})
	}
	return out
}

// checkFlow reports cycles and unreachable steps (ACT-001).
func (u *unit) checkFlow(g *graph) {
	n := len(g.lowered)
	succ := make([][]int32, n)
	for i, s := range g.lowered {
		for _, t := range []int32{s.next, s.onSuccess, s.onError} {
			if t >= 0 {
				succ[i] = append(succ[i], t)
			}
		}
		for _, b := range s.branches {
			succ[i] = append(succ[i], b.step)
		}
	}
	state := make([]int8, n) // 0 new, 1 on the path, 2 done
	var visit func(i int32) bool
	visit = func(i int32) bool {
		state[i] = 1
		for _, j := range succ[i] {
			if state[j] == 1 || state[j] == 0 && visit(j) {
				return true
			}
		}
		state[i] = 2
		return false
	}
	if n > 0 && visit(0) {
		u.report(plxerr.InvalidActionGraph, g.file, g.ptr+"/steps", "action graph %q has a cycle", g.key)
		return
	}
	for i, s := range g.lowered {
		if state[i] == 0 {
			u.report(plxerr.InvalidActionGraph, g.file, s.ptr, "step %q cannot be reached from the first step", s.id)
		}
	}
}

// navigation is a navigate step's target, for redirect loops.
type navigation struct {
	target *page
	ptr    string
}

// routeParams checks the params of a navigate step against the target
// route's parameters (SCH-025, PLX-1203–1205) and records the edge.
func (u *unit) routeParams(g *graph, raw json.RawMessage, _ *texpr, c vctx) *value {
	st := g.steps[stepIndex(c.ptr)]
	r := u.routes[literalString(st.Input["route"])]
	if r == nil {
		return nil // the route input reports it
	}
	var params []schema.Param
	switch {
	case r.page != nil:
		params = r.page.doc.Params
	default:
		params = r.native.Params
	}
	pc := c
	pc.code = plxerr.RouteParameterTypeInvalid
	if r.page != nil {
		pc.pl = r.page.plugin
	}
	return u.namedParams(raw, params, "route "+strconv.Quote(r.name), pc)
}

// paramCodes are the codes a check of named values reports: for the form
// of the object, a missing value and an undeclared name.
type paramCodes struct{ form, missing, unknown plxerr.Code }

var (
	// routeCodes are a route's (SCH-025, PLX-1203–1205), also used for a
	// flow's inputs.
	routeCodes = paramCodes{plxerr.RouteParameterTypeInvalid, plxerr.RouteParameterMissing, plxerr.UnknownRouteParameter}
	// fieldCodes are a host event payload's.
	fieldCodes = paramCodes{plxerr.PropTypeMismatch, plxerr.MissingRequiredProp, plxerr.UnknownProp}
)

// namedParams checks an object of values and bindings against declared
// parameters: a route's (SCH-025, PLX-1203–1205) or a flow's inputs.
func (u *unit) namedParams(raw json.RawMessage, params []schema.Param, what string, c vctx) *value {
	return u.namedValues(raw, params, what, c, routeCodes)
}

// fieldValues checks an object of values and bindings against declared
// fields, such as a host event's.
func (u *unit) fieldValues(raw json.RawMessage, params []schema.Param, what string, c vctx) *value {
	return u.namedValues(raw, params, what, c, fieldCodes)
}

// namedValues checks an object of values and bindings against declared
// names and types, reporting with the given codes.
func (u *unit) namedValues(raw json.RawMessage, params []schema.Param, what string, c vctx, codes paramCodes) *value {
	v, ok := decodeJSON(raw)
	obj, isObj := v.(map[string]any)
	if !ok || !isObj || isBinding(obj) {
		u.report(codes.form, c.file, c.ptr, "the values of %s are written as an object of values and bindings", what)
		return nil
	}
	out := &value{kind: fbs.ValueKindMap}
	declared := map[string]bool{}
	for _, p := range params {
		declared[p.Name] = true
		pv, present := obj[p.Name]
		if !present {
			if p.Required != nil && *p.Required && len(p.Default) == 0 {
				u.report(codes.missing, c.file, c.ptr, "%s needs %q", what, p.Name)
			}
			continue
		}
		te, err := parseTypeExpr(p.Type)
		if err != nil {
			continue
		}
		if x := u.check(c.at(p.Name), pv, te); x != nil {
			out.entries = append(out.entries, entry{key: p.Name, value: x})
		}
	}
	for _, name := range sortedKeys(obj) {
		if !declared[name] {
			u.report(codes.unknown, c.file, c.ptr+plxerr.Pointer(name), "%s has no %q", what, name)
		}
	}
	return out
}

// stepIndex extracts the step index from a pointer ".../steps/<i>/...".
func stepIndex(ptr string) int {
	parts := strings.Split(ptr, "/")
	for i := len(parts) - 2; i >= 0; i-- {
		if parts[i] == "steps" {
			n, _ := strconv.Atoi(parts[i+1])
			return n
		}
	}
	return 0
}

// navigations records the navigation edges of a graph and returns the
// pages its first step leads to unconditionally, following next edges.
func (u *unit) navigations(g *graph) []navigation {
	var out []navigation
	from := graphFrom(g)
	unconditional := map[int]bool{}
	for i := 0; i >= 0 && i < len(g.steps) && !unconditional[i]; {
		unconditional[i] = true
		next := g.steps[i].Next
		i = -1
		for j, st := range g.steps {
			if st.ID == next {
				i = j
			}
		}
	}
	for i, st := range g.steps {
		if st.Action != "navigate" && st.Action != "openDialog" && st.Action != "openBottomSheet" {
			continue
		}
		ptr := g.ptr + plxerr.Pointer("steps", strconv.Itoa(i), "input", "route")
		r := u.routes[literalString(st.Input["route"])]
		if r == nil {
			continue
		}
		switch {
		case r.native != nil:
			u.graph.add(Edge{From: from, Kind: EdgeNavigatesNative, To: r.name, File: g.file, Path: ptr})
		case r.page.plugin == g.plugin:
			u.graph.add(Edge{From: from, Kind: EdgeNavigates, To: r.page.doc.ID, File: g.file, Path: ptr})
		default:
			u.graph.add(Edge{From: from, Kind: EdgeNavigatesPlugin, To: r.page.doc.ID, File: g.file, Path: ptr})
		}
		if r.page != nil && unconditional[i] && st.Action == "navigate" {
			out = append(out, navigation{target: r.page, ptr: ptr})
		}
	}
	return out
}

// checkRedirects reports pages that redirect to each other from onEnter
// unconditionally, in a cycle (PLX-1206).
func (u *unit) checkRedirects() {
	next := map[*page][]navigation{}
	var pages []*page
	for _, pl := range u.plugins {
		for _, pg := range pl.pages {
			pages = append(pages, pg)
			if g := pg.graphs["onEnter"]; g != nil {
				next[pg] = g.navs
			}
		}
	}
	reported := map[*page]bool{}
	for _, start := range pages {
		seen := map[*page]bool{}
		for p := start; p != nil && !seen[p]; {
			seen[p] = true
			nav := next[p]
			if len(nav) == 0 {
				break
			}
			p = nav[0].target
			if p == start && !reported[start] {
				reported[start] = true
				u.report(plxerr.RedirectLoop, start.file, "/lifecycle/onEnter", "page %q redirects from onEnter in a cycle back to itself", start.doc.Key)
			}
		}
	}
}
