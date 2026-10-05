// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package importer

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/nightCode42/plux3/backend/internal/compiler"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

var projectDir = filepath.Join("..", "..", "..", "schema", "testdata", "documents", "loan-calculator")

// embed places an imported fragment in the conformance project: its types and
// variable in the app, its source in the plugin, and its base URL, on a domain
// the plugin declares, in every environment.
func embed(t *testing.T, r Result) fs.FS {
	t.Helper()
	m := fstest.MapFS{}
	err := filepath.WalkDir(projectDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		rel, _ := filepath.Rel(projectDir, p)
		m[filepath.ToSlash(rel)] = &fstest.MapFile{Data: data}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	edit := func(file string, f func(doc map[string]any)) {
		var doc map[string]any
		if err := json.Unmarshal(m[file].Data, &doc); err != nil {
			t.Fatal(err)
		}
		f(doc)
		data, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		m[file] = &fstest.MapFile{Data: data}
	}
	vars := r.Fragment["variables"].([]any)
	edit("app.json", func(doc map[string]any) {
		doc["requiredFeatures"] = "raise"
		doc["types"] = append(doc["types"].([]any), r.Fragment["types"].([]any)...)
		doc["variables"] = append(doc["variables"].([]any), vars...)
		for _, env := range doc["environments"].([]any) {
			values := env.(map[string]any)["values"].(map[string]any)
			for _, v := range vars {
				values[v.(map[string]any)["name"].(string)] = "https://api.example.com"
			}
		}
	})
	edit("plugins/loans/plugin.json", func(doc map[string]any) {
		doc["dataSources"] = r.Fragment["dataSources"]
	})
	return m
}

// TestImportedSourcesCompile_DAT_002 checks that the fragments the importer
// writes, with or without examples, compile with no errors: the mock matches
// the source's type, the base URL is a variable and the operations resolve.
func TestImportedSourcesCompile_DAT_002(t *testing.T) {
	gql := ImportGraphQL(Source{"schema.graphql", read(t, "schema.graphql")}, []Source{{"ops.graphql", read(t, "ops.graphql")}})
	for name, r := range map[string]Result{
		"openapi without a read example": ImportOpenAPI("todo30.json", read(t, "todo30.json")),
		"openapi with examples":          ImportOpenAPI("petstore31.yaml", read(t, "petstore31.yaml")),
		"graphql without examples":       gql,
	} {
		t.Run(name, func(t *testing.T) {
			if r.Fragment == nil || len(r.Fragment["dataSources"].([]any)) != 1 {
				t.Fatalf("no source: %v", r.Diagnostics)
			}
			res := compiler.Compile(embed(t, r), compiler.DefaultOptions())
			for _, d := range res.Diagnostics {
				if d.Severity == plxerr.SeverityError {
					t.Errorf("%s", d.String())
				}
			}
		})
	}
}
