// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// requireAssurance makes the plugin's first source ask for a level and,
// when route is not empty, reach its origin that way.
func requireAssurance(level, route string) func(*testing.T, fstest.MapFS) {
	return onSource("0", func(_ *testing.T, src, _ map[string]any) {
		src["requiresAssurance"] = level
		if route != "" {
			src["route"] = route
		}
	})
}

// Verifies: SEC-007.
// A source that asks for an assurance level carries it in its
// configuration, for the device to enforce; AL0 asks for nothing.
func TestDataSourceAssuranceIsEncoded(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ level, want string }{{"AL2", "AL2"}, {"AL0", ""}, {"", ""}} {
		m := project(t, dataDir)
		if c.level != "" {
			requireAssurance(c.level, "")(t, m)
		}
		res := compileFS(m)
		onlyRaised(t, res)
		cfg, strs := sourceConfig(t, readAll(t, res)[1], "tasks")
		got := ""
		if v := valueEntry(cfg, strs, "requiresAssurance"); v != nil {
			got = strs(v.S())
		}
		if got != c.want {
			t.Errorf("requiresAssurance %q: the bundle carries %q, want %q", c.level, got, c.want)
		}
	}
}

// Verifies: SEC-007, SEC-030.
// A direct source that asks for an assurance level is warned about
// (PLX-1504), because only the gateway can enforce the level on the
// server; a gateway source, and a direct one that asks for none, are not.
func TestDirectSourceAssuranceIsWarnedAbout(t *testing.T) {
	t.Parallel()
	const ptr = "/dataSources/0/requiresAssurance"
	m := project(t, dataDir)
	requireAssurance("AL1", "direct")(t, m)
	wantDiag(t, compileFS(m), plxerr.DirectSourceAssuranceUnenforceable, shopFile, ptr)

	for name, edit := range map[string]func(*testing.T, fstest.MapFS){
		"through the gateway": requireAssurance("AL1", "plux"),
		"by default":          requireAssurance("AL1", ""),
		"direct, AL0":         requireAssurance("AL0", "direct"),
	} {
		m := project(t, dataDir)
		edit(t, m)
		if hasDiag(compileFS(m).Diagnostics, plxerr.DirectSourceAssuranceUnenforceable, shopFile, ptr) {
			t.Errorf("%s: PLX-1504 reported", name)
		}
	}
}

// betaGate is the routing project's guard flow.
const betaGate = "plugins/nav/actions/beta-gate.graph.json"

// guardReading makes the routing project's guard decide on an expression.
func guardReading(t *testing.T, when string) *Result {
	t.Helper()
	m := project(t, routingDir)
	edit(t, m, betaGate, func(doc map[string]any) {
		at(t, doc, "steps/0/input")["when"] = map[string]any{"$expr": when}
	})
	return compileFS(m)
}

// Verifies: NAV-009.
// A guard reads the app's state like a page expression does, and a path
// that names no state is reported where it is written (B19 (a)).
func TestGuardsReadAppState(t *testing.T) {
	t.Parallel()
	base := compileFS(project(t, routingDir)).Diagnostics
	if got := guardReading(t, "app.counter > 0").Diagnostics; len(got) != len(base) {
		t.Errorf("a guard reading app.counter:\n%s", list(got))
	}
	for _, when := range []string{"app.nothing > 0", "plugin.nothing > 0"} {
		res := guardReading(t, when)
		found := false
		for _, d := range res.Diagnostics {
			found = found || d.File == betaGate && strings.HasPrefix(d.Path, "/steps/0/input/when")
		}
		if !found {
			t.Errorf("%q: no diagnostic at the guard's condition:\n%s", when, list(res.Diagnostics))
		}
	}
}
