// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema"
)

// reservedLinkPrefix is the path every app answers with a route name,
// https://<host>/p/<route-name>?… (NAV-008), so no pattern may claim it.
const reservedLinkPrefix = "/p"

// linkScalars are the primitive types a link's text converts to.
var linkScalars = map[string]bool{
	"string": true, "int": true, "double": true, "bool": true, "decimal": true, "date": true, "dateTime": true,
}

// resolveNavigation checks the app's navigation against its routes: the
// not-found route, the deep-link patterns and the tabbed shells (NAV-005,
// NAV-006, NAV-008, NAV-011, ADR-0040).
func (u *unit) resolveNavigation() {
	nav := u.project.App.Doc.Navigation
	if nav == nil {
		return
	}
	if nav.NotFound != "" {
		if _, ok := u.routes[nav.NotFound]; !ok {
			u.report(plxerr.UnknownRoute, "app.json", "/navigation/notFound", "no page or native route is named %q", nav.NotFound)
		}
	}
	if nav.DeepLinks != nil {
		shapes := u.newKeys("deep-link path")
		for i, link := range nav.DeepLinks.Routes {
			u.deepLink(link, plxerr.Pointer("navigation", "deepLinks", "routes", strconv.Itoa(i)), shapes)
		}
	}
	shells := u.newKeys("shell")
	for i, sh := range nav.Shells {
		ptr := plxerr.Pointer("navigation", "shells", strconv.Itoa(i))
		shells.claim(sh.Key, "app.json", ptr+"/key")
		tabs := u.newKeys("tab")
		for j, tab := range sh.Tabs {
			tptr := ptr + plxerr.Pointer("tabs", strconv.Itoa(j))
			tabs.claim(tab.Key, "app.json", tptr+"/key")
			if _, ok := u.routes[tab.InitialRoute]; !ok {
				u.report(plxerr.UnknownRoute, "app.json", tptr+"/initialRoute", "no page or native route is named %q", tab.InitialRoute)
			}
		}
	}
}

// deepLink checks one path pattern: it maps to a page route, each of its
// {name} segments names a parameter of that page once, of a type a
// link's text converts to, it leaves the reserved prefix alone, and no
// earlier pattern has the same shape.
func (u *unit) deepLink(link schema.DeepLinkRoute, ptr string, shapes *keys) {
	path := ptr + "/path"
	if link.Path == reservedLinkPrefix || strings.HasPrefix(link.Path, reservedLinkPrefix+"/") {
		u.report(plxerr.ConstraintViolation, "app.json", path, "paths under %s/ are reserved for links by route name", reservedLinkPrefix)
		return
	}
	segments := strings.Split(strings.TrimPrefix(link.Path, "/"), "/")
	shape := make([]string, len(segments))
	r := u.routes[link.Route]
	switch {
	case r == nil:
		u.report(plxerr.UnknownRoute, "app.json", ptr+"/route", "no page is named %q", link.Route)
	case r.page == nil:
		u.report(plxerr.UnknownRoute, "app.json", ptr+"/route", "deep links open plugin pages; %q is a native route", link.Route)
		r = nil
	}
	params := map[string]string{}
	if r != nil {
		for _, p := range r.page.doc.Params {
			params[p.Name] = p.Type
		}
	}
	used := u.newKeys("path parameter")
	for i, s := range segments {
		name, isParam := strings.CutPrefix(s, "{")
		if !isParam {
			shape[i] = s
			continue
		}
		name = strings.TrimSuffix(name, "}")
		shape[i] = "{}"
		used.claim(name, "app.json", path)
		typ, ok := params[name]
		switch {
		case r == nil:
		case !ok:
			u.report(plxerr.UnknownRouteParameter, "app.json", path, "route %q has no parameter %q", link.Route, name)
		case !u.fromLink(typ, r.page.plugin):
			u.report(plxerr.RouteParameterTypeInvalid, "app.json", path, "parameter %q of route %q is a %s, which a link cannot carry", name, link.Route, typ)
		}
	}
	shapes.claim("/"+strings.Join(shape, "/"), "app.json", path)
}

