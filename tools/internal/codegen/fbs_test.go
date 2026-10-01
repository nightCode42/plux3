// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package codegen

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Verifies: BND-006.
func TestLoadFBSReadsEveryFieldKind(t *testing.T) {
	t.Parallel()
	s, err := LoadFBS(filepath.Join("testdata", "fbs"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Roots["TEST"] != "Root" {
		t.Errorf("roots = %v", s.Roots)
	}
	names := make([]string, len(s.Tables))
	for i, tb := range s.Tables {
		names[i] = tb.Name
	}
	if !slices.Equal(names, []string{"Leaf", "Root"}) {
		t.Fatalf("tables = %v (structs are inlined, not listed)", names)
	}
	want := []FBSField{
		{Name: "scalar", ID: 0, Kind: FieldScalar, Size: 1, Align: 1},
		{Name: "pair", ID: 1, Kind: FieldStruct, Size: 16, Align: 8},
		{Name: "name", ID: 2, Kind: FieldString, Required: true},
		{Name: "leaf", ID: 3, Kind: FieldTable, Table: "Leaf"},
		{Name: "bytes", ID: 4, Kind: FieldVectorScalar, Size: 1, Align: 1},
		{Name: "pairs", ID: 5, Kind: FieldVectorStruct, Size: 16, Align: 8},
		{Name: "names", ID: 6, Kind: FieldVectorString},
		{Name: "leaves", ID: 7, Kind: FieldVectorTable, Table: "Leaf"},
		{Name: "wide", ID: 9, Kind: FieldScalar, Size: 8, Align: 8},
	}
	if got := s.Tables[1].Fields; !slices.Equal(got, want) {
		t.Errorf("Root fields:\n got %+v\nwant %+v", got, want)
	}
	files, err := FBSFiles(s)
	if err != nil {
		t.Fatal(err)
	}
	src := string(files[0].Content)
	for _, part := range []string{
		"DO NOT EDIT.", "package bundle", `{name: "leaves", id: 7, kind: fieldVectorTable, size: 0, align: 0, table: 0, required: false}`,
		`"TEST": 1, // Root`,
	} {
		if !strings.Contains(src, part) {
			t.Errorf("layout file lacks %q:\n%s", part, src)
		}
	}
	dart := string(files[1].Content)
	for _, part := range []string{
		"DO NOT EDIT.", "FieldLayout('leaves', 7, FieldKind.vectorTable, 0, 0, 0, required: false)",
		"FieldLayout('name', 2, FieldKind.string, 0, 0, -1, required: true)", "'TEST': 1, // Root",
	} {
		if !strings.Contains(dart, part) {
			t.Errorf("Dart layout file lacks %q:\n%s", part, dart)
		}
	}
}

func TestLoadFBSRejectsUnsupportedInput(t *testing.T) {
	t.Parallel()
	if _, err := LoadFBS(filepath.Join("testdata", "fbs-union")); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("union: %v", err)
	}
	if _, err := LoadFBS(t.TempDir()); err == nil {
		t.Error("an empty directory was accepted")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.bfbs"), []byte("not a schema"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFBS(dir); err == nil {
		t.Error("garbage was accepted")
	}
	s := &FBS{Tables: []FBSTable{{Name: "A", Fields: []FBSField{{Name: "b", Kind: FieldTable, Table: "Missing"}}}}}
	if _, err := FBSFiles(s); err == nil {
		t.Error("a dangling table reference was accepted")
	}
	s = &FBS{Roots: map[string]string{"XXXX": "Missing"}}
	if _, err := FBSFiles(s); err == nil {
		t.Error("a dangling root was accepted")
	}
}
