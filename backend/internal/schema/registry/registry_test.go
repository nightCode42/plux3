// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// root is the repository root, relative to this package.
var root = filepath.Join("..", "..", "..", "..")

// registryBase is the base URI of the registry schemas' $id.
const registryBase = "https://plux.dev/schema/v1/registry/"

// compileRegistrySchemas compiles schema/json/registry/*.schema.json.
func compileRegistrySchemas(t *testing.T) map[string]*jsonschema.Schema {
	t.Helper()
	dir := filepath.Join(root, "schema", "json", "registry")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	for _, e := range entries {
		doc := readJSON(t, filepath.Join(dir, e.Name()))
		if err := c.AddResource(registryBase+e.Name(), doc); err != nil {
			t.Fatal(err)
		}
	}
	out := map[string]*jsonschema.Schema{}
	for _, name := range []string{"widget-descriptor", "value-type", "enum", "action"} {
		sch, err := c.Compile(registryBase + name + ".schema.json")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out[name] = sch
	}
	return out
}

// readJSON parses a JSON file for jsonschema.
func readJSON(t *testing.T, path string) any {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // G304: test fixtures under the repository root.
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return doc
}

// TestRegistryFilesMatchSchemas validates every registry file against its
// JSON Schema; schemagen checks what a schema cannot express.
//
// Verifies: WGT-001.
func TestRegistryFilesMatchSchemas(t *testing.T) {
	schemas := compileRegistrySchemas(t)
	dirs := map[string]string{
		"schema/widgets/layer1": "widget-descriptor",
		"schema/widgets/layer2": "widget-descriptor",
		"schema/widgets/types":  "value-type",
		"schema/widgets/enums":  "enum",
		"schema/actions":        "action",
	}
	var n int
	for dir, kind := range dirs {
		files, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(dir), "*.json"))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			n++
			if err := schemas[kind].Validate(readJSON(t, f)); err != nil {
				t.Errorf("%s: %v", f, err)
			}
		}
	}
	if want := len(widgets) + len(valueTypes) + len(enums) + len(actions); n != want {
		t.Errorf("validated %d files; the generated tables hold %d entries", n, want)
	}
}