// fromLink reports whether a parameter of this type can be read from a
// link's text: a scalar, or an enum the app or the page's plugin declares.
func (u *unit) fromLink(typ string, pl *plugin) bool {
	te, err := parseTypeExpr(typ)
	if err != nil {
		return true // the declaration reports it
	}
	if te.elem != nil {
		return false
	}
	return linkScalars[te.name] || isEnum(u.project.App.Doc.Types, te.name) || isEnum(pl.doc.Types, te.name)
}

// isEnum reports whether decls declare an enum of this name.
func isEnum(decls []schema.TypeDecl, name string) bool {
	for _, d := range decls {
		if d.Name == name {
			return len(d.Enum) > 0
		}
	}
	return false
}

// resolveHostEvents indexes the app's host events by name (HST-013).
func (u *unit) resolveHostEvents() {
	u.hostEvents = map[string]*schema.HostEventDecl{}
	names := u.newKeys("host event")
	events := u.project.App.Doc.HostEvents
	for i := range events {
		ev := &events[i]
		ptr := plxerr.Pointer("hostEvents", strconv.Itoa(i))
		names.claim(ev.Name, "app.json", ptr+"/name")
		fields := u.newKeys("field")
		for j, f := range ev.Fields {
			fields.claim(f.Name, "app.json", ptr+plxerr.Pointer("fields", strconv.Itoa(j), "name"))
		}
		if _, seen := u.hostEvents[ev.Name]; !seen {
			u.hostEvents[ev.Name] = ev
		}
	}
}

// hasTab reports whether a shell of the app has a tab with this key; the
// runtime switches the enclosing shell's tab (NAV-005).
func (u *unit) hasTab(key string) bool {
	if nav := u.project.App.Doc.Navigation; nav != nil {
		for _, sh := range nav.Shells {
			for _, tab := range sh.Tabs {
				if tab.Key == key {
					return true
				}
			}
		}
	}
	return false
}

// checkShellTabs checks each tab's label as text and its icon as an
// IconData, both of which may bind app-level state (NAV-006).
func (u *unit) checkShellTabs() {
	nav := u.project.App.Doc.Navigation
	if nav == nil || u.appScope == nil {
		return
	}
	for i, sh := range nav.Shells {
		for j, tab := range sh.Tabs {
			ptr := plxerr.Pointer("navigation", "shells", strconv.Itoa(i), "tabs", strconv.Itoa(j))
			c := vctx{file: "app.json", scope: u.appScope, code: plxerr.PropTypeMismatch, from: u.project.App.Doc.ID}
			c.ptr = ptr + "/label"
			u.checkRaw(c, tab.Label, &texpr{name: "string"})
			c.ptr = ptr + "/icon"
			u.checkRaw(c, tab.Icon, &texpr{name: "IconData"})
		}
	}
}

// eventPayload checks an emitHostEvent payload against the fields the
// host event declares: a nullable field may be left out (HST-013).
func (u *unit) eventPayload(g *graph, raw json.RawMessage, c vctx) *value {
	st := g.steps[stepIndex(c.ptr)]
	ev := u.hostEvents[literalString(st.Input["event"])]
	if ev == nil {
		return nil // the event input reports it
	}
	params := make([]schema.Param, 0, len(ev.Fields))
	for _, f := range ev.Fields {
		te, err := parseTypeExpr(f.Type)
		if err != nil {
			continue
		}
		required := !te.nullable
		params = append(params, schema.Param{Name: f.Name, Type: f.Type, Required: &required})
	}
	pc := c
	pc.code = plxerr.PropTypeMismatch
	return u.fieldValues(raw, params, "host event "+strconv.Quote(ev.Name), pc)
}
