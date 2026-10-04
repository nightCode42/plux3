// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// obj is a JSON object in a fixture.
type obj = map[string]any

// fixture returns a small, valid registry: a Layer 1 widget with a Flutter
// counterpart, a structural primitive, a Layer 2 component, a value type
// with a constant, a mirrored and a Plux enum, an action and the snapshot.
func fixture() map[string]obj {
	rev := []any{obj{"revision": 1, "runtime": "0.1.0"}}
	return map[string]obj{
		"schema/widgets/layer1/Box.json": {
			"$schema": "../../json/registry/widget-descriptor.schema.json",
			"type":    "Box", "id": 1, "layer": 1, "phase": "P3", "revision": 1, "revisions": rev,
			"flutter":  obj{"library": "package:flutter/widgets.dart", "class": "Box", "constructors": []any{""}},
			"category": "layout", "icon": "box", "description": "A box.", "platforms": []any{"android", "ios"},
			"cost": 5, "accessibility": obj{"role": "none", "interactive": false},
			"props": []any{
				obj{"name": "width", "id": 1, "type": "double", "default": 0.0, "constraints": obj{"min": 0.0}, "flutter": "width"},
				obj{"name": "padding", "id": 2, "type": "Insets", "default": "zero", "flutter": "padding"},
				obj{"name": "axis", "id": 3, "type": "Axis?", "default": "vertical", "flutter": "axis", "description": "The axis."},
			},
			"events":   []any{obj{"name": "onTap", "id": 1, "flutter": "onTap"}},
			"slots":    []any{obj{"name": "child", "id": 1, "flutter": "child"}},
			"excluded": []any{obj{"flutter": "key", "reason": "non-serialisable", "note": "Keys come from node IDs."}},
		},
		"schema/widgets/layer1/Each.json": {
			"$schema": "../../json/registry/widget-descriptor.schema.json",
			"type":    "Each", "id": 2, "layer": 1, "phase": "P3", "revision": 1, "revisions": rev,
			"category": "structure", "icon": "repeat", "description": "Repeats a template.", "platforms": []any{"android"},
			"cost": 1, "accessibility": obj{"role": "none", "interactive": false}, "typeParameters": []any{"T"},
			"props": []any{obj{"name": "items", "id": 1, "type": "list<T>", "required": true}},
			"slots": []any{obj{"name": "item", "id": 1, "template": true}},
		},
		"schema/widgets/layer2/Panel.json": {
			"$schema": "../../json/registry/widget-descriptor.schema.json",
			"type":    "Panel", "id": 3, "layer": 2, "phase": "P3", "revision": 1, "revisions": rev,
			"category": "feedback", "icon": "panel", "description": "A panel.", "platforms": []any{"ios"},
			"cost": 9, "accessibility": obj{"role": "text", "interactive": false},
			"children": obj{"min": 1, "max": 3},
		},
		"schema/widgets/types/Insets.json": {
			"$schema": "../../json/registry/value-type.schema.json",
			"name":    "Insets", "id": 1, "revision": 1, "revisions": rev, "description": "Insets.",
			"flutter":   []any{obj{"library": "package:flutter/painting.dart", "class": "EdgeInsets", "constructors": []any{"all"}}},
			"fields":    []any{obj{"name": "all", "id": 1, "type": "double", "flutter": "value"}},
			"constants": []any{obj{"name": "zero", "value": obj{"all": 0}}},
		},
		"schema/widgets/enums/Axis.json": {
			"$schema": "../../json/registry/enum.schema.json",
			"name":    "Axis", "id": 1, "revision": 1, "revisions": rev, "description": "Mirrors Axis.",
			"flutter": obj{"library": "package:flutter/widgets.dart", "enum": "Axis"},
			"values":  []any{obj{"name": "horizontal", "id": 1}, obj{"name": "vertical", "id": 2}},
		},
		"schema/widgets/enums/Mode.json": {
			"$schema": "../../json/registry/enum.schema.json",
			"name":    "Mode", "id": 2, "revision": 1, "revisions": rev, "description": "A Plux mode.",
			"values": []any{obj{"name": "on", "id": 1}},
		},
		"schema/actions/store.json": {
			"$schema": "../json/registry/action.schema.json",
			"name":    "store", "id": 1, "phase": "P5", "category": "state", "description": "Stores a value.",
			"typeParameters": []any{obj{"name": "T", "description": "The entry's type."}},
			"inputs": []any{
				obj{"name": "path", "id": 1, "type": "string", "required": true, "ref": "state"},
				obj{"name": "value", "id": 2, "type": "T", "required": true},
				obj{"name": "cases", "id": 3, "type": "list<string>"},
			},
			"output": "T", "branches": []any{"done"}, "branchesFrom": "cases", "effects": []any{"state", "storage"},
		},
		"schema/widgets/flutter-api.json": {
			"flutter": "3.47.5",
			"classes": obj{
				"package:flutter/widgets.dart#Box": obj{"constructors": obj{"": []any{
					obj{"name": "key", "type": "Key?", "required": false, "named": true},
					obj{"name": "width", "type": "double", "required": false, "named": true, "default": "0.0"},
					obj{"name": "padding", "type": "EdgeInsets", "required": false, "named": true},
					obj{"name": "axis", "type": "Axis", "required": false, "named": true},
					obj{"name": "onTap", "type": "void Function()?", "required": false, "named": true},
					obj{"name": "child", "type": "Widget?", "required": false, "named": true},
				}}},
				"package:flutter/painting.dart#EdgeInsets": obj{"constructors": obj{"all": []any{
					obj{"name": "value", "type": "double", "required": true, "named": false},
				}}},
			},
			"enums": obj{"package:flutter/widgets.dart#Axis": []any{obj{"name": "horizontal"}, obj{"name": "vertical"}}},
		},
	}
}

