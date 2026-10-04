// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"encoding/json"
	"strconv"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema"
)

// guardOutput is the registry value type a guard graph returns
// (ADR-0040, NAV-009).
const guardOutput = "GuardResult"

// guardsFeature is required by a plugin bundle with a guarded page: a
// runtime that cannot run guards refuses the bundle, so a guarded page
// never opens unguarded (BND-008, ADR-0040). Guards first run in runtime
// 0.2.0.
const guardsFeature = "navigation.guards"

// guardRuntimes lists the runtime each revision of guardsFeature first
// shipped in.
var guardRuntimes = []string{"0.2.0"}

// guardRedirect is a guard's redirect that the graph takes whatever its
// inputs: the target page and where the graph names it.
type guardRedirect struct {
	target *page
	file   string
	ptr    string
}

// checkGuards checks the guards of every page (NAV-009): each guard graph
// returns a GuardResult, a literal redirect names a route whose
// parameters it can fill, a guarded page needs a runtime that runs guards,
// and no page redirects back to itself through guards that redirect
// unconditionally (PLX-1206).
func (u *unit) checkGuards() {
	redirects := map[*page]guardRedirect{}
	var pages []*page
	for _, pl := range u.plugins {
		for _, pg := range pl.pages {
			pages = append(pages, pg)
			if r, ok := u.checkPageGuards(pg); ok {
				redirects[pg] = r
			}
		}
	}
	reported := map[*page]bool{}
	for _, start := range pages {
		seen := map[*page]bool{}
		for p := start; !seen[p]; {
			seen[p] = true
			r, ok := redirects[p]
			if !ok {
				break
			}
			p = r.target
			if p == start && !reported[start] {
				reported[start] = true
				first := redirects[start]
				u.report(plxerr.RedirectLoop, first.file, first.ptr, "page %q redirects through its guards in a cycle back to itself", start.doc.Key)
			}
		}
	}
}

// checkPageGuards checks one page's guards and returns the redirect its
// first guard takes unconditionally, if any: a later guard runs only when
// the earlier ones allow, so only the first can redirect every entry.
func (u *unit) checkPageGuards(pg *page) (guardRedirect, bool) {
	if !isGuarded(pg) {
		return guardRedirect{}, false
	}
	ptr := "/routeOptions/guards"
	if len(pg.guards) == 0 {
		ptr = "/security/requiresAssurance"
	}
	u.useRevision(guardsFeature, guardRuntimes, 1, vctx{file: pg.file, ptr: ptr, pl: pg.plugin})
	var first guardRedirect
	found := false
	for i, g := range pg.guards {
		if !u.checkGuardGraph(pg, i, g) {
			continue
		}
		if i == 0 {
			first, found = u.unconditionalRedirect(g)
		}
	}
	return first, found
}

// isGuarded reports whether entering the page needs guards: it has guard
// graphs or requires an assurance level above AL0.
func isGuarded(pg *page) bool {
	if len(pg.guards) > 0 {
		return true
	}
	sec := pg.doc.Security
	return sec != nil && sec.RequiresAssurance != "" && sec.RequiresAssurance != "AL0"
}

// checkGuardGraph checks the page's i-th guard graph: it returns a
// GuardResult, takes no inputs when it is a flow, and its literal
// redirects name routes it can enter. It reports whether the graph is a
// guard at all.
func (u *unit) checkGuardGraph(pg *page, i int, g *graph) bool {
	ptr := plxerr.Pointer("routeOptions", "guards", strconv.Itoa(i), "$graph")
	if g.doc == nil || g.doc.Output != guardOutput {
		u.report(plxerr.InvalidActionGraph, pg.file, ptr, "guard graph %q must declare the output %s", g.key, guardOutput)
		return false
	}
	if g.page == nil && len(g.doc.Inputs) > 0 {
		// Nothing passes inputs to a guard; a page's own guard reads the
		// page's parameters instead.
		u.report(plxerr.InvalidActionGraph, pg.file, ptr, "guard flow %q declares inputs, which no entry can pass", g.key)
		return false
	}
	for j, st := range g.steps {
		if st.Action == "stop" {
			u.checkGuardRedirect(g, st, plxerr.Pointer("steps", strconv.Itoa(j), "input", "result"))
		}
	}
	return true
}

