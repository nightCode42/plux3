// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/bundle"
	"github.com/nightCode42/plux3/backend/internal/bundle/fbs"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema/registry"
)

// featuresDir is the conformance project that exercises what the loan
// calculator does not: components, slots, templates, native slots and
// actions, responsive overrides and the optimiser.
var featuresDir = filepath.Join("..", "..", "..", "schema", "testdata", "documents", "features")

// IDs in the features project.
const (
	listPageID    = "01b0c450-6c00-7000-8000-00000000000b"
	outerPadding  = "01b0c450-6c00-7000-8000-00000000004a"
	innerPadding  = "01b0c450-6c00-7000-8000-00000000004b"
	foldedText    = "01b0c450-6c00-7000-8000-00000000004c"
	responsiveTxt = "01b0c450-6c00-7000-8000-00000000004d"
	listItem      = "01b0c450-6c00-7000-8000-000000000050"
	mapView       = "01b0c450-6c00-7000-8000-000000000057"
	hiddenText    = "01b0c450-6c00-7000-8000-000000000059"
)

// compileFeatures compiles the features project.
func compileFeatures(t *testing.T, mode Mode) *Result {
	t.Helper()
	opts := DefaultOptions()
	opts.Mode = mode
	res := Compile(os.DirFS(featuresDir), opts)
	if len(res.Diagnostics) > 0 {
		t.Fatalf("diagnostics:\n%s", list(res.Diagnostics))
	}
	return res
}

// pageNodes returns the nodes of a page section by UUID, and its strings.
func pageNodes(t *testing.T, b *bundle.Bundle, id string) (map[string]*fbs.Node, []string) {
	t.Helper()
	s, ok := b.Section(bundle.SectionPage, bundle.ID(uuidBytes(id)))
	if !ok {
		t.Fatalf("no page section %s", id)
	}
	pg := fbs.GetRootAsPage(s.Data, 0)
	strs := make([]string, pg.StringsLength())
	for i := range strs {
		strs[i] = string(pg.Strings(i))
	}
	nodes := map[string]*fbs.Node{}
	for i := range pg.NodesLength() {
		n := new(fbs.Node)
		pg.Nodes(n, i)
		var u fbs.Uuid
		n.Id(&u)
		var raw [16]byte
		binary.BigEndian.PutUint64(raw[:8], u.Hi())
		binary.BigEndian.PutUint64(raw[8:], u.Lo())
		nodes[uuidString(raw)] = n
	}
	return nodes, strs
}

// prop returns the value of a node's prop with the permanent ID.
func propValue(n *fbs.Node, id uint32) (*fbs.Value, bool) {
	var p fbs.Prop
	for i := range n.PropsLength() {
		n.Props(&p, i)
		if p.Id() == id {
			return p.Value(nil), true
		}
	}
	return nil, false
}