// writeFixture writes files under a new root.
func writeFixture(t *testing.T, files map[string]obj) string {
	t.Helper()
	root := t.TempDir()
	for path, content := range files {
		data, err := json.MarshalIndent(content, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// member returns the i-th element of the list at key in o.
func member(o obj, key string, i int) obj {
	return o[key].([]any)[i].(obj)
}

// Verifies: WGT-001, WGT-003, BND-011.
func TestLoadValidFixture(t *testing.T) {
	t.Parallel()
	r, err := Load(writeFixture(t, fixture()))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Widgets) != 3 || r.Widgets[0].Type != "Box" || r.Widgets[2].Type != "Panel" {
		t.Errorf("widgets not loaded in order: %d", len(r.Widgets))
	}
	if len(r.Coverage) != 2 || len(r.Coverage[0].Params) != 6 || len(r.EnumCoverage) != 1 {
		t.Fatalf("coverage: %+v", r.Coverage)
	}
	if got := r.Coverage[0].Params[0]; got.Exclusion == nil || got.Exclusion.Reason != ReasonNonSerialisable {
		t.Errorf("key: %+v", got)
	}
	if got := r.Coverage[0].Params[4].Members; len(got) != 1 || got[0] != "event onTap" {
		t.Errorf("onTap covered by %v", got)
	}
	for _, key := range []string{
		"widget/Box", "widget/Box/prop/width", "widget/Box/event/onTap", "widget/Box/slot/child",
		"type/Insets/field/all", "enum/Axis/value/vertical", "action/store/input/value",
	} {
		if _, ok := r.Lock.IDs[key]; !ok {
			t.Errorf("lock lacks %s", key)
		}
	}
	if !r.Widgets[0].Props[0].IsBindable() {
		t.Error("props are bindable by default")
	}
}

// TestLoadRejects mutates the valid fixture and expects one problem.
//
// Verifies: WGT-001, WGT-003, WGT-004, BND-011, SCH-023.
func TestLoadRejects(t *testing.T) {
	t.Parallel()
	const box, each, panel = "schema/widgets/layer1/Box.json", "schema/widgets/layer1/Each.json", "schema/widgets/layer2/Panel.json"
	const insets, axis, action = "schema/widgets/types/Insets.json", "schema/widgets/enums/Axis.json", "schema/actions/store.json"
	const api = "schema/widgets/flutter-api.json"
	tests := []struct {
		name   string
		mutate func(f map[string]obj)
		want   string
	}{
		{"unknown field", func(f map[string]obj) { f[box]["colour"] = "red" }, `unknown field "colour"`},
		{"file name", func(f map[string]obj) { f[box]["type"] = "Crate" }, "named after its entry: Crate.json"},
		{"$schema", func(f map[string]obj) { f[box]["$schema"] = "x.json" }, "$schema must be"},
		{"layer directory", func(f map[string]obj) { f[panel]["layer"] = 1; f[panel]["category"] = "structure" }, "belongs in schema/widgets/layer1"},
		{"primitive category", func(f map[string]obj) { f[each]["category"] = "layout" }, "must be a structural primitive"},
		{"layer 2 counterpart", func(f map[string]obj) { f[panel]["flutter"] = f[box]["flutter"] }, "Layer 2 component has no Flutter counterpart"},
		{"duplicate widget ID", func(f map[string]obj) { f[each]["id"] = 1 }, "ID 1 is also used by"},
		{"duplicate prop ID", func(f map[string]obj) { member(f[box], "props", 1)["id"] = 1 }, "ID 1 is also used by width"},
		{"name used twice", func(f map[string]obj) { member(f[box], "slots", 0)["name"] = "width" }, "the name is also used by a prop"},
		{"revisions", func(f map[string]obj) { f[box]["revision"] = 2 }, "revisions must list revisions 1 to 2"},
		{"member revision", func(f map[string]obj) { member(f[box], "props", 0)["revision"] = 2 }, "newer than the entry's revision"},
		{"runtime version", func(f map[string]obj) {
			f[box]["revision"] = 2
			f[box]["revisions"] = []any{obj{"revision": 1, "runtime": "0.2.0"}, obj{"revision": 2, "runtime": "0.1.0"}}
		}, "older than the previous revision"},
		{"bad semver", func(f map[string]obj) { f[box]["revisions"] = []any{obj{"revision": 1, "runtime": "1.0"}} }, "not a semantic version"},
		{"deprecation", func(f map[string]obj) {
			member(f[box], "props", 0)["deprecated"] = obj{"revision": 3, "message": "Use size."}
		}, "outside revisions 1–1"},
		{"sentence", func(f map[string]obj) { f[box]["description"] = "A box" }, "must be a sentence"},
		{"platform order", func(f map[string]obj) { f[box]["platforms"] = []any{"ios", "android"} }, "distinct and in the order"},
		{"role", func(f map[string]obj) { f[box]["accessibility"] = obj{"role": "widget", "interactive": false} }, "unknown accessibility role"},
		{"unknown type", func(f map[string]obj) { member(f[box], "props", 0)["type"] = "Length" }, "unknown type Length"},
		{"type syntax", func(f map[string]obj) { member(f[box], "props", 0)["type"] = "map<int,double>" }, "map keys are strings"},
		{"type parameter unused", func(f map[string]obj) { f[each]["typeParameters"] = []any{"T", "U"} }, "type parameter U is never used"},
		{"type parameter undeclared", func(f map[string]obj) { delete(f[each], "typeParameters") }, "undeclared type parameter T"},
		{"default type", func(f map[string]obj) { member(f[box], "props", 0)["default"] = "wide" }, "is not a double"},
		{"default constant", func(f map[string]obj) { member(f[box], "props", 1)["default"] = "half" }, `"half" is not a constant of Insets`},
		{"default enum", func(f map[string]obj) { member(f[box], "props", 2)["default"] = "diagonal" }, "diagonal is not a value of Axis"},
		{"default and required", func(f map[string]obj) { member(f[box], "props", 0)["required"] = true }, "a required value has no default"},
		{"constraint kind", func(f map[string]obj) { member(f[box], "props", 1)["constraints"] = obj{"min": 1.0} }, "min and max constrain numbers"},
		{"constraint range", func(f map[string]obj) { member(f[box], "props", 0)["constraints"] = obj{"min": 2.0, "max": 1.0} }, "min 2 exceeds max 1"},
		{"default outside constraint", func(f map[string]obj) { member(f[box], "props", 0)["constraints"] = obj{"min": 1.0} }, "outside [min, max]"},
		{"pattern", func(f map[string]obj) { member(f[box], "props", 0)["constraints"] = obj{"pattern": "["} }, "pattern constrains strings"},
		{"template list", func(f map[string]obj) { member(f[each], "slots", 0)["list"] = true }, "a template slot holds one node"},
		{"children bounds", func(f map[string]obj) { f[panel]["children"] = obj{"min": 4, "max": 3} }, "min 4 exceeds max 3"},
		{"children and slots", func(f map[string]obj) { f[box]["children"] = obj{} }, "children or named slots, never both"},
		{"mapping without counterpart", func(f map[string]obj) { member(f[each], "props", 0)["flutter"] = "items" }, "members map no Flutter parameters"},
		{"uncovered parameter", func(f map[string]obj) { f[box]["excluded"] = []any{} }, "Box.key is neither supported nor excluded"},
		{"stale mapping", func(f map[string]obj) { member(f[box], "props", 0)["flutter"] = "breadth" }, "maps Flutter parameter breadth, which no mirrored constructor declares"},
		{"stale exclusion", func(f map[string]obj) {
			f[box]["excluded"] = []any{obj{"flutter": "key", "reason": "non-serialisable"}, obj{"flutter": "size", "reason": "deferred"}}
		}, "excluded Flutter parameter size is declared by no mirrored constructor"},
		{"excluded and covered", func(f map[string]obj) {
			f[box]["excluded"] = []any{obj{"flutter": "key", "reason": "non-serialisable"}, obj{"flutter": "width", "reason": "deferred"}}
		}, "width is both excluded and covered by prop width"},
		{"false deprecation", func(f map[string]obj) {
			f[box]["excluded"] = []any{obj{"flutter": "key", "reason": "deprecated"}}
		}, "excluded as deprecated but Flutter does not deprecate it"},
		{"constructor prop type", func(f map[string]obj) {
			f[box]["props"] = append(f[box]["props"].([]any), obj{"name": "adaptive", "id": 4, "type": "double", "bindable": false, "constructor": "adaptive"})
		}, `a prop selecting constructor "adaptive" is an optional bool that is not bindable`},
		{"bindable constructor prop", func(f map[string]obj) {
			f[box]["props"] = append(f[box]["props"].([]any), obj{"name": "adaptive", "id": 4, "type": "bool", "constructor": "adaptive"})
		}, "an optional bool that is not bindable"},
		{"constructor prop mapping", func(f map[string]obj) {
			f[box]["props"] = append(f[box]["props"].([]any), obj{"name": "adaptive", "id": 4, "type": "bool", "bindable": false, "constructor": "adaptive", "flutter": "width"})
		}, `a prop selecting constructor "adaptive" maps no Flutter parameter`},
		{"constructor not mirrored", func(f map[string]obj) {
			f[box]["props"] = append(f[box]["props"].([]any), obj{"name": "adaptive", "id": 4, "type": "bool", "bindable": false, "constructor": "adaptive"})
		}, `constructor "adaptive" is not among the widget's Flutter constructors`},
		{"missing class", func(f map[string]obj) { delete(f[api]["classes"].(obj), "package:flutter/widgets.dart#Box") }, "run 'make widgets-api'"},
		{"missing constructor", func(f map[string]obj) { f[box]["flutter"].(obj)["constructors"] = []any{"", "tight"} }, `constructor "tight"`},
		{"uncovered enum value", func(f map[string]obj) { f[axis]["values"] = []any{obj{"name": "vertical", "id": 2}} }, "Axis.horizontal is neither supported nor excluded"},
		{"foreign enum value", func(f map[string]obj) {
			f[axis]["values"] = append(f[axis]["values"].([]any), obj{"name": "diagonal", "id": 3})
		}, "value diagonal is not a value of the mirrored enum Axis"},
		{"type and enum clash", func(f map[string]obj) {
			f["schema/widgets/enums/Insets.json"] = obj{
				"$schema": "../../json/registry/enum.schema.json", "name": "Insets", "id": 9,
				"revision": 1, "revisions": []any{obj{"revision": 1, "runtime": "0.1.0"}}, "description": "Clash.", "values": []any{obj{"name": "a", "id": 1}},
			}
		}, "the name Insets is also a value type"},
		{"constant value", func(f map[string]obj) { f[insets]["constants"] = []any{obj{"name": "zero", "value": obj{"none": 0}}} }, `Insets has no field "none"`},
		{"action ref type", func(f map[string]obj) { member(f[action], "inputs", 0)["type"] = "int" }, "an input naming a state is a string"},
		{"action branchesFrom type", func(f map[string]obj) { member(f[action], "inputs", 2)["type"] = "string" }, "branchesFrom names an input of type list<string>"},
		{"action branchesFrom input", func(f map[string]obj) { f[action]["branchesFrom"] = "lanes" }, "branchesFrom names no input lanes"},
		{"action standard branch", func(f map[string]obj) { f[action]["branches"] = []any{"next"} }, "branch next is a standard edge"},
		{"action effects order", func(f map[string]obj) { f[action]["effects"] = []any{"storage", "state"} }, "distinct and in the order"},
		{"action category", func(f map[string]obj) { f[action]["category"] = "misc" }, `unknown category "misc"`},
		{"action unused parameter", func(f map[string]obj) {
			f[action]["typeParameters"] = []any{obj{"name": "T", "description": "Used."}, obj{"name": "U", "description": "Unused."}}
		}, "type parameter U is never used"},
		{"snapshot", func(f map[string]obj) { f[api]["flutter"] = "" }, "names no Flutter version"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			files := fixture()
			tt.mutate(files)
			_, err := Load(writeFixture(t, files))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %v, want %q", err, tt.want)
			}
		})
	}
}