// unconditionalRedirect is the page a guard redirects every entry to,
// with where it says so.
func (u *unit) unconditionalRedirect(g *graph) (guardRedirect, bool) {
	t := unconditionalStop(g)
	if t < 0 {
		return guardRedirect{}, false
	}
	target := u.redirectTarget(g.steps[t].Input["result"])
	if target == nil {
		return guardRedirect{}, false
	}
	return guardRedirect{target: target, file: g.file, ptr: plxerr.Pointer("steps", strconv.Itoa(t), "input", "result")}, true
}

// literalRedirect reads a stop result that redirects to a literal route:
// the route, and its parameters when they are written as an object of
// values rather than as one expression (params is nil then). ok is false
// for any other result, such as a constant name or an expression.
func literalRedirect(raw json.RawMessage) (route string, params map[string]json.RawMessage, ok bool) {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil || isBindingRaw(raw) || literalString(obj["decision"]) != "redirect" {
		return "", nil, false
	}
	route = literalString(obj["route"])
	if route == "" {
		return "", nil, false
	}
	if p := obj["params"]; len(p) > 0 && !isBindingRaw(p) {
		params = map[string]json.RawMessage{}
		if json.Unmarshal(p, &params) != nil {
			params = nil
		}
	} else if len(p) == 0 {
		params = map[string]json.RawMessage{}
	}
	return route, params, true
}

// redirectTarget is the page a literal redirect opens, or nil.
func (u *unit) redirectTarget(raw json.RawMessage) *page {
	route, _, ok := literalRedirect(raw)
	if !ok {
		return nil
	}
	if r := u.routes[route]; r != nil {
		return r.page
	}
	return nil
}

// checkGuardRedirect checks a stop step that redirects to a literal page
// route whose parameters are written out: each is one the page declares, of a type a string converts to, and
// every required parameter is given (ADR-0040: a redirect's parameters
// convert as a deep link's do).
func (u *unit) checkGuardRedirect(g *graph, st schema.Step, ptr string) {
	route, params, ok := literalRedirect(st.Input["result"])
	if !ok {
		return
	}
	r := u.routes[route]
	if r == nil || r.page == nil || params == nil {
		// An unknown route is reported where the value is checked against
		// GuardResult's route type, a native route's parameters come from
		// the catalogue (ADR-0041), and a params expression is checked at
		// run time.
		return
	}
	declared := map[string]schema.Param{}
	for _, p := range r.page.doc.Params {
		declared[p.Name] = p
	}
	for _, name := range sortedKeys(params) {
		p, ok := declared[name]
		switch {
		case !ok:
			u.report(plxerr.UnknownRouteParameter, g.file, ptr+plxerr.Pointer("params", name), "route %q has no parameter %q", route, name)
		case !u.fromLink(p.Type, r.page.plugin):
			u.report(plxerr.RouteParameterTypeInvalid, g.file, ptr+plxerr.Pointer("params", name), "parameter %q of route %q is a %s, which a redirect's text cannot carry", name, route, p.Type)
		}
	}
	for _, p := range r.page.doc.Params {
		if _, given := params[p.Name]; !given && p.Required != nil && *p.Required && len(p.Default) == 0 {
			u.report(plxerr.RouteParameterMissing, g.file, ptr+"/params", "route %q needs its parameter %q", route, p.Name)
		}
	}
}

// unconditionalStop is the index of the stop step a graph reaches from its
// first step whatever its inputs, following onSuccess or next through
// steps that do not branch; -1 when there is none.
func unconditionalStop(g *graph) int {
	index := map[string]int{}
	for i, st := range g.steps {
		index[st.ID] = i
	}
	seen := map[int]bool{}
	for i := 0; i < len(g.steps) && !seen[i]; {
		seen[i] = true
		st := g.steps[i]
		switch st.Action {
		case "stop":
			return i
		case "condition":
			return -1
		}
		next := st.OnSuccess
		if next == "" {
			next = st.Next
		}
		j, ok := index[next]
		if !ok {
			return -1
		}
		i = j
	}
	return -1
}