// Verifies: CMP-003, CMP-021, CMP-022, CMP-024, BND-015, BND-016, BND-017, WGT-010, SCH-030, SCH-032.
func TestCompileFeaturesProject(t *testing.T) {
	t.Parallel()
	res := compileFeatures(t, Release)
	read := readAll(t, res)
	if len(read) != 2 || res.Plugins[0].Key != "tasks" {
		t.Fatalf("bundles %d", len(read))
	}
	nodes, strs := pageNodes(t, read[1], listPageID)
	text, _ := registry.LookupWidget("Text")
	padding, _ := registry.LookupWidget("Padding")
	data, _ := text.Prop("data")
	pad, _ := padding.Prop("padding")

	t.Run("padding flattened", func(t *testing.T) {
		if _, ok := nodes[innerPadding]; ok {
			t.Error("the inner Padding is still encoded")
		}
		ref, ok := propValue(nodes[outerPadding], pad.ID)
		if !ok || ref.Kind() != fbs.ValueKindStyle {
			t.Fatal("the outer Padding has no padding style")
		}
		v := styleValue(t, read[1], uint64(ref.I())) //nolint:gosec // G115: the ID's bits are stored as they are.
		vt, _ := registry.LookupValueType("EdgeInsets")
		got := map[string]float64{}
		var e fbs.Entry
		for i := range v.EntriesLength() {
			v.Entries(&e, i)
			got[fieldName(vt, e.Key())] = e.Value(nil).D()
		}
		want := map[string]float64{"left": 12, "top": 8, "right": 12, "bottom": 8}
		for k, w := range want {
			if got[k] != w {
				t.Errorf("%s = %v, want %v (%v)", k, got[k], w, got)
			}
		}
	})
	t.Run("constant folded", func(t *testing.T) {
		v, ok := propValue(nodes[foldedText], data.ID)
		if !ok || v.Kind() != fbs.ValueKindString || strs[v.S()] != "Tasks 3" {
			t.Errorf("the folded text is %v", v)
		}
		if nodes[foldedText].Hints()&byte(fbs.NodeHintsStatic) == 0 {
			t.Error("the folded text is not static")
		}
	})
	t.Run("invisible removed", func(t *testing.T) {
		if _, ok := nodes[hiddenText]; ok {
			t.Error("a node with visible false is encoded")
		}
	})
	t.Run("responsive overrides", func(t *testing.T) {
		n := nodes[responsiveTxt]
		if n.OverridesLength() != 2 {
			t.Fatalf("%d overrides", n.OverridesLength())
		}
		var o fbs.Override
		n.Overrides(&o, 0)
		if o.Kind() != fbs.OverrideKindSizeClass || strs[o.Key()] != "medium" {
			t.Errorf("first override %v %q", o.Kind(), strs[o.Key()])
		}
		if n.Hints()&byte(fbs.NodeHintsStatic) != 0 {
			t.Error("a node with overrides is static")
		}
	})
	t.Run("template item", func(t *testing.T) {
		n := nodes[listItem]
		if n.Hints()&byte(fbs.NodeHintsRepaintBoundary) == 0 || n.Component(nil) == nil {
			t.Errorf("list item: hints %v", n.Hints())
		}
	})
	t.Run("native slot", func(t *testing.T) {
		n := nodes[mapView]
		if n.Widget() != 0 || n.NativeSlot() == 0 || strs[n.NativeSlot()] != "MapView" {
			t.Errorf("MapView: widget %d, native slot %d", n.Widget(), n.NativeSlot())
		}
	})
	t.Run("page root", func(t *testing.T) {
		pg := fbs.GetRootAsPage(mustSection(t, read[1], bundle.SectionPage, listPageID).Data, 0)
		var root fbs.Node
		pg.Nodes(&root, 0)
		if root.Hints()&byte(fbs.NodeHintsRepaintBoundary) == 0 {
			t.Error("the page root has no repaint boundary")
		}
	})
	t.Run("components", func(t *testing.T) {
		// The shared badge is compiled once into the app bundle, the
		// plugin's row into the plugin bundle (CMP-021).
		for i, b := range read {
			var comps int
			for _, s := range b.Sections {
				if s.Kind == bundle.SectionComponent {
					comps++
				}
			}
			if comps != 1 {
				t.Errorf("bundle %d has %d component sections", i, comps)
			}
		}
	})
}

// styleValue returns the value of a style in a bundle's styles section.
func styleValue(t *testing.T, b *bundle.Bundle, id uint64) *fbs.Value {
	t.Helper()
	for _, s := range b.Sections {
		if s.Kind != bundle.SectionStyles {
			continue
		}
		st := fbs.GetRootAsStyles(s.Data, 0)
		var style fbs.Style
		if st.StylesByKey(&style, id) {
			return style.Value(nil)
		}
	}
	t.Fatalf("no style %x", id)
	return nil
}

// mustSection returns a section of a bundle by UUID.
func mustSection(t *testing.T, b *bundle.Bundle, k bundle.SectionKind, id string) bundle.Section {
	t.Helper()
	s, ok := b.Section(k, bundle.ID(uuidBytes(id)))
	if !ok {
		t.Fatalf("no %s section %s", k, id)
	}
	return s
}

// Verifies: CMP-002, QA-003.
// The bundles of the features project are pinned byte for byte, in both
// modes.
func TestFeaturesGoldenBundles(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(goldenRoot, "features")
	for _, mode := range []Mode{Release, Development} {
		res := compileFeatures(t, mode)
		readAll(t, res)
		for _, b := range append([]*Bundle{res.App}, res.Plugins...) {
			name := b.Key + ".pxb"
			if mode == Development {
				name = b.Key + ".dev.pxb"
			}
			checkGolden(t, filepath.Join(dir, name), b.Data)
		}
	}
}

// Verifies: WGT-004, BND-008.
// When the app allows it, using a registry revision newer than the
// minimum runtime warns and adds the revision's feature to the bundle
// that uses it.
func TestRaisedFeaturesReachTheBundle(t *testing.T) {
	t.Parallel()
	m := project(t, featuresDir)
	edit(t, m, "app.json", func(doc map[string]any) {
		doc["minRuntimeVersion"] = "0.0.9"
		doc["requiredFeatures"] = "raise"
	})
	res := compileFS(m)
	if res.Diagnostics.HasErrors() || res.Plugins == nil {
		t.Fatalf("diagnostics:\n%s", list(res.Diagnostics))
	}
	pl := res.Plugins[0]
	for _, want := range []string{"pxl.v1", "widget.Scaffold.v1", "widget.ListView.v1", "type.EdgeInsets.v1"} {
		if !slices.Contains(pl.Features, want) {
			t.Errorf("plugin features %v lack %s", pl.Features, want)
		}
	}
	if !slices.IsSorted(pl.Features) {
		t.Errorf("features are not sorted: %v", pl.Features)
	}
	meta := readAll(t, res)[1].Meta
	if meta.RequiredFeaturesLength() != len(pl.Features) {
		t.Errorf("meta lists %d features, the result %d", meta.RequiredFeaturesLength(), len(pl.Features))
	}
	for _, d := range res.Diagnostics {
		if d.Code != plxerr.RequiredFeaturesRaised {
			t.Errorf("unexpected diagnostic %s", d)
		}
	}
}