// TestSchemasRejectInvalidDescriptors checks that the schemas reject what
// they must.
//
// Verifies: WGT-001, SCH-023.
func TestSchemasRejectInvalidDescriptors(t *testing.T) {
	sch := compileRegistrySchemas(t)["widget-descriptor"]
	valid := `{"type": "Box", "id": 1, "layer": 1, "phase": "P3", "revision": 1,
		"revisions": [{"revision": 1, "runtime": "0.1.0"}], "category": "layout", "icon": "box",
		"description": "A box.", "platforms": ["android"], "cost": 5,
		"accessibility": {"role": "none", "interactive": false}`
	tests := []struct {
		name, doc string
		ok        bool
	}{
		{"minimal", valid + `}`, true},
		{"children and slots", valid + `, "children": {}, "slots": []}`, false},
		{"unknown property", valid + `, "colour": "red"}`, false},
		{"unknown exclusion reason", valid + `, "excluded": [{"flutter": "key", "reason": "unwanted"}]}`, false},
		{"zero ID", strings.Replace(valid, `"id": 1`, `"id": 0`, 1) + `}`, false},
		{"layer 3", strings.Replace(valid, `"layer": 1`, `"layer": 3`, 1) + `}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := jsonschema.UnmarshalJSON(strings.NewReader(tt.doc))
			if err != nil {
				t.Fatal(err)
			}
			if err := sch.Validate(doc); (err == nil) != tt.ok {
				t.Errorf("valid = %t, want %t: %v", err == nil, tt.ok, err)
			}
		})
	}
}

// TestTablesMatchLock checks every generated ID against the permanent-ID
// lock, so the Go tables encode exactly the recorded IDs.
//
// Verifies: BND-011.
func TestTablesMatchLock(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(root, "schema", "widgets", "ids.lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	var lock struct {
		IDs map[string]uint32 `json:"ids"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		t.Fatal(err)
	}
	want := func(key string, id uint32) {
		t.Helper()
		if got, ok := lock.IDs[key]; !ok || got != id {
			t.Errorf("%s: table ID %d, lock %d (present %t)", key, id, got, ok)
		}
	}
	for _, w := range widgets {
		want("widget/"+w.Type, w.ID)
		for _, p := range w.Props {
			want("widget/"+w.Type+"/prop/"+p.Name, p.ID)
		}
		for _, e := range w.Events {
			want("widget/"+w.Type+"/event/"+e.Name, e.ID)
		}
		for _, s := range w.Slots {
			want("widget/"+w.Type+"/slot/"+s.Name, s.ID)
		}
	}
	for _, vt := range valueTypes {
		want("type/"+vt.Name, vt.ID)
		for _, f := range vt.Fields {
			want("type/"+vt.Name+"/field/"+f.Name, f.ID)
		}
	}
	for _, e := range enums {
		want("enum/"+e.Name, e.ID)
		for _, v := range e.Values {
			want("enum/"+e.Name+"/value/"+v.Name, v.ID)
		}
	}
	for _, a := range actions {
		want("action/"+a.Name, a.ID)
		for _, in := range a.Inputs {
			want("action/"+a.Name+"/input/"+in.Name, in.ID)
		}
	}
}

// TestTablesSortedAndComplete checks the invariants lookups rely on.
//
// Verifies: WGT-002.
func TestTablesSortedAndComplete(t *testing.T) {
	sorted := func(name string, keys []string) {
		t.Helper()
		if !slices.IsSorted(keys) {
			t.Errorf("%s are not sorted", name)
		}
	}
	var names []string
	for _, w := range Widgets() {
		names = append(names, w.Type)
		if len(w.Runtimes) != int(w.Revision) {
			t.Errorf("%s: %d runtimes for revision %d", w.Type, len(w.Runtimes), w.Revision)
		}
		if w.Children != nil && len(w.Slots) > 0 {
			t.Errorf("%s has children and slots", w.Type)
		}
	}
	sorted("widgets", names)
	names = names[:0]
	for _, vt := range ValueTypes() {
		names = append(names, vt.Name)
	}
	sorted("value types", names)
	names = names[:0]
	for _, e := range Enums() {
		names = append(names, e.Name)
	}
	sorted("enums", names)
	names = names[:0]
	for _, a := range Actions() {
		names = append(names, a.Name)
	}
	sorted("actions", names)

	for _, name := range []string{"If", "Match", "ForEach", "Responsive", "Slot"} {
		if w, ok := LookupWidget(name); !ok || w.Layer != 1 || w.Category != "structure" {
			t.Errorf("structural primitive %s: %+v, %t", name, w, ok)
		}
	}
}

// TestLookups exercises the lookup functions.
func TestLookups(t *testing.T) {
	text, ok := LookupWidget("Text")
	if !ok {
		t.Fatal("Text is not registered")
	}
	if p, ok := text.Prop("data"); !ok || p.Type != "string" || !p.Required {
		t.Errorf("Text.data = %+v, %t", p, ok)
	}
	if _, ok := text.Prop("colour"); ok {
		t.Error("Text.colour found")
	}
	if text.Runtime(1) == "" || text.Runtime(0) != "" || text.Runtime(text.Revision+1) != "" {
		t.Errorf("Runtime: %q %q", text.Runtime(1), text.Runtime(0))
	}
	button, _ := LookupWidget("ElevatedButton")
	if e, ok := button.Event("onPressed"); !ok || e.ID == 0 {
		t.Errorf("ElevatedButton.onPressed = %+v, %t", e, ok)
	}
	if _, ok := button.Event("onSwipe"); ok {
		t.Error("ElevatedButton.onSwipe found")
	}
	if s, ok := button.Slot("child"); !ok || s.List {
		t.Errorf("ElevatedButton.child = %+v, %t", s, ok)
	}
	if _, ok := button.Slot("children"); ok {
		t.Error("ElevatedButton.children slot found")
	}
	if _, ok := LookupWidget("Nope"); ok {
		t.Error("unknown widget found")
	}

	insets, ok := LookupValueType("EdgeInsets")
	if f, found := insets.Field("all"); !ok || !found || f.Type != "double" {
		t.Errorf("EdgeInsets.all = %+v", f)
	}
	if _, found := insets.Field("diagonal"); found {
		t.Error("EdgeInsets.diagonal found")
	}
	align, ok := LookupEnum("TextAlign")
	if v, found := align.Value("center"); !ok || !found || v.ID == 0 {
		t.Errorf("TextAlign.center = %+v", v)
	}
	if _, found := align.Value("middle"); found {
		t.Error("TextAlign.middle found")
	}
	set, ok := LookupAction("setState")
	if in, found := set.Input("path"); !ok || !found || in.Ref != "state" || !in.Required {
		t.Errorf("setState.path = %+v", in)
	}
	if _, found := set.Input("target"); found {
		t.Error("setState.target found")
	}
	for _, missing := range []bool{
		func() bool { _, ok := LookupValueType("Nope"); return ok }(),
		func() bool { _, ok := LookupEnum("Nope"); return ok }(),
		func() bool { _, ok := LookupAction("nope"); return ok }(),
	} {
		if missing {
			t.Error("unknown entry found")
		}
	}
}

// TestAccessorsReturnCopies checks that callers cannot reorder the tables.
func TestAccessorsReturnCopies(t *testing.T) {
	ws := Widgets()
	ws[0], ws[1] = ws[1], ws[0]
	if Widgets()[0].Type == ws[0].Type {
		t.Error("Widgets shares its backing array")
	}
}