// Verifies: BND-011.
func TestLoadRejectsReassignedAndReusedIDs(t *testing.T) {
	t.Parallel()
	files := fixture()
	files[LockFile] = obj{"$comment": "test", "ids": obj{"widget/Box": 7, "widget/Old": 2}}
	_, err := Load(writeFixture(t, files))
	for _, want := range []string{"widget/Box: ID changed from 7 to 1", "widget/Each: ID 2 already belongs to widget/Old"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("error %v, want %q", err, want)
		}
	}
}

// Verifies: BND-011.
func TestLockKeepsRetiredEntries(t *testing.T) {
	t.Parallel()
	files := fixture()
	files[LockFile] = obj{"$comment": "test", "ids": obj{"widget/Old": 9}}
	r, err := Load(writeFixture(t, files))
	if err != nil {
		t.Fatal(err)
	}
	if r.Lock.IDs["widget/Old"] != 9 || r.Lock.IDs["widget/Box"] != 1 {
		t.Errorf("lock: %v", r.Lock.IDs)
	}
}

// Verifies: BND-011.
func TestLockCheckAppendOnly(t *testing.T) {
	t.Parallel()
	base := Lock{IDs: map[string]uint32{"widget/A": 1, "widget/B": 2}}
	if err := (Lock{IDs: map[string]uint32{"widget/A": 1, "widget/B": 2, "widget/C": 3}}).CheckAppendOnly(base); err != nil {
		t.Errorf("appending: %v", err)
	}
	err := Lock{IDs: map[string]uint32{"widget/A": 5}}.CheckAppendOnly(base)
	for _, want := range []string{"widget/A: ID changed from 1 to 5", "widget/B: removed from the lock"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("error %v, want %q", err, want)
		}
	}
}