// Verifies: PXL-002.
// The body of a forEach sees `item`, typed by the loop's items, and
// `index`; the steps after the loop do not.
func TestForEachBodyScope(t *testing.T) {
	t.Parallel()
	const listFile = "plugins/tasks/pages/list.page.json"
	const button = "root/slots/body/children/8/events/onPressed"
	for _, tc := range []struct {
		step, input, expr string
		code              plxerr.Code
	}{
		{"2", "props/title", "item.nothing", plxerr.PXLUnknownField},
		{"2", "props/title", "index + \"x\"", plxerr.PXLTypeMismatch},
		{"1", "items", "[item]", plxerr.PXLUnknownIdentifier},
		{"4", "record/text", "item.title", plxerr.PXLUnknownIdentifier},
	} {
		m := project(t, featuresDir)
		edit(t, m, listFile, func(doc map[string]any) {
			in := at(t, doc, button+"/steps/"+tc.step+"/input")
			parent, name := in, tc.input
			if dir, leaf, ok := strings.Cut(tc.input, "/"); ok {
				parent, name = at(t, in, dir), leaf
			}
			parent[name] = map[string]any{"$expr": tc.expr}
		})
		res := compileFS(m)
		wantDiag(t, res, tc.code, listFile, "/"+button+"/steps/"+tc.step+"/input/"+tc.input)
	}
}

// Verifies: PXL-002.
// In nested loops the inner body sees the inner loop's item, whose items
// may read the outer item.
func TestNestedForEachScope(t *testing.T) {
	t.Parallel()
	const listFile = "plugins/tasks/pages/list.page.json"
	const steps = "root/slots/body/children/8/events/onPressed/steps"
	for expr, code := range map[string]plxerr.Code{
		"item + string(index)": 0,
		"item.title":           plxerr.PXLTypeMismatch,
	} {
		m := project(t, featuresDir)
		edit(t, m, listFile, func(doc map[string]any) {
			at(t, doc, steps+"/1/branches")["body"] = "inner"
			log := at(t, doc, steps+"/2")
			log["input"].(map[string]any)["props"].(map[string]any)["title"] = map[string]any{"$expr": expr}
			parent := at(t, doc, "root/slots/body/children/8/events/onPressed")
			parent["steps"] = append(parent["steps"].([]any), raw(t,
				`{"id": "inner", "action": "forEach", "input": {"items": {"$expr": "[item.title]"}}, "branches": {"body": "log"}}`))
		})
		res := compileFS(m)
		if code == 0 {
			if len(res.Diagnostics) > 0 {
				t.Errorf("%s:\n%s", expr, list(res.Diagnostics))
			}
			continue
		}
		wantDiag(t, res, code, listFile, "/"+steps+"/2/input/props/title")
	}
}

