// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package pxl

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// TestSourcesMatchSchemas validates schema/pxl against schema/json/pxl.
func TestSourcesMatchSchemas(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..", "..", "schema")
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	for _, name := range []string{"bytecode", "stdlib", "currencies"} {
		sch, err := c.Compile(filepath.Join(root, "json", "pxl", name+".schema.json"))
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(root, "pxl", name+".json")) //nolint:gosec // G304: repository sources.
		if err != nil {
			t.Fatal(err)
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if err := sch.Validate(doc); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// TestTablesAreComplete checks that every core overload is implemented, the
// others are not, and the currency table answers lookups.
//
// Verifies: PXL-006, PXL-005.
func TestTablesAreComplete(t *testing.T) {
	t.Parallel()
	inline := map[string]bool{"coalesce": true, "ifNull": true, "isNull": true, "typeOf": true}
	impl := builtins()
	for i, def := range stdOverloads {
		switch {
		case def.group == "core" && !inline[def.name] && impl[i] == nil:
			t.Errorf("%s is not implemented", overloadKey(def))
		case def.group != "core" && impl[i] != nil:
			t.Errorf("%s belongs to group %s but is implemented", overloadKey(def), def.group)
		}
	}
	for code, want := range map[string]int{"EUR": 2, "JPY": 0, "BHD": 3, "CLF": 4} {
		if got, ok := minorUnits(code); !ok || got != want {
			t.Errorf("minorUnits(%s) = %d, %t", code, got, ok)
		}
	}
	if _, ok := minorUnits("XXX"); ok {
		t.Error("XXX is a currency")
	}
	if !strings.HasPrefix(groupFeature("format"), "pxl.format") || groupFeature("none") != "" {
		t.Error("groupFeature")
	}
}