func TestParseLock(t *testing.T) {
	t.Parallel()
	tests := []struct {
		ids  map[string]uint32
		want string
	}{
		{map[string]uint32{"widget/A": 1, "widget/A/prop/x": 1, "enum/E/value/v": 1, "action/a/input/i": 1, "type/T/field/f": 1}, ""},
		{nil, ""},
		{map[string]uint32{"gadget/A": 1}, "malformed key"},
		{map[string]uint32{"widget/A/field/x": 1}, "malformed key"},
		{map[string]uint32{"widget//x": 1}, "malformed key"},
		{map[string]uint32{"widget/A": 0}, "IDs start at 1"},
		{map[string]uint32{"widget/A": 1, "widget/B": 1}, "share ID 1"},
	}
	for _, tt := range tests {
		_, err := ParseLock(tt.ids)
		if (err == nil) != (tt.want == "") || err != nil && !strings.Contains(err.Error(), tt.want) {
			t.Errorf("ParseLock(%v) = %v, want %q", tt.ids, err, tt.want)
		}
	}
}

func TestLockEncodeRoundTrip(t *testing.T) {
	t.Parallel()
	l := Lock{IDs: map[string]uint32{"widget/B": 2, "widget/A": 1}}
	path := filepath.Join(t.TempDir(), "ids.lock.json")
	if err := os.WriteFile(path, l.Encode(), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadLock(path)
	if err != nil || len(got.IDs) != 2 || got.IDs["widget/B"] != 2 {
		t.Fatalf("ReadLock = %v, %v", got, err)
	}
	if !strings.Contains(string(l.Encode()), "\"widget/A\": 1,\n    \"widget/B\": 2") {
		t.Errorf("keys are not sorted:\n%s", l.Encode())
	}
	if empty := string(Lock{}.Encode()); !strings.Contains(empty, `"ids": {}`) {
		t.Errorf("empty lock: %s", empty)
	}
	missing, err := ReadLock(filepath.Join(t.TempDir(), "none.json"))
	if err != nil || len(missing.IDs) != 0 {
		t.Errorf("missing lock = %v, %v", missing, err)
	}
	if err := os.WriteFile(path, []byte(`{"ids": {"x": 1}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadLock(path); err == nil {
		t.Error("malformed lock accepted")
	}
}

func TestParseType(t *testing.T) {
	t.Parallel()
	valid := []string{"string", "int?", "list<Insets>", "map<string,list<double?>>?", "T", "list<T>", "money", "route?"}
	for _, s := range valid {
		ty, err := ParseType(s)
		if err != nil {
			t.Errorf("ParseType(%q): %v", s, err)
			continue
		}
		if ty.String() != s {
			t.Errorf("ParseType(%q).String() = %q", s, ty.String())
		}
	}
	invalid := []string{"", "list", "list<>", "list<int", "map<int,string>", "map<string, int>", "integer", "int??", "List<int>x", "?"}
	for _, s := range invalid {
		if _, err := ParseType(s); err == nil {
			t.Errorf("ParseType(%q) accepted", s)
		}
	}
}

func TestCheckLiteral(t *testing.T) {
	t.Parallel()
	ix := &index{
		types: map[string]*ValueType{"Size": {Name: "Size", Fields: []Field{
			{Name: "w", Type: "double", Required: true}, {Name: "h", Type: "double"},
		}, Constants: []Constant{{Name: "zero"}}}},
		enums: map[string]*Enum{"Axis": {Name: "Axis", Values: []EnumValue{{Name: "vertical"}}}},
	}
	tests := []struct {
		typ, lit string
		ok       bool
	}{
		{"string", `"x"`, true},
		{"string", `1`, false},
		{"int", `3`, true},
		{"int", `3.0`, false},
		{"int", `9007199254740992`, false},
		{"int", `"3"`, false},
		{"double", `3`, true},
		{"double", `true`, false},
		{"bool", `false`, true},
		{"bool", `0`, false},
		{"decimal", `"-12.50"`, true},
		{"decimal", `"1e3"`, false},
		{"decimal", `12.5`, false},
		{"money", `{"amount": "9.99", "currency": "EUR"}`, true},
		{"money", `{"amount": "9.99", "currency": "eur"}`, false},
		{"money", `"9.99 EUR"`, false},
		{"date", `"2026-09-26"`, true},
		{"date", `"2026-02-30"`, false},
		{"dateTime", `"2026-09-26T10:00:00+02:00"`, true},
		{"dateTime", `"2026-09-26T10:00:00"`, false},
		{"duration", `300`, true},
		{"duration", `-1`, false},
		{"duration", `1.5`, false},
		{"color", `"#5B3DF5"`, true},
		{"color", `"#5B3DF5CC"`, true},
		{"color", `"5B3DF5"`, false},
		{"route", `"loan-result"`, true},
		{"route", `"Loan"`, false},
		{"asset", `"logo"`, false},
		{"string?", `null`, true},
		{"string", `null`, false},
		{"list<int>", `[1, 2]`, true},
		{"list<int>", `[1, "2"]`, false},
		{"list<int>", `{}`, false},
		{"map<string,bool>", `{"a": true}`, true},
		{"map<string,bool>", `{"a": 1}`, false},
		{"map<string,bool>", `[]`, false},
		{"Axis", `"vertical"`, true},
		{"Axis", `"diagonal"`, false},
		{"Size", `{"w": 1, "h": 2}`, true},
		{"Size", `"zero"`, true},
		{"Size", `{"h": 2}`, false},
		{"Size", `{"w": 1, "d": 2}`, false},
		{"Size", `{"w": "wide"}`, false},
		{"Size", `"one"`, false},
		{"Size", `4`, false},
		{"T", `1`, false},
	}
	for _, tt := range tests {
		ty, err := ParseType(tt.typ)
		if err != nil {
			t.Fatal(err)
		}
		v, err := decodeLiteral(json.RawMessage(tt.lit))
		if err != nil {
			t.Fatal(err)
		}
		if err := ix.checkLiteral(v, ty); (err == nil) != tt.ok {
			t.Errorf("%s %s: error %v, want ok=%t", tt.typ, tt.lit, err, tt.ok)
		}
	}
	if _, err := decodeLiteral(json.RawMessage(`{`)); err == nil {
		t.Error("invalid JSON accepted")
	}
}

func TestFlutterNames(t *testing.T) {
	t.Parallel()
	var n FlutterNames
	if err := json.Unmarshal([]byte(`"a"`), &n); err != nil || len(n) != 1 {
		t.Errorf("string: %v %v", n, err)
	}
	if err := json.Unmarshal([]byte(`["a", "b"]`), &n); err != nil || len(n) != 2 {
		t.Errorf("list: %v %v", n, err)
	}
	for _, bad := range []string{`[]`, `1`, `{"a": 1}`} {
		if err := json.Unmarshal([]byte(bad), &n); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestVocabularies(t *testing.T) {
	t.Parallel()
	if len(Reasons()) != 6 || len(Roles()) == 0 || len(ActionCategories()) != 17 || len(ActionEffects()) == 0 || len(Platforms()) != 2 {
		t.Error("vocabularies are incomplete")
	}
	r := Reasons()
	r[0] = "changed"
	if Reasons()[0] == "changed" {
		t.Error("Reasons shares its backing array")
	}
}