// Verifies: CMP-020.
// Strings are interned per section, identical style objects are stored
// once, and props equal to their descriptor default are omitted.
func TestInterningDeduplicationAndDefaults(t *testing.T) {
	t.Parallel()
	m := fixture(t)
	edit(t, m, calculatorPage, func(doc map[string]any) {
		col := at(t, doc, column)
		col["props"].(map[string]any)["mainAxisAlignment"] = "start" // the default
		col["props"].(map[string]any)["mainAxisSize"] = "min"
		children := col["children"].([]any)
		for i, id := range map[int]string{1: "01a0c450-6c00-7fff-8000-000000000101", 3: "01a0c450-6c00-7fff-8000-000000000103"} {
			children[i] = map[string]any{
				"id": id, "type": "Padding", "props": raw(t, `{"padding": {"all": 4}}`),
				"slots": map[string]any{"child": children[i]},
			}
		}
	})
	res := compileFS(m)
	if len(res.Diagnostics) > 0 {
		t.Fatalf("diagnostics:\n%s", list(res.Diagnostics))
	}
	b := readAll(t, res)[1]
	nodes, strs := pageNodes(t, b, calculatorID)
	seen := map[string]bool{}
	for _, s := range strs {
		if seen[s] {
			t.Errorf("string %q is stored twice", s)
		}
		seen[s] = true
	}
	colWidget, _ := registry.LookupWidget("Column")
	alignment, _ := colWidget.Prop("mainAxisAlignment")
	size, _ := colWidget.Prop("mainAxisSize")
	col := nodes["01a0c450-6c00-7024-8000-00000004599c"]
	if _, ok := propValue(col, alignment.ID); ok {
		t.Error("the default mainAxisAlignment is encoded")
	}
	if _, ok := propValue(col, size.ID); !ok {
		t.Error("mainAxisSize is missing")
	}
	padding, _ := registry.LookupWidget("Padding")
	pad, _ := padding.Prop("padding")
	a, _ := propValue(nodes["01a0c450-6c00-7fff-8000-000000000101"], pad.ID)
	c, _ := propValue(nodes["01a0c450-6c00-7fff-8000-000000000103"], pad.ID)
	if a == nil || c == nil || a.Kind() != fbs.ValueKindStyle || a.I() != c.I() {
		t.Fatal("the two paddings do not share a style")
	}
	count := func(b *bundle.Bundle) (n int) {
		for _, s := range b.Sections {
			if s.Kind == bundle.SectionStyles {
				n += fbs.GetRootAsStyles(s.Data, 0).StylesLength()
			}
		}
		return n
	}
	if got, base := count(b), count(readAll(t, compileFS(fixture(t)))[1]); got != base+1 {
		t.Errorf("%d styles, want %d and the one shared EdgeInsets", got, base)
	}
}

// Verifies: PXL-002.
// Inside an If whose condition checks a path is not null, the then slot
// reads it as not null, and the else slot does when the check is == null;
// outside the If it stays nullable.
func TestIfNarrowsItsSlots(t *testing.T) {
	t.Parallel()
	const ifNode = column + "/children/6"
	for _, tc := range []struct {
		name, cond, slot, expr string
		code                   plxerr.Code
	}{
		{"then", "plugin.lastSchedule != null", "then", "string(plugin.lastSchedule.monthlyPayment)", 0},
		{"else", "plugin.lastSchedule == null", "else", "string(plugin.lastSchedule.monthlyPayment)", 0},
		{"wrong slot", "plugin.lastSchedule == null", "then", "string(plugin.lastSchedule.monthlyPayment)", plxerr.PXLNullableAccess},
		{"other condition", "page.months > 3", "then", "string(plugin.lastSchedule.monthlyPayment)", plxerr.PXLNullableAccess},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := fixture(t)
			edit(t, m, calculatorPage, func(doc map[string]any) {
				col := at(t, doc, column)
				col["children"] = append(col["children"].([]any), map[string]any{
					"id": "01a0c450-6c00-7fff-8000-000000000201", "type": "If",
					"props": map[string]any{"condition": map[string]any{"$expr": tc.cond}},
					"slots": map[string]any{
						"then": map[string]any{"id": "01a0c450-6c00-7fff-8000-000000000203", "type": "Text", "props": map[string]any{"data": "-"}},
						tc.slot: map[string]any{
							"id": "01a0c450-6c00-7fff-8000-000000000202", "type": "Text",
							"props": map[string]any{"data": map[string]any{"$expr": tc.expr}},
						},
					},
				})
			})
			res := compileFS(m)
			if tc.code == 0 {
				if len(res.Diagnostics) > 0 {
					t.Fatalf("diagnostics:\n%s", list(res.Diagnostics))
				}
				return
			}
			wantDiag(t, res, tc.code, calculatorPage, "/"+ifNode+"/slots/"+tc.slot)
		})
	}
}

// Verifies: PXL-002.
// A step entered only from a step's onError reads its error as not null.
func TestOnErrorNarrowsTheError(t *testing.T) {
	t.Parallel()
	for _, onError := range []bool{true, false} {
		m := fixture(t)
		edit(t, m, calculateGraph, func(doc map[string]any) {
			steps := doc["steps"].([]any)
			st := at(t, doc, "steps/1")
			if onError {
				at(t, doc, "steps/0")["onError"] = "report"
			} else {
				st["next"] = "report"
			}
			doc["steps"] = append(steps, raw(t, `{"id": "report", "action": "showSnackbar",
				"input": {"message": {"$expr": "steps.store.error.message"}}}`))
		})
		res := compileFS(m)
		nullable := false
		for _, d := range res.Diagnostics {
			nullable = nullable || d.Code == plxerr.PXLNullableAccess
		}
		if nullable == onError {
			t.Errorf("onError %v: diagnostics:\n%s", onError, list(res.Diagnostics))
		}
	}
}
