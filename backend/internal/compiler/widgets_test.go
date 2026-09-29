// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/icons/fonts"

	"github.com/nightCode42/plux3/backend/internal/bundle"
	"github.com/nightCode42/plux3/backend/internal/bundle/fbs"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema/registry"
)

// widgetsDir is the widget gallery: pages that use every widget of the
// phases reached, for the runtime's rendering tests (ADR-0031).
var widgetsDir = filepath.Join("..", "..", "..", "schema", "testdata", "documents", "widgets")

// Verifies: CMP-002, QA-003.
// The widget gallery compiles without diagnostics, uses every P3 Layer 1
// widget, and its bundles are pinned byte for byte.
func TestWidgetsGoldenBundles(t *testing.T) {
	t.Parallel()
	opts := DefaultOptions()
	opts.IconFont = fonts.Build
	res := Compile(os.DirFS(widgetsDir), opts)
	if len(res.Diagnostics) > 0 {
		t.Fatalf("diagnostics:\n%s", list(res.Diagnostics))
	}
	used := map[uint32]bool{}
	for _, b := range readAll(t, res) {
		for _, s := range b.Sections {
			var nodes func(*fbs.Node, int) bool
			var count int
			switch s.Kind {
			case bundle.SectionPage:
				pg := fbs.GetRootAsPage(s.Data, 0)
				nodes, count = pg.Nodes, pg.NodesLength()
			case bundle.SectionComponent:
				c := fbs.GetRootAsComponent(s.Data, 0)
				nodes, count = c.Nodes, c.NodesLength()
			default:
				continue
			}
			var n fbs.Node
			for i := range count {
				if nodes(&n, i) {
					used[n.Widget()] = true
				}
			}
		}
	}
	for _, w := range registry.Widgets() {
		if w.Phase == "P3" && w.Layer == 1 && !used[w.ID] {
			t.Errorf("the gallery does not use %s", w.Type)
		}
	}
	for _, b := range append([]*Bundle{res.App}, res.Plugins...) {
		checkGolden(t, filepath.Join(goldenRoot, "widgets", b.Key+".pxb"), b.Data)
	}
	checkGoldenFiles(t, filepath.Join(goldenRoot, "widgets", "icons"), res.Files)
	if !slices.ContainsFunc(res.Plugins, func(b *Bundle) bool { return b.Key == "gallery" }) {
		t.Error("no gallery plugin")
	}
}

// Verifies: PXL-002.
// A handler reads its event through the payload type the registry
// declares, even a registry value type: a RangeSlider's RangeValues has
// start and end (the gallery stores event.start), and nothing else.
func TestRegistryTypedEventPayload(t *testing.T) {
	t.Parallel()
	m := project(t, widgetsDir)
	const page = "plugins/gallery/pages/inputs.page.json"
	edit(t, m, page, func(doc map[string]any) {
		var find func(any) map[string]any
		find = func(v any) map[string]any {
			switch v := v.(type) {
			case map[string]any:
				if v["type"] == "RangeSlider" {
					return v
				}
				for _, c := range v {
					if n := find(c); n != nil {
						return n
					}
				}
			case []any:
				for _, c := range v {
					if n := find(c); n != nil {
						return n
					}
				}
			}
			return nil
		}
		step := at(t, find(doc), "events/onChanged/steps/0/input/value")
		step["$expr"] = "event.middle"
	})
	res := Compile(m, DefaultOptions())
	if len(res.Diagnostics) != 1 || res.Diagnostics[0].Code != plxerr.PXLUnknownField || res.Diagnostics[0].File != page {
		t.Fatalf("event.middle:\n%s", list(res.Diagnostics))
	}
}
