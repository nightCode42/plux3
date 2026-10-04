// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"testing"

	"github.com/nightCode42/plux3/backend/internal/bundle"
	"github.com/nightCode42/plux3/backend/internal/bundle/fbs"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

const counterCard = "plugins/nav/components/counter-card.component.json"

// TestExportedComponentsAreIndexed checks that a plugin bundle lists its
// exported components by key, which PluxView shows by name (ADR-0023).
// Verifies: NAV-004.
func TestExportedComponentsAreIndexed(t *testing.T) {
	t.Parallel()
	res := compileFS(project(t, routingDir))
	if res.Diagnostics.HasErrors() {
		t.Fatalf("diagnostics:\n%v", res.Diagnostics)
	}
	var meta *fbs.Meta
	for _, b := range readAll(t, res) {
		if b.Kind != bundle.KindPlugin {
			continue
		}
		for _, s := range b.Sections {
			if s.Kind == bundle.SectionMeta {
				meta = fbs.GetRootAsMeta(s.Data, 0)
			}
		}
	}
	if meta == nil || meta.ComponentsLength() != 1 {
		t.Fatal("the plugin bundle lists no exported component")
	}
	var e fbs.ComponentEntry
	meta.Components(&e, 0)
	if string(e.Key()) != "counter-card" {
		t.Errorf("exported %q", e.Key())
	}
}

// TestMixedScreenRules checks ADR-0023's rules: only app state is exposed,
// and an exported component's key names it alone.
// Verifies: NAV-004, HST-021.
func TestMixedScreenRules(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		edit func(*testing.T, map[string]any)
		file string
		code plxerr.Code
		path string
	}{
		{"exposed page state", func(t *testing.T, doc map[string]any) {
			doc["state"] = raw(t, `[{"id": "01f0c450-6c00-7000-8000-0000000002f0", "name": "x", "type": "int", "default": 0, "exposed": true}]`)
		}, "plugins/nav/pages/mixed.page.json", plxerr.InvalidStructure, "/state/0/exposed"},
		{"exported component named like a route", func(_ *testing.T, doc map[string]any) {
			doc["key"] = "home"
		}, counterCard, plxerr.DuplicateRouteName, "/key"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			m := project(t, routingDir)
			edit(t, m, c.file, func(doc map[string]any) { c.edit(t, doc) })
			res := compileFS(m)
			if !hasDiag(res.Diagnostics, c.code, c.file, c.path) {
				t.Errorf("want %v at %s#%s, got:\n%v", c.code, c.file, c.path, res.Diagnostics)
			}
		})
	}
}

func hasDiag(ds plxerr.Diagnostics, code plxerr.Code, file, path string) bool {
	for _, d := range ds {
		if d.Code == code && d.File == file && d.Path == path {
			return true
		}
	}
	return false
}
