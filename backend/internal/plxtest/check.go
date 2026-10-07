// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package plxtest

import (
	"slices"
	"strconv"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema"
)

// names are what a project declares and scenarios may refer to.
type names struct {
	routes, exposed, sources map[string]bool
}

func declared(p *schema.Project) names {
	n := names{routes: map[string]bool{}, exposed: map[string]bool{}, sources: map[string]bool{}}
	if p.App.Doc != nil {
		for _, s := range p.App.Doc.State {
			if s.Exposed != nil && *s.Exposed {
				n.exposed[s.Name] = true
			}
		}
		for _, d := range p.App.Doc.DataSources {
			n.sources[d.Name] = true
		}
	}
	for _, pl := range p.Plugins {
		n.addPlugin(pl)
	}
	return n
}

// addPlugin records the data sources of a plugin and the routes and data
// sources of its pages.
func (n names) addPlugin(pl schema.Plugin) {
	if pl.Doc != nil {
		for _, d := range pl.Doc.DataSources {
			n.sources[d.Name] = true
		}
	}
	for _, pg := range pl.Pages {
		if pg.Doc == nil {
			continue
		}
		if pg.Doc.Route != "" {
			n.routes[pg.Doc.Route] = true
		}
		for _, d := range pg.Doc.DataSources {
			n.sources[d.Name] = true
		}
	}
}

// Check reports what the scenarios of a file refer to that the project
// does not declare, and what plux test cannot run yet: a page route that
// no page has, an app state entry that is not exposed (the runtime's
// state API reaches only exposed entries, STA-030), a data source that is
// not declared, a flow, and a data source mock value, which would need a
// release of its own.
func Check(p *schema.Project, f *File) []Finding {
	c := &checker{n: declared(p), f: f}
	for i, s := range f.Doc.Scenarios {
		c.scenario(i, s)
	}
	return c.out
}

// checker collects the findings of one scenario file.
type checker struct {
	n   names
	f   *File
	out []Finding
}

func (c *checker) add(code plxerr.Code, ptr []string, format string, args ...any) {
	fd := Finding{Diagnostic: plxerr.NewDiagnostic(code, plxerr.Location{File: c.f.Path, Path: plxerr.Pointer(ptr...)}, format, args...)}
	fd.Line, fd.Column = c.f.Locate(fd.Path)
	c.out = append(c.out, fd)
}

// scenario checks the scenario with index i.
func (c *checker) scenario(i int, s schema.Scenario) {
	at := []string{"scenarios", strconv.Itoa(i)}
	sub := func(more ...string) []string { return append(slices.Clone(at), more...) }
	if s.Flow != "" {
		c.add(plxerr.ScenarioUnsupported, sub("flow"), "scenario %q tests the flow %q; flows cannot be started on their own", s.Name, s.Flow)
	}
	if s.Page != "" && !c.n.routes[s.Page] {
		c.add(plxerr.ScenarioReferenceUnknown, sub("page"), "scenario %q starts at the route %q, which no page has", s.Name, s.Page)
	}
	if g := s.Given; g != nil {
		c.given(g, sub)
	}
	for j, e := range s.Expect {
		for _, k := range sortedKeys(e.StateEquals) {
			if !c.n.exposed[k] {
				c.add(plxerr.ScenarioReferenceUnknown, sub("expect", strconv.Itoa(j), "stateEquals", k), "app state %q is not an exposed entry of the app", k)
			}
		}
	}
}

// given checks the preconditions of a scenario; sub extends the scenario's
// path.
func (c *checker) given(g *schema.ScenarioGiven, sub func(more ...string) []string) {
	for _, k := range sortedKeys(g.State) {
		if !c.n.exposed[k] {
			c.add(plxerr.ScenarioReferenceUnknown, sub("given", "state", k), "app state %q is not an exposed entry of the app", k)
		}
	}
	for _, k := range sortedKeys(g.DataSources) {
		if !c.n.sources[k] {
			c.add(plxerr.ScenarioReferenceUnknown, sub("given", "dataSources", k), "the project declares no data source %q", k)
		}
		if g.DataSources[k].Mock != nil {
			c.add(plxerr.ScenarioUnsupported, sub("given", "dataSources", k, "mock"), "a mock value replacing the declared mock of %q is not supported; select the declared mock by its state", k)
		}
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
